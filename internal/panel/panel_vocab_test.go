package panel

import (
	"strings"
	"testing"

	"github.com/sur1cat/steno/internal/core"
)

// --- панель ------------------------------------------------------------------

func TestPanelSavesAndReturnsVocabulary(t *testing.T) {
	srv, st, _ := testPanel(t)
	a := login(t, srv, "тайна")

	code, body := a.do("POST", "/api/projects", map[string]any{
		"name":       "Платежи",
		"about":      "приём денег",
		"aliases":    []string{"биллинг"},
		"sources":    []core.Source{},
		"people":     []string{"Орынгали"},
		"vocabulary": []string{"Сапар"},
	})
	if code != 200 {
		t.Fatalf("сохранение проекта: код %d, тело %s", code, body)
	}

	p, err := st.Project("Платежи")
	if err != nil {
		t.Fatal(err)
	}
	if !hasWord(p.People, "Орынгали") || !hasWord(p.Vocabulary, "Сапар") {
		t.Fatalf("панель не донесла словарь до базы: %+v", p)
	}
	if !hasWord(p.Vocabulary, "Орынгали") {
		t.Errorf("имя из панели не попало в словарь whisper: %v", p.Vocabulary)
	}

	var got struct {
		Projects []struct {
			Name       string   `json:"name"`
			People     []string `json:"people"`
			Vocabulary []string `json:"vocabulary"`
		} `json:"projects"`
	}
	a.get("/api/settings", &got)
	if len(got.Projects) != 1 {
		t.Fatalf("настройки вернули %d проектов", len(got.Projects))
	}
	x := got.Projects[0]
	if !hasWord(x.People, "Орынгали") {
		t.Errorf("панель не показывает людей: %+v", x)
	}
	// Форме словарь отдаётся без имён: иначе в двух полях подряд одно и то же.
	if hasWord(x.Vocabulary, "Орынгали") {
		t.Errorf("имя человека продублировалось в поле словаря: %v", x.Vocabulary)
	}
	if !hasWord(x.Vocabulary, "Сапар") {
		t.Errorf("панель потеряла слова проекта: %v", x.Vocabulary)
	}
}

func hasWord(words []string, want string) bool {
	for _, w := range words {
		if strings.EqualFold(w, want) {
			return true
		}
	}
	return false
}
