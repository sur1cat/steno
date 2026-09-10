package tui

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/sur1cat/steno/internal/core"
)

// Под тестами терминала нет, и lipgloss по умолчанию выключает цвет совсем.
// Тогда проверялась бы не та отрисовка, которую увидит человек: ширина строк
// со стилями считается иначе, а подсветку найденного вообще не на чем поймать.
// Поэтому цвет включаем принудительно.
func TestMain(mn *testing.M) {
	lipgloss.SetColorProfile(termenv.ANSI)
	os.Exit(mn.Run())
}

// Терминальный интерфейс легко написать так, что он «вроде работает»: экран
// рисуется, клавиши нажимаются, а состояние при этом не меняется. Поэтому здесь
// проверяется не картинка, а цепочка «нажали клавишу → вот что стало в модели →
// вот что из этого видно на экране», и отдельно — что видно на пустой базе и на
// узком терминале.

// --- обвязка -----------------------------------------------------------------

func uiKey(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "home":
		return tea.KeyMsg{Type: tea.KeyHome}
	case "end":
		return tea.KeyMsg{Type: tea.KeyEnd}
	case "pgup":
		return tea.KeyMsg{Type: tea.KeyPgUp}
	case "pgdown":
		return tea.KeyMsg{Type: tea.KeyPgDown}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "delete":
		return tea.KeyMsg{Type: tea.KeyDelete}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	case "ctrl+s":
		return tea.KeyMsg{Type: tea.KeyCtrlS}
	case "ctrl+n":
		return tea.KeyMsg{Type: tea.KeyCtrlN}
	case "ctrl+k":
		return tea.KeyMsg{Type: tea.KeyCtrlK}
	case "ctrl+t":
		return tea.KeyMsg{Type: tea.KeyCtrlT}
	case "ctrl+w":
		return tea.KeyMsg{Type: tea.KeyCtrlW}
	case "ctrl+u":
		return tea.KeyMsg{Type: tea.KeyCtrlU}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// press отправляет клавиши по одной и возвращает последнюю непустую команду:
// по ней видно, попросил ли интерфейс выйти или запустить фоновую работу.
func press(m *uiModel, keys ...string) tea.Cmd {
	var last tea.Cmd
	for _, k := range keys {
		if _, cmd := m.Update(uiKey(k)); cmd != nil {
			last = cmd
		}
	}
	return last
}

// tabTo доводит курсор формы до нужного поля так же, как человек, — tab-ами.
// Считать нажатия числом нельзя: полей в форме со временем прибавляется, и
// тест, помнящий их количество, ломается не там, где ошибка.
func tabTo(m *uiModel, field int) {
	for i := 0; i <= m.form.fieldCount() && m.form.field != field; i++ {
		press(m, "tab")
	}
}

// typeIn набирает текст так же, как человек: по одной руне за нажатие.
func typeIn(m *uiModel, s string) {
	for _, r := range s {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

// screen — то, что человек видит, без управляющих последовательностей.
func screen(m *uiModel) string { return ansi.Strip(m.View()) }

func bodyText(m *uiModel) string { return ansi.Strip(strings.Join(m.body, "\n")) }

func uiTestModel(t *testing.T, seed func(*core.Store)) *uiModel {
	t.Helper()
	st, err := core.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if seed != nil {
		seed(st)
	}
	m := NewUIModel(core.DefaultConfig(), st)
	m.w, m.h = 110, 24
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	return m
}

const uiTestMeeting = "2026-09-08-1100-a1b2"

// uiSeed кладёт в базу один разобранный созвон и живое состояние двух проектов:
// заведённого и такого, который есть только в задачах.
func uiSeed(t *testing.T) func(*core.Store) {
	t.Helper()
	return func(st *core.Store) {
		must := func(err error) {
			t.Helper()
			if err != nil {
				t.Fatal(err)
			}
		}
		must(st.SaveProject(core.Project{
			Name: "Платежи", About: "приём денег и подписки",
			Aliases: []string{"биллинг", "payments"},
			Sources: []core.Source{{Kind: "repo", Value: "git@github.com:org/pay"}},
		}))

		started := time.Date(2026, 9, 8, 11, 0, 0, 0, time.Local)
		ended := started.Add(47 * time.Minute)
		must(st.CreateMeeting(&core.Meeting{
			ID: uiTestMeeting, Title: "Планёрка по релизу 2.4",
			MeetURL: "https://meet.example/abc", StartedAt: started, Status: "recording",
		}))
		must(st.FinishMeeting(uiTestMeeting, started, ended,
			[]string{"Участник А", "Участник Б"}, "published", "", "остался один"))
		must(st.SaveSegments(uiTestMeeting, []core.Segment{
			{Start: 12, End: 30, Speaker: "Участник А", Text: "Давайте начнём с релиза."},
			{Start: 31, End: 58, Speaker: "Участник Б", Text: "Миграция схемы не успевает до среды."},
			{Start: 300, End: 340, Speaker: "Участник А", Text: "Тогда двигаем дату."},
			{Start: 412, End: 440, Speaker: "Участник Б", Text: "Я закончу миграцию к четвергу."},
		}))
		must(st.SaveFollowup(uiTestMeeting, "claude", &core.Followup{
			Title: "Релиз 2.4 сдвинули на пятницу",
			TLDR:  []string{"Релиз переносится со среды на пятницу."},
			ActionItems: []core.ActionItem{
				{Owner: "Участник Б", What: "закончить миграцию схемы", Due: "2026-09-11",
					At: 412, Project: "Платежи", Quote: "я закончу миграцию к четвергу"},
			},
			Decisions:     []core.Decision{{What: "релиз в пятницу", Why: "миграция не успевает", At: 380}},
			OpenQuestions: []core.OpenQuestion{{Question: "кто дежурит в выходные", WaitingOn: "Участник А", At: 1890}},
			Risks:         []string{"Откат миграции не проверялся на объёме прода."},
			Timeline:      []core.TimelineItem{{At: 10, Title: "Релиз", Summary: "обсудили дату"}},
		}))

		must(st.AddItem(core.ProjectItem{ID: "T-0001", Project: "Платежи", Kind: core.KindTask,
			Text: "закончить миграцию схемы", Owner: "Участник Б", Due: "2026-09-11",
			OpenedIn: uiTestMeeting}))
		must(st.AddItem(core.ProjectItem{ID: "Q-0002", Project: "Платежи", Kind: core.KindQuestion,
			Text: "кто дежурит в выходные", Owner: "Участник А", OpenedIn: uiTestMeeting}))
		must(st.AddItem(core.ProjectItem{ID: "T-0003", Project: "Онбординг", Kind: core.KindTask,
			Text: "прототип первых экранов", Owner: "Участник Д", Due: "2026-09-20",
			OpenedIn: uiTestMeeting}))
		must(st.AddItem(core.ProjectItem{ID: "T-0004", Project: "Платежи", Kind: core.KindTask,
			Text: "старая задача", Owner: "Участник А", OpenedIn: uiTestMeeting}))
		must(st.CloseItem("T-0004", "done", "сделано раньше", uiTestMeeting))
	}
}

func itemStatus(t *testing.T, st *core.Store, id string) string {
	t.Helper()
	items, err := st.ProjectItems("Платежи")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.ID == id {
			return it.Status
		}
	}
	return "нет такого"
}

func wantContains(t *testing.T, where, what, why string) {
	t.Helper()
	if !strings.Contains(where, what) {
		t.Errorf("%s: не нашёл %q в\n%s", why, what, where)
	}
}

func wantNotContains(t *testing.T, where, what, why string) {
	t.Helper()
	if strings.Contains(where, what) {
		t.Errorf("%s: не ждали %q, а оно есть в\n%s", why, what, where)
	}
}

// --- пустая установка --------------------------------------------------------

// Свежая установка — первое, что человек видит, а не краевой случай. Пустой
// экран без объяснения читается как поломка; на этом уже ловились `steno
// projects` (падал паникой) и `steno list` (молчал в пустоту).
func TestUIEmptyInstallExplainsEveryTab(t *testing.T) {
	m := uiTestModel(t, nil)

	for _, c := range []struct {
		key, want string
	}{
		{"1", "Созвонов пока нет"},
		{"2", "Задач пока нет"},
		{"3", "Проектов нет"},
		{"4", "Поиск по всему"},
		{"5", "Календарь"}, // каналы есть всегда: их список задан в коде
	} {
		if m.editing() { // с поиска цифрой не уйти: цифра — часть запроса
			press(m, "esc")
		}
		press(m, c.key)
		s := screen(m)
		wantContains(t, s, c.want, "пустая база, вкладка "+c.key)
		// Подсказка по клавишам обязана быть на экране всегда.
		wantContains(t, s, "?", "подсказка на пустой вкладке "+c.key)
	}
}

// Действия над пустым списком не должны ронять интерфейс: курсор стоять
// некуда, и «открыть выбранное» здесь означает «выбранного нет».
func TestUIEmptyInstallSurvivesEveryKey(t *testing.T) {
	m := uiTestModel(t, nil)
	for _, tab := range []string{"1", "2", "3", "4", "5"} {
		for _, k := range []string{"enter", "d", "x", "u", "t", "e", "D", "s", "n", " ",
			"j", "k", "g", "G", "pgdown", "pgup", "a", "c", "p", "o", "v", "r"} {
			// Каждая клавиша проверяется с чистого листа: иначе первая же,
			// открывшая карточку, съедает все следующие.
			press(m, "esc", "esc", "esc")
			if m.editing() {
				press(m, "esc")
			}
			press(m, tab)
			if m.editing() { // на поиске курсор сразу в поле ввода
				press(m, "esc")
			}
			press(m, k)
			if s := screen(m); strings.TrimSpace(s) == "" {
				t.Fatalf("вкладка %s, клавиша %q: пустой экран", tab, k)
			}
		}
	}
	press(m, "esc", "esc", "esc")
	if m.editing() {
		press(m, "esc")
	}
	// «n» на вкладке проектов открыла форму — из неё надо уметь выйти.
	press(m, "3", "n")
	if m.screen() != scrForm {
		t.Fatalf("n не открыла форму, экран %v", m.screen())
	}
	press(m, "esc")
	if m.screen() != scrList {
		t.Fatalf("esc не вернул к списку, экран %v", m.screen())
	}
}

// --- навигация ---------------------------------------------------------------

func TestUITabsSwitch(t *testing.T) {
	m := uiTestModel(t, uiSeed(t))

	press(m, "2")
	if m.tab != tabTasks {
		t.Fatalf("2 не переключила на задачи: %v", m.tab)
	}
	wantContains(t, screen(m), "закончить миграцию схемы", "вкладка задач")

	press(m, "shift+tab")
	if m.tab != tabMeetings {
		t.Fatalf("shift+tab не вернул на созвоны: %v", m.tab)
	}
	// Круг: с первой вкладки shift+tab уводит на последнюю.
	press(m, "shift+tab")
	if m.tab != tabCount-1 {
		t.Fatalf("shift+tab с первой вкладки: %v, ждали последнюю", m.tab)
	}
	press(m, "tab")
	if m.tab != tabMeetings {
		t.Fatalf("tab с последней вкладки: %v, ждали созвоны", m.tab)
	}
	// Каждая вкладка достижима цифрой, и подпись в шапке ей соответствует.
	for i := uiTab(0); i < tabCount; i++ {
		if m.editing() { // поиск открывается сразу в поле ввода
			press(m, "esc")
		}
		press(m, fmt.Sprintf("%d", i+1))
		if m.tab != i {
			t.Fatalf("цифра %d открыла вкладку %v", i+1, m.tab)
		}
		wantContains(t, screen(m), uiTabTitles()[i], "название вкладки в шапке")
	}
}

func TestUIQuitAndHelp(t *testing.T) {
	m := uiTestModel(t, uiSeed(t))

	if cmd := press(m, "?"); cmd != nil {
		t.Fatal("? не должна ничего запускать")
	}
	if m.screen() != scrHelp {
		t.Fatalf("? не открыла справку: %v", m.screen())
	}
	full := bodyText(m)
	for _, want := range []string{"Везде", "Созвоны", "Задачи", "Проекты", "Форма проекта",
		"Поиск", "Каналы"} {
		wantContains(t, full, want, "справка по клавишам")
	}
	wantContains(t, full, "без запущенного serve", "справка объясняет, что serve не нужен")
	// Длинная справка листается, а не обрывается на первом экране.
	if len(m.body) <= m.listHeight() {
		t.Fatalf("справка из %d строк — проверка на прокрутку ничего не значит", len(m.body))
	}
	press(m, "pgdown")
	if m.bodyOff == 0 {
		t.Fatal("справка не листается")
	}
	// Листаем до конца, а не на заранее сосчитанное число страниц: справка
	// растёт, и проверка не должна ломаться от каждой добавленной строки.
	seen := screen(m)
	for i := 0; i < len(m.body); i++ {
		before := m.bodyOff
		press(m, "pgdown")
		seen += screen(m)
		if m.bodyOff == before {
			break
		}
	}
	wantContains(t, seen, "Форма проекта", "долистали до раздела о форме")

	press(m, "esc")
	if m.screen() != scrList {
		t.Fatalf("esc не закрыл справку: %v", m.screen())
	}

	if cmd := press(m, "q"); cmd == nil {
		t.Fatal("q не попросила выйти")
	}
	if cmd := press(m, "ctrl+c"); cmd == nil {
		t.Fatal("ctrl+c не попросила выйти")
	}
}

// --- созвоны -----------------------------------------------------------------

func TestUIMeetingCardAndTranscript(t *testing.T) {
	m := uiTestModel(t, uiSeed(t))
	wantContains(t, screen(m), "Планёрка по релизу 2.4", "список созвонов")
	wantContains(t, screen(m), "разослан", "статус созвона по-русски")

	press(m, "enter")
	if m.screen() != scrMeeting {
		t.Fatalf("enter не открыл follow-up: %v", m.screen())
	}
	body := bodyText(m)
	for _, want := range []string{
		"Релиз 2.4 сдвинули на пятницу", // заголовок follow-up
		"Коротко",
		"Задачи", "закончить миграцию схемы", "2026-09-11",
		"Решения", "релиз в пятницу",
		"Открытые вопросы", "кто дежурит в выходные",
		"Риски", "Откат миграции",
		"Как шёл разговор",
	} {
		wantContains(t, body, want, "карточка созвона")
	}

	press(m, "t")
	if m.screen() != scrTranscript {
		t.Fatalf("t не открыла расшифровку: %v", m.screen())
	}
	wantContains(t, bodyText(m), "Миграция схемы не успевает до среды", "расшифровка")

	press(m, "esc")
	if m.screen() != scrMeeting {
		t.Fatalf("esc из расшифровки: %v, ждали карточку", m.screen())
	}
	press(m, "esc")
	if m.screen() != scrList || m.tab != tabMeetings {
		t.Fatalf("esc из карточки: экран %v, вкладка %v", m.screen(), m.tab)
	}
}

// Ссылки на разосланное лежат в map, и без сортировки они прыгали бы местами
// при каждой перерисовке. А Telegram и Slack постоянного адреса не оставляют:
// строка «telegram» с пустотой после неё читается как сбой рассылки.
func TestUIPublicationsStableAndExplained(t *testing.T) {
	m := uiTestModel(t, func(st *core.Store) {
		uiSeed(t)(st)
		st.SavePublication(uiTestMeeting, "google_docs", "https://docs.example/1", "")
		st.SavePublication(uiTestMeeting, "slack", "https://slack.example/2", "")
		st.SavePublication(uiTestMeeting, "telegram", "", "")
	})
	press(m, "enter")
	wantContains(t, bodyText(m), "https://docs.example/1", "ссылки видны в карточке")

	// Порядок обхода map случаен, поэтому одной проверки мало: неотсортированный
	// вывод совпал бы с ожидаемым просто по везению. Повторяем — совпасть
	// пятьдесят раз подряд случайность уже не может.
	links := map[string]string{
		"google_docs": "https://docs.example/1",
		"slack":       "https://slack.example/2",
		"telegram":    "",
		"email":       "https://mail.example/3",
	}
	want := []string{"email", "google_docs", "slack", "telegram"}
	for i := 0; i < 50; i++ {
		lines := uiLinkLines(links)
		if len(lines) != len(want) {
			t.Fatalf("строк %d, ждали %d", len(lines), len(want))
		}
		for j, target := range want {
			if !strings.HasPrefix(ansi.Strip(lines[j]), target) {
				t.Fatalf("строка %d — %q, ждали %q (порядок ссылок не задан)",
					j, ansi.Strip(lines[j]), target)
			}
		}
	}

	// Канал без постоянного адреса не должен выглядеть пустой строкой.
	last := ansi.Strip(uiLinkLines(links)[3])
	if strings.TrimSpace(last) == "telegram" {
		t.Fatalf("канал без ссылки показан пустой строкой: %q", last)
	}
	wantContains(t, last, "отправлено", "канал без постоянного адреса")
}

// Длинная расшифровка листается, и дальше конца не уезжает: иначе человек
// прокручивает в пустоту и думает, что текст кончился раньше.
func TestUITranscriptScrolls(t *testing.T) {
	m := uiTestModel(t, func(st *core.Store) {
		st.CreateMeeting(&core.Meeting{ID: "m1", Title: "Долгий", MeetURL: "u",
			StartedAt: time.Now(), Status: "transcribed"})
		var segs []core.Segment
		for i := 0; i < 200; i++ {
			segs = append(segs, core.Segment{Start: float64(i * 10), End: float64(i*10 + 9),
				Speaker: "Кто-то", Text: "реплика номер такая-то"})
		}
		st.SaveSegments("m1", segs)
	})
	press(m, "enter") // follow-up нет, но карточка открывается
	wantContains(t, bodyText(m), "Follow-up ещё нет", "созвон без разбора")
	press(m, "t")

	if m.bodyOff != 0 {
		t.Fatalf("расшифровка открылась не с начала: %d", m.bodyOff)
	}
	press(m, "pgdown")
	if m.bodyOff == 0 {
		t.Fatal("pgdown не прокрутила")
	}
	press(m, "G")
	maxOff := len(m.body) - m.listHeight()
	if m.bodyOff != maxOff {
		t.Fatalf("G встала на %d, конец на %d", m.bodyOff, maxOff)
	}
	press(m, "pgdown", "pgdown", "j")
	if m.bodyOff != maxOff {
		t.Fatalf("уехали за конец: %d при пределе %d", m.bodyOff, maxOff)
	}
	press(m, "g")
	if m.bodyOff != 0 {
		t.Fatalf("g не вернула в начало: %d", m.bodyOff)
	}
	press(m, "k", "pgup")
	if m.bodyOff != 0 {
		t.Fatalf("уехали выше начала: %d", m.bodyOff)
	}
}

// --- задачи ------------------------------------------------------------------

func TestUITaskCloseAndReopen(t *testing.T) {
	m := uiTestModel(t, uiSeed(t))
	press(m, "2")

	before := len(m.visibleItems())
	if before != 3 {
		t.Fatalf("открытых пунктов %d, ждали 3", before)
	}

	// Курсор ставим на «закончить миграцию схемы» — она первая по сроку.
	it, ok := m.selectedItem()
	if !ok || it.ID != "T-0001" {
		t.Fatalf("под курсором %+v, ждали T-0001 (ближайший срок)", it)
	}

	press(m, "d")
	if got := itemStatus(t, m.st, "T-0001"); got != "done" {
		t.Fatalf("после d статус в базе %q, ждали done", got)
	}
	if n := len(m.visibleItems()); n != before-1 {
		t.Fatalf("в списке осталось %d, ждали %d", n, before-1)
	}
	wantNotContains(t, screen(m), "закончить миграцию схемы", "закрытая задача ушла из списка")
	wantContains(t, screen(m), "T-0001", "сообщение о закрытии называет пункт")

	// Закрытые видно по «a», и оттуда же пункт возвращается в работу.
	press(m, "a")
	wantContains(t, screen(m), "закончить миграцию схемы", "с закрытыми")
	for i, x := range m.visibleItems() {
		if x.ID == "T-0001" {
			m.cursor[tabTasks] = i
		}
	}
	press(m, "u")
	if got := itemStatus(t, m.st, "T-0001"); got != "open" {
		t.Fatalf("после u статус %q, ждали open", got)
	}

	// Снять — это не «сделано»: у отброшенного пункта свой статус.
	// Список после возврата пересортирован, курсор наводим заново.
	for i, x := range m.visibleItems() {
		if x.ID == "T-0001" {
			m.cursor[tabTasks] = i
		}
	}
	press(m, "x")
	if got := itemStatus(t, m.st, "T-0001"); got != "dropped" {
		t.Fatalf("после x статус %q, ждали dropped", got)
	}
}

// Закрывать уже закрытое нечего, и молчать об этом нельзя: человек нажал
// клавишу и должен понять, почему ничего не произошло.
func TestUITaskCloseTwiceExplains(t *testing.T) {
	m := uiTestModel(t, uiSeed(t))
	press(m, "2", "a")
	for i, x := range m.visibleItems() {
		if x.ID == "T-0004" {
			m.cursor[tabTasks] = i
		}
	}
	press(m, "d")
	if m.statusIs != uiErr {
		t.Fatalf("повторное закрытие прошло молча: статус %q (%d)", m.status, m.statusIs)
	}
	wantContains(t, screen(m), "уже закрыт", "объяснение про закрытый пункт")
}

func TestUITaskFilters(t *testing.T) {
	m := uiTestModel(t, uiSeed(t))
	press(m, "2")

	// Фильтр по проекту: p открывает выбор, enter принимает.
	press(m, "p")
	if m.screen() != scrPicker {
		t.Fatalf("p не открыла выбор проекта: %v", m.screen())
	}
	wantContains(t, screen(m), "все проекты", "первый пункт выбора — «все»")
	press(m, "down", "enter") // «все проекты» → первый настоящий
	if m.itemFilter.Project == "" {
		t.Fatal("проект в фильтре не выставился")
	}
	chosen := m.itemFilter.Project
	for _, it := range m.visibleItems() {
		if it.Project != chosen {
			t.Fatalf("после фильтра по «%s» виден пункт из «%s»", chosen, it.Project)
		}
	}
	wantContains(t, screen(m), "проект: "+chosen, "строка состояния называет фильтр")

	// Фильтр по виду: оставляем только вопросы.
	press(m, "v")
	press(m, "down", "down", "enter") // всё → задачи → вопросы
	if m.itemFilter.Kind != core.KindQuestion {
		t.Fatalf("вид в фильтре %q, ждали question", m.itemFilter.Kind)
	}
	for _, it := range m.visibleItems() {
		if it.Kind != core.KindQuestion {
			t.Fatalf("среди вопросов виден %s", it.Kind)
		}
	}

	// Фильтр по исполнителю поверх остальных.
	press(m, "o")
	press(m, "down", "enter")
	if m.itemFilter.Owner == "" {
		t.Fatal("исполнитель в фильтре не выставился")
	}

	press(m, "c")
	if !m.itemFilter.empty() {
		t.Fatalf("c не сняла фильтры: %+v", m.itemFilter)
	}
	if n := len(m.visibleItems()); n != 3 {
		t.Fatalf("после снятия фильтров видно %d пунктов, ждали 3", n)
	}
}

func TestUITaskEnterOpensItsMeeting(t *testing.T) {
	m := uiTestModel(t, uiSeed(t))
	press(m, "2", "enter")
	if m.screen() != scrMeeting {
		t.Fatalf("enter на задаче не открыл созвон: %v", m.screen())
	}
	if m.meeting == nil || m.meeting.ID != uiTestMeeting {
		t.Fatalf("открылся не тот созвон: %+v", m.meeting)
	}
}

// --- поиск -------------------------------------------------------------------

func TestUISearchFindsAndJumps(t *testing.T) {
	m := uiTestModel(t, uiSeed(t))
	press(m, "4")
	if !m.searchFocus {
		t.Fatal("на пустом поиске курсор должен стоять в поле ввода")
	}
	typeIn(m, "миграцию")
	if len(m.hits) == 0 {
		t.Fatal("по слову «миграцию» ничего не нашлось")
	}
	wantContains(t, screen(m), "найдено", "счётчик найденного")

	// Буквы уходят в поле, а не в команды: «q» не должна закрывать интерфейс.
	if cmd := press(m, "q"); cmd != nil {
		t.Fatal("q во время набора запроса попыталась выйти")
	}
	press(m, "backspace")
	if got := m.query.String(); got != "миграцию" {
		t.Fatalf("после q и backspace в поле %q", got)
	}

	press(m, "enter") // из ввода — к найденному
	if m.searchFocus {
		t.Fatal("enter не перевёл к списку найденного")
	}

	// Находка в follow-up открывается в follow-up, а не в расшифровке.
	for i, h := range m.hits {
		if h.Kind == "follow-up" {
			m.cursor[tabSearch] = i
		}
	}
	press(m, "enter")
	if m.screen() != scrMeeting {
		t.Fatalf("находка в follow-up открыла %v", m.screen())
	}
	press(m, "esc")

	press(m, "esc") // из списка — очистить запрос
	if m.query.String() != "" {
		t.Fatalf("esc не очистил запрос: %q", m.query.String())
	}
	if !m.searchFocus {
		t.Fatal("после очистки курсор должен вернуться в поле ввода")
	}
}

// Переход к найденному — половина смысла поиска: часовая расшифровка,
// открытая с первой строки, не отвечает на вопрос «где это было сказано».
func TestUISearchJumpsToFoundPlace(t *testing.T) {
	m := uiTestModel(t, func(st *core.Store) {
		st.CreateMeeting(&core.Meeting{ID: "long-1", Title: "Долгий созвон", MeetURL: "u",
			StartedAt: time.Now(), Status: "transcribed"})
		var segs []core.Segment
		for i := 0; i < 120; i++ {
			speaker := "Участник А"
			if i%2 == 1 {
				speaker = "Участник Б"
			}
			text := "обычная рабочая реплика без ничего особенного"
			if i == 100 {
				text = "а вот здесь мы говорили про квазары и про их природу"
			}
			segs = append(segs, core.Segment{Start: float64(i * 20), End: float64(i*20 + 19),
				Speaker: speaker, Text: text})
		}
		st.SaveSegments("long-1", segs)
	})

	press(m, "4")
	typeIn(m, "квазар")
	if len(m.hits) == 0 {
		t.Fatal("не нашлось того, что точно есть в расшифровке")
	}
	press(m, "enter") // к найденному
	press(m, "enter") // открыть

	if m.screen() != scrTranscript {
		t.Fatalf("открылось не расшифровкой: %v", m.screen())
	}
	if m.bodyOff == 0 {
		t.Fatal("расшифровка открылась с начала, а не на найденном месте")
	}
	if m.bodyOff >= len(m.body) {
		t.Fatalf("прокрутка %d за пределами %d строк", m.bodyOff, len(m.body))
	}
	wantContains(t, ansi.Strip(strings.Join(m.bodyWindow(), "\n")),
		"про квазары", "найденное место видно на экране")
}

func TestUISearchNothingFound(t *testing.T) {
	m := uiTestModel(t, uiSeed(t))
	press(m, "4")
	typeIn(m, "квазар")
	wantContains(t, screen(m), "Ничего не нашлось", "пустая выдача объясняется")
}

// --- проекты -----------------------------------------------------------------

func TestUIProjectsList(t *testing.T) {
	m := uiTestModel(t, uiSeed(t))
	press(m, "3")
	s := screen(m)
	wantContains(t, s, "Платежи", "заведённый проект")
	// Проект, которого нет в реестре, но по которому висят задачи, тоже виден:
	// иначе его задачи некуда деть.
	wantContains(t, s, "Онбординг", "проект только из задач")
	// И видно, чем он отличается от заведённого: иначе человек ищет, где
	// править описание проекта, которого в реестре нет.
	wantContains(t, s, "не заведён", "пометка у проекта не из реестра")
	wantContains(t, s, "не собрана", "у заведённого проекта видно состояние справки")
	// Список отсортирован по названию, «не определён» — в конце.
	if got := m.projects[0].Name; got != "Онбординг" {
		t.Fatalf("первым в списке «%s», ждали «Онбординг» по алфавиту", got)
	}

	m.selectProject("Платежи")
	press(m, "enter")
	if m.screen() != scrProject {
		t.Fatalf("enter не открыл карточку проекта: %v", m.screen())
	}
	body := bodyText(m)
	wantContains(t, body, "приём денег", "описание проекта")
	wantContains(t, body, "биллинг", "псевдонимы")
	wantContains(t, body, "git@github.com:org/pay", "источник")
	wantContains(t, body, "не собрана", "состояние справки")
	wantContains(t, body, "закончить миграцию схемы", "открытые пункты проекта")
}

func TestUIProjectCreate(t *testing.T) {
	m := uiTestModel(t, nil)
	press(m, "3", "n")
	if m.screen() != scrForm {
		t.Fatalf("n не открыла форму: %v", m.screen())
	}

	typeIn(m, "Онбординг")
	press(m, "tab")
	typeIn(m, "первые экраны")
	press(m, "tab")
	typeIn(m, "регистрация, signup")

	press(m, "ctrl+n") // выбор вида источника
	if m.screen() != scrPicker {
		t.Fatalf("ctrl+n не открыла выбор вида: %v", m.screen())
	}
	press(m, "enter") // первый вид — репозиторий
	if m.screen() != scrForm {
		t.Fatalf("после выбора не вернулись в форму: %v", m.screen())
	}
	typeIn(m, "git@github.com:org/onboarding")

	press(m, "ctrl+s")
	if m.screen() != scrList {
		t.Fatalf("после сохранения остались на %v", m.screen())
	}

	p, err := m.st.Project("Онбординг")
	if err != nil {
		t.Fatalf("проект не сохранился: %v", err)
	}
	if p.About != "первые экраны" {
		t.Errorf("описание %q", p.About)
	}
	if strings.Join(p.Aliases, "|") != "регистрация|signup" {
		t.Errorf("псевдонимы %v", p.Aliases)
	}
	if len(p.Sources) != 1 || p.Sources[0].Kind != "repo" ||
		p.Sources[0].Value != "git@github.com:org/onboarding" {
		t.Errorf("источники %+v", p.Sources)
	}
	wantContains(t, screen(m), "Онбординг", "новый проект виден в списке")
	wantContains(t, screen(m), "заведён", "сообщение о заведении")
}

func TestUIProjectEditAndSourceKinds(t *testing.T) {
	m := uiTestModel(t, uiSeed(t))
	press(m, "3")
	m.selectProject("Платежи")
	press(m, "e")
	if m.form == nil || m.form.old != "Платежи" {
		t.Fatalf("форма правки не заполнилась: %+v", m.form)
	}
	if got := m.form.name.String(); got != "Платежи" {
		t.Fatalf("в поле названия %q", got)
	}
	if got := m.form.aliases.String(); got != "биллинг, payments" {
		t.Fatalf("в поле псевдонимов %q", got)
	}

	// Правим описание: ctrl+u стирает поле целиком.
	press(m, "tab")
	press(m, "ctrl+u")
	typeIn(m, "деньги и подписки")

	// Меняем вид источника и добавляем второй.
	tabTo(m, uiFormFixed) // мимо словаря — на первый источник
	if m.form.currentSource() != 0 {
		t.Fatalf("курсор на поле %d, ждали первый источник", m.form.field)
	}
	press(m, "ctrl+t")
	if m.form.sources[0].kind != "path" {
		t.Fatalf("ctrl+t дала вид %q, ждали path", m.form.sources[0].kind)
	}
	press(m, "ctrl+t", "ctrl+t", "ctrl+t")
	if m.form.sources[0].kind != "repo" {
		t.Fatalf("вид по кругу вернулся к %q", m.form.sources[0].kind)
	}

	press(m, "ctrl+n")
	press(m, "down", "down", "enter") // repo → path → url
	typeIn(m, "https://pay.example.com")
	press(m, "ctrl+s")

	p, err := m.st.Project("Платежи")
	if err != nil {
		t.Fatal(err)
	}
	if p.About != "деньги и подписки" {
		t.Errorf("описание после правки %q", p.About)
	}
	if len(p.Sources) != 2 || p.Sources[1].Kind != "url" {
		t.Fatalf("источники после правки %+v", p.Sources)
	}
}

func TestUIProjectSourceRemove(t *testing.T) {
	m := uiTestModel(t, uiSeed(t))
	press(m, "3")
	m.selectProject("Платежи")
	press(m, "e")
	tabTo(m, uiFormFixed) // на первый источник
	press(m, "ctrl+k")
	if len(m.form.sources) != 0 {
		t.Fatalf("ctrl+k не убрала источник: %+v", m.form.sources)
	}
	press(m, "ctrl+s")
	p, _ := m.st.Project("Платежи")
	if len(p.Sources) != 0 {
		t.Fatalf("источник остался в базе: %+v", p.Sources)
	}
}

// Переименование — не удаление. Задачи, накопленные по проекту, обязаны
// переехать вместе с названием, иначе проект после правки выглядит пустым,
// а его задачи висят под именем, которого больше нет ни в одном списке.
func TestUIProjectRenameKeepsItems(t *testing.T) {
	m := uiTestModel(t, uiSeed(t))
	press(m, "3")
	m.selectProject("Платежи")
	press(m, "e")
	press(m, "ctrl+u")
	typeIn(m, "Биллинг")
	press(m, "ctrl+s")

	if _, err := m.st.Project("Биллинг"); err != nil {
		t.Fatalf("проект под новым именем не найден: %v", err)
	}
	if _, err := m.st.Project("Платежи"); err == nil {
		t.Fatal("проект остался и под старым именем")
	}
	moved, err := m.st.ProjectItems("Биллинг")
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 3 {
		t.Fatalf("под новым именем %d пунктов, ждали 3", len(moved))
	}
	left, _ := m.st.ProjectItems("Платежи")
	if len(left) != 0 {
		t.Fatalf("под старым именем осталось %d пунктов", len(left))
	}
	wantContains(t, screen(m), "переименован", "сообщение о переименовании")
}

func TestUIProjectRenameOntoExistingRefuses(t *testing.T) {
	m := uiTestModel(t, uiSeed(t))
	if err := m.st.SaveProject(core.Project{Name: "Онбординг"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	press(m, "3")
	m.selectProject("Платежи")
	press(m, "e")
	press(m, "ctrl+u")
	typeIn(m, "Онбординг")
	press(m, "ctrl+s")

	if m.screen() != scrForm {
		t.Fatalf("форма закрылась, хотя сохранить было нельзя: %v", m.screen())
	}
	wantContains(t, screen(m), "уже есть", "объяснение отказа")
	if _, err := m.st.Project("Платежи"); err != nil {
		t.Fatal("исходный проект пропал при неудачном переименовании")
	}
}

// uiSeedWithForeignItem — тот же набор плюс пункт, родившийся на прошлом
// созвоне и закрытый на этом. Именно он показывает разницу между «уйдёт
// вместе с созвоном» и «вернётся в работу»: в uiSeed все пункты родились на
// одном созвоне, и на таком наборе удаление, сносящее подряд всё, выглядело бы
// правильным.
func uiSeedWithForeignItem(t *testing.T) func(*core.Store) {
	t.Helper()
	base := uiSeed(t)
	return func(st *core.Store) {
		base(st)
		must := func(err error) {
			t.Helper()
			if err != nil {
				t.Fatal(err)
			}
		}
		earlier := time.Date(2026, 9, 1, 10, 0, 0, 0, time.Local)
		must(st.CreateMeeting(&core.Meeting{ID: "прошлый", Title: "Прошлая планёрка",
			MeetURL: "https://meet.example/old", StartedAt: earlier, Status: "published"}))
		must(st.AddItem(core.ProjectItem{ID: "Q-чужой", Project: "Платежи", Kind: core.KindQuestion,
			Text: "кто платит за прод", Owner: "Участник А", OpenedIn: "прошлый"}))
		must(st.CloseItem("Q-чужой", "done", "решили на планёрке", uiTestMeeting))
		// Решение — третий вид пункта. Нужен, чтобы цена удаления вышла
		// длинной: на короткой фразе не видно, что рамка режет ей середину.
		must(st.AddItem(core.ProjectItem{ID: "D-0005", Project: "Платежи", Kind: core.KindDecision,
			Text: "релиз в пятницу", OpenedIn: uiTestMeeting}))
	}
}

// Удаление созвона — самое разрушительное действие в интерфейсе, и цена его не
// видна из названия строки: вместе с созвоном уходят задачи и решения проектов.
func TestUIMeetingDelete(t *testing.T) {
	m := uiTestModel(t, uiSeedWithForeignItem(t))
	press(m, "1")
	row, ok := m.selectedMeeting()
	if !ok || row.ID != uiTestMeeting {
		t.Fatalf("курсор не на подопытном созвоне: %+v", row)
	}

	press(m, "D")
	if m.screen() != scrConfirm {
		t.Fatalf("D не спросила подтверждения: %v", m.screen())
	}
	s := screen(m)
	wantContains(t, s, "Планёрка по релизу 2.4", "в вопросе назван созвон")
	// Цена — это то, чего в названии строки нет вовсе. Спрашиваем сам вопрос, а
	// не экран: перенос по словам вправе разорвать «follow-up» по дефису, и
	// поиск подстроки в отрисованном экране этого не переживёт.
	for _, want := range []string{"расшифровка", "follow-up", "3 задачи", "1 решение",
		"1 вопрос", "1 пункт откроется заново"} {
		if !strings.Contains(m.confirm.text, want) {
			t.Errorf("в цене удаления нет %q:\n  %s", want, m.confirm.text)
		}
	}
	// А на экране — что её не обрезали. Рамка режет строку шире себя
	// многоточием, и режет ровно середину: там, где задачи и решения.
	wantContains(t, s, "1 пункт откроется заново", "хвост цены на экране")
	wantNotContains(t, s, "…", "цену обрезали многоточием")

	// Любая клавиша, кроме «y», — отмена.
	press(m, "n")
	if _, err := m.st.Meeting(uiTestMeeting); err != nil {
		t.Fatal("созвон удалился без подтверждения")
	}

	press(m, "D", "y")
	if _, err := m.st.Meeting(uiTestMeeting); err == nil {
		t.Fatal("созвон не удалился")
	}
	if m.screen() != scrList {
		t.Fatalf("после удаления остались не в списке: %v", m.screen())
	}
	for _, r := range m.meetings {
		if r.ID == uiTestMeeting {
			t.Fatal("удалённый созвон остался в списке на экране")
		}
	}
	// Пункт, родившийся на этом созвоне, ушёл вместе с ним.
	if got := itemStatus(t, m.st, "T-0001"); got != "нет такого" {
		t.Errorf("задача созвона пережила его: статус %q", got)
	}
	// Чужой пункт, закрытый на нём, вернулся в работу.
	if got := itemStatus(t, m.st, "Q-чужой"); got != "open" {
		t.Errorf("чужой пункт остался в статусе %q — основание закрытия стёрто", got)
	}
	wantContains(t, screen(m), "созвон удалён", "сообщение об удалении")
	wantContains(t, screen(m), "вернулось в работу: 1 пункт", "сказали про открытый заново пункт")
}

// С карточки и с расшифровки удалять тоже надо — и уходить с них: страница
// удалённого созвона осталась бы на экране пустой.
func TestUIMeetingDeleteLeavesTheCard(t *testing.T) {
	for _, path := range []struct {
		name string
		keys []string
	}{
		{"карточка", []string{"1", "enter"}},
		{"расшифровка", []string{"1", "enter", "t"}},
	} {
		t.Run(path.name, func(t *testing.T) {
			m := uiTestModel(t, uiSeedWithForeignItem(t))
			press(m, path.keys...)
			if m.screen() == scrList {
				t.Fatalf("не открыли %s: %v", path.name, m.screen())
			}
			press(m, "D")
			if m.screen() != scrConfirm {
				t.Fatalf("D на %s не спросила подтверждения: %v", path.name, m.screen())
			}
			press(m, "y")
			if m.screen() != scrList {
				t.Fatalf("после удаления остались на экране %v", m.screen())
			}
			if _, err := m.st.Meeting(uiTestMeeting); err == nil {
				t.Fatal("созвон не удалился")
			}
			if strings.TrimSpace(screen(m)) == "" {
				t.Fatal("пустой экран после удаления")
			}
		})
	}
}

func TestUIProjectDelete(t *testing.T) {
	m := uiTestModel(t, uiSeed(t))
	press(m, "3")
	m.selectProject("Платежи")

	press(m, "D")
	if m.screen() != scrConfirm {
		t.Fatalf("D не спросила подтверждения: %v", m.screen())
	}
	wantContains(t, screen(m), "Платежи", "в вопросе назван проект")

	// Любая клавиша, кроме «y», — отмена.
	press(m, "n")
	if _, err := m.st.Project("Платежи"); err != nil {
		t.Fatal("проект удалился без подтверждения")
	}

	press(m, "D", "y")
	if _, err := m.st.Project("Платежи"); err == nil {
		t.Fatal("проект не удалился")
	}
	// Задачи — история, они остаются.
	items, _ := m.st.ProjectItems("Платежи")
	if len(items) != 3 {
		t.Fatalf("после удаления проекта осталось %d пунктов, ждали 3", len(items))
	}
	wantContains(t, screen(m), "остались", "предупреждение про сохранённые задачи")
}

// Удалять из реестра то, чего в реестре нет, нечего — и об этом надо сказать,
// а не молча ничего не сделать.
func TestUIDeleteUnregisteredProjectExplains(t *testing.T) {
	m := uiTestModel(t, uiSeed(t))
	press(m, "3")
	m.selectProject("Онбординг")
	press(m, "D")
	if m.screen() == scrConfirm {
		t.Fatal("спросили подтверждение на проект, которого нет в реестре")
	}
	if m.statusIs != uiErr {
		t.Fatalf("промолчали: статус %q", m.status)
	}
}

// Справка по коду — поход в Claude, и запускать его без источников бессмысленно.
func TestUIBuildContextNeedsSources(t *testing.T) {
	m := uiTestModel(t, uiSeed(t))
	press(m, "3")
	m.selectProject("Онбординг")
	if cmd := press(m, "s"); cmd != nil {
		t.Fatal("запустили сборку справки без источников")
	}
	wantContains(t, screen(m), "нет источников", "объяснение отказа")

	m.selectProject("Платежи")
	cmd := press(m, "s")
	if cmd == nil {
		t.Fatal("сборка справки не запустилась для проекта с источником")
	}
	if !m.building["Платежи"] {
		t.Fatal("проект не помечен как «собирается»")
	}
	wantContains(t, screen(m), "собираю справку", "сообщение о начатой сборке")

	// Ответ фоновой работы возвращает интерфейс в спокойное состояние.
	m.Update(uiContextDone{Project: "Платежи", Spend: core.Spend{USD: 0.02}})
	if m.building["Платежи"] {
		t.Fatal("после ответа проект остался помеченным")
	}
	wantContains(t, screen(m), "собрана", "сообщение о готовой справке")
}

// --- форма: отказы -----------------------------------------------------------

func TestUIFormRefusesEmptyName(t *testing.T) {
	m := uiTestModel(t, nil)
	press(m, "3", "n")
	// Курсор уводим с названия: форма обязана вернуть его туда, где проблема,
	// иначе человек читает «нужно название» и не понимает, куда печатать.
	press(m, "tab")
	typeIn(m, "какое-то описание")
	press(m, "ctrl+s")

	if m.screen() != scrForm {
		t.Fatalf("форма закрылась с пустым названием: %v", m.screen())
	}
	wantContains(t, screen(m), "название", "объяснение про пустое название")
	if m.form.field != 0 {
		t.Fatalf("курсор остался на поле %d, а проблема в названии", m.form.field)
	}
	if m.form.about.String() != "какое-то описание" {
		t.Fatalf("набранное потерялось при отказе: %q", m.form.about.String())
	}
}

func TestUIFormRefusesEmptySource(t *testing.T) {
	m := uiTestModel(t, nil)
	press(m, "3", "n")
	typeIn(m, "Проект")
	press(m, "ctrl+n", "enter") // добавили источник и ничего не набрали
	press(m, "ctrl+s")
	if m.screen() != scrForm {
		t.Fatalf("форма закрылась с пустым источником: %v", m.screen())
	}
	wantContains(t, screen(m), "пустой", "объяснение про пустой источник")
	if m.form.currentSource() != 0 {
		t.Fatalf("курсор не встал на пустой источник: поле %d", m.form.field)
	}
	if _, err := m.st.Project("Проект"); err == nil {
		t.Fatal("проект сохранился, хотя форма отказала")
	}
}

// --- каналы ------------------------------------------------------------------

// chanValues — то, что реально лежит в базе для канала.
func chanValues(t *testing.T, st *core.Store, key string) (bool, map[string]string) {
	t.Helper()
	have, err := st.ChannelSettings()
	if err != nil {
		t.Fatal(err)
	}
	row := have[key]
	if row.Values == nil {
		row.Values = map[string]string{}
	}
	return row.Enabled, row.Values
}

func TestUIChannelsList(t *testing.T) {
	m := uiTestModel(t, uiSeed(t))
	press(m, "5")
	if m.tab != tabChannels {
		t.Fatalf("5 не открыла каналы: %v", m.tab)
	}
	s := screen(m)
	// Список берётся из ChannelDefs, а не из своего перечня: новый канал
	// появится здесь сам.
	for _, d := range core.ChannelDefs() {
		wantContains(t, s, d.Name, "канал в списке")
	}
	wantContains(t, s, "выключен", "видно состояние канала")
}

// Ради этого всё и затевалось: человек добавляет разрешённый чат Telegram.
// Значения списка добавляются и убираются по одному, а не правятся одной
// строкой через запятую.
func TestUIChannelListFieldAddsAndRemovesOneByOne(t *testing.T) {
	m := uiTestModel(t, nil)
	press(m, "5")
	m.selectChannel("telegram")
	press(m, "enter")
	if m.chanForm == nil || m.chanForm.key != "telegram" {
		t.Fatalf("форма канала не открылась: %+v", m.chanForm)
	}

	// Встаём на строку «добавить значение» списка allowed_chats.
	goToAdd := func() {
		t.Helper()
		for i := 0; i < 40; i++ {
			r := m.chanForm.current()
			if r.item == uiRowAdd && m.chanForm.fields[r.field].def.Key == "allowed_chats" {
				return
			}
			press(m, "tab")
		}
		t.Fatal("не нашёл строку «добавить» у allowed_chats")
	}

	goToAdd()
	press(m, "enter")
	typeIn(m, "-100111")
	goToAdd()
	press(m, "enter")
	typeIn(m, "-100222")
	goToAdd()
	press(m, "enter")
	typeIn(m, "-100333")

	// Канал включаем и сохраняем.
	m.chanForm.cursor = 0
	press(m, " ")
	press(m, "ctrl+s")
	if m.screen() != scrList {
		t.Fatalf("после сохранения остались на %v: %s", m.screen(), m.status)
	}

	on, values := chanValues(t, m.st, "telegram")
	if !on {
		t.Fatal("канал не включился")
	}
	if got := core.ChList(values["allowed_chats"]); strings.Join(got, "|") != "-100111|-100222|-100333" {
		t.Fatalf("в базе allowed_chats = %q", values["allowed_chats"])
	}

	// Убираем средний — по одному, а не правкой строки целиком.
	press(m, "enter")
	for i := 0; i < 40; i++ {
		r := m.chanForm.current()
		if r.item == 1 && m.chanForm.fields[r.field].def.Key == "allowed_chats" {
			break
		}
		press(m, "tab")
	}
	press(m, "ctrl+k")
	press(m, "ctrl+s")

	_, values = chanValues(t, m.st, "telegram")
	if got := core.ChList(values["allowed_chats"]); strings.Join(got, "|") != "-100111|-100333" {
		t.Fatalf("после удаления среднего allowed_chats = %q", values["allowed_chats"])
	}
}

// Переключатель хранится строкой, и распознаётся только «1». «on» или «true»
// не сработали бы — молча.
func TestUIChannelSwitchStoredAsFlag(t *testing.T) {
	m := uiTestModel(t, nil)
	press(m, "5")
	m.selectChannel("telegram")
	press(m, "enter")

	for i := 0; i < 40; i++ {
		r := m.chanForm.current()
		if r.field >= 0 && m.chanForm.fields[r.field].def.Key == "listen" {
			break
		}
		press(m, "tab")
	}
	press(m, " ")
	press(m, "ctrl+s")

	_, values := chanValues(t, m.st, "telegram")
	if values["listen"] != "1" {
		t.Fatalf("listen сохранён как %q, а chBool узнаёт только \"1\"", values["listen"])
	}
	if !core.ChBool(values["listen"]) {
		t.Fatal("chBool не признал сохранённое значение")
	}
}

// Пробел на строке с текстом — это пробел, а не переключатель: иначе адрес с
// пробелом набрать нельзя, а человек не поймёт почему.
func TestUIChannelSpaceTypesInTextField(t *testing.T) {
	m := uiTestModel(t, nil)
	press(m, "5")
	m.selectChannel("slack")
	press(m, "enter")
	for i := 0; i < 40; i++ {
		r := m.chanForm.current()
		if r.field >= 0 && m.chanForm.fields[r.field].def.Key == "channel" {
			break
		}
		press(m, "tab")
	}
	typeIn(m, "наши")
	press(m, " ")
	typeIn(m, "созвоны")
	press(m, "ctrl+s")

	_, values := chanValues(t, m.st, "slack")
	if values["channel"] != "наши созвоны" {
		t.Fatalf("в поле канала оказалось %q", values["channel"])
	}
}

// Включать и выключать — самое частое действие, и лезть ради него в форму
// незачем. А про источник, который на ходу не переключается, надо сказать.
func TestUIChannelToggleFromList(t *testing.T) {
	m := uiTestModel(t, nil)
	press(m, "5")
	m.selectChannel("calendar")
	before, _ := chanValues(t, m.st, "calendar")

	press(m, " ")
	after, _ := chanValues(t, m.st, "calendar")
	if after == before {
		t.Fatalf("пробел не переключил канал: было %v, стало %v", before, after)
	}
	wantContains(t, screen(m), "Календарь", "сообщение называет канал")
	wantContains(t, screen(m), "перезапуска", "предупреждение про неживой канал")

	// А живой канал переключается без оговорок.
	m.selectChannel("slack")
	press(m, " ")
	wantNotContains(t, screen(m), "перезапуска", "живому каналу перезапуск не нужен")
}

// Правка канала уходит в базу, а не в конфиг: после первого запуска steno.json
// для каналов уже ничего не решает, и запись туда была бы молчаливой потерей.
func TestUIChannelSavePreservesUnknownKeys(t *testing.T) {
	m := uiTestModel(t, func(st *core.Store) {
		st.SaveChannel("google_docs", false, map[string]string{
			"folder_id":        "старая",
			"credentials_file": "/secrets/org-key.json",
		})
	})
	press(m, "5")
	m.selectChannel("google_docs")
	press(m, "enter")
	for i := 0; i < 40; i++ {
		r := m.chanForm.current()
		if r.field >= 0 && m.chanForm.fields[r.field].def.Key == "folder_id" {
			break
		}
		press(m, "tab")
	}
	press(m, "ctrl+u")
	typeIn(m, "1AbCновая")
	press(m, "ctrl+s")

	_, values := chanValues(t, m.st, "google_docs")
	if values["folder_id"] != "1AbCновая" {
		t.Fatalf("папка сохранилась как %q", values["folder_id"])
	}
	// Поля, которого в форме нет, терять нельзя: у давних установок там лежит
	// путь к ключу организации, и правка соседнего поля не должна его сносить.
	if values["credentials_file"] != "/secrets/org-key.json" {
		t.Fatalf("незнакомое форме поле потерялось: %+v", values)
	}
}

// Секретам в интерфейсе места нет — ни значений, ни имён переменных окружения.
func TestUIChannelsShowNoSecrets(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "секретный-токен-бота")
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-секрет")
	m := uiTestModel(t, nil)
	press(m, "5")

	seen := screen(m)
	for _, ch := range m.channels {
		m.selectChannel(ch.Key)
		press(m, "enter")
		lines, _ := m.channelFormView()
		seen += "\n" + ansi.Strip(strings.Join(lines, "\n"))
		press(m, "esc")
	}
	for _, bad := range []string{
		"секретный-токен-бота", "xoxb-секрет",
		"TELEGRAM_BOT_TOKEN", "SLACK_BOT_TOKEN", "ANTHROPIC_API_KEY",
	} {
		wantNotContains(t, seen, bad, "секрет в интерфейсе")
	}
}

// Доступ в Google подключается браузером, и в терминале кнопки быть не может.
// Показать состояние и сказать, где это делается, — обязательно: иначе канал
// просто не работает, а почему — не написано нигде.
func TestUIChannelGoogleFieldIsExplainedNotEditable(t *testing.T) {
	m := uiTestModel(t, nil)
	press(m, "5")
	m.selectChannel("calendar")
	press(m, "enter")

	lines, _ := m.channelFormView()
	text := ansi.Strip(strings.Join(lines, "\n"))
	wantContains(t, text, "Доступ в Google", "название поля")
	wantContains(t, text, "не подключено", "состояние доступа")
	wantContains(t, text, "панели", "где подключается")

	// Курсор по нему не ходит: править там нечего.
	for _, r := range m.chanForm.rows() {
		if r.field >= 0 && m.chanForm.fields[r.field].def.Kind == "google" {
			t.Fatal("курсор может встать на поле, которое нельзя изменить")
		}
	}
}

// Подписи полей бывают длиннее любой заранее выбранной колонки: «Писать в личку
// тем, на ком задача» — тридцать три знака. На фиксированной ширине такая
// подпись наезжала на значение справа, и строка читалась как каша.
func TestUIChannelFormLabelsDoNotCollide(t *testing.T) {
	m := uiTestModel(t, nil)
	m.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	press(m, "5")

	for _, key := range []string{"slack", "telegram", "calendar", "gmail", "google_docs", "http"} {
		m.selectChannel(key)
		press(m, "enter")
		lines, _ := m.channelFormView()
		press(m, "esc")

		// Значения всех строк начинаются в одной колонке, и подпись до неё
		// помещается целиком.
		col := -1
		for _, l := range lines {
			plain := ansi.Strip(l)
			for _, mark := range []string{"[ ] нет", "[×] да", "+ "} {
				i := uiColumnOf(plain, mark)
				if i < 0 {
					continue
				}
				if col < 0 {
					col = i
				} else if i != col {
					t.Fatalf("%s: значение в колонке %d, у соседей %d\n%s",
						key, i, col, strings.Join(linesPlain(lines), "\n"))
				}
			}
		}
		for _, fs := range m.channels {
			if fs.Key != key {
				continue
			}
			for _, fd := range fs.Fields {
				if fd.Kind == "google" || col < 0 {
					continue
				}
				if ansi.StringWidth(fd.Label)+2 > col {
					t.Fatalf("%s: подпись «%s» шире колонки %d", key, fd.Label, col)
				}
			}
		}
	}
}

func linesPlain(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, ansi.Strip(l))
	}
	return out
}

// Длинная форма (у календаря семь полей с пояснениями) не помещается на экран,
// и прокручиваться она должна за курсором, а не оставаться в начале.
func TestUIChannelFormScrollsWithCursor(t *testing.T) {
	m := uiTestModel(t, nil)
	m.Update(tea.WindowSizeMsg{Width: 90, Height: 16})
	press(m, "5")
	m.selectChannel("calendar")
	press(m, "enter")

	lines, _ := m.channelFormView()
	if len(lines) <= m.listHeight() {
		t.Fatalf("форма из %d строк помещается в %d — проверка бессмысленна",
			len(lines), m.listHeight())
	}
	// Уходим на последнее поле формы.
	rows := m.chanForm.rows()
	for i := 0; i < len(rows)-1; i++ {
		press(m, "tab")
	}
	last := m.chanForm.fields[m.chanForm.current().field].def
	wantContains(t, screen(m), last.Label, "видно поле под курсором")
}

// --- фильтр по подстроке -----------------------------------------------------

func TestUIListFilter(t *testing.T) {
	m := uiTestModel(t, uiSeed(t))
	press(m, "2", "/")
	if !m.editing() {
		t.Fatal("/ не включила набор фильтра")
	}
	typeIn(m, "прототип")
	rows := m.visibleItems()
	if len(rows) != 1 || rows[0].ID != "T-0003" {
		t.Fatalf("под фильтром %d строк: %+v", len(rows), rows)
	}
	press(m, "enter") // принять фильтр, вернуться к списку
	if m.editing() {
		t.Fatal("enter не завершил набор фильтра")
	}
	if len(m.visibleItems()) != 1 {
		t.Fatal("фильтр слетел после enter")
	}
	press(m, "esc")
	if len(m.visibleItems()) != 3 {
		t.Fatalf("esc не снял фильтр: видно %d", len(m.visibleItems()))
	}

	// Фильтр, под который ничего не подошло, объясняется словами.
	press(m, "/")
	typeIn(m, "кракозябра")
	wantContains(t, screen(m), "ничего не подошло", "пустая выдача фильтра")
}

// --- узкий терминал ----------------------------------------------------------

// Восемьдесят колонок — не редкость, а норма; а имена проектов, названия
// созвонов и тексты задач регулярно длиннее экрана. Ни одна строка не имеет
// права торчать за край: терминал перенесёт её сам, и вёрстка поедет вся.
func TestUINarrowTerminalNeverOverflows(t *testing.T) {
	long := strings.Repeat("очень-длинное-слово-", 12)
	m := uiTestModel(t, func(st *core.Store) {
		uiSeed(t)(st)
		st.SaveProject(core.Project{
			Name:    "Проект с неприлично длинным названием, которое не влезает ни в какой экран",
			About:   long,
			Aliases: []string{long, "ещё один невероятно длинный псевдоним такой длины"},
			Sources: []core.Source{{Kind: "path", Value: "/" + long}},
		})
		st.CreateMeeting(&core.Meeting{ID: "long-1", Title: long, MeetURL: "u",
			StartedAt: time.Now(), Status: "published"})
		st.SaveSegments("long-1", []core.Segment{{Speaker: long, Text: long}})
		st.AddItem(core.ProjectItem{ID: "T-9999", Project: long, Kind: core.KindTask,
			Text: long, Owner: long, Due: "2026-12-31", OpenedIn: "long-1"})
	})

	for _, size := range [][2]int{{80, 24}, {40, 12}, {120, 40}, {20, 8}} {
		w, h := size[0], size[1]
		m.Update(tea.WindowSizeMsg{Width: w, Height: h})

		check := func(what string) {
			t.Helper()
			out := m.View()
			lines := strings.Split(out, "\n")
			if len(lines) > h && h >= uiChromeLines+1 {
				t.Errorf("%dx%d, %s: %d строк вместо %d", w, h, what, len(lines), h)
			}
			for i, l := range lines {
				if got := ansi.StringWidth(l); got > w {
					t.Fatalf("%dx%d, %s: строка %d шириной %d\n%q",
						w, h, what, i, got, ansi.Strip(l))
				}
			}
		}

		for _, tab := range []string{"1", "2", "3", "4", "5"} {
			if m.editing() {
				press(m, "esc")
			}
			press(m, tab)
			check("вкладка " + tab)
		}
		press(m, "enter")
		check("форма канала")
		press(m, "tab", "tab", "tab")
		check("форма канала, курсор ниже")
		press(m, "esc")
		press(m, "1", "enter")
		check("карточка созвона")
		press(m, "t")
		check("расшифровка")
		press(m, "esc", "esc", "3", "enter")
		check("карточка проекта")
		press(m, "e")
		check("форма")
		press(m, "esc", "esc")
		press(m, "2", "p")
		check("выбор проекта")
		press(m, "esc")
		press(m, "?")
		check("справка")
		press(m, "esc")
		press(m, "4")
		typeIn(m, "миграц")
		check("поиск")
		press(m, "esc", "esc")
	}
}

// --- мелочи, на которых легко ошибиться --------------------------------------

func TestUITruncAndPad(t *testing.T) {
	for _, c := range []struct {
		in    string
		width int
		want  int
	}{
		{"короткое", 20, 8},
		{"длинная строка целиком", 10, 10},
		{"кириллица", 5, 5},
		{"", 4, 0},
		{"что-нибудь", 0, 0},
	} {
		if got := ansi.StringWidth(uiTrunc(c.in, c.width)); got != c.want {
			t.Errorf("uiTrunc(%q, %d) шириной %d, ждали %d", c.in, c.width, got, c.want)
		}
	}
	if got := ansi.StringWidth(uiFit("абв", 10)); got != 10 {
		t.Errorf("uiFit не добил до ширины: %d", got)
	}
	if got := ansi.StringWidth(uiFit("длинная строка", 6)); got != 6 {
		t.Errorf("uiFit не обрезал: %d", got)
	}
	// Строка со стилями меряется по видимой ширине, а не по числу байт.
	if got := ansi.StringWidth(uiFit(uiAccent.Render("абв"), 5)); got != 5 {
		t.Errorf("uiFit со стилями: %d", got)
	}
}

func TestUIWrapKeepsEverything(t *testing.T) {
	text := "Очень длинная задача, которую надо перенести по словам так, чтобы ничего не потерялось по дороге"
	lines := uiWrapLines("  · ", text, 30)
	if len(lines) < 3 {
		t.Fatalf("перенос дал %d строк: %v", len(lines), lines)
	}
	joined := strings.Join(strings.Fields(strings.Join(lines, " ")), " ")
	if !strings.Contains(joined, "ничего не потерялось по дороге") {
		t.Fatalf("хвост потерялся: %q", joined)
	}
	for _, l := range lines {
		if w := ansi.StringWidth(l); w > 30 {
			t.Fatalf("строка шире ширины переноса: %d, %q", w, l)
		}
	}
	// Отступ под первую строку сохраняется на продолжениях.
	if !strings.HasPrefix(lines[1], "    ") {
		t.Fatalf("продолжение без отступа: %q", lines[1])
	}
}

func TestUITextField(t *testing.T) {
	f := newTextField("привет")
	f.insert([]rune("!"))
	if f.String() != "привет!" {
		t.Fatalf("после вставки %q", f.String())
	}
	f.left()
	f.left()
	f.insert([]rune("х"))
	if f.String() != "привех т!" {
		// Курсор стоял между «е» и «т» после двух шагов влево от конца.
		if f.String() != "привехт!" {
			t.Fatalf("вставка в середину дала %q", f.String())
		}
	}
	f.set("абв")
	f.backspace()
	if f.String() != "аб" {
		t.Fatalf("backspace дал %q", f.String())
	}
	f.home()
	f.del()
	if f.String() != "б" {
		t.Fatalf("delete в начале дал %q", f.String())
	}
	f.set("одно два три")
	f.killWord()
	if f.String() != "одно два " {
		t.Fatalf("ctrl+w дал %q", f.String())
	}
	f.clear()
	if !f.empty() {
		t.Fatalf("clear оставил %q", f.String())
	}
	// Курсор не должен уезжать за границы, сколько ни жми.
	f.set("аб")
	for i := 0; i < 5; i++ {
		f.left()
	}
	if f.cur != 0 {
		t.Fatalf("курсор ушёл влево за начало: %d", f.cur)
	}
	for i := 0; i < 5; i++ {
		f.right()
	}
	if f.cur != 2 {
		t.Fatalf("курсор ушёл вправо за конец: %d", f.cur)
	}
	f.backspace()
	f.backspace()
	f.backspace()
	if f.String() != "" {
		t.Fatalf("backspace на пустом поле сломался: %q", f.String())
	}
}

// Поле шириной в несколько колонок должно показывать место вокруг курсора, а
// не начало строки: иначе, набирая длинный путь, человек не видит, что печатает.
func TestUITextFieldScrollsToCursor(t *testing.T) {
	f := newTextField("/очень/длинный/путь/до/репозитория/проекта")
	view := ansi.Strip(f.view(12, true))
	if w := ansi.StringWidth(view); w != 12 {
		t.Fatalf("поле шириной %d вместо 12: %q", w, view)
	}
	if !strings.Contains(view, "проекта") {
		t.Fatalf("не видно конца строки, где стоит курсор: %q", view)
	}
	f.home()
	view = ansi.Strip(f.view(12, true))
	if !strings.HasPrefix(view, "/очень") {
		t.Fatalf("после home не видно начала: %q", view)
	}
}

// uiColumnOf — в какой колонке экрана начинается подстрока. Именно колонка, а
// не байтовое смещение: в кириллице байт вдвое больше, чем колонок.
func uiColumnOf(s, sub string) int {
	i := strings.Index(s, sub)
	if i < 0 {
		return -1
	}
	return ansi.StringWidth(s[:i])
}

// Заголовок колонки, сдвинутый на две позиции от самой колонки, читается как
// чужой: «срок» стоит над исполнителем, и таблица врёт. Проверяем, что подпись
// начинается ровно там же, где значение.
func TestUIColumnsLineUpWithRows(t *testing.T) {
	m := uiTestModel(t, uiSeed(t))
	for _, c := range []struct{ tab, caption, value string }{
		{"1", "статус", "разослан"},
		{"1", "название", "Планёрка"},
		{"1", "когда", "08.09 11:00"},
		{"2", "проект", "Платежи"},
		{"2", "кто", "Участник Б"},
		{"2", "срок", "2026-09-11"},
		{"2", "id", "T-0001"},
		{"3", "проект", "Онбординг"},
		{"3", "справка", "не заведён"},
		{"5", "канал", "Календарь"},
		{"5", "состояние", "выключен"},
		{"5", "что делает", "приносит созвоны"},
	} {
		press(m, c.tab)
		head := ansi.Strip(m.columns())
		rows := m.content()
		if len(rows) == 0 {
			t.Fatalf("вкладка %s: строк нет", c.tab)
		}
		row := ansi.Strip(rows[0])
		hi, ri := uiColumnOf(head, c.caption), uiColumnOf(row, c.value)
		if hi < 0 || ri < 0 {
			t.Fatalf("вкладка %s: подпись %q на %d, значение %q на %d\n%s\n%s",
				c.tab, c.caption, hi, c.value, ri, head, row)
		}
		if hi != ri {
			t.Errorf("вкладка %s: подпись %q в колонке %d, значение %q в колонке %d\n%s\n%s",
				c.tab, c.caption, hi, c.value, ri, head, row)
		}
	}
}

// Сорвавшийся созвон снаружи выглядит как обычный. Слова берём те же, что в
// панели (internal/panel/web/app/src/lib/fmt.ts): один созвон не должен
// называться в терминале иначе, чем в браузере.
func TestUIStatusWordsAndAlarm(t *testing.T) {
	for _, c := range []struct {
		status, word string
		marked       bool
	}{
		{"published", "разослан", false},
		{"transcribed", "расшифрован", false},
		{"summarized", "есть follow-up", false},
		{"publish_failed", "не разослан", true},
		{"failed", "сорвался", true},
		{"recording", "идёт запись", true},
		{"uploading", "разбираю файл", true},
	} {
		cell := uiStatusCell(c.status, uiStatusWidth)
		if got := strings.TrimSpace(ansi.Strip(cell)); got != c.word {
			t.Errorf("статус %q показан как %q, ждали %q", c.status, got, c.word)
		}
		if marked := cell != ansi.Strip(cell); marked != c.marked {
			t.Errorf("статус %q выделен=%v, ждали %v", c.status, marked, c.marked)
		}
	}

	// И список этим пользуется, а не рисует статус по-своему.
	m := uiTestModel(t, func(st *core.Store) {
		st.CreateMeeting(&core.Meeting{ID: "bad-1", Title: "Не разослан", MeetURL: "u",
			StartedAt: time.Now(), Status: "recording"})
		st.SetStatus("bad-1", "publish_failed", "телеграм ответил 429")
	})
	_, _, statusW, _ := m.colsMeetings()
	wantContains(t, m.content()[0], uiStatusCell("publish_failed", statusW),
		"строка списка рисует статус общей ячейкой")
}

func TestUIItemSortPutsDueFirst(t *testing.T) {
	items := []core.ProjectItem{
		{ID: "a", Status: "open", Due: ""},
		{ID: "b", Status: "open", Due: "2026-09-20"},
		{ID: "c", Status: "done", Due: "2026-01-01"},
		{ID: "d", Status: "open", Due: "2026-09-11"},
	}
	uiSortItems(items)
	got := ""
	for _, it := range items {
		got += it.ID
	}
	if got != "dbac" {
		t.Fatalf("порядок %q, ждали dbac: сначала открытое по сроку, потом без срока, потом закрытое", got)
	}
}

func TestUIHighlightMarksFound(t *testing.T) {
	s := uiHighlight("до " + core.MarkStart + "миграции" + core.MarkEnd + " после")
	if ansi.Strip(s) != "до миграции после" {
		t.Fatalf("подсветка испортила текст: %q", ansi.Strip(s))
	}
	if s == ansi.Strip(s) {
		t.Fatal("найденное ничем не выделено")
	}
}

// Первое, что человек делает в незнакомом интерфейсе, — жмёт стрелки. Раньше
// вправо-влево не делали ничего нигде, разделы переключались только цифрами и
// tab, и со стороны это выглядело как «ничего никуда не переходит».
func TestUIArrowsWorkOnEveryScreen(t *testing.T) {
	m := uiTestModel(t, uiSeed(t))

	// В списках вправо-влево ходят по разделам по кругу.
	n := int(tabCount)
	for i := 0; i < n; i++ {
		from := m.tab
		want := uiTab((int(m.tab) + 1) % n)
		press(m, "right")
		if m.tab != want {
			t.Fatalf("вправо из %d привело в %d, ждали %d", from, m.tab, want)
		}
	}
	for i := 0; i < n; i++ {
		from := m.tab
		want := uiTab((int(m.tab) + n - 1) % n)
		press(m, "left")
		if m.tab != want {
			t.Fatalf("влево из %d привело в %d, ждали %d", from, m.tab, want)
		}
	}

	// В каждом списке вверх-вниз двигают курсор.
	for _, tab := range []string{"1", "2", "3", "5"} {
		press(m, tab)
		if m.rowCount() < 2 {
			continue
		}
		press(m, "down")
		if m.cursor[m.tab] != 1 {
			t.Errorf("раздел %s: вниз не сдвинул курсор", tab)
		}
		press(m, "up")
		if m.cursor[m.tab] != 0 {
			t.Errorf("раздел %s: вверх не вернул курсор", tab)
		}
	}

	// Из карточки влево возвращает в список.
	press(m, "1", "enter")
	if m.screen() != scrMeeting {
		t.Fatalf("карточка не открылась")
	}
	press(m, "left")
	if m.screen() != scrList {
		t.Fatalf("влево из карточки не вернуло в список, экран %v", m.screen())
	}

	// И из расшифровки, и из справки — тоже.
	press(m, "enter", "t")
	if m.screen() != scrTranscript {
		t.Fatalf("расшифровка не открылась")
	}
	press(m, "left")
	if m.screen() != scrMeeting {
		t.Fatalf("влево из расшифровки не вернуло в карточку")
	}
	press(m, "left", "?")
	if m.screen() != scrHelp {
		t.Fatalf("справка не открылась")
	}
	press(m, "left")
	if m.screen() == scrHelp {
		t.Fatalf("влево не закрыло справку")
	}

	// В карточке вверх-вниз листают текст.
	press(m, "1", "enter", "t")
	// Окно нарочно ниже расшифровки: иначе листать нечего и проверка пуста.
	m.Update(tea.WindowSizeMsg{Width: 90, Height: uiChromeLines + 3})
	if len(m.body) <= m.listHeight() {
		t.Fatalf("расшифровка помещается целиком (%d строк в %d) — листать нечего",
			len(m.body), m.listHeight())
	}
	before := m.bodyOff
	press(m, "down")
	if m.bodyOff == before {
		t.Errorf("вниз не пролистало расшифровку")
	}
	press(m, "up")
	if m.bodyOff != before {
		t.Errorf("вверх не вернуло расшифровку назад")
	}
}

// «Поиск» открывается с курсором в строке запроса, и стрелка вправо уходила в
// пустое поле: человек, листающий разделы стрелками, застревал на четвёртом.
func TestUIArrowsDoNotTrapInSearch(t *testing.T) {
	m := uiTestModel(t, uiSeed(t))
	press(m, "4")
	if !m.searchFocus {
		t.Fatalf("поиск открылся не в строке запроса")
	}
	press(m, "right")
	if m.tab != tabChannels {
		t.Fatalf("вправо из пустого поиска привело в %d, ждали каналы", m.tab)
	}
	press(m, "left")
	if m.tab != tabSearch {
		t.Fatalf("влево не вернуло в поиск")
	}
	// И влево из поиска — тоже наружу, а не в пустое поле.
	press(m, "left")
	if m.tab != tabProjects {
		t.Fatalf("влево из пустого поиска привело в %d, ждали проекты", m.tab)
	}
	press(m, "right")

	// А в наборанном запросе стрелки снова двигают курсор по тексту, иначе
	// набранное нельзя поправить.
	press(m, "р", "е", "л")
	press(m, "left")
	if m.tab != tabSearch {
		t.Fatalf("влево в непустом запросе ушло из раздела")
	}
	if m.query.cur != 2 {
		t.Errorf("курсор в запросе на %d, ждали 2", m.query.cur)
	}
}

// В форме канала на строке-переключателе курсору вправо-влево ходить негде, и
// эти стрелки тоже должны переключать.
func TestUIArrowsToggleSwitchInChannelForm(t *testing.T) {
	m := uiTestModel(t, nil)
	press(m, "5")
	m.selectChannel("telegram")
	press(m, "enter")
	if m.chanForm == nil {
		t.Fatalf("форма канала не открылась")
	}
	was := m.chanForm.enabled
	press(m, "right")
	if m.chanForm.enabled == was {
		t.Errorf("вправо не переключило «канал включён»")
	}
	press(m, "left")
	if m.chanForm.enabled != was {
		t.Errorf("влево не переключило обратно")
	}
}

// Сплошной текст на широком окне нельзя тянуть во всю ширину: строка в двести
// колонок нечитаема — глаз теряет начало следующей, пока доходит до конца
// текущей. Таблицам это ограничение, наоборот, вредит.
func TestUIWideWindowKeepsTextReadable(t *testing.T) {
	// Риск нарочно длиннее любого окна: на коротком посеве проверка прошла бы
	// и без всякого ограничения ширины.
	long := strings.Repeat("миграция схемы платежей не проверялась на объёме прода, ", 12)
	m := uiTestModel(t, func(st *core.Store) {
		uiSeed(t)(st)
		f, err := st.Followup(uiTestMeeting)
		if err != nil {
			t.Fatal(err)
		}
		f.Risks = append(f.Risks, long)
		if err := st.SaveFollowup(uiTestMeeting, "claude", f); err != nil {
			t.Fatal(err)
		}
	})
	for _, w := range []int{120, 160, 200} {
		m.Update(tea.WindowSizeMsg{Width: w, Height: 40})
		press(m, "1", "enter")
		wide := 0
		for _, l := range m.body {
			n := ansi.StringWidth(l)
			if n > wide {
				wide = n
			}
			if n > uiProseWidth(w)+uiGutter {
				t.Errorf("ширина %d: строка карточки в %d колонок:\n%s", w, n, ansi.Strip(l))
			}
		}
		// Длинный текст обязан заполнить отведённое, иначе мерить нечего.
		if wide < 60 {
			t.Fatalf("ширина %d: самая длинная строка всего %d — проверка пуста", w, wide)
		}
		// И при этом текст не сжат в узкую полосу на любом окне.
		if uiProseWidth(w) < 80 {
			t.Errorf("ширина %d: текст сжат до %d колонок", w, uiProseWidth(w))
		}
		press(m, "left")
	}
}

// Обратная беда: таблица, растянутая на всё окно, разносит текст задачи и её
// срок на сотню пробелов, и строка перестаёт читаться как одна строка.
func TestUIWideWindowKeepsTablesTight(t *testing.T) {
	// Задача заведомо длиннее любого разумного предела: на коротком посеве
	// колонка и так не дотянулась бы до края, и проверка была бы пустой.
	m := uiTestModel(t, func(st *core.Store) {
		uiSeed(t)(st)
		if err := st.AddItem(core.ProjectItem{ID: "T-7777", Project: "Платежи", Kind: core.KindTask,
			Text:  strings.Repeat("разобрать миграцию схемы платежей ", 8),
			Owner: "Участник Б", OpenedIn: uiTestMeeting}); err != nil {
			t.Fatal(err)
		}
	})
	for _, w := range []int{120, 160, 200} {
		m.Update(tea.WindowSizeMsg{Width: w, Height: 40})
		for _, tab := range []string{"1", "2", "3", "5"} {
			press(m, tab)
			head := ansi.Strip(m.columns())
			// Между концом самого длинного значения главной колонки и началом
			// соседней колонки не должно быть пропасти.
			rows := m.content()
			longest := 0
			for _, r := range rows {
				plain := strings.TrimRight(ansi.Strip(r), " ")
				if n := ansi.StringWidth(plain); n > longest {
					longest = n
				}
			}
			// Предел назван числом, а не вызовом того же tableWidth: иначе
			// проверка сверяет функцию сама с собой и проходит при любой её
			// поломке.
			const readable = 150
			if longest > readable {
				t.Errorf("ширина %d, раздел %s: строка %d колонок, читаемый предел %d",
					w, tab, longest, readable)
			}
			if n := ansi.StringWidth(strings.TrimRight(head, " ")); n > readable {
				t.Errorf("ширина %d, раздел %s: заголовок в %d колонок", w, tab, n)
			}
		}
	}
}

// Главная колонка тянется за содержимым, а не за окном: на широком экране
// растёт название, а не пустота между колонками.
func TestUIMainColumnFollowsContentNotWindow(t *testing.T) {
	m := uiTestModel(t, uiSeed(t))
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 40})
	press(m, "2")
	whatNarrow, _, _, _, _ := m.colsTasks()

	// Колонка держится содержимого, а не окна: на двухстах колонках она обязана
	// остаться по длине самой длинной задачи, а не растянуться на весь запас.
	longest := 0
	for _, it := range m.visibleItems() {
		text := it.Text
		if it.Kind != core.KindTask {
			text = uiKindWord(it.Kind) + ": " + text
		}
		if n := ansi.StringWidth(text); n > longest {
			longest = n
		}
	}
	if whatNarrow > longest+4 {
		t.Errorf("колонка «что» в %d колонок при самой длинной задаче в %d — тянется за окном",
			whatNarrow, longest)
	}

	// Длинная задача — колонка обязана вырасти.
	m2 := uiTestModel(t, func(st *core.Store) {
		uiSeed(t)(st)
		must := func(err error) {
			t.Helper()
			if err != nil {
				t.Fatal(err)
			}
		}
		must(st.AddItem(core.ProjectItem{ID: "T-9999", Project: "Платежи", Kind: core.KindTask,
			Text: "разобрать, почему миграция схемы платежей не укладывается в окно " +
				"обслуживания, и предложить план на следующую неделю",
			Owner: "Участник Б", OpenedIn: uiTestMeeting}))
	})
	m2.Update(tea.WindowSizeMsg{Width: 200, Height: 40})
	press(m2, "2")
	whatWide, _, _, _, _ := m2.colsTasks()

	if whatWide <= whatNarrow {
		t.Errorf("колонка «что» не выросла под длинную задачу: %d и %d", whatNarrow, whatWide)
	}
	// Но и не съела всё окно.
	if whatWide > m2.tableWidth() {
		t.Errorf("колонка «что» в %d колонок шире таблицы в %d", whatWide, m2.tableWidth())
	}
}

