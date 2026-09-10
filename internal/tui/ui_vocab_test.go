package tui

import (
	"strings"
	"testing"
)

// --- терминальный интерфейс ----------------------------------------------------

// Тот же словарь правится из `steno ui`. Поле, добавленное только в панель,
// существует только в панели: человек, живущий в терминале, о нём не узнает.
func TestUIProjectFormKeepsVocabulary(t *testing.T) {
	m := uiTestModel(t, nil)
	press(m, "3", "n")

	typeIn(m, "Платежи")
	press(m, "tab")
	typeIn(m, "приём денег")
	press(m, "tab")
	typeIn(m, "биллинг")
	press(m, "tab")
	typeIn(m, "Орынгали, Рустем")
	press(m, "tab")
	typeIn(m, "Сапар, ЛК")
	press(m, "ctrl+s")

	if m.screen() != scrList {
		t.Fatalf("после сохранения остались на %v: %s", m.screen(), screen(m))
	}
	p, err := m.st.Project("Платежи")
	if err != nil {
		t.Fatalf("проект не сохранился: %v", err)
	}
	if strings.Join(p.People, ",") != "Орынгали,Рустем" {
		t.Errorf("люди из формы: %v", p.People)
	}
	if strings.Join(p.OtherWords(), ",") != "Сапар,ЛК" {
		t.Errorf("словарь из формы: %v", p.OtherWords())
	}
	if !hasWord(p.Vocabulary, "Орынгали") {
		t.Errorf("имя из формы не попало в словарь whisper: %v", p.Vocabulary)
	}

	// Открыли на правку: имена в своём поле, и они же не продублированы в
	// поле словаря — иначе стёртое в одном остаётся в другом.
	m.selectProject("Платежи")
	press(m, "e")
	if m.screen() != scrForm {
		t.Fatalf("e не открыла форму: %v", m.screen())
	}
	if m.form.people.String() != "Орынгали, Рустем" {
		t.Errorf("поле людей при правке: %q", m.form.people.String())
	}
	if strings.Contains(m.form.words.String(), "Орынгали") {
		t.Errorf("имя человека продублировалось в поле словаря: %q", m.form.words.String())
	}
	wantContains(t, screen(m), "кто участвует", "подпись поля с людьми")

	// И в карточке проекта словарь виден: править его можно там, где он
	// показан, а не там, где о нём надо догадаться.
	press(m, "esc", "enter")
	if m.screen() != scrProject {
		t.Fatalf("enter не открыл карточку: %v", m.screen())
	}
	body := bodyText(m)
	wantContains(t, body, "Орынгали", "люди в карточке проекта")
	wantContains(t, body, "Сапар", "слова проекта в карточке")
}

func hasWord(words []string, want string) bool {
	for _, w := range words {
		if strings.EqualFold(w, want) {
			return true
		}
	}
	return false
}
