package panel

import (
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/google"
	"golang.org/x/oauth2"
)

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
	if _, err := os.Stat(google.GoogleTokenPath(p.cfg)); !os.IsNotExist(err) {
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

func connectGoogle(t *testing.T, cfg *core.Config, account string, scopes ...string) {
	t.Helper()
	cfg.Google.ClientID = "steno.apps.googleusercontent.com"
	t.Setenv(cfg.Google.ClientSecretEnv, "секрет-приложения")
	if len(scopes) == 0 {
		scopes = google.GoogleScopes
	}
	if err := google.SaveGoogleToken(cfg, &google.GoogleToken{
		Account: account, Scopes: scopes,
		Token: &oauth2.Token{AccessToken: "живой", RefreshToken: "долгий-доступ",
			Expiry: time.Now().Add(time.Hour)},
	}); err != nil {
		t.Fatal(err)
	}
}
