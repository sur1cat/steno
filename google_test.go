package main

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// Доступ в Google — то место, где поломка выглядит как исправность. Токен,
// потерявший право на продление, работает час, а потом отваливается молча:
// созвоны идут, бот на них не приходит, и никто не связывает одно с другим.
// Поэтому здесь проверяется не «вызвалось без ошибки», а именно развилки и
// потери.

func googleTestConfig(t *testing.T) *Config {
	t.Helper()
	cfg := defaultConfig()
	cfg.DataDir = t.TempDir()
	return cfg
}

// connectGoogle — то состояние, в котором оказывается установка после нажатой
// кнопки и согласия у Google.
func connectGoogle(t *testing.T, cfg *Config, account string, scopes ...string) {
	t.Helper()
	cfg.Google.ClientID = "steno.apps.googleusercontent.com"
	t.Setenv(cfg.Google.ClientSecretEnv, "секрет-приложения")
	if len(scopes) == 0 {
		scopes = googleScopes
	}
	if err := saveGoogleToken(cfg, &googleToken{
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
	if _, err := googleClient(context.Background(), cfg, broken, "", googleScopes...); err == nil {
		t.Fatal("испорченный ключ сошёл за рабочий")
	}

	connectGoogle(t, cfg, "user@example.com")
	opt, err := googleClient(context.Background(), cfg, broken, "", googleScopes...)
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
	opt, err := googleClient(context.Background(), cfg, key,
		"notes@example.com", googleScopes...)
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
	_, err := googleClient(context.Background(), cfg, "", "", googleScopes...)
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
	if err := os.WriteFile(googleTokenPath(cfg), []byte("{сломано"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := googleClient(context.Background(), cfg, key, "", googleScopes...); err == nil {
		t.Fatal("испорченный доступ подменён ключом организации молча")
	}
}

// По кнопке steno работает от имени того, кто её нажал. Чужой календарь так не
// открыть — и узнать об этом надо словами, а не по пустому списку встреч.
func TestGoogleClientRefusesForeignSubject(t *testing.T) {
	cfg := googleTestConfig(t)
	connectGoogle(t, cfg, "user@example.com")

	_, err := googleClient(context.Background(), cfg, "", "other@example.com", googleScopes...)
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
	if _, err := googleClient(context.Background(), cfg, "",
		"USER@Example.com", googleScopes...); err != nil {
		t.Errorf("свой же календарь не открылся: %v", err)
	}
}

// Согласие бывает частичным: галочки на экране Google снимаются поодиночке.
func TestGoogleClientNoticesPartialConsent(t *testing.T) {
	cfg := googleTestConfig(t)
	connectGoogle(t, cfg, "user@example.com",
		"https://www.googleapis.com/auth/calendar.readonly")

	_, err := googleClient(context.Background(), cfg, "", "",
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
	if got := missingScopes([]string{cal, mail}, []string{cal, drive}); len(got) != 1 || got[0] != drive {
		t.Errorf("не найдено недостающее: %v", got)
	}
	if got := missingScopes([]string{cal, mail, drive}, []string{cal, drive}); len(got) != 0 {
		t.Errorf("выданное показано недостающим: %v", got)
	}
	if got := missingScopes(nil, []string{cal}); len(got) != 1 {
		t.Errorf("пустое согласие сошло за полное: %v", got)
	}
	if got := missingScopes([]string{cal}, nil); len(got) != 0 {
		t.Errorf("не нужное никому показано недостающим: %v", got)
	}
	// И словами: адрес вида .../auth/drive человеку не говорит ничего.
	if got := humanScopes([]string{drive, mail}); got[0] != "документам" || got[1] != "почте" {
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
	stored := &googleToken{
		Account: "user@example.com", Scopes: googleScopes,
		Token: &oauth2.Token{AccessToken: "старый", RefreshToken: "долгий-доступ",
			Expiry: time.Now().Add(-time.Minute)},
	}
	if err := saveGoogleToken(cfg, stored); err != nil {
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
	back, err := loadGoogleToken(cfg)
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
	fi, err := os.Stat(googleTokenPath(cfg))
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
	if back, err = loadGoogleToken(cfg); err != nil || back.Token.RefreshToken != "выданный-заново" {
		t.Fatalf("новое право на продление не записано: %v, %q", err, back.Token.RefreshToken)
	}
}

// --- кнопка в панели -----------------------------------------------------------

func TestPanelGoogleConnect(t *testing.T) {
	srv, _, p := testPanel(t)
	a := login(t, srv, "тайна")

	var st googleState
	a.get("/api/google/status", &st)
	if st.Ready || st.Connected {
		t.Fatalf("на пустой установке подключаться нечем: %+v", st)
	}
	if st.Why == "" {
		t.Error("панели нечего сказать человеку про недоступную кнопку")
	}
	// Кнопка, ведущая в ошибку, хуже отсутствующей: ручка отказывает сразу.
	if code, _ := a.do("POST", "/api/google/connect", nil); code != 400 {
		t.Errorf("ссылка на согласие выдана без заведённого входа: %d", code)
	}

	p.cfg.Google.ClientID = "steno.apps.googleusercontent.com"
	t.Setenv(p.cfg.Google.ClientSecretEnv, "секрет-приложения")
	a.get("/api/google/status", &st)
	if !st.Ready || st.Connected {
		t.Fatalf("кнопка не появилась: %+v", st)
	}

	code, raw := a.do("POST", "/api/google/connect", nil)
	if code != 200 {
		t.Fatalf("подключение: %d %s", code, raw)
	}
	var conn struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &conn); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(conn.URL)
	if err != nil {
		t.Fatalf("ссылка на согласие не разбирается: %v", err)
	}
	q := u.Query()
	// Без offline+consent Google выдаёт доступ на час и без права продлить: всё
	// работает до вечера, а потом перестаёт без единого сообщения.
	if q.Get("access_type") != "offline" {
		t.Error("доступ спрошен без права продлить: кончится через час")
	}
	if q.Get("prompt") != "consent" {
		t.Error("согласие не спрошено заново: право на продление придёт пустым")
	}
	if !strings.HasSuffix(q.Get("redirect_uri"), "/api/google/callback") {
		t.Errorf("возврат ведёт не в панель: %q", q.Get("redirect_uri"))
	}
	if q.Get("state") == "" {
		t.Error("нет одноразовой метки: подсунуть чужое согласие смог бы кто угодно")
	}

	// Чужая метка не принимается — иначе чужая страница подключила бы к steno
	// свой ящик.
	got, loc := a.noRedirect("GET", "/api/google/callback?code=подделка&state=чужая")
	if got != 303 || !strings.Contains(loc, "google=fail") {
		t.Errorf("чужая метка принята: %d %s", got, loc)
	}
	// Отказать должна именно метка, а не сорвавшийся обмен кодом: иначе проверки
	// может не быть вовсе, а ответ будет выглядеть так же.
	if why, _ := url.QueryUnescape(loc); !strings.Contains(why, "устарела") {
		t.Errorf("отказ пришёл не от проверки метки: %s", loc)
	}
	if strings.Contains(loc, "code=") {
		t.Error("код согласия уехал обратно в адресную строку")
	}
}

func TestPanelGoogleDisconnect(t *testing.T) {
	srv, _, p := testPanel(t)
	a := login(t, srv, "тайна")
	connectGoogle(t, p.cfg, "user@example.com")

	var st googleState
	a.get("/api/google/status", &st)
	if !st.Connected || st.Account != "user@example.com" {
		t.Fatalf("подключение не видно: %+v", st)
	}
	if code, _ := a.do("POST", "/api/google/disconnect", nil); code != 200 {
		t.Fatalf("отключение: %d", code)
	}
	if _, err := os.Stat(googleTokenPath(p.cfg)); !os.IsNotExist(err) {
		t.Error("файл доступа остался на диске")
	}
	a.get("/api/google/status", &st)
	if st.Connected {
		t.Error("панель показывает подключение, которого больше нет")
	}
}

// Частичное согласие панель обязана назвать: канал молча не заработает, и
// причину будут искать где угодно, только не здесь.
func TestPanelGoogleStatusTellsAboutPartialConsent(t *testing.T) {
	srv, _, p := testPanel(t)
	a := login(t, srv, "тайна")
	connectGoogle(t, p.cfg, "user@example.com",
		"https://www.googleapis.com/auth/calendar.readonly")

	var st googleState
	a.get("/api/google/status", &st)
	if !st.Connected {
		t.Fatal("подключение не видно")
	}
	if !strings.Contains(st.Why, "почте") || !strings.Contains(st.Why, "документам") {
		t.Errorf("панель не сказала, чего не хватает: %q", st.Why)
	}
	if st.Severity != "warn" {
		t.Errorf("нехватка прав — это поломка, а не пояснение: %q", st.Severity)
	}
}

// severity — единственное, по чему панель отличает «всё хорошо» от «не
// работает». Соблазнительно вывести это из ready и connected, поэтому здесь
// проверяются как раз те состояния, на которых такой вывод врёт.
func TestPanelGoogleStatusSeverity(t *testing.T) {
	srv, _, p := testPanel(t)
	a := login(t, srv, "тайна")

	var st googleState
	// Ни кнопки, ни ключа: каналы Google просто не работают.
	a.get("/api/google/status", &st)
	if st.Why == "" || st.Severity != "warn" {
		t.Errorf("отсутствие доступа подано как пояснение: %+v", st)
	}

	// Ключ организации: доступ есть, беспокоиться не о чем. Кнопку при этом
	// тоже заводим — ready остаётся true, и по нему этот случай не отличить.
	p.cfg.GoogleDocs.CredentialsFile = "/секреты/ключ-организации.json"
	p.cfg.Google.ClientID = "steno.apps.googleusercontent.com"
	t.Setenv(p.cfg.Google.ClientSecretEnv, "секрет-приложения")
	a.get("/api/google/status", &st)
	if !st.Ready {
		t.Fatal("подготовили не то состояние")
	}
	if st.Why == "" || st.Severity != "info" {
		t.Errorf("рабочий ключ организации подан как проблема: %+v", st)
	}

	// Кнопка заведена, ключа нет, никто не подключался: говорить нечего.
	p.cfg.GoogleDocs.CredentialsFile = ""
	a.get("/api/google/status", &st)
	if st.Why != "" || st.Severity != "info" {
		t.Errorf("нажать кнопку — не новость: %+v", st)
	}
}
