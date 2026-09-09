package main

import (
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func testPanel(t *testing.T) (*httptest.Server, *Store) {
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
	return srv, st
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

// Без входа архив созвонов не должен отдаваться никому.
func TestPanelRequiresLogin(t *testing.T) {
	srv, _ := testPanel(t)
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	for _, path := range []string{"/", "/tasks", "/search?q=релиз", "/m/m1", "/audio/m1"} {
		resp, err := c.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther {
			t.Errorf("%s отдался без входа: %d", path, resp.StatusCode)
		}
	}
}

func login(t *testing.T, srv *httptest.Server, pass string) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	resp, err := c.PostForm(srv.URL+"/login", url.Values{"password": {pass}, "next": {"/"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return c
}

func TestPanelPages(t *testing.T) {
	srv, st := testPanel(t)
	seedOne(t, st)
	c := login(t, srv, "тайна")

	get := func(path string) string {
		t.Helper()
		resp, err := c.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("%s: код %d", path, resp.StatusCode)
		}
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}

	list := get("/")
	if !strings.Contains(list, "Планёрка по релизу") {
		t.Error("созвона нет в списке")
	}
	if !strings.Contains(list, "2 задачи") {
		t.Error("счётчик задач не согласован по числу")
	}

	// Поиск идёт и по расшифровке, и по follow-up.
	found := get("/search?q=миграц")
	if !strings.Contains(found, "<mark>") {
		t.Error("поиск ничего не подсветил")
	}
	if !strings.Contains(found, "Аня") {
		t.Error("в результате нет говорящего")
	}

	one := get("/m/m1")
	for _, want := range []string{"Релиз сдвинули", "закончить миграцию",
		"закончу к четвергу", `id="t20"`} {
		if !strings.Contains(one, want) {
			t.Errorf("на странице созвона нет %q", want)
		}
	}

	tasks := get("/tasks")
	// «Не назначен» идёт первым: это то, что вообще ни на ком не висит.
	// Ищем именно заголовки групп — имя встречается ещё и в фильтре сверху.
	iNone := strings.Index(tasks, "<h2>не назначен</h2>")
	iBorya := strings.Index(tasks, "<h2>Боря</h2>")
	if iNone < 0 || iBorya < 0 || iNone > iBorya {
		t.Errorf("порядок групп неверный: не назначен=%d, Боря=%d", iNone, iBorya)
	}
	// Фильтр сверху должен идти в том же порядке, что и группы: иначе одного
	// человека приходится искать на странице в двух местах по разным правилам.
	_, rest, ok := strings.Cut(tasks, `<div class="owners">`)
	if !ok {
		t.Fatal("на странице нет фильтра по людям")
	}
	chips, _, _ := strings.Cut(rest, "</div>")
	fNone := strings.Index(chips, "не назначен")
	fBorya := strings.Index(chips, "Боря")
	if fNone < 0 || fBorya < 0 || fNone > fBorya {
		t.Errorf("фильтр отсортирован иначе, чем группы: не назначен=%d, Боря=%d", fNone, fBorya)
	}
	if !strings.Contains(tasks, "late") {
		t.Error("просроченная задача не подсвечена")
	}
}

func TestPanelWrongPassword(t *testing.T) {
	srv, _ := testPanel(t)
	c := login(t, srv, "не та")
	resp, err := c.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "Не тот пароль") && !strings.Contains(string(b), "name=\"password\"") {
		t.Error("с неверным паролем пустили внутрь")
	}
}
