package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Панель — это JSON-API плюс собранный бандл. Проверяем API: страницы рисует
// браузер, и требовать от Go определённой разметки больше нельзя — да и не
// нужно, вся правда о данных здесь.

func testPanel(t *testing.T) (*httptest.Server, *Store, *Panel) {
	t.Helper()
	t.Setenv("STENO_PANEL_PASSWORD", "тайна")
	dir := t.TempDir()
	st, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	cfg := defaultConfig()
	cfg.DataDir = dir
	p, err := newPanel(cfg, st, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(p.handler())
	t.Cleanup(srv.Close)
	return srv, st, p
}

func seedOne(t *testing.T, st *Store) {
	t.Helper()
	m := &Meeting{ID: "m1", Title: "Планёрка по релизу",
		MeetURL:   "https://meet.google.com/abc-defg-hij",
		StartedAt: time.Now().Add(-2 * time.Hour), Status: "published",
		Participants: []string{"Аня", "Боря"}}
	if err := st.CreateMeeting(m); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveSegments("m1", []Segment{
		{Start: 10, End: 15, Speaker: "Аня", Text: "что там с миграцией схемы"},
		{Start: 20, End: 28, Speaker: "Боря", Text: "закончу к четвергу, прогоню на стейджинге"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveFollowup("m1", "test", &Followup{
		Title: "Релиз сдвинули",
		TLDR:  []string{"Релиз переносится на пятницу"},
		ActionItems: []ActionItem{
			{Owner: "Боря", What: "закончить миграцию", Due: "2020-01-01", At: 20},
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
		if h.Speaker == "Аня" {
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
	if len(res.Owners) == 0 || res.Owners[0] != unassigned {
		t.Errorf("порядок людей в фильтре: %v", res.Owners)
	}

	// Фильтр по человеку.
	var mine struct {
		Tasks []struct {
			Owner string `json:"owner"`
		} `json:"tasks"`
	}
	a.get("/api/tasks?owner="+"%D0%91%D0%BE%D1%80%D1%8F", &mine) // Боря
	if len(mine.Tasks) != 1 || mine.Tasks[0].Owner != "Боря" {
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
		"sources": []Source{
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
		"sources": []Source{{Kind: "text", Value: "то же"}},
	})
	ps, _ := st.Projects()
	if len(ps) != 1 || ps[0].Name != "Платёжка" {
		t.Fatalf("после переименования: %+v", ps)
	}

	// Непонятный вид источника — внятный отказ, а не молчаливое проглатывание.
	if code, _ := a.do("POST", "/api/projects", map[string]any{
		"name": "Кривой", "sources": []Source{{Kind: "магия", Value: "что-то"}},
	}); code != 400 {
		t.Errorf("непонятный вид источника принят, код %d", code)
	}
	// Пустое значение — тоже отказ: источник без значения ничего не даст, а
	// выглядеть в списке будет настоящим.
	if code, _ := a.do("POST", "/api/projects", map[string]any{
		"name": "Кривой", "sources": []Source{{Kind: "url", Value: "  "}},
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
	if err := st.AddItem(ProjectItem{ID: "T-1", Project: "Платежи", Kind: KindTask,
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

// Панель не должна показывать значения секретов — только факт, задан ли.
func TestPanelSettingsHidesSecretValues(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-очень-секретный-ключ")
	srv, _, _ := testPanel(t)
	a := login(t, srv, "тайна")

	_, raw := a.do("GET", "/api/settings", nil)
	body := string(raw)
	if strings.Contains(body, "sk-ant-очень-секретный-ключ") {
		t.Fatal("панель отдала значение секрета")
	}
	if !strings.Contains(body, "ANTHROPIC_API_KEY") || !strings.Contains(body, `"set":true`) {
		t.Error("панель не показала, что ключ настроен")
	}
}

// Каналы живут в базе и правятся в панели. Проверяем всю дорогу: перенос из
// конфига, правку через API и то, что сервис берёт настройку из базы.
func TestPanelChannels(t *testing.T) {
	srv, st, p := testPanel(t)
	a := login(t, srv, "тайна")

	p.cfg.Slack.Enabled = true
	p.cfg.Slack.Channel = "#старый"
	if n, err := importChannels(st, p.cfg); err != nil || n == 0 {
		t.Fatalf("перенос из конфига: %d, %v", n, err)
	}

	var settings struct {
		Channels []Channel `json:"channels"`
	}
	a.get("/api/settings", &settings)
	var slack *Channel
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
	live := activeChannels(st, p.cfg)
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
	fresh := defaultConfig()
	fresh.Slack.Enabled = true
	if err := applyChannels(st, fresh); err != nil {
		t.Fatal(err)
	}
	if fresh.Slack.Enabled {
		t.Error("канал, выключенный в панели, снова включился из конфига")
	}

	if code, _ := a.do("POST", "/api/channels/выдумка", map[string]any{}); code != 404 {
		t.Errorf("несуществующий канал принят, код %d", code)
	}
}

// --- расписание и приглашение -----------------------------------------------

func TestPanelSchedule(t *testing.T) {
	srv, st, _ := testPanel(t)
	a := login(t, srv, "тайна")

	soon := time.Now().Add(2 * time.Hour)
	if err := st.SaveScheduled(ScheduleEntry{
		Key: "k1", CalendarID: "ivan@example.com", Title: "Планёрка",
		MeetURL: "https://meet.google.com/abc-defg-hij", StartsAt: soon,
		Attendees: []string{"Иван", "Аня"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveScheduled(ScheduleEntry{
		Key: "k2", Title: "Один на один", StartsAt: soon.Add(time.Hour),
		Attendees: []string{"Иван"}, Skip: "участников 1, нужно хотя бы 2",
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

	d := newDispatcher(p.cfg, st, log.New(io.Discard, "", 0))
	// Подменяем ДО первого Start: по умолчанию здесь recordAndProcess, который
	// поднимает docker или настоящий Chromium. Тесту это не нужно никогда.
	went := make(chan *Meeting, 4)
	d.run = func(_ context.Context, _ *Config, _ *Store, m *Meeting) error {
		went <- m
		return nil
	}
	p.d = d

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
