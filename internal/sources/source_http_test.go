package sources

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sur1cat/steno/internal/core"
)

// Вход по ссылке проверяется настоящими запросами к настоящему серверу: его
// договор — это коды ответов и текст отказа, и подделанный Request их не
// проверяет.

const testSecret = "секрет-длиной-побольше-шестнадцати"

// startedMeetings — созвоны, которые дошли до записи. Сама запись подменена:
// настоящая подняла бы docker.
func testHTTPServer(t *testing.T) (*httptest.Server, chan *core.Meeting) {
	t.Helper()
	dir := t.TempDir()
	st, err := core.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	cfg := core.DefaultConfig()
	d := NewDispatcher(cfg, st, log.New(io.Discard, "", 0))
	started := make(chan *core.Meeting, 8)
	d.Run = func(_ context.Context, _ *core.Config, _ *core.Store, m *core.Meeting) error {
		started <- m
		return nil
	}
	s := &HTTPSource{Cfg: cfg, D: d, Log: log.New(io.Discard, "", 0)}
	srv := httptest.NewServer(s.handler(context.Background(), testSecret))
	t.Cleanup(srv.Close)
	return srv, started
}

func post(t *testing.T, srv *httptest.Server, path, auth, ctype, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest("POST", srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

const joinBody = `{"url":"https://meet.google.com/abc-defg-hij","title":"Планёрка"}`

func TestHTTPJoinWithSecret(t *testing.T) {
	srv, started := testHTTPServer(t)
	code, body := post(t, srv, "/join", "Bearer "+testSecret, "application/json", joinBody)
	if code != http.StatusOK {
		t.Fatalf("код %d, тело %s", code, body)
	}
	var out struct {
		Text      string `json:"text"`
		MeetingID string `json:"meeting_id"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("ответ не JSON: %v (%s)", err, body)
	}
	if out.MeetingID == "" {
		t.Error("в ответе нет meeting_id — не за что зацепиться тому, кто звал бота")
	}
	select {
	case m := <-started:
		if m.MeetURL != "https://meet.google.com/abc-defg-hij" {
			t.Errorf("бот пошёл не туда: %q", m.MeetURL)
		}
		if m.Title != "Планёрка" {
			t.Errorf("название не доехало: %q", m.Title)
		}
	default:
		t.Fatal("запрос принят, а бот никуда не пошёл")
	}
}

// Форма без Content-Type — это curl без -H и слэш-команда Slack.
func TestHTTPJoinAcceptsPlainBody(t *testing.T) {
	srv, started := testHTTPServer(t)
	code, body := post(t, srv, "/join", "Bearer "+testSecret, "",
		"text=запиши https://meet.google.com/abc-defg-hij")
	if code != http.StatusOK {
		t.Fatalf("код %d, тело %s", code, body)
	}
	if len(started) != 1 {
		t.Fatal("ссылка в форме не распознана")
	}
}

// Главное свойство этого входа: без секрета бот никуда не идёт.
func TestHTTPJoinRefusesWithoutSecret(t *testing.T) {
	srv, started := testHTTPServer(t)
	cases := []struct{ name, auth, want string }{
		{"без заголовка", "", "Authorization"},
		{"чужой секрет", "Bearer не-тот-секрет", "неверный секрет"},
		{"не Bearer", "Basic " + testSecret, "не Bearer"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, body := post(t, srv, "/join", c.auth, "application/json", joinBody)
			if code != http.StatusUnauthorized {
				t.Fatalf("код %d, ожидали 401; тело %s", code, body)
			}
			if !strings.Contains(body, c.want) {
				t.Errorf("отказ не объясняет причину %q: %s", c.want, body)
			}
			// Отказ обязан показывать, как надо: иначе человек с curl идёт
			// читать README ровно тогда, когда созвон уже идёт.
			if !strings.Contains(body, "curl -X POST") {
				t.Errorf("в отказе нет рабочего примера: %s", body)
			}
		})
	}
	if len(started) != 0 {
		t.Fatalf("бот пошёл на созвон по неподтверждённой заявке (%d раз)", len(started))
	}
}

// Секрет не должен появляться ни в одном ответе: его читает тот, кто стучится.
func TestHTTPSecretNeverLeaksIntoAnswers(t *testing.T) {
	srv, _ := testHTTPServer(t)
	bodies := []string{}
	for _, c := range []struct{ path, auth, ctype, body string }{
		{"/join", "Bearer " + testSecret, "application/json", joinBody},
		{"/join", "", "application/json", joinBody},
		{"/join", "Bearer чужой", "application/json", joinBody},
		{"/join", "Bearer " + testSecret, "application/json", `{"url":"https://zoom.us/j/1"}`},
		{"/нет-такого", "", "", "x"},
	} {
		_, b := post(t, srv, c.path, c.auth, c.ctype, c.body)
		bodies = append(bodies, b)
	}
	resp, err := srv.Client().Get(srv.URL + "/join")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	bodies = append(bodies, string(raw))

	for _, b := range bodies {
		if strings.Contains(b, testSecret) {
			t.Fatalf("секрет уехал в ответ: %s", b)
		}
		// И имя переменной окружения тоже: тому, кто стучится, оно не помогает,
		// а показывает, что искать.
		if strings.Contains(b, "STENO_HTTP_TOKEN") {
			t.Fatalf("имя переменной с секретом уехало в ответ: %s", b)
		}
	}
}

func TestHTTPJoinExplainsBadRequests(t *testing.T) {
	srv, started := testHTTPServer(t)
	cases := []struct{ name, ctype, body, want string }{
		{"чужая площадка", "application/json", `{"url":"https://zoom.us/j/123456"}`, "Zoom"},
		{"нет ссылки", "application/json", `{"title":"без ссылки"}`, "нет поля url"},
		{"кривой JSON", "application/json", `{"url": `, "это не JSON"},
		{"мусор в теле", "", "просто текст без ссылки", "не нашёл ссылку"},
		{"ссылка-приглашение", "application/json", `{"url":"https://meet.google.com/lookup/abcdef"}`, "кода комнаты"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, body := post(t, srv, "/join", "Bearer "+testSecret, c.ctype, c.body)
			if code != http.StatusBadRequest {
				t.Fatalf("код %d, ожидали 400; тело %s", code, body)
			}
			if !strings.Contains(body, c.want) {
				t.Errorf("отказ не называет причину %q: %s", c.want, body)
			}
		})
	}
	if len(started) != 0 {
		t.Fatal("бот пошёл на созвон по кривой заявке")
	}
}

func TestHTTPMethodAndPathRefusals(t *testing.T) {
	srv, _ := testHTTPServer(t)

	resp, err := srv.Client().Get(srv.URL + "/join")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("на GET /join код %d, ожидали 405", resp.StatusCode)
	}
	if !strings.Contains(string(raw), "curl -X POST") {
		t.Errorf("на GET /join не показали, как надо: %s", raw)
	}

	code, body := post(t, srv, "/joinn", "Bearer "+testSecret, "application/json", joinBody)
	if code != http.StatusNotFound {
		t.Fatalf("на неизвестный адрес код %d, ожидали 404", code)
	}
	if !strings.Contains(body, "/join") || !strings.Contains(body, "/healthz") {
		t.Errorf("404 не перечисляет адреса: %s", body)
	}

	resp, err = srv.Client().Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(raw)) != "ok" {
		t.Errorf("/healthz ответил %d %q", resp.StatusCode, raw)
	}
}

// Один и тот же созвон, присланный дважды, не приводит второго бота.
func TestHTTPJoinTwiceBringsOneBot(t *testing.T) {
	srv, started := testHTTPServer(t)
	if code, body := post(t, srv, "/join", "Bearer "+testSecret, "application/json", joinBody); code != 200 {
		t.Fatalf("первый запрос: %d %s", code, body)
	}
	code, body := post(t, srv, "/join", "Bearer "+testSecret, "application/json", joinBody)
	if code != http.StatusOK {
		t.Fatalf("второй запрос: %d %s", code, body)
	}
	if !strings.Contains(body, "уже иду") {
		t.Errorf("повтор не назван повтором: %s", body)
	}
	if len(started) != 1 {
		t.Fatalf("на один созвон завели %d ботов", len(started))
	}
}

// Без общего секрета источник не поднимается вовсе, и говорит, чем это чинится.
func TestHTTPRefusesToStartWithoutSecret(t *testing.T) {
	dir := t.TempDir()
	st, err := core.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := core.DefaultConfig()
	cfg.HTTP.TokenEnv = "STENO_TEST_HTTP_TOKEN_UNSET"
	t.Setenv(cfg.HTTP.TokenEnv, "")

	s := &HTTPSource{Cfg: cfg, D: NewDispatcher(cfg, st, log.New(io.Discard, "", 0)),
		Log: log.New(io.Discard, "", 0)}
	err = s.Run(context.Background())
	if err == nil {
		t.Fatal("поднялись без общего секрета — порт открыт всем желающим")
	}
	msg := err.Error()
	if !strings.Contains(msg, cfg.HTTP.TokenEnv) {
		t.Errorf("отказ не называет переменную: %s", msg)
	}
	if !strings.Contains(msg, cfg.HTTP.TokenEnv+"=") || len(msg) < 80 {
		t.Errorf("отказ не даёт готовую строку для .env: %s", msg)
	}
}

// Готовый секрет обязан быть разным каждый раз и достаточно длинным.
func TestNewSecretIsRandom(t *testing.T) {
	a, b := newSecret(), newSecret()
	if a == b {
		t.Fatal("два подряд секрета совпали")
	}
	if len(a) < 32 {
		t.Fatalf("секрет короткий: %q", a)
	}
}
