package google

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sur1cat/steno/internal/core"
	"golang.org/x/oauth2"
)

// Доступ в Google — то место, где поломка выглядит как исправность. Токен,
// потерявший право на продление, работает час, а потом отваливается молча:
// созвоны идут, бот на них не приходит, и никто не связывает одно с другим.
// Поэтому здесь проверяется не «вызвалось без ошибки», а именно развилки и
// потери.

func googleTestConfig(t *testing.T) *core.Config {
	t.Helper()
	cfg := core.DefaultConfig()
	cfg.DataDir = t.TempDir()
	return cfg
}

// connectGoogle — то состояние, в котором оказывается установка после нажатой
// кнопки и согласия у Google.
func connectGoogle(t *testing.T, cfg *core.Config, account string, scopes ...string) {
	t.Helper()
	cfg.Google.ClientID = "steno.apps.googleusercontent.com"
	t.Setenv(cfg.Google.ClientSecretEnv, "секрет-приложения")
	if len(scopes) == 0 {
		scopes = GoogleScopes
	}
	if err := SaveGoogleToken(cfg, &GoogleToken{
		Account: account, Scopes: scopes,
		Token: &oauth2.Token{AccessToken: "живой", RefreshToken: "долгий-доступ",
			Expiry: time.Now().Add(time.Hour)},
	}); err != nil {
		t.Fatal(err)
	}
}

// writeFakeKey кладёт файл, который google.JWTConfigFromJSON примет за ключ
// service-account. Настоящий ключ для теста не нужен: подписывать им никто не
// собирается, проверяется только выбор пути.
func writeFakeKey(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "key.json")
	raw, _ := json.Marshal(map[string]string{
		"type":           "service_account",
		"client_email":   "steno@project.iam.gserviceaccount.com",
		"private_key_id": "1",
		"private_key":    "-----BEGIN PRIVATE KEY-----\nне настоящий\n-----END PRIVATE KEY-----\n",
		"token_uri":      "https://oauth2.googleapis.com/token",
	})
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// --- развилка ------------------------------------------------------------------