// Заголовки колонок над пустым списком объясняют пустоту хуже, чем ничего:
// человек читает «что · кто · срок» и ищет строки, которых нет.
func TestUIEmptyListHasNoColumnHeaders(t *testing.T) {
	m := uiTestModel(t, nil)
	for _, tab := range []string{"1", "2", "3", "5"} {
		press(m, tab)
		if m.rowCount() != 0 {
			continue
		}
		if got := ansi.Strip(m.contextLine()); strings.TrimSpace(got) != "" {
			t.Errorf("раздел %s пуст, а заголовки колонок нарисованы: %q", tab, got)
		}
	}
	// А как только строки есть — заголовки на месте.
	m2 := uiTestModel(t, uiSeed(t))
	press(m2, "2")
	wantContains(t, ansi.Strip(m2.contextLine()), "срок", "у непустого списка заголовки колонок есть")
}

// Обрезанное значение не должно упираться многоточием прямо в соседнюю колонку:
// на узком окне строка склеивалась в «…в выхУчастник А».
func TestUINarrowColumnsKeepGap(t *testing.T) {
	m := uiTestModel(t, func(st *core.Store) {
		uiSeed(t)(st)
		must := func(err error) {
			t.Helper()
			if err != nil {
				t.Fatal(err)
			}
		}
		must(st.AddItem(core.ProjectItem{ID: "T-8888", Project: "Платежи", Kind: core.KindQuestion,
			Text: "очень длинный вопрос, который заведомо не влезает ни в какую колонку " +
				"и обязан быть обрезан", Owner: "Участник С", OpenedIn: uiTestMeeting}))
	})
	for _, w := range []int{60, 70, 80, 100} {
		m.Update(tea.WindowSizeMsg{Width: w, Height: 24})
		for _, tab := range []string{"1", "2", "3", "5"} {
			press(m, tab)
			for _, r := range m.content() {
				plain := ansi.Strip(r)
				if i := strings.Index(plain, "…"); i >= 0 && i+1 < len(plain) {
					// После многоточия обязан идти пробел, а не начало соседней
					// колонки.
					rest := plain[i+len("…"):]
					if rest != "" && !strings.HasPrefix(rest, " ") {
						t.Errorf("ширина %d, раздел %s: обрезка вплотную к соседу:\n%s",
							w, tab, plain)
					}
				}
			}
		}
	}
}

