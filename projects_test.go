package main

import (
	"io"
	"log"
	"strings"
	"testing"
)

func testProjects() []Project {
	return []Project{
		{Name: "Платежи", Aliases: []string{"биллинг", "payments"}, About: "приём денег"},
		{Name: "Онбординг", Aliases: []string{"регистрация"}, About: "первые экраны"},
	}
}

// Вслух проект называют не так, как он записан. Решать это должен список
// псевдонимов, а не то, что модель придумала в этот раз.
func TestMatchProject(t *testing.T) {
	ps := testProjects()
	for said, want := range map[string]string{
		"Платежи":     "Платежи",
		"биллинг":     "Платежи",
		"Payments":    "Платежи",
		"ПЛАТЕЖИ":     "Платежи",
		"регистрация": "Онбординг",
		"что-то ещё":  unassignedProject,
		"":            unassignedProject,
	} {
		if got := matchProject(ps, said); got != want {
			t.Errorf("matchProject(%q) = %q, ожидали %q", said, got, want)
		}
	}
}

// Главное в живом состоянии: следующий созвон закрывает то, что висело, а не
// заводит копию. Проверяем весь оборот.
func TestProjectStateLifecycle(t *testing.T) {
	dir := t.TempDir()
	st, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	cfg := defaultConfig()
	cfg.Projects = testProjects()

	// Первый созвон: две задачи по разным проектам.
	first := &Followup{
		ActionItems: []ActionItem{
			{Owner: "Участник Б", What: "закончить миграцию", Due: "2026-09-11",
				Project: "биллинг", Quote: "я закончу"},
			{Owner: "Участник Д", What: "прототип онбординга", Project: "Онбординг"},
		},
		OpenQuestions: []OpenQuestion{
			{Question: "кто дежурит", WaitingOn: "Участник А", Project: "непонятный"},
		},
	}
	for i := range first.ActionItems {
		first.ActionItems[i].Project = matchProject(cfg.Projects, first.ActionItems[i].Project)
	}
	for i := range first.OpenQuestions {
		first.OpenQuestions[i].Project = matchProject(cfg.Projects, first.OpenQuestions[i].Project)
	}
	added, closed, err := applyFollowup(st, cfg.Projects, "m1", first)
	if err != nil {
		t.Fatal(err)
	}
	if added != 3 || closed != 0 {
		t.Fatalf("добавлено %d, закрыто %d — ожидали 3 и 0", added, closed)
	}

	open, err := st.OpenItems("")
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 3 {
		t.Fatalf("открытых %d, ожидали 3", len(open))
	}
	// Псевдоним «биллинг» должен был схлопнуться в «Платежи».
	pay, err := st.OpenItems("Платежи")
	if err != nil {
		t.Fatal(err)
	}
	if len(pay) != 1 || pay[0].Text != "закончить миграцию" {
		t.Fatalf("по Платежам: %+v", pay)
	}
	// Непонятный проект не приписан чужому.
	if got := matchProject(cfg.Projects, "непонятный"); got != unassignedProject {
		t.Errorf("чужой проект приписан: %q", got)
	}

	// То, что уходит в промпт следующего созвона.
	prompt := renderOpenItems(open)
	if !strings.Contains(prompt, "Платежи:") || !strings.Contains(prompt, pay[0].ID) {
		t.Fatalf("в промпт не попали проект и идентификатор:\n%s", prompt)
	}
	if !strings.Contains(prompt, "закончить миграцию") {
		t.Error("в промпт не попал текст задачи")
	}

	// Второй созвон: задачу закрыли, ничего нового по ней не завели.
	second := &Followup{
		Updates: []ItemUpdate{{ID: pay[0].ID, Status: "done", Note: "прогнали на стейдже"}},
	}
	added, closed, err = applyFollowup(st, cfg.Projects, "m2", second)
	if err != nil {
		t.Fatal(err)
	}
	if added != 0 || closed != 1 {
		t.Fatalf("добавлено %d, закрыто %d — ожидали 0 и 1", added, closed)
	}

	if left, _ := st.OpenItems("Платежи"); len(left) != 0 {
		t.Fatalf("задача осталась открытой: %+v", left)
	}
	all, err := st.ProjectItems("Платежи")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Status != "done" {
		t.Fatalf("состояние: %+v", all)
	}
	if all[0].ClosedIn != "m2" || all[0].Note != "прогнали на стейдже" {
		t.Errorf("не записано, где и почему закрылось: %+v", all[0])
	}

	// Закрыть повторно нельзя — иначе повторный прогон process всё перепишет.
	if err := st.CloseItem(all[0].ID, "done", "ещё раз", "m3"); err != nil {
		t.Fatal(err)
	}
	again, _ := st.ProjectItems("Платежи")
	if again[0].ClosedIn != "m2" {
		t.Errorf("повторное закрытие перетёрло исходное: %+v", again[0])
	}
}

func TestTouchedProjects(t *testing.T) {
	dir := t.TempDir()
	st, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	f := &Followup{
		ActionItems:   []ActionItem{{What: "a", Project: "Платежи"}},
		OpenQuestions: []OpenQuestion{{Question: "q", Project: "Онбординг"}},
	}
	got := touchedProjects(f, st, "m1")
	if len(got) != 2 || got[0] != "Онбординг" || got[1] != "Платежи" {
		t.Fatalf("получили %v", got)
	}
}

func TestRenderProjectHTMLEscapes(t *testing.T) {
	items := []ProjectItem{
		{ID: "T-1", Kind: KindTask, Status: "open", Owner: "<b>Участник Б</b>",
			Text: "миграция <script>alert(1)</script>", Quote: "«цитата»"},
	}
	out := renderProjectHTML("Платежи & Co", items)
	if strings.Contains(out, "<script>alert(1)</script>") {
		t.Error("в документ проекта утёк живой HTML")
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Error("текст не экранирован")
	}
	if !strings.Contains(out, "Платежи &amp; Co") {
		t.Error("название проекта не экранировано")
	}
}

var _ = log.New(io.Discard, "", 0)
