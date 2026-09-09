package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Расшифровка и названия встреч приходят от людей и из чужих сообщений. Ничто
// из этого не должно доехать до браузера как разметка.
//
// С переездом на React защита стала устройством, а не привычкой: страницы
// собирает браузер, сервер отдаёт данные значениями, и вставить туда разметку
// неоткуда. Проверяем оба конца этого утверждения — что в HTML пользовательский
// текст не попадает вовсе и что в JSON он лежит как есть, без готовых тегов.
func TestPanelNeverEmitsUserHTML(t *testing.T) {
	srv, st, _ := testPanel(t)

	const evil = `релизный <script>alert(2)</script> план`
	if err := st.CreateMeeting(&Meeting{ID: "x1", Title: evil,
		MeetURL:   "https://meet.google.com/abc-defg-hij",
		StartedAt: time.Now(), Status: "published"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveSegments("x1", []Segment{{Start: 1, End: 4, Speaker: "Аня",
		Text: `говорим про миграцию <img src=x onerror=alert(1)>`}}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveFollowup("x1", "t", &Followup{Title: evil, TLDR: []string{"перенос"}}); err != nil {
		t.Fatal(err)
	}

	a := login(t, srv, "тайна")

	// Страница — статичный бандл, одинаковый для всех адресов. Название встречи
	// в неё не подставляется ничем и никогда.
	_, page := a.do("GET", "/m/x1", nil)
	for _, bad := range []string{"<script>alert(2)</script>", "<img src=x", "релизный"} {
		if strings.Contains(string(page), bad) {
			t.Errorf("пользовательский текст утёк в HTML: %q", bad)
		}
	}

	// В JSON текст лежит сырым — и это правильно: подставлять его в разметку
	// будет React, который экранирует всё, что не помечено разметкой явно.
	var res struct {
		Hits []struct {
			Parts []struct {
				Text string `json:"text"`
				Hit  bool   `json:"hit"`
			} `json:"parts"`
		} `json:"hits"`
	}
	_, raw := a.do("GET", "/api/search?q=миграц", nil)
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) == 0 {
		t.Fatal("поиск ничего не нашёл")
	}
	full, marked := "", false
	for _, p := range res.Hits[0].Parts {
		full += p.Text
		if p.Hit {
			marked = true
		}
	}
	if !marked {
		t.Error("подсветка найденного пропала")
	}
	if !strings.Contains(full, "<img src=x") {
		t.Errorf("текст пришёл изменённым: %q", full)
	}
	// Готовой разметки в ответе быть не должно: браузер получает данные, а не
	// куски чужой страницы.
	for _, bad := range []string{"<mark>", "\x02", "\x03"} {
		if strings.Contains(string(raw), bad) {
			t.Errorf("в JSON приехала разметка: %q", bad)
		}
	}
}
