package tui

import (
	"os"
	"testing"

	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/spec"
)

// t на задаче с готовым ТЗ открывает его карточку; из карточки a при
// выключенном исполнении отвечает словами, а не молчит; ← возвращает в список.
// Нижняя панель задачи говорит о ТЗ, не открывая его.
func TestUISpecCardOpensAndRefusesToRunWhenOff(t *testing.T) {
	// Настройка агента — не из окружения того, кто гоняет тесты: у него в
	// ~/steno исполнение может быть и включено.
	t.Setenv("STENO_CONFIG", t.TempDir()+"/нет.json")
	m := uiTestModel(t, uiSeed(t))
	store, err := spec.Open(m.st)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(&spec.Spec{
		ID: "S-0001", ItemID: "T-0001", Project: "Платежи", Repo: t.TempDir(), Status: spec.StatusDraft,
		Title:    "Миграция схемы",
		Places:   []spec.Place{{Path: "db/migrate.py", Why: "тут миграции", Found: true}},
		Steps:    []string{"добавить шаг миграции"},
		Unknowns: []spec.Unknown{{Question: "какая версия схемы?", Why: "не сказано", Ask: "Участник Б"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	press(m, "2")
	if it, ok := m.selectedItem(); !ok || it.ID != "T-0001" {
		t.Fatalf("под курсором не T-0001: %+v", it)
	}
	wantContains(t, screen(m), "S-0001", "нижняя панель называет ТЗ задачи")

	press(m, "t")
	if m.screen() != scrSpec {
		t.Fatalf("t не открыл карточку ТЗ, экран %v", m.screen())
	}
	body := bodyText(m)
	for _, want := range []string{"Миграция схемы", "db/migrate.py", "какая версия схемы?", "Чего не хватает"} {
		wantContains(t, body, want, "карточка ТЗ")
	}
	wantContains(t, screen(m), "агенту", "подсказка о клавише a")

	press(m, "a")
	if m.screen() != scrSpec {
		t.Fatalf("a при выключенном исполнении ушёл с карточки: экран %v", m.screen())
	}
	wantContains(t, screen(m), "steno agent on", "отказ объясняет, как включить")

	press(m, "left")
	if m.screen() != scrList || m.spec != nil {
		t.Fatalf("← не вернул в список: экран %v", m.screen())
	}
}

// Задача без проекта — ТЗ собрать нельзя, и говорится об этом сразу, без
// похода к модели. И на вопросе t не работает: исполнять в нём нечего.
func TestUISpecNeedsAProjectAndATask(t *testing.T) {
	m := uiTestModel(t, uiSeed(t))
	if err := m.st.AddItem(core.ProjectItem{ID: "T-0009", Project: core.UnassignedProject,
		Kind: core.KindTask, Text: "ничья задача", OpenedIn: uiTestMeeting}); err != nil {
		t.Fatal(err)
	}
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	press(m, "2")
	for i, x := range m.visibleItems() {
		if x.ID == "T-0009" {
			m.cursor[tabTasks] = i
		}
	}
	if cmd := press(m, "t"); cmd != nil {
		t.Fatal("по задаче без проекта ушли собирать ТЗ")
	}
	wantContains(t, screen(m), "не определён проект", "отказ объясняет причину")
	if len(m.specBuilding) != 0 {
		t.Fatal("сборка помечена идущей")
	}

	for i, x := range m.visibleItems() {
		if x.ID == "Q-0002" {
			m.cursor[tabTasks] = i
		}
	}
	if cmd := press(m, "t"); cmd != nil {
		t.Fatal("по вопросу ушли собирать ТЗ")
	}
	wantContains(t, screen(m), "не задача", "отказ по вопросу")
}

// Сборка идёт в фоне и по возвращении сообщает результат, а карточка
// перечитывается из базы — журнал исполнения растёт, пока агент работает.
func TestUISpecDoneMessagesAndReload(t *testing.T) {
	t.Setenv("STENO_CONFIG", t.TempDir()+"/нет.json")
	m := uiTestModel(t, uiSeed(t))
	m.specBuilding["T-0001"] = true
	m.Update(uiSpecDone{ItemID: "T-0001", Spec: &spec.Spec{ID: "S-x", Status: spec.StatusRejected, Reject: "это разговор"}})
	if m.specBuilding["T-0001"] {
		t.Fatal("сборка не снята")
	}
	wantContains(t, screen(m), "это разговор", "отказ модели виден в строке состояния")

	store, _ := spec.Open(m.st)
	sp := &spec.Spec{
		ID: "S-0002", ItemID: "T-0001", Project: "Платежи", Repo: t.TempDir(), Status: spec.StatusRunning,
		Title: "идёт", Branch: "steno/idet-1", RunBy: "тест", RunLog: "12:00:00 note первая строка",
		Places:   []spec.Place{{Path: "a.py", Found: true}},
		Steps:    []string{"сделать"},
		Unknowns: []spec.Unknown{{Question: "что?"}},
	}
	if err := store.Save(sp); err != nil {
		t.Fatal(err)
	}
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	press(m, "2", "t")
	if m.screen() != scrSpec {
		t.Fatalf("карточка не открылась: %v", m.screen())
	}
	wantContains(t, bodyText(m), "первая строка", "журнал исполнения в карточке")
	sp.RunLog += "\n12:00:03 do правит a.py"
	if err := store.Save(sp); err != nil {
		t.Fatal(err)
	}
	press(m, "r")
	wantContains(t, bodyText(m), "правит a.py", "r перечитал журнал")

	// При включённом исполнении по идущему ТЗ второй запуск всё равно не
	// даётся. Настройка — из файла, названного в конфиге, а не из окружения
	// того, кто гоняет тесты.
	cfgPath := t.TempDir() + "/steno.json"
	if err := os.WriteFile(cfgPath, []byte(`{"agent":{"enabled":true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	m.cfg.Path = cfgPath
	press(m, "a")
	wantContains(t, screen(m), "уже идёт работа", "по идущему ТЗ второй запуск не даётся")
}
