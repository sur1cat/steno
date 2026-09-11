package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/sources"
)

// Панель — это JSON-API плюс собранный бандл. Проверяем API: страницы рисует
// браузер, и требовать от Go определённой разметки больше нельзя — да и не
// нужно, вся правда о данных здесь.

func testPanel(t *testing.T) (*httptest.Server, *core.Store, *Panel) {
	t.Helper()
	t.Setenv("STENO_PANEL_PASSWORD", "тайна")
	dir := t.TempDir()
	st, err := core.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	cfg := core.DefaultConfig()
	cfg.DataDir = dir
	p, err := NewPanel(cfg, st, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(p.handler())
	t.Cleanup(srv.Close)
	return srv, st, p
}

func seedOne(t *testing.T, st *core.Store) {
	t.Helper()
	m := &core.Meeting{ID: "m1", Title: "Планёрка по релизу",
		MeetURL:   "https://meet.google.com/abc-defg-hij",
		StartedAt: time.Now().Add(-2 * time.Hour), Status: "published",
		Participants: []string{"Участник А", "Участник Б"}}
	if err := st.CreateMeeting(m); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveSegments("m1", []core.Segment{
		{Start: 10, End: 15, Speaker: "Участник А", Text: "что там с миграцией схемы"},
		{Start: 20, End: 28, Speaker: "Участник Б", Text: "закончу к четвергу, прогоню на стейджинге"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveFollowup("m1", "test", &core.Followup{
		Title: "Релиз сдвинули",
		TLDR:  []string{"Релиз переносится на пятницу"},
		ActionItems: []core.ActionItem{
			{Owner: "Участник Б", What: "закончить миграцию", Due: "2020-01-01", At: 20},
			{Owner: "не назначен", What: "решить про дежурство", At: 30},
		},
	}); err != nil {
		t.Fatal(err)
	}
}

// --- маленький клиент API ---------------------------------------------------

type apiClient struct {
	t   *testing.T
	c   *http.Client
	url string
}

func (a *apiClient) do(method, path string, body any) (int, []byte) {
	a.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, a.url+path, rdr)
	if err != nil {
		a.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.c.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// noRedirect возвращает код и адрес перенаправления, не ходя по нему: у ручек,
// которые отвечают редиректом, проверять надо именно его.
func (a *apiClient) noRedirect(method, path string) (int, string) {
	a.t.Helper()
	c := *a.c
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequest(method, a.url+path, nil)
	if err != nil {
		a.t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, resp.Header.Get("Location")
}

// get разбирает ответ в v и валит тест на любом коде, кроме 200: страницы,
// которые «почти работают», хуже упавших.
func (a *apiClient) get(path string, v any) {
	a.t.Helper()
	code, b := a.do("GET", path, nil)
	if code != 200 {
		a.t.Fatalf("GET %s: код %d, тело %s", path, code, b)
	}
	if v != nil {
		if err := json.Unmarshal(b, v); err != nil {
			a.t.Fatalf("GET %s: %v (тело %s)", path, err, b)
		}
	}
}

func login(t *testing.T, srv *httptest.Server, pass string) *apiClient {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	a := &apiClient{t: t, c: &http.Client{Jar: jar}, url: srv.URL}
	a.do("POST", "/api/login", map[string]string{"password": pass})
	return a
}

// --- доступ -----------------------------------------------------------------

// Без входа архив созвонов не должен отдаваться никому. Отвечать надо кодом, а
// не редиректом: редирект на HTML в ответ на fetch выглядит как успешный ответ
// с мусором вместо данных.
func TestPanelAPIRequiresLogin(t *testing.T) {
	srv, _, _ := testPanel(t)
	a := &apiClient{t: t, c: srv.Client(), url: srv.URL}
	for _, path := range []string{
		"/api/meetings", "/api/tasks", "/api/search?q=релиз", "/api/meetings/m1",
		"/api/projects", "/api/settings", "/api/schedule", "/audio/m1",
	} {
		if code, _ := a.do("GET", path, nil); code != http.StatusUnauthorized {
			t.Errorf("%s отдался без входа: %d", path, code)
		}
	}
	// Удаление — тем более: ручка, стирающая созвон, без входа опаснее всех
	// остальных вместе взятых.
	if code, _ := a.do("DELETE", "/api/meetings/m1", nil); code != http.StatusUnauthorized {
		t.Errorf("удаление созвона доступно без входа: %d", code)
	}
	// Сам бандл не секрет: страница входа — это он и есть.
	if code, _ := a.do("GET", "/tasks", nil); code != 200 {
		t.Errorf("бандл не отдался: %d", code)
	}
}

func TestPanelWrongPassword(t *testing.T) {
	srv, _, _ := testPanel(t)
	a := login(t, srv, "не та")
	if code, _ := a.do("GET", "/api/meetings", nil); code != http.StatusUnauthorized {
		t.Errorf("с неверным паролем пустили внутрь: %d", code)
	}
	var s struct {
		Authenticated bool `json:"authenticated"`
	}
	a.get("/api/session", &s)
	if s.Authenticated {
		t.Error("сессия считает неверный пароль верным")
	}
}

// --- данные -----------------------------------------------------------------

func TestPanelMeetings(t *testing.T) {
	srv, st, _ := testPanel(t)
	seedOne(t, st)
	a := login(t, srv, "тайна")

	var list struct {
		Meetings []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			Tasks int    `json:"tasks"`
		} `json:"meetings"`
		Total int `json:"total"`
	}
	a.get("/api/meetings", &list)
	if len(list.Meetings) != 1 || list.Meetings[0].Title != "Планёрка по релизу" {
		t.Fatalf("список созвонов: %+v", list)
	}
	if list.Meetings[0].Tasks != 2 {
		t.Errorf("счётчик задач: %d, ждали 2", list.Meetings[0].Tasks)
	}
	if list.Total != 1 {
		t.Errorf("всего созвонов: %d", list.Total)
	}

	var one struct {
		Followup struct {
			Title       string `json:"title"`
			ActionItems []struct {
				What string  `json:"what"`
				At   float64 `json:"at"`
			} `json:"action_items"`
		} `json:"followup"`
		Segments []struct {
			Start float64 `json:"start"`
			Text  string  `json:"text"`
		} `json:"segments"`
	}
	a.get("/api/meetings/m1", &one)
	if one.Followup.Title != "Релиз сдвинули" {
		t.Errorf("follow-up: %+v", one.Followup)
	}
	if len(one.Followup.ActionItems) != 2 || one.Followup.ActionItems[0].What != "закончить миграцию" {
		t.Errorf("задачи follow-up: %+v", one.Followup.ActionItems)
	}
	// Таймкод задачи должен указывать в расшифровку — на нём держится вся
	// перемотка: без него ссылка «послушать это место» вела бы в никуда.
	if one.Followup.ActionItems[0].At != 20 {
		t.Errorf("таймкод задачи: %v", one.Followup.ActionItems[0].At)
	}
	if len(one.Segments) != 2 || one.Segments[1].Start != 20 {
		t.Errorf("расшифровка: %+v", one.Segments)
	}

	if code, _ := a.do("GET", "/api/meetings/нет-такого", nil); code != 404 {
		t.Errorf("несуществующий созвон отдался с кодом %d", code)
	}
}

// Удаления созвона в панели не было вовсе: ни ручки, ни кнопки. Ошибиться
// кнопкой записи или загрузить не тот файл — обычное дело, а убрать созвон было
// нечем ни здесь, ни в терминале.
func TestPanelDeleteMeeting(t *testing.T) {
	srv, st, _ := testPanel(t)
	seedOne(t, st)
	a := login(t, srv, "тайна")

	// Пункт с прошлого созвона, закрытый на этом: он не принадлежит удаляемому
	// и должен вернуться в работу, а не уйти вместе с ним.
	if err := st.AddItem(core.ProjectItem{ID: "Q-старый", Project: "Платежи", Kind: core.KindQuestion,
		Text: "кто дежурит", OpenedIn: "m0"}); err != nil {
		t.Fatal(err)
	}
	if err := st.CloseItem("Q-старый", "done", "решили", "m1"); err != nil {
		t.Fatal(err)
	}
	if err := st.AddItem(core.ProjectItem{ID: "T-родился", Project: "Платежи", Kind: core.KindTask,
		Text: "закончить миграцию", OpenedIn: "m1"}); err != nil {
		t.Fatal(err)
	}

	// Цена приезжает вместе с созвоном: модалка, открывшаяся с пустым местом
	// вместо цифр, — это ровно тот момент, когда жмут «да» не глядя.
	var one struct {
		Removal struct {
			Segments    int    `json:"segments"`
			OpenedTasks int    `json:"openedTasks"`
			Reopen      int    `json:"reopen"`
			Text        string `json:"text"`
		} `json:"removal"`
	}
	a.get("/api/meetings/m1", &one)
	if one.Removal.Segments != 2 || one.Removal.OpenedTasks != 1 || one.Removal.Reopen != 1 {
		t.Fatalf("цена удаления: %+v", one.Removal)
	}
	if !strings.Contains(one.Removal.Text, "откроется заново") {
		t.Errorf("в цене не сказано про открытый заново пункт: %q", one.Removal.Text)
	}

	code, body := a.do("DELETE", "/api/meetings/m1", nil)
	if code != 200 {
		t.Fatalf("удаление: код %d, тело %s", code, body)
	}
	var res struct {
		OK      bool `json:"ok"`
		Removed struct {
			Reopen int `json:"reopen"`
		} `json:"removed"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Removed.Reopen != 1 {
		t.Errorf("ответ на удаление: %s", body)
	}

	if code, _ := a.do("GET", "/api/meetings/m1", nil); code != 404 {
		t.Errorf("удалённый созвон всё ещё отдаётся: %d", code)
	}
	var list struct {
		Meetings []struct {
			ID string `json:"id"`
		} `json:"meetings"`
		Total int `json:"total"`
	}
	a.get("/api/meetings", &list)
	if len(list.Meetings) != 0 || list.Total != 0 {
		t.Errorf("удалённый созвон остался в списке: %+v", list)
	}

	items, err := st.ProjectItems("Платежи")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "Q-старый" {
		t.Fatalf("после удаления в проекте: %+v", items)
	}
	if items[0].Status != "open" || items[0].ClosedIn != "" {
		t.Errorf("чужой пункт не вернулся в работу: %+v", items[0])
	}

	// Опечатка в адресе не должна выглядеть как успешное удаление.
	if code, _ := a.do("DELETE", "/api/meetings/нет-такого", nil); code != 404 {
		t.Errorf("удаление несуществующего созвона: код %d", code)
	}
}

// Поиск идёт и по расшифровке, и по follow-up, а подсветка приходит кусками:
// в индексе лежит сырой текст, и класть в JSON готовый HTML значило бы отдать
// браузеру чужую разметку.
func TestPanelSearch(t *testing.T) {
	srv, st, _ := testPanel(t)
	seedOne(t, st)
	a := login(t, srv, "тайна")

	var res struct {
		Hits []struct {
			Speaker string `json:"speaker"`
			Parts   []struct {
				Text string `json:"text"`
				Hit  bool   `json:"hit"`
			} `json:"parts"`
		} `json:"hits"`
	}
	a.get("/api/search?q=миграц", &res)
	if len(res.Hits) == 0 {
		t.Fatal("поиск ничего не нашёл")
	}
	found, speaker := false, false
	for _, h := range res.Hits {
		if h.Speaker == "Участник А" {
			speaker = true
		}
		for _, p := range h.Parts {
			if p.Hit && strings.HasPrefix(strings.ToLower(p.Text), "миграц") {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("поиск ничего не подсветил: %+v", res.Hits)
	}
	if !speaker {
		t.Error("в результате нет говорящего")
	}
}

func TestPanelTasks(t *testing.T) {
	srv, st, _ := testPanel(t)
	seedOne(t, st)
	a := login(t, srv, "тайна")

	var res struct {
		Tasks []struct {
			Owner string `json:"owner"`
			Due   string `json:"due"`
		} `json:"tasks"`
		Owners []string `json:"owners"`
	}
	a.get("/api/tasks", &res)
	if len(res.Tasks) != 2 {
		t.Fatalf("задач: %+v", res.Tasks)
	}
	// «Не назначен» идёт первым: это то, что вообще ни на ком не висит, и
	// именно оно требует решения.
	if len(res.Owners) == 0 || res.Owners[0] != core.Unassigned {
		t.Errorf("порядок людей в фильтре: %v", res.Owners)
	}

	// Фильтр по человеку.
	var mine struct {
		Tasks []struct {
			Owner string `json:"owner"`
		} `json:"tasks"`
	}
	a.get("/api/tasks?owner="+"%D0%A3%D1%87%D0%B0%D1%81%D1%82%D0%BD%D0%B8%D0%BA%20%D0%91", &mine) // Участник Б
	if len(mine.Tasks) != 1 || mine.Tasks[0].Owner != "Участник Б" {
		t.Errorf("фильтр по человеку: %+v", mine.Tasks)
	}
}

// --- проекты ----------------------------------------------------------------

// Проекты заводятся в панели, а не в JSON. Проверяем весь оборот: создать,
// переименовать, закрыть пункт, вернуть, удалить.
func TestPanelProjectCRUD(t *testing.T) {
	srv, st, _ := testPanel(t)
	a := login(t, srv, "тайна")

	code, body := a.do("POST", "/api/projects", map[string]any{
		"name":    "Платежи",
		"aliases": []string{"биллинг", "payments"},
		"about":   "Приём денег и подписки",
		"sources": []core.Source{
			{Kind: "text", Value: "Приём денег"},
			{Kind: "path", Value: "/tmp/payments"},
			{Kind: "url", Value: "https://pay.example.com"},
		},
	})
	if code != 200 {
		t.Fatalf("не создался: %d %s", code, body)
	}
	got, err := st.Project("Платежи")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Aliases) != 2 || got.Aliases[0] != "биллинг" {
		t.Fatalf("псевдонимы: %v", got.Aliases)
	}
	if len(got.Sources) != 3 || got.Sources[1].Kind != "path" ||
		got.Sources[2].Value != "https://pay.example.com" {
		t.Fatalf("источники: %+v", got.Sources)
	}

	var settings struct {
		Projects []struct {
			Name string `json:"name"`
		} `json:"projects"`
	}
	a.get("/api/settings", &settings)
	if len(settings.Projects) != 1 || settings.Projects[0].Name != "Платежи" {
		t.Errorf("проект не показан в настройках: %+v", settings.Projects)
	}

	// Переименование не должно оставлять дубль.
	a.do("POST", "/api/projects", map[string]any{
		"oldName": "Платежи", "name": "Платёжка",
		"aliases": []string{"биллинг"}, "about": "то же",
		"sources": []core.Source{{Kind: "text", Value: "то же"}},
	})
	ps, _ := st.Projects()
	if len(ps) != 1 || ps[0].Name != "Платёжка" {
		t.Fatalf("после переименования: %+v", ps)
	}

	// Непонятный вид источника — внятный отказ, а не молчаливое проглатывание.
	if code, _ := a.do("POST", "/api/projects", map[string]any{
		"name": "Кривой", "sources": []core.Source{{Kind: "магия", Value: "что-то"}},
	}); code != 400 {
		t.Errorf("непонятный вид источника принят, код %d", code)
	}
	// Пустое значение — тоже отказ: источник без значения ничего не даст, а
	// выглядеть в списке будет настоящим.
	if code, _ := a.do("POST", "/api/projects", map[string]any{
		"name": "Кривой", "sources": []core.Source{{Kind: "url", Value: "  "}},
	}); code != 400 {
		t.Errorf("пустой источник принят, код %d", code)
	}

	if code, _ := a.do("DELETE", "/api/projects/Платёжка", nil); code != 200 {
		t.Errorf("удаление: код %d", code)
	}
	if ps, _ := st.Projects(); len(ps) != 0 {
		t.Fatalf("проект не удалился: %+v", ps)
	}
}

// Задачу можно закрыть и вернуть руками: сделанную тихо иначе не закрыть, а
// закрытую коммитом ошибочно — не вернуть.
func TestPanelCloseAndReopenItem(t *testing.T) {
	srv, st, _ := testPanel(t)
	a := login(t, srv, "тайна")
	if err := st.AddItem(core.ProjectItem{ID: "T-1", Project: "Платежи", Kind: core.KindTask,
		Text: "починить вебхуки"}); err != nil {
		t.Fatal(err)
	}

	a.do("POST", "/api/items/T-1/close", nil)
	items, _ := st.ProjectItems("Платежи")
	if len(items) != 1 || items[0].Status != "done" {
		t.Fatalf("после закрытия: %+v", items)
	}
	a.do("POST", "/api/items/T-1/reopen", nil)
	items, _ = st.ProjectItems("Платежи")
	if items[0].Status != "open" {
		t.Fatalf("после возврата: %+v", items)
	}

	var page struct {
		Items []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"items"`
	}
	a.get("/api/projects/Платежи", &page)
	if len(page.Items) != 1 || page.Items[0].ID != "T-1" {
		t.Errorf("страница проекта: %+v", page.Items)
	}
}

// --- настройки --------------------------------------------------------------

// Секретов в панели нет вовсе — ни значений, ни имён переменных, в которых они
// лежат. Проверяем по настоящему телу ответа, а не по типам: поле легко вернуть
// одной строчкой в map[string]any, и никакая структура этого не заметит.
//
// Имя переменной — не безобидная мелочь. Человеку из отдела продаж оно не
// говорит ничего и починить он по нему ничего не может, зато любому, кто
// заглянул через плечо, оно показывает, что и где искать.
func TestPanelSettingsHasNoSecrets(t *testing.T) {
	for env, val := range map[string]string{
		"ANTHROPIC_API_KEY":    "sk-ant-очень-секретный-ключ",
		"SLACK_BOT_TOKEN":      "xoxb-секрет",
		"SLACK_SIGNING_SECRET": "подпись-секрет",
		"TELEGRAM_BOT_TOKEN":   "телеграм-секрет",
		"STENO_HTTP_TOKEN":     "http-секрет",
		"GOOGLE_CLIENT_SECRET": "google-секрет",
	} {
		t.Setenv(env, val)
	}
	srv, st, p := testPanel(t)
	a := login(t, srv, "тайна")

	// Каналы едут в том же ответе, поэтому проверяем и их: перенесём настройки
	// из конфига, чтобы в ответе лежали не пустые заготовки.
	if _, err := core.ImportChannels(st, p.cfg); err != nil {
		t.Fatal(err)
	}
	// А это — установка, обновившаяся со старой версии: в базе лежат значения
	// полей, которых в форме больше нет. Отдавать их панели нельзя ровно тем,
	// что человек их там увидит.
	if err := st.SaveChannel("google_docs", true, map[string]string{
		"folder_id":        "1AbCпапка",
		"credentials_file": "/секреты/ключ-организации.json",
		"subject":          "notes@example.com",
	}); err != nil {
		t.Fatal(err)
	}

	_, raw := a.do("GET", "/api/settings", nil)
	body := string(raw)
	for _, secret := range []string{"sk-ant-очень-секретный-ключ", "xoxb-секрет",
		"подпись-секрет", "телеграм-секрет", "http-секрет", "google-секрет", "тайна"} {
		if strings.Contains(body, secret) {
			t.Errorf("панель отдала значение секрета %q", secret)
		}
	}
	for _, env := range []string{"ANTHROPIC_API_KEY", "STENO_PANEL_PASSWORD",
		"SLACK_BOT_TOKEN", "SLACK_SIGNING_SECRET", "TELEGRAM_BOT_TOKEN",
		"STENO_HTTP_TOKEN", "GOOGLE_CLIENT_SECRET"} {
		if strings.Contains(body, env) {
			t.Errorf("панель отдала имя переменной окружения %s", env)
		}
	}
	// И ловушка на будущее: любое ИМЯ_ВОТ_ТАКОГО_ВИДА в ответе — это почти
	// наверняка вернувшаяся переменная окружения, даже если её ещё не завели.
	if m := envNameRe.FindString(body); m != "" {
		t.Errorf("в ответе завелось что-то похожее на переменную окружения: %s", m)
	}
	// Убранные из формы поля не должны доезжать до панели даже из базы.
	for _, hidden := range []string{"/секреты/ключ-организации.json", "notes@example.com",
		"credentials_file", "subject"} {
		if strings.Contains(body, hidden) {
			t.Errorf("панель отдала убранное поле %q", hidden)
		}
	}
	if !strings.Contains(body, "1AbCпапка") {
		t.Error("вместе с убранными полями пропали и настоящие")
	}
	// Кнопку «Подключить Google» панель рисует по полю канала, а не по секрету.
	if !strings.Contains(body, `"kind":"google"`) {
		t.Error("панели нечем нарисовать подключение к Google")
	}
}

var envNameRe = regexp.MustCompile(`[A-Z][A-Z0-9]*(_[A-Z0-9]+)+`)

// Каналы живут в базе и правятся в панели. Проверяем всю дорогу: перенос из
// конфига, правку через API и то, что сервис берёт настройку из базы.
func TestPanelChannels(t *testing.T) {
	srv, st, p := testPanel(t)
	a := login(t, srv, "тайна")

	p.cfg.Slack.Enabled = true
	p.cfg.Slack.Channel = "#старый"
	if n, err := core.ImportChannels(st, p.cfg); err != nil || n == 0 {
		t.Fatalf("перенос из конфига: %d, %v", n, err)
	}

	var settings struct {
		Channels []core.Channel `json:"channels"`
	}
	a.get("/api/settings", &settings)
	var slack *core.Channel
	for i := range settings.Channels {
		if settings.Channels[i].Key == "slack" {
			slack = &settings.Channels[i]
		}
	}
	if slack == nil {
		t.Fatal("канала slack нет в настройках")
	}
	if !slack.Enabled || slack.Values["channel"] != "#старый" {
		t.Fatalf("канал приехал из конфига не таким: %+v", slack)
	}
	if len(slack.Fields) == 0 {
		t.Error("панели нечего рисовать: у канала нет описания полей")
	}

	// Правка через панель.
	if code, body := a.do("POST", "/api/channels/slack", map[string]any{
		"enabled": true,
		"values":  map[string]string{"channel": "#созвоны", "dm_owners": "1"},
	}); code != 200 {
		t.Fatalf("сохранение канала: %d %s", code, body)
	}
	// База главнее конфига: сервис должен увидеть новое значение, не
	// перечитывая файл.
	live := core.ActiveChannels(st, p.cfg)
	if live.Slack.Channel != "#созвоны" || !live.Slack.DMOwners {
		t.Fatalf("настройки из базы не доехали до сервиса: %+v", live.Slack)
	}
	// А сам конфиг остался нетронутым: его читают несколько горутин сразу.
	if p.cfg.Slack.Channel != "#старый" {
		t.Error("activeChannels поправил общий конфиг вместо копии")
	}

	// Выключение тоже переживает перезапуск.
	a.do("POST", "/api/channels/slack", map[string]any{
		"enabled": false, "values": map[string]string{"channel": "#созвоны"},
	})
	fresh := core.DefaultConfig()
	fresh.Slack.Enabled = true
	if err := core.ApplyChannels(st, fresh); err != nil {
		t.Fatal(err)
	}
	if fresh.Slack.Enabled {
		t.Error("канал, выключенный в панели, снова включился из конфига")
	}

	if code, _ := a.do("POST", "/api/channels/выдумка", map[string]any{}); code != 404 {
		t.Errorf("несуществующий канал принят, код %d", code)
	}
}

// Поля, которых в форме больше нет, панель не показывает — но и не теряет. У
// компании в них лежит путь к ключу организации: потерять его, правя соседнюю
// галочку, значит остаться без Google после первой же правки в панели.
func TestPanelKeepsHiddenChannelFields(t *testing.T) {
	srv, st, p := testPanel(t)
	a := login(t, srv, "тайна")

	p.cfg.GoogleDocs.Enabled = true
	p.cfg.GoogleDocs.CredentialsFile = "/секреты/ключ-организации.json"
	p.cfg.GoogleDocs.Subject = "notes@example.com"
	if _, err := core.ImportChannels(st, p.cfg); err != nil {
		t.Fatal(err)
	}
	// Так это лежит у тех, кто задал путь через панель прошлой версии.
	if err := st.SaveChannel("google_docs", true, map[string]string{
		"folder_id":        "1AbCпапка",
		"credentials_file": "/секреты/другой-ключ.json",
		"subject":          "notes@example.com",
	}); err != nil {
		t.Fatal(err)
	}

	// Человек меняет папку — единственное, что он в этой форме видит.
	if code, body := a.do("POST", "/api/channels/google_docs", map[string]any{
		"enabled": true,
		"values":  map[string]string{"folder_id": "1XyZновая", "project_docs": "1"},
	}); code != 200 {
		t.Fatalf("сохранение канала: %d %s", code, body)
	}

	live := core.ActiveChannels(st, p.cfg)
	if live.GoogleDocs.FolderID != "1XyZновая" {
		t.Errorf("папка не сохранилась: %q", live.GoogleDocs.FolderID)
	}
	if live.GoogleDocs.CredentialsFile != "/секреты/другой-ключ.json" {
		t.Errorf("ключ организации потерян при правке соседнего поля: %q",
			live.GoogleDocs.CredentialsFile)
	}
	if live.GoogleDocs.Subject != "notes@example.com" {
		t.Errorf("«от чьего имени» потеряно: %q", live.GoogleDocs.Subject)
	}
}

// --- выбор папки --------------------------------------------------------------

// fakeHome собирает домашний каталог, в котором есть всё, что панель обязана
// различать: репозиторий, скрытая папка, скрытый репозиторий, файл, ссылка
// наружу и папка, куда не пускают.
func fakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for _, d := range []string{"work", "work/payments", "work/payments/.git",
		".dotfiles", ".dotfiles/.git", ".cache"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "work", "заметки.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc", filepath.Join(home, "наружу")); err != nil {
		t.Fatal(err)
	}
	closed := filepath.Join(home, "закрытая")
	if err := os.Mkdir(closed, 0o000); err != nil {
		t.Fatal(err)
	}
	// Иначе уборка после теста споткнётся о собственную же папку.
	t.Cleanup(func() { os.Chmod(closed, 0o755) })
	t.Setenv("HOME", home)
	real, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

type browseAnswer struct {
	Path   string `json:"path"`
	Parent string `json:"parent"`
	Dirs   []struct {
		Name   string `json:"name"`
		Path   string `json:"path"`
		IsRepo bool   `json:"isRepo"`
	} `json:"dirs"`
}

func (b browseAnswer) names() []string {
	var out []string
	for _, d := range b.Dirs {
		out = append(out, d.Name)
	}
	return out
}

func TestPanelBrowse(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("под root права на каталоги ничего не значат")
	}
	home := fakeHome(t)
	srv, _, _ := testPanel(t)
	a := login(t, srv, "тайна")

	var top browseAnswer
	a.get("/api/browse", &top)
	if top.Path != home {
		t.Errorf("пустой путь привёл не домой: %q, ждали %q", top.Path, home)
	}
	if top.Parent != "" {
		t.Errorf("выше дома есть куда идти: %q", top.Parent)
	}
	got := strings.Join(top.names(), " ")
	if !strings.Contains(got, "work") {
		t.Errorf("обычная папка не показана: %v", top.names())
	}
	// Скрытая папка, которая сама и есть репозиторий, — то, что человек ищет.
	if !strings.Contains(got, ".dotfiles") {
		t.Errorf("скрытый репозиторий не показан: %v", top.names())
	}
	for _, hidden := range []string{".cache", "закрытая", "наружу", "заметки.txt"} {
		if strings.Contains(got, hidden) {
			t.Errorf("показано лишнее (%s): %v", hidden, top.names())
		}
	}

	var work browseAnswer
	a.get("/api/browse?path="+url.QueryEscape(filepath.Join(home, "work")), &work)
	if work.Parent != home {
		t.Errorf("наверх ведёт не домой: %q", work.Parent)
	}
	if len(work.Dirs) != 1 || work.Dirs[0].Name != "payments" || !work.Dirs[0].IsRepo {
		t.Fatalf("репозиторий не отмечен: %+v", work.Dirs)
	}
	if work.Dirs[0].Path != filepath.Join(home, "work", "payments") {
		t.Errorf("путь папки: %q", work.Dirs[0].Path)
	}
}

// Наружу из дома — никак. Панель может стоять и на сервере компании, и обзор
// всей файловой системы там означает /etc через браузер.
func TestPanelBrowseStaysHome(t *testing.T) {
	home := fakeHome(t)
	srv, _, _ := testPanel(t)
	a := login(t, srv, "тайна")

	for _, path := range []string{
		"/etc",
		"..",
		"../../..",
		filepath.Join(home, ".."),
		filepath.Join(home, "work", "..", "..", "etc"),
		// Ссылка наружу выглядит как обычная папка внутри дома и прошла бы
		// любую проверку по строке.
		filepath.Join(home, "наружу"),
		filepath.Dir(home),
	} {
		code, body := a.do("GET", "/api/browse?path="+url.QueryEscape(path), nil)
		if code != 400 {
			t.Errorf("выпустило наружу по пути %q: код %d, тело %s", path, code, body)
		}
	}
	// А внутри дома всё по-прежнему открывается, в том числе «..» обратно в дом.
	var back browseAnswer
	a.get("/api/browse?path="+url.QueryEscape(filepath.Join(home, "work", "..")), &back)
	if back.Path != home {
		t.Errorf("путь внутри дома не открылся: %q", back.Path)
	}
}

// --- расписание и приглашение -----------------------------------------------

func TestPanelSchedule(t *testing.T) {
	srv, st, _ := testPanel(t)
	a := login(t, srv, "тайна")

	soon := time.Now().Add(2 * time.Hour)
	if err := st.SaveScheduled(core.ScheduleEntry{
		Key: "k1", CalendarID: "user@example.com", Title: "Планёрка",
		MeetURL: "https://meet.google.com/abc-defg-hij", StartsAt: soon,
		Attendees: []string{"Участник Б", "Участник А"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveScheduled(core.ScheduleEntry{
		Key: "k2", Title: "Один на один", StartsAt: soon.Add(time.Hour),
		Attendees: []string{"Участник Б"}, Skip: "участников 1, нужно хотя бы 2",
	}); err != nil {
		t.Fatal(err)
	}

	var page struct {
		Entries []struct {
			Key        string `json:"key"`
			Skip       string `json:"skip"`
			Override   string `json:"override"`
			WillAttend bool   `json:"willAttend"`
		} `json:"entries"`
	}
	a.get("/api/schedule", &page)
	if len(page.Entries) != 2 {
		t.Fatalf("расписание: %+v", page.Entries)
	}
	if !page.Entries[0].WillAttend {
		t.Error("на нормальную встречу бот собирается не идти")
	}
	// Причина названа словами: молчаливое «бот не пришёл» разбирать невозможно.
	if page.Entries[1].WillAttend || page.Entries[1].Skip == "" {
		t.Errorf("пропуск без причины: %+v", page.Entries[1])
	}

	// «Не ходить» должно переживать пересборку расписания и останавливать бота.
	if code, body := a.do("POST", "/api/schedule/k1/override",
		map[string]string{"decision": "skip"}); code != 200 {
		t.Fatalf("отмена похода: %d %s", code, body)
	}
	if got := st.ScheduleOverride("k1"); got != "skip" {
		t.Errorf("решение не сохранилось: %q", got)
	}
	a.get("/api/schedule", &page)
	if page.Entries[0].WillAttend {
		t.Error("после «не ходить» бот всё равно собирается идти")
	}

	// Снятие решения возвращает всё как в календаре.
	a.do("POST", "/api/schedule/k1/override", map[string]string{"decision": ""})
	a.get("/api/schedule", &page)
	if !page.Entries[0].WillAttend {
		t.Error("решение не снялось")
	}

	if code, _ := a.do("POST", "/api/schedule/k1/override",
		map[string]string{"decision": "может быть"}); code != 400 {
		t.Errorf("непонятное решение принято, код %d", code)
	}
}

// Три исхода приглашения различаются словами: человеку, чей созвон пропустили
// из-за нехватки слотов, нельзя отвечать «уже иду».
func TestPanelInvite(t *testing.T) {
	srv, st, p := testPanel(t)
	a := login(t, srv, "тайна")

	// Без сервиса звать некому — но и падать не за что.
	if code, _ := a.do("POST", "/api/invite",
		map[string]string{"url": "https://meet.google.com/abc-defg-hij"}); code != 503 {
		t.Errorf("без диспетчера ответили %d", code)
	}

	d := sources.NewDispatcher(p.cfg, st, log.New(io.Discard, "", 0))
	// Подменяем ДО первого Start: по умолчанию здесь RecordAndProcess, который
	// поднимает docker или настоящий Chromium. Тесту это не нужно никогда.
	went := make(chan *core.Meeting, 4)
	d.Run = func(_ context.Context, _ *core.Config, _ *core.Store, m *core.Meeting) error {
		went <- m
		return nil
	}
	p.D = d

	var res struct {
		Status    string `json:"status"`
		MeetingID string `json:"meetingId"`
		Message   string `json:"message"`
	}
	code, body := a.do("POST", "/api/invite", map[string]string{
		"url": "https://meet.google.com/abc-defg-hij", "title": "Разбор инцидента"})
	if code != 200 {
		t.Fatalf("приглашение: %d %s", code, body)
	}
	json.Unmarshal(body, &res)
	if res.Status != "started" || res.MeetingID == "" {
		t.Fatalf("первый заход: %+v", res)
	}
	select {
	case m := <-went:
		if m.Title != "Разбор инцидента" {
			t.Errorf("название не доехало до записи: %q", m.Title)
		}
		if m.MeetURL != "https://meet.google.com/abc-defg-hij" {
			t.Errorf("ссылка не доехала: %q", m.MeetURL)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("бот так и не пошёл на созвон")
	}

	// Повторный заход на тот же созвон — «уже иду», а не второй бот в звонке.
	_, body = a.do("POST", "/api/invite",
		map[string]string{"url": "https://meet.google.com/abc-defg-hij"})
	json.Unmarshal(body, &res)
	if res.Status != "duplicate" {
		t.Errorf("повторное приглашение: %+v", res)
	}

	// Мусор вместо ссылки — отказ, а не поход бота по чужому адресу.
	if code, _ := a.do("POST", "/api/invite",
		map[string]string{"url": "https://evil.example/?x=meet.google.com"}); code != 400 {
		t.Errorf("чужая ссылка принята, код %d", code)
	}
}

// Раздел «Разбор» правится в панели тем же путём, что и каналы, — и это
// главное, ради чего он тут: владельцу steno не должно быть нужно лезть в JSON,
// чтобы переехать с Claude на свою модель.
//
// Проверяем всю дорогу: что раздел доезжает до панели, что он не канал, что
// секретов в нём нет и что сохранённое доходит до сервиса.
func TestPanelBrainSection(t *testing.T) {
	srv, st, p := testPanel(t)
	a := login(t, srv, "тайна")

	if _, err := core.ImportChannels(st, p.cfg); err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Channels []core.Channel `json:"channels"`
	}
	a.get("/api/settings", &settings)
	var brain *core.Channel
	for i := range settings.Channels {
		if settings.Channels[i].Key == "brain" {
			brain = &settings.Channels[i]
		}
	}
	if brain == nil {
		t.Fatal("раздела «Разбор» нет в настройках — провайдера не выбрать иначе как в JSON")
	}
	if brain.Kind != core.ChannelKindBrain {
		t.Errorf("раздел приехал каналом (%q) — панель нарисует ему выключатель", brain.Kind)
	}
	if brain.In || brain.Out {
		t.Error("«Разбор» не приносит созвоны и не уносит follow-up")
	}
	// Умолчание — Claude: тот, кто ничего не выбирал, остаётся с прежним.
	if brain.Values["provider"] != core.ProviderClaude {
		t.Errorf("умолчание провайдера: %q", brain.Values["provider"])
	}
	// Выбор провайдера должен быть выбором, а не текстовым полем: владелец
	// хочет нажимать, а не вспоминать, как пишется «openai».
	var provider *core.ChannelField
	for i := range brain.Fields {
		if brain.Fields[i].Key == "provider" {
			provider = &brain.Fields[i]
		}
	}
	if provider == nil || provider.Kind != "select" {
		t.Fatalf("провайдер выбирается не списком: %+v", provider)
	}
	// Все четыре пути должны быть в списке: список — единственное место, где
	// человек про них узнаёт, и путь, не попавший сюда, для него не существует.
	have := map[string]bool{}
	for _, o := range provider.Options {
		have[o.Value] = true
		if o.Label == "" {
			t.Errorf("вариант %q без подписи", o.Value)
		}
	}
	for _, want := range []string{core.ProviderClaude, core.ProviderOpenAI,
		core.ProviderCodex, core.ProviderCommand} {
		if !have[want] {
			t.Errorf("в списке нет провайдера %q", want)
		}
	}
	// Имени переменной с ключом здесь быть не должно ни в поле, ни в значении.
	for _, f := range brain.Fields {
		if strings.Contains(f.Key, "key") || strings.Contains(f.Key, "secret") {
			t.Errorf("в форме «Разбора» завелось поле про секрет: %q", f.Key)
		}
	}

	// Правка через панель.
	if code, body := a.do("POST", "/api/channels/brain", map[string]any{
		"enabled": true,
		"values": map[string]string{
			"provider": core.ProviderOpenAI, "preset": "groq",
			"model": "openai/gpt-oss-120b", "json_mode": "auto",
		},
	}); code != 200 {
		t.Fatalf("сохранение раздела: %d %s", code, body)
	}
	fresh := core.DefaultConfig()
	if err := core.ApplyChannels(st, fresh); err != nil {
		t.Fatal(err)
	}
	if fresh.BrainProvider() != core.ProviderOpenAI {
		t.Fatalf("провайдер из панели не доехал до сервиса: %q", fresh.BrainProvider())
	}
	if got := fresh.OpenAIEndpoint(); got != "https://api.groq.com/openai/v1/chat/completions" {
		t.Errorf("адрес: %q", got)
	}
	if got := fresh.OpenAIKeyEnv(); got != "GROQ_API_KEY" {
		t.Errorf("имя переменной с ключом подставилось не по заготовке: %q", got)
	}
}

// Имя переменной с ключом в панели не показывается — и потому не должно из неё
// же и стираться. Иначе правка модели молча оставляла бы steno без ключа.
func TestPanelKeepsBrainKeyEnv(t *testing.T) {
	srv, st, p := testPanel(t)
	a := login(t, srv, "тайна")

	p.cfg.Brain.Provider = core.ProviderOpenAI
	p.cfg.Brain.OpenAI.BaseURL = "https://llm.example.com/v1"
	p.cfg.Brain.OpenAI.Model = "старая"
	p.cfg.Brain.OpenAI.APIKeyEnv = "МОЙ_ОСОБЫЙ_КЛЮЧ"
	if _, err := core.ImportChannels(st, p.cfg); err != nil {
		t.Fatal(err)
	}

	if code, body := a.do("POST", "/api/channels/brain", map[string]any{
		"enabled": true,
		"values": map[string]string{
			"provider": core.ProviderOpenAI,
			"base_url": "https://llm.example.com/v1", "model": "новая", "json_mode": "auto",
		},
	}); code != 200 {
		t.Fatalf("сохранение: %d %s", code, body)
	}

	fresh := core.DefaultConfig()
	fresh.Brain.OpenAI.APIKeyEnv = "МОЙ_ОСОБЫЙ_КЛЮЧ" // так это лежит в конфиге на диске
	if err := core.ApplyChannels(st, fresh); err != nil {
		t.Fatal(err)
	}
	if fresh.BrainModel() != "новая" {
		t.Errorf("правка модели не доехала: %q", fresh.BrainModel())
	}
	if fresh.OpenAIKeyEnv() != "МОЙ_ОСОБЫЙ_КЛЮЧ" {
		t.Errorf("правка в панели стёрла имя переменной с ключом: %q", fresh.OpenAIKeyEnv())
	}
}