// Подсказка про стрелки — единственное, что объясняет навигацию человеку,
// который её не знает. На узком окне она укорачивается, но не исчезает.
func TestUIArrowHintSurvivesNarrowWindow(t *testing.T) {
	m := uiTestModel(t, uiSeed(t))
	for _, w := range []int{70, 80, 100, 120, 160} {
		m.Update(tea.WindowSizeMsg{Width: w, Height: 24})
		if got := ansi.Strip(m.tabBar()); !strings.Contains(got, "←") {
			t.Errorf("ширина %d: в шапке нет подсказки про стрелки:\n%s", w, got)
		}
	}
}

// Полосы сверху и снизу закрашены до самого края. Обёртка стилем поверх готовой
// строки этого не делала: внутри уже есть свои сбросы цвета, и фон гас на первом
// же из них — полоса обрывалась на середине экрана.
func TestUIBarsPaintedToTheEdge(t *testing.T) {
	m := uiTestModel(t, uiSeed(t))
	for _, w := range []int{80, 120, 160} {
		m.Update(tea.WindowSizeMsg{Width: w, Height: 24})
		// Подсказка снизу полосой больше не является: она идёт сразу под рамкой
		// поля и читается вместе с ним одним блоком. Раньше она была прибита к
		// нижнему краю, и между ней и содержимым зияли сорок пустых строк —
		// человек так и сказал: «глаза не знают куда смотреть». От неё теперь
		// требуется не ширина во весь экран, а то, чтобы она в него помещалась.
		title := m.titleBar()
		if n := ansi.StringWidth(title); n != w {
			t.Errorf("ширина %d: шапка занимает %d колонок", w, n)
		}
		// Сброс цвета, за которым идёт пробел, — это незакрашенный хвост.
		if i := strings.Index(title, "\x1b[0m "); i >= 0 {
			t.Errorf("ширина %d: шапка не закрашена с позиции %d:\n%q", w, i, title)
		}
		if n := ansi.StringWidth(m.hintLine()); n > w {
			t.Errorf("ширина %d: подсказка не влезает, занимает %d колонок", w, n)
		}
	}
}