// Подключённый кнопкой аккаунт главнее ключа организации. Ключ здесь заведомо
// испорчен: если развилка уйдёт к нему, это будет видно ошибкой, а не догадкой.
func TestGoogleClientPrefersConnectedAccount(t *testing.T) {
	cfg := googleTestConfig(t)
	broken := filepath.Join(cfg.DataDir, "не-ключ.json")
	if err := os.WriteFile(broken, []byte("это не ключ"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Пока никто не подключался, остаётся только ключ — и он не читается.
	if _, err := GoogleClient(context.Background(), cfg, broken, "", GoogleScopes...); err == nil {
		t.Fatal("испорченный ключ сошёл за рабочий")
	}

	connectGoogle(t, cfg, "user@example.com")
	opt, err := GoogleClient(context.Background(), cfg, broken, "", GoogleScopes...)
	if err != nil {
		t.Fatalf("нажатая кнопка ничего не изменила: %v", err)
	}
	// Проверять надо и то, что клиент вообще есть: «ни ошибки, ни клиента» —
	// исход, на котором всё падает потом и в другом месте.
	if opt == nil {
		t.Fatal("доступ выдан без клиента")
	}
}

// Без подключения работает ключ организации — так живёт компания, и ломать это
// появлением кнопки нельзя. Ни client id, ни секрета здесь нет: пройти вторым
// путём эта установка не может физически.
func TestGoogleClientFallsBackToKeyFile(t *testing.T) {
	cfg := googleTestConfig(t)
	key := writeFakeKey(t, cfg.DataDir)
	opt, err := GoogleClient(context.Background(), cfg, key,
		"notes@example.com", GoogleScopes...)
	if err != nil {
		t.Fatalf("ключ организации перестал работать: %v", err)
	}
	if opt == nil {
		t.Fatal("доступ выдан без клиента")
	}
}

// Когда нет ни того ни другого, ошибка должна называть оба пути словами.
// «Не указан файл ключа service-account» человеку, который поставил steno себе,
// не говорит ничего: файла у него не будет никогда.
func TestGoogleClientWithoutAccessNamesBothWays(t *testing.T) {
	cfg := googleTestConfig(t)
	_, err := GoogleClient(context.Background(), cfg, "", "", GoogleScopes...)
	if err == nil {
		t.Fatal("доступ выдан из ниоткуда")
	}
	for _, want := range []string{"Подключить Google", "ключ организации"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("в ошибке не сказано про «%s»: %v", want, err)
		}
	}
}

// Испорченный файл доступа — не повод молча взять ключ организации: у них
// разные права и разные имена, и подмена одного другим тихо уводит бота не туда.
func TestGoogleClientDoesNotHideBrokenToken(t *testing.T) {
	cfg := googleTestConfig(t)
	key := writeFakeKey(t, cfg.DataDir)
	if err := os.WriteFile(GoogleTokenPath(cfg), []byte("{сломано"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := GoogleClient(context.Background(), cfg, key, "", GoogleScopes...); err == nil {
		t.Fatal("испорченный доступ подменён ключом организации молча")
	}
}

// По кнопке steno работает от имени того, кто её нажал. Чужой календарь так не
// открыть — и узнать об этом надо словами, а не по пустому списку встреч.
func TestGoogleClientRefusesForeignSubject(t *testing.T) {
	cfg := googleTestConfig(t)
	connectGoogle(t, cfg, "user@example.com")

	_, err := GoogleClient(context.Background(), cfg, "", "other@example.com", GoogleScopes...)
	if err == nil {
		t.Fatal("чужой календарь открылся молча")
	}
	for _, want := range []string{"user@example.com", "other@example.com"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("в ошибке нет %s: %v", want, err)
		}
	}
	// Свой — открывается, и регистр адреса значения не имеет: в календаре он
	// может быть записан как угодно.
	if _, err := GoogleClient(context.Background(), cfg, "",
		"USER@Example.com", GoogleScopes...); err != nil {
		t.Errorf("свой же календарь не открылся: %v", err)
	}
}

// Согласие бывает частичным: галочки на экране Google снимаются поодиночке.
func TestGoogleClientNoticesPartialConsent(t *testing.T) {
	cfg := googleTestConfig(t)
	connectGoogle(t, cfg, "user@example.com",
		"https://www.googleapis.com/auth/calendar.readonly")

	_, err := GoogleClient(context.Background(), cfg, "", "",
		"https://www.googleapis.com/auth/drive")
	if err == nil {
		t.Fatal("доступ к документам взялся из ниоткуда")
	}
	if !strings.Contains(err.Error(), "документам") {
		t.Errorf("область доступа названа не по-человечески: %v", err)
	}
}

func TestMissingScopes(t *testing.T) {
	const (
		cal   = "https://www.googleapis.com/auth/calendar.readonly"
		mail  = "https://www.googleapis.com/auth/gmail.readonly"
		drive = "https://www.googleapis.com/auth/drive"
	)
	if got := MissingScopes([]string{cal, mail}, []string{cal, drive}); len(got) != 1 || got[0] != drive {
		t.Errorf("не найдено недостающее: %v", got)
	}
	if got := MissingScopes([]string{cal, mail, drive}, []string{cal, drive}); len(got) != 0 {
		t.Errorf("выданное показано недостающим: %v", got)
	}
	if got := MissingScopes(nil, []string{cal}); len(got) != 1 {
		t.Errorf("пустое согласие сошло за полное: %v", got)
	}
	if got := MissingScopes([]string{cal}, nil); len(got) != 0 {
		t.Errorf("не нужное никому показано недостающим: %v", got)
	}
	// И словами: адрес вида .../auth/drive человеку не говорит ничего.
	if got := HumanScopes([]string{drive, mail}); got[0] != "документам" || got[1] != "почте" {
		t.Errorf("область доступа не переведена: %v", got)
	}
}

// --- продление -----------------------------------------------------------------

type fixedTokenSource struct{ tok *oauth2.Token }

func (f fixedTokenSource) Token() (*oauth2.Token, error) { return f.tok, nil }

// Самая дорогая поломка из возможных: право на продление Google присылает один
// раз, вместе с согласием, и при обновлении поле приходит пустым. Записать его
// поверх — значит потерять доступ насовсем, но не сразу, а через неделю. Панель
// всё это время показывает «подключено».
func TestSavingTokenSourceKeepsRefreshToken(t *testing.T) {
	cfg := googleTestConfig(t)
	stored := &GoogleToken{
		Account: "user@example.com", Scopes: GoogleScopes,
		Token: &oauth2.Token{AccessToken: "старый", RefreshToken: "долгий-доступ",
			Expiry: time.Now().Add(-time.Minute)},
	}
	if err := SaveGoogleToken(cfg, stored); err != nil {
		t.Fatal(err)
	}

	// Так отвечает Google при обновлении: новый короткий токен и пустое поле
	// долгого.
	src := &savingTokenSource{cfg: cfg, stored: stored, inner: fixedTokenSource{
		&oauth2.Token{AccessToken: "новый", Expiry: time.Now().Add(time.Hour)}}}
	got, err := src.Token()
	if err != nil {
		t.Fatal(err)
	}
	if got.RefreshToken != "долгий-доступ" {
		t.Fatalf("право на продление потеряно на месте: %q", got.RefreshToken)
	}
	back, err := LoadGoogleToken(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if back.Token.RefreshToken != "долгий-доступ" {
		t.Errorf("на диске осталось %q — через неделю steno отвалился бы молча",
			back.Token.RefreshToken)
	}
	if back.Token.AccessToken != "новый" {
		t.Errorf("обновлённый токен не записан: %q", back.Token.AccessToken)
	}
	// Права как у .env: рядом лежит база со всеми расшифровками, и ключ от
	// почты того же человека не должен читаться всей машиной.
	fi, err := os.Stat(GoogleTokenPath(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("права на файл доступа %o, ожидали 600", perm)
	}

	// А выданное заново право на продление должно именно заменить старое:
	// оставить прежнее — тот же молчаливый отвал, только позже.
	src.inner = fixedTokenSource{&oauth2.Token{
		AccessToken: "ещё новее", RefreshToken: "выданный-заново"}}
	if got, err = src.Token(); err != nil || got.RefreshToken != "выданный-заново" {
		t.Fatalf("новое право на продление не взято: %v, %q", err, got.RefreshToken)
	}
	if back, err = LoadGoogleToken(cfg); err != nil || back.Token.RefreshToken != "выданный-заново" {
		t.Fatalf("новое право на продление не записано: %v, %q", err, back.Token.RefreshToken)
	}
}
