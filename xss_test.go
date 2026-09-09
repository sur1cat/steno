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

// Расшифровка и названия встреч приходят от людей и из чужих сообщений.
// Ничто из этого не должно доехать до браузера как разметка.
func TestSearchEscapesUserContent(t *testing.T) {
	t.Setenv("STENO_PANEL_PASSWORD", "тайна")
	dir := t.TempDir()
	st, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := defaultConfig()
	cfg.DataDir = dir
	p, _ := newPanel(cfg, st, log.New(io.Discard, "", 0))
	srv := httptest.NewServer(p.handler())
	defer srv.Close()

	if err := st.CreateMeeting(&Meeting{ID: "x1",
		Title:     `релизный <script>alert(2)</script> план`,
		MeetURL:   "https://meet.google.com/abc-defg-hij",
		StartedAt: time.Now(), Status: "published"}); err != nil {
		t.Fatal(err)
	}
	st.SaveSegments("x1", []Segment{{Start: 1, End: 4, Speaker: "Аня",
		Text: `говорим про миграцию <img src=x onerror=alert(1)>`}})
	st.SaveFollowup("x1", "t", &Followup{Title: `релизный <script>alert(2)</script> план`,
		TLDR: []string{"перенос"}})

	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	resp, _ := c.PostForm(srv.URL+"/login", url.Values{"password": {"тайна"}, "next": {"/"}})
	resp.Body.Close()
	r, err := c.Get(srv.URL + "/search?q=миграц")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	body := string(b)

	// Проверяем ровно то, что подсунули, а не теги вообще: в странице есть
	// собственный <svg> в иконке. «onerror=alert» как экранированный текст
	// безвреден — это просто буквы.
	for _, bad := range []string{
		`<img src=x`,
		`<script>alert(2)</script>`,
	} {
		if strings.Contains(body, bad) {
			t.Errorf("в страницу поиска утёк живой HTML: %q", bad)
		}
	}
	if !strings.Contains(body, "&lt;img") {
		t.Error("текст не экранирован вовсе")
	}
	if !strings.Contains(body, "<mark>") {
		t.Error("подсветка найденного пропала")
	}
}
