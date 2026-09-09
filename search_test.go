package main

import "testing"

// Поиск не должен приписывать чужие слова: кусок индекса обязан целиком
// принадлежать одному говорящему, иначе в результатах имя не совпадает с
// найденной репликой.
func TestSearchAttributesToRightSpeaker(t *testing.T) {
	dir := t.TempDir()
	st, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err := st.CreateMeeting(&Meeting{ID: "s1", Title: "Планёрка",
		MeetURL: "https://meet.google.com/abc-defg-hij", Status: "published"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveSegments("s1", []Segment{
		{Start: 10, End: 14, Speaker: "Аня", Text: "какие есть возражения"},
		{Start: 20, End: 26, Speaker: "Боря", Text: "я закончу миграцию к четвергу"},
		{Start: 30, End: 34, Speaker: "Вика", Text: "тогда письмо перепишу завтра"},
	}); err != nil {
		t.Fatal(err)
	}

	hits, err := st.Search("миграц", 10)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, h := range hits {
		if h.Kind != "расшифровка" {
			continue
		}
		found = true
		if h.Speaker != "Боря" {
			t.Errorf("слова Бори приписаны %q", h.Speaker)
		}
		if h.At != 20 {
			t.Errorf("таймкод %v, ожидали 20 — начало реплики Бори", h.At)
		}
	}
	if !found {
		t.Fatal("в расшифровке ничего не нашлось")
	}
}

// Ввод из поля поиска не должен ронять запрос: кавычки и дефисы в FTS5 — это
// синтаксис.
func TestFtsQuerySurvivesJunk(t *testing.T) {
	for _, in := range []string{`"кавычка`, "тире - тире", "NEAR(", "*", "a", ""} {
		if q := ftsQuery(in); q != "" && !contains(q, `"`) {
			t.Errorf("ftsQuery(%q) = %q — слова должны быть в кавычках", in, q)
		}
	}
	if got := ftsQuery("миграция схемы"); got != `"миграция"* AND "схемы"*` {
		t.Errorf("получили %q", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
