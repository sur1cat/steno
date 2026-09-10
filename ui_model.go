package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Состояние терминального интерфейса и переходы между экранами. Здесь нет ни
// одной строки отрисовки: всё, что рисуется, живёт в ui_view.go и читает эту
// структуру. Разделение не ради красоты — иначе «нажали d, задача закрылась»
// проверяется только глазами на живом терминале, а такие проверки не делают.

type uiTab int

const (
	tabMeetings uiTab = iota
	tabTasks
	tabProjects
	tabSearch
	tabChannels
	tabCount
)

// Считается один раз и при первом обращении, а не при инициализации
// пакета: язык к тому моменту ещё не прочитан из конфига.
var uiTabTitles = sync.OnceValue(func() [tabCount]string {
	return [tabCount]string{tr("Созвоны"), tr("Задачи"), tr("Проекты"), tr("Поиск"), tr("Каналы")}
})

type uiScreen int

const (
	scrList uiScreen = iota
	scrMeeting
	scrTranscript
	scrProject
	scrForm
	scrChannel
	scrPicker
	scrConfirm
	scrHelp
)

type uiPickTarget int

const (
	pickProject uiPickTarget = iota
	pickOwner
	pickKind
	pickSourceKind
)

// uiPicker — выбор одного значения из списка. Одним и тем же экраном выбирается
// и проект для фильтра, и вид источника в форме: список с курсором — то же
// самое, что и список созвонов, и заводить под каждый выбор свой экран незачем.
type uiPicker struct {
	title   string
	labels  []string
	values  []string
	cursor  int
	target  uiPickTarget
	current string
}

type uiAction int

const (
	actDeleteProject uiAction = iota
	actDeleteMeeting
)

type uiConfirm struct {
	text   string
	action uiAction
	arg    string
}

// uiFormSource — источник в форме проекта: вид выбран, значение набирается.
type uiFormSource struct {
	kind  string
	value textField
}

type uiForm struct {
	old     string // исходное название; "" — заводим новый проект
	name    textField
	about   textField
	aliases textField
	people  textField
	words   textField
	sources []uiFormSource
	field   int // см. uiFormFixed; дальше — источники
}

// uiFormFixed — поля формы до источников. Порядок здесь и в formView должен
// совпадать: номер поля — это и позиция курсора, и строка на экране.
const (
	uiFieldName = iota
	uiFieldAbout
	uiFieldAliases
	uiFieldPeople
	uiFieldWords
	uiFormFixed // сколько их всего; с этого номера начинаются источники
)

func (f *uiForm) fieldCount() int { return uiFormFixed + len(f.sources) }

func (f *uiForm) clampField() {
	if f.field < 0 {
		f.field = f.fieldCount() - 1
	}
	if f.field >= f.fieldCount() {
		f.field = 0
	}
}

// current — поле под курсором; nil, если курсор на несуществующем поле.
func (f *uiForm) current() *textField {
	switch {
	case f.field == uiFieldName:
		return &f.name
	case f.field == uiFieldAbout:
		return &f.about
	case f.field == uiFieldAliases:
		return &f.aliases
	case f.field == uiFieldPeople:
		return &f.people
	case f.field == uiFieldWords:
		return &f.words
	case f.field-uiFormFixed < len(f.sources):
		return &f.sources[f.field-uiFormFixed].value
	}
	return nil
}

func (f *uiForm) currentSource() int {
	if i := f.field - uiFormFixed; i >= 0 && i < len(f.sources) {
		return i
	}
	return -1
}

type uiModel struct {
	cfg *Config
	st  *Store

	w, h int

	tab   uiTab
	stack []uiScreen

	// Списки. Читаются целиком при запуске и после каждой правки: база
	// локальная, а «показать свежее» без лишней кнопки дороже пары запросов.
	meetings []MeetingRow
	items    []ProjectItem
	projects []uiProjectRow
	hits     []SearchHit
	channels []Channel

	cursor  [tabCount]int
	listOff [tabCount]int

	filter    [tabCount]textField
	filtering bool

	itemFilter uiItemFilter

	query       textField
	searchFocus bool

	// Открытая карточка.
	meeting     *Meeting
	followup    *Followup
	segments    []Segment
	links       map[string]string
	projectName string

	// Тело карточки: готовые строки и прокрутка. Строки живут в модели, а не
	// собираются на каждый кадр, потому что от их числа зависит, докуда можно
	// прокрутить, — а это уже состояние.
	body     []string
	bodyOff  int
	segLines []int // на какой строке расшифровки начинается сегмент

	// Follow-up выделенного созвона — для нижней панели. Помним, чей именно:
	// панель перерисовывается на каждый кадр, а ходить в базу на каждый кадр
	// незачем.
	peekID   string
	peek     *Followup
	peekTags []string

	form     *uiForm
	chanForm *uiChanForm
	picker   *uiPicker
	confirm  *uiConfirm

	// Проекты, по которым прямо сейчас собирается справка.
	building map[string]bool

	status   string
	statusIs int // 0 — обычное сообщение, 1 — ошибка, 2 — удача
}

const (
	uiPlain = 0
	uiErr   = 1
	uiOK    = 2
)

func newUIModel(cfg *Config, st *Store) *uiModel {
	return &uiModel{
		cfg: cfg, st: st,
		w: 80, h: 24,
		stack:    []uiScreen{scrList},
		building: map[string]bool{},
	}
}

func (m *uiModel) Init() tea.Cmd { return nil }

func (m *uiModel) screen() uiScreen { return m.stack[len(m.stack)-1] }

func (m *uiModel) push(s uiScreen) {
	m.stack = append(m.stack, s)
	m.bodyOff = 0
	m.rebuildBody()
}

func (m *uiModel) pop() {
	if len(m.stack) > 1 {
		m.stack = m.stack[:len(m.stack)-1]
	}
	m.bodyOff = 0
	m.rebuildBody()
}

func (m *uiModel) fail(err error) {
	m.status = err.Error()
	m.statusIs = uiErr
}

func (m *uiModel) say(format string, args ...any) {
	m.sayText(fmt.Sprintf(format, args...))
}

// sayText — say для готовой строки. Отдельный метод, а не say без аргументов:
// say — обёртка над Printf, и переведённая строка в роли формата справедливо
// ловится go vet.
func (m *uiModel) sayText(s string) {
	m.status = s
	m.statusIs = uiOK
}

// --- загрузка ---------------------------------------------------------------

// reload перечитывает всё из базы. Отдельных «обнови только это» нет намеренно:
// закрытая задача меняет и счётчики проектов, и карточку созвона, и частичное
// обновление разошлось бы с базой на первом же неучтённом месте.
func (m *uiModel) reload() error {
	rows, err := m.st.ListMeetings(500, 0)
	if err != nil {
		return err
	}
	m.meetings = rows

	items, err := uiLoadItems(m.st)
	if err != nil {
		return err
	}
	m.items = items

	projects, err := uiLoadProjects(m.st)
	if err != nil {
		return err
	}
	m.projects = projects

	// Каналы читаем тем же способом, что панель: описание полей из channelDefs,
	// значения — из базы поверх конфига.
	m.channels = panelChannels(m.st, m.cfg)

	m.runSearch()
	m.clampCursor()
	m.rebuildBody()
	return nil
}

func (m *uiModel) runSearch() {
	q := strings.TrimSpace(m.query.String())
	if q == "" {
		m.hits = nil
		return
	}
	hits, err := m.st.Search(q, 200)
	if err != nil {
		m.hits = nil
		m.fail(err)
		return
	}
	m.hits = hits
	if m.cursor[tabSearch] >= len(hits) {
		m.cursor[tabSearch] = 0
	}
}

// --- видимые списки ----------------------------------------------------------

func (m *uiModel) visibleMeetings() []MeetingRow {
	return uiFilterMeetings(m.meetings, m.filter[tabMeetings].String())
}

func (m *uiModel) visibleItems() []ProjectItem {
	f := m.itemFilter
	f.Text = m.filter[tabTasks].String()
	return uiFilterItems(m.items, f)
}

func (m *uiModel) visibleProjects() []uiProjectRow {
	return uiFilterProjects(m.projects, m.filter[tabProjects].String())
}

func (m *uiModel) visibleChannels() []Channel {
	return uiFilterChannels(m.channels, m.filter[tabChannels].String())
}

func (m *uiModel) rowCount() int {
	switch m.tab {
	case tabMeetings:
		return len(m.visibleMeetings())
	case tabTasks:
		return len(m.visibleItems())
	case tabProjects:
		return len(m.visibleProjects())
	case tabSearch:
		return len(m.hits)
	case tabChannels:
		return len(m.visibleChannels())
	}
	return 0
}

func (m *uiModel) clampCursor() {
	n := m.rowCount()
	c := m.cursor[m.tab]
	if c >= n {
		c = n - 1
	}
	if c < 0 {
		c = 0
	}
	m.cursor[m.tab] = c

	h := m.listHeight()
	off := m.listOff[m.tab]
	if c < off {
		off = c
	}
	if c >= off+h {
		off = c - h + 1
	}
	if off > n-h {
		off = n - h
	}
	if off < 0 {
		off = 0
	}
	m.listOff[m.tab] = off
}

// listHeight — сколько строк списка помещается: экран минус шапка и подвал.
func (m *uiModel) listHeight() int {
	h := m.h - uiChromeLines
	if h < 1 {
		h = 1
	}
	return h
}

func (m *uiModel) selectedMeeting() (MeetingRow, bool) {
	rows := m.visibleMeetings()
	if i := m.cursor[tabMeetings]; i >= 0 && i < len(rows) {
		return rows[i], true
	}
	return MeetingRow{}, false
}

func (m *uiModel) selectedItem() (ProjectItem, bool) {
	rows := m.visibleItems()
	if i := m.cursor[tabTasks]; i >= 0 && i < len(rows) {
		return rows[i], true
	}
	return ProjectItem{}, false
}

func (m *uiModel) selectedProject() (uiProjectRow, bool) {
	rows := m.visibleProjects()
	if i := m.cursor[tabProjects]; i >= 0 && i < len(rows) {
		return rows[i], true
	}
	return uiProjectRow{}, false
}

func (m *uiModel) selectedChannel() (Channel, bool) {
	rows := m.visibleChannels()
	if i := m.cursor[tabChannels]; i >= 0 && i < len(rows) {
		return rows[i], true
	}
	return Channel{}, false
}

func (m *uiModel) selectedHit() (SearchHit, bool) {
	if i := m.cursor[tabSearch]; i >= 0 && i < len(m.hits) {
		return m.hits[i], true
	}
	return SearchHit{}, false
}

// openProject — карточка проекта по имени; nil, если такого нет.
func (m *uiModel) openProjectRow() (uiProjectRow, bool) {
	for _, p := range m.projects {
		if p.Name == m.projectName {
			return p, true
		}
	}
	return uiProjectRow{}, false
}

// --- обработка сообщений -----------------------------------------------------

func (m *uiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.rebuildBody()
		m.clampCursor()
		return m, nil
	case uiContextDone:
		delete(m.building, msg.Project)
		if msg.Err != nil {
			m.fail(fmt.Errorf(tr("справка «%s»: %v"), msg.Project, msg.Err))
		} else {
			m.say(tr("справка «%s» собрана, %s"), msg.Project, msg.Spend)
			if err := m.reload(); err != nil {
				m.fail(err)
			}
		}
		return m, nil
	case tea.KeyMsg:
		return m, m.handleKey(msg)
	}
	return m, nil
}

// handleKey — единственная точка, где интерфейс меняется от нажатия. Возвращает
// команду bubbletea (выход или фоновая работа); всё остальное — правка модели.
func (m *uiModel) handleKey(msg tea.KeyMsg) tea.Cmd {
	k := msg.String()
	if k == "ctrl+c" {
		return tea.Quit
	}
	m.status, m.statusIs = "", uiPlain

	switch m.screen() {
	case scrHelp:
		if k == "esc" || k == "?" || k == "q" || k == "enter" || k == "left" {
			m.pop()
		} else {
			m.scrollBody(k)
		}
		return nil
	case scrConfirm:
		return m.keyConfirm(k)
	case scrPicker:
		return m.keyPicker(k)
	case scrForm:
		return m.keyForm(msg)
	case scrChannel:
		return m.keyChannelForm(msg)
	case scrMeeting, scrTranscript, scrProject:
		return m.keyCard(k)
	}

	if m.editing() {
		return m.keyEditing(msg)
	}
	return m.keyList(k)
}

// editing — идёт ли набор текста. Пока он идёт, буквы попадают в поле, а не в
// команды: иначе «q» в слове «квартал» закрывало бы интерфейс.
func (m *uiModel) editing() bool {
	if m.screen() != scrList {
		return false
	}
	if m.tab == tabSearch {
		return m.searchFocus
	}
	return m.filtering
}

func (m *uiModel) keyEditing(msg tea.KeyMsg) tea.Cmd {
	field := &m.filter[m.tab]
	if m.tab == tabSearch {
		field = &m.query
	}
	before := field.String()

	switch msg.String() {
	case "esc":
		if m.tab == tabSearch {
			m.searchFocus = false
			if len(m.hits) == 0 {
				m.query.clear()
			}
		} else {
			field.clear()
			m.filtering = false
		}
	case "enter":
		if m.tab == tabSearch {
			m.searchFocus = false
		} else {
			m.filtering = false
		}
	// В пустом поле курсору ходить некуда, а «Поиск» открывается сразу с
	// курсором в строке запроса: без этого стрелка вправо упиралась в пустое
	// поле, и человек, листающий разделы стрелками, застревал на четвёртом.
	case "left":
		if field.empty() {
			m.setTab((m.tab + tabCount - 1) % tabCount)
			return nil
		}
		field.left()
	case "right":
		if field.empty() {
			m.setTab((m.tab + 1) % tabCount)
			return nil
		}
		field.right()
	case "home", "ctrl+a":
		field.home()
	case "end", "ctrl+e":
		field.end()
	case "backspace":
		field.backspace()
	case "delete":
		field.del()
	case "ctrl+w":
		field.killWord()
	case "ctrl+u":
		field.clear()
	case "up", "down", "pgup", "pgdown":
		// Стрелки в однострочном поле делать нечего, а найденное листать надо
		// прямо во время набора: иначе, чтобы посмотреть выдачу, приходится
		// сначала выходить из ввода.
		m.moveCursor(msg.String())
	case "tab":
		m.setTab((m.tab + 1) % tabCount)
		return nil
	case "shift+tab":
		m.setTab((m.tab + tabCount - 1) % tabCount)
		return nil
	default:
		if len(msg.Runes) > 0 {
			field.insert(msg.Runes)
		}
	}

	if field.String() != before {
		if m.tab == tabSearch {
			m.runSearch()
		}
		m.cursor[m.tab] = 0
		m.listOff[m.tab] = 0
	}
	m.clampCursor()
	return nil
}

func (m *uiModel) keyList(k string) tea.Cmd {
	switch k {
	case "q":
		return tea.Quit
	case "?":
		m.push(scrHelp)
		return nil
	case "1":
		m.setTab(tabMeetings)
		return nil
	case "2":
		m.setTab(tabTasks)
		return nil
	case "3":
		m.setTab(tabProjects)
		return nil
	case "4":
		m.setTab(tabSearch)
		return nil
	case "5":
		m.setTab(tabChannels)
		return nil
	// Стрелки — первое, что человек нажимает, и раньше вправо-влево не делали
	// ничего: разделы переключались только цифрами и tab, о которых надо было
	// сначала прочитать в справке.
	case "tab", "right":
		m.setTab((m.tab + 1) % tabCount)
		return nil
	case "shift+tab", "left":
		m.setTab((m.tab + tabCount - 1) % tabCount)
		return nil
	case "r":
		if err := m.reload(); err != nil {
			m.fail(err)
		} else {
			m.sayText(tr("перечитал из базы"))
		}
		return nil
	case "esc":
		m.clearFilters()
		return nil
	case "/":
		if m.tab == tabSearch {
			m.searchFocus = true
		} else {
			m.filtering = true
			m.filter[m.tab].end()
		}
		return nil
	}

	if m.moveCursor(k) {
		return nil
	}

	switch m.tab {
	case tabMeetings:
		return m.keyMeetings(k)
	case tabTasks:
		return m.keyTasks(k)
	case tabProjects:
		return m.keyProjects(k)
	case tabSearch:
		return m.keySearch(k)
	case tabChannels:
		return m.keyChannels(k)
	}
	return nil
}

func (m *uiModel) setTab(t uiTab) {
	m.tab = t
	m.filtering = false
	m.searchFocus = t == tabSearch && len(m.hits) == 0
	m.clampCursor()
}

func (m *uiModel) clearFilters() {
	switch m.tab {
	case tabSearch:
		m.query.clear()
		m.runSearch()
		m.searchFocus = true
	default:
		m.filter[m.tab].clear()
		if m.tab == tabTasks {
			m.itemFilter = uiItemFilter{}
		}
	}
	m.cursor[m.tab] = 0
	m.listOff[m.tab] = 0
	m.clampCursor()
}

// moveCursor — движение по списку. Возвращает true, если клавиша была про него.
func (m *uiModel) moveCursor(k string) bool {
	n := m.rowCount()
	h := m.listHeight()
	c := m.cursor[m.tab]
	switch k {
	case "up", "k", "ctrl+p":
		c--
	case "down", "j", "ctrl+n":
		c++
	case "pgup", "ctrl+b":
		c -= h
	// Пробел здесь не листает, хотя в читалке это привычно: в списке каналов он
	// включает и выключает канал, и одна и та же клавиша не может значить в
	// соседних списках разное. Страница листается pgdn и ctrl+f.
	case "pgdown", "ctrl+f":
		c += h
	case "home", "g":
		c = 0
	case "end", "G":
		c = n - 1
	default:
		return false
	}
	if c < 0 {
		c = 0
	}
	if c > n-1 {
		c = n - 1
	}
	m.cursor[m.tab] = c
	m.clampCursor()
	return true
}

func (m *uiModel) keyMeetings(k string) tea.Cmd {
	switch k {
	case "enter":
		if row, ok := m.selectedMeeting(); ok {
			m.openMeeting(row.ID, scrMeeting, -1)
		}
	case "t":
		if row, ok := m.selectedMeeting(); ok {
			m.openMeeting(row.ID, scrTranscript, -1)
		}
	// Заглавная D — как у проектов: удаление не должно стоять на той же
	// клавише, что «сделана» (d) в соседнем разделе.
	case "D":
		if row, ok := m.selectedMeeting(); ok {
			m.askDeleteMeeting(row.ID, row.Title)
		}
	}
	return nil
}

func (m *uiModel) keyTasks(k string) tea.Cmd {
	switch k {
	case "enter":
		it, ok := m.selectedItem()
		if !ok {
			return nil
		}
		if it.OpenedIn == "" {
			m.fail(fmt.Errorf(tr("у пункта %s не записан созвон, на котором он появился"), it.ID))
			return nil
		}
		m.openMeeting(it.OpenedIn, scrMeeting, -1)
	case "d":
		m.closeSelectedItem("done", tr("закрыто из терминала"))
	case "x":
		m.closeSelectedItem("dropped", tr("снято из терминала"))
	case "u":
		it, ok := m.selectedItem()
		if !ok {
			return nil
		}
		if it.Status == "open" {
			m.fail(fmt.Errorf(tr("%s и так открыт"), it.ID))
			return nil
		}
		if err := m.st.ReopenItem(it.ID); err != nil {
			m.fail(err)
			return nil
		}
		m.afterItemChange(tr("%s снова в работе"), it.ID)
	case "p":
		m.openProjectPicker()
	case "o":
		m.openOwnerPicker()
	case "v":
		m.openKindPicker()
	case "a":
		m.itemFilter.Closed = !m.itemFilter.Closed
		m.cursor[tabTasks] = 0
		m.clampCursor()
	case "c":
		m.itemFilter = uiItemFilter{}
		m.filter[tabTasks].clear()
		m.cursor[tabTasks] = 0
		m.clampCursor()
		m.sayText(tr("фильтры сняты"))
	}
	return nil
}

func (m *uiModel) closeSelectedItem(status, note string) {
	it, ok := m.selectedItem()
	if !ok {
		return
	}
	if it.Status != "open" {
		m.fail(fmt.Errorf(tr("%s уже закрыт"), it.ID))
		return
	}
	if err := m.st.CloseItem(it.ID, status, note, ""); err != nil {
		m.fail(err)
		return
	}
	word := tr("сделана")
	if status == "dropped" {
		word = tr("снята")
	}
	m.afterItemChange("%s — %s", it.ID, word)
}

func (m *uiModel) afterItemChange(format string, args ...any) {
	if err := m.reload(); err != nil {
		m.fail(err)
		return
	}
	m.clampCursor()
	m.say(format, args...)
}

func (m *uiModel) openProjectPicker() {
	labels := []string{tr("все проекты")}
	values := []string{""}
	for _, p := range m.projects {
		labels = append(labels, p.Name)
		values = append(values, p.Name)
	}
	m.showPicker(&uiPicker{title: tr("Проект"), labels: labels, values: values,
		target: pickProject, current: m.itemFilter.Project})
}

func (m *uiModel) openOwnerPicker() {
	labels := []string{tr("все исполнители")}
	values := []string{""}
	for _, o := range uiOwners(m.items) {
		labels = append(labels, o)
		values = append(values, o)
	}
	m.showPicker(&uiPicker{title: tr("Исполнитель"), labels: labels, values: values,
		target: pickOwner, current: m.itemFilter.Owner})
}

func (m *uiModel) openKindPicker() {
	m.showPicker(&uiPicker{
		title:  tr("Вид пункта"),
		labels: []string{tr("всё"), tr("задачи"), tr("открытые вопросы"), tr("решения")},
		values: []string{"", string(KindTask), string(KindQuestion), string(KindDecision)},
		target: pickKind, current: string(m.itemFilter.Kind)})
}

func (m *uiModel) showPicker(p *uiPicker) {
	for i, v := range p.values {
		if v == p.current {
			p.cursor = i
		}
	}
	m.picker = p
	m.push(scrPicker)
}

func (m *uiModel) keyPicker(k string) tea.Cmd {
	p := m.picker
	if p == nil {
		m.pop()
		return nil
	}
	switch k {
	case "esc", "q", "left":
		m.picker = nil
		m.pop()
	case "up", "k":
		if p.cursor > 0 {
			p.cursor--
		}
	case "down", "j":
		if p.cursor < len(p.values)-1 {
			p.cursor++
		}
	case "home", "g":
		p.cursor = 0
	case "end", "G":
		p.cursor = len(p.values) - 1
	case "enter", "right":
		v := ""
		if p.cursor >= 0 && p.cursor < len(p.values) {
			v = p.values[p.cursor]
		}
		switch p.target {
		case pickProject:
			m.itemFilter.Project = v
			m.cursor[tabTasks] = 0
		case pickOwner:
			m.itemFilter.Owner = v
			m.cursor[tabTasks] = 0
		case pickKind:
			m.itemFilter.Kind = ItemKind(v)
			m.cursor[tabTasks] = 0
		case pickSourceKind:
			if m.form != nil {
				m.form.sources = append(m.form.sources, uiFormSource{kind: v})
				m.form.field = uiFormFixed + len(m.form.sources) - 1
			}
		}
		m.picker = nil
		m.pop()
		m.clampCursor()
	}
	return nil
}

func (m *uiModel) keyProjects(k string) tea.Cmd {
	switch k {
	case "enter":
		if p, ok := m.selectedProject(); ok {
			m.projectName = p.Name
			m.push(scrProject)
		}
	case "n":
		m.form = &uiForm{}
		m.push(scrForm)
	case "e":
		if p, ok := m.selectedProject(); ok {
			m.editProject(p)
		}
	case "D":
		if p, ok := m.selectedProject(); ok {
			m.askDeleteProject(p)
		}
	case "s":
		if p, ok := m.selectedProject(); ok {
			return m.buildContext(p)
		}
	}
	return nil
}

func (m *uiModel) editProject(p uiProjectRow) {
	f := &uiForm{old: p.Name}
	f.name = newTextField(p.Name)
	f.about = newTextField(p.About)
	f.aliases = newTextField(strings.Join(p.Aliases, ", "))
	f.people = newTextField(strings.Join(p.People, ", "))
	// Имена людей лежат и в общем словаре — их туда сводит SaveProject ради
	// whisper. В форме показываем словарь без них: иначе в двух полях подряд
	// одно и то же, и стёртое в одном остаётся в другом.
	f.words = newTextField(strings.Join(p.project().otherWords(), ", "))
	for _, s := range p.Sources {
		f.sources = append(f.sources, uiFormSource{kind: s.Kind, value: newTextField(s.Value)})
	}
	m.form = f
	m.push(scrForm)
}

func (m *uiModel) askDeleteProject(p uiProjectRow) {
	if !p.Registered {
		m.fail(fmt.Errorf(tr("«%s» в реестре нет — он только упомянут в задачах, удалять нечего"), p.Name))
		return
	}
	m.confirm = &uiConfirm{
		text:   fmt.Sprintf(tr("Удалить проект «%s»? Задачи и решения по нему останутся."), p.Name),
		action: actDeleteProject, arg: p.Name,
	}
	m.push(scrConfirm)
}

// askDeleteMeeting спрашивает, назвав цену. Название созвона о цене не говорит
// ничего: вместе с ним уходят задачи и решения проектов, а чужие пункты,
// закрытые на этом созвоне, возвращаются в работу. Считает всё это MeetingToll
// — та же функция, что зовут панель и `steno rm`.
func (m *uiModel) askDeleteMeeting(id, title string) {
	toll, err := m.st.MeetingToll(id)
	if err != nil {
		m.fail(err)
		return
	}
	name := strings.TrimSpace(title)
	if name == "" {
		name = id
	}
	m.confirm = &uiConfirm{
		text:   fmt.Sprintf(tr("Удалить созвон «%s»? Навсегда, %s."), name, toll.Text),
		action: actDeleteMeeting, arg: id,
	}
	m.push(scrConfirm)
}

func (m *uiModel) keyConfirm(k string) tea.Cmd {
	c := m.confirm
	// Согласие — только «y». Enter на вопросе «удалить?» слишком легко нажать
	// по инерции после Enter, которым карточку открыли.
	switch k {
	// Стрелки здесь не значат ничего и не должны закрывать вопрос: выбирать
	// не из чего, а случайное нажатие не повод молча отменить действие.
	case "up", "down", "left", "right":
		return nil
	case "y", "Y", "н", "Н":
		m.confirm = nil
		m.pop()
		if c == nil {
			return nil
		}
		switch c.action {
		case actDeleteProject:
			if err := m.st.DeleteProject(c.arg); err != nil {
				m.fail(err)
				return nil
			}
			if err := m.reload(); err != nil {
				m.fail(err)
				return nil
			}
			// Карточка удалённого проекта осталась бы на экране пустой.
			if m.screen() == scrProject && m.projectName == c.arg {
				m.pop()
			}
			m.clampCursor()
			m.say(tr("проект «%s» удалён; задачи и решения по нему остались"), c.arg)
		case actDeleteMeeting:
			toll, err := m.st.DeleteMeeting(c.arg)
			if err != nil {
				m.fail(err)
				return nil
			}
			// Карточка удалённого созвона осталась бы на экране пустой, а из
			// расшифровки «назад» вело бы в неё же. Уходим со всех его экранов.
			for m.meeting != nil && m.meeting.ID == c.arg &&
				(m.screen() == scrMeeting || m.screen() == scrTranscript) {
				m.pop()
			}
			if m.meeting != nil && m.meeting.ID == c.arg {
				m.meeting, m.followup, m.segments, m.links = nil, nil, nil, nil
			}
			// Нижняя панель помнит follow-up выделенной строки. Его больше нет,
			// а помеченный прочитанным peekID не дал бы перечитать.
			m.peekID, m.peek, m.peekTags = "", nil, nil
			if err := m.reload(); err != nil {
				m.fail(err)
				return nil
			}
			m.clampCursor()
			if toll.Reopen > 0 {
				m.say(tr("созвон удалён; вернулось в работу: %s"), toll.reopenWords())
			} else {
				m.sayText(tr("созвон удалён"))
			}
		}
	default:
		m.confirm = nil
		m.pop()
		m.sayText(tr("отменил"))
	}
	return nil
}

func (m *uiModel) buildContext(p uiProjectRow) tea.Cmd {
	if len(p.Sources) == 0 {
		m.fail(fmt.Errorf(tr("у «%s» нет источников — добавь репозиторий или каталог (e — правка)"), p.Name))
		return nil
	}
	if m.building[p.Name] {
		m.say(tr("справка «%s» уже собирается"), p.Name)
		return nil
	}
	m.building[p.Name] = true
	m.say(tr("собираю справку «%s» — это поход в Claude, займёт до минуты"), p.Name)
	cfg, st, pr := m.cfg, m.st, p.project()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		return uiBuildContext(ctx, cfg, st, pr)
	}
}

func (m *uiModel) keySearch(k string) tea.Cmd {
	switch k {
	case "enter":
		if h, ok := m.selectedHit(); ok {
			// Найденное в follow-up читается в follow-up, найденное в
			// расшифровке — в расшифровке, с прокруткой к тому месту.
			if h.Kind == "расшифровка" {
				m.openMeeting(h.MeetingID, scrTranscript, h.At)
			} else {
				m.openMeeting(h.MeetingID, scrMeeting, -1)
			}
		}
	case "i":
		m.searchFocus = true
	}
	return nil
}

// openMeeting открывает карточку созвона. at ≥ 0 — прокрутить расшифровку к
// этой секунде: иначе переход из поиска высаживает в начало часовой записи.
func (m *uiModel) openMeeting(id string, screen uiScreen, at float64) {
	mt, err := m.st.Meeting(id)
	if err != nil {
		m.fail(fmt.Errorf(tr("созвон %s: %v"), id, err))
		return
	}
	m.meeting = mt
	if f, err := m.st.Followup(id); err == nil {
		m.followup = f
	} else {
		m.followup = nil
	}
	m.segments, _ = m.st.Segments(id)
	m.links, _ = m.st.Publications(id)
	m.push(screen)
	if screen == scrTranscript && at >= 0 {
		m.bodyOff = m.lineForSecond(at)
		m.clampBody()
	}
}

// lineForSecond — строка расшифровки, ближайшая к секунде. Точного совпадения
// не бывает: сниппет поиска приходит с временем сегмента, а строк у сегмента
// может быть несколько.
func (m *uiModel) lineForSecond(at float64) int {
	best := 0
	for i, sg := range m.segments {
		if i >= len(m.segLines) {
			break
		}
		if sg.Start <= at+0.001 {
			best = m.segLines[i]
			continue
		}
		break
	}
	return best
}

func (m *uiModel) keyCard(k string) tea.Cmd {
	switch k {
	case "q":
		return tea.Quit
	// Влево — назад: в карточку пришли из списка, и стрелка обратно читается
	// без объяснений. Esc делает то же самое.
	case "esc", "left":
		m.pop()
		return nil
	case "?":
		m.push(scrHelp)
		return nil
	}

	switch m.screen() {
	case scrMeeting:
		switch k {
		case "t":
			if len(m.segments) == 0 {
				m.fail(errors.New(tr("расшифровки нет — созвон ещё не разобран")))
				return nil
			}
			m.push(scrTranscript)
			return nil
		case "D":
			if m.meeting != nil {
				m.askDeleteMeeting(m.meeting.ID, m.meeting.Title)
			}
			return nil
		}
	case scrTranscript:
		// Та же клавиша, что на карточке: расшифровка — это тот же созвон, и
		// решение удалить его чаще всего принимают, дочитав именно её.
		if k == "D" && m.meeting != nil {
			m.askDeleteMeeting(m.meeting.ID, m.meeting.Title)
			return nil
		}
	case scrProject:
		switch k {
		case "e":
			if p, ok := m.openProjectRow(); ok {
				m.editProject(p)
			}
			return nil
		case "s":
			if p, ok := m.openProjectRow(); ok {
				return m.buildContext(p)
			}
			return nil
		case "D":
			if p, ok := m.openProjectRow(); ok {
				m.askDeleteProject(p)
			}
			return nil
		}
	}
	m.scrollBody(k)
	return nil
}

func (m *uiModel) scrollBody(k string) {
	h := m.listHeight()
	switch k {
	case "up", "k", "ctrl+p":
		m.bodyOff--
	case "down", "j", "ctrl+n":
		m.bodyOff++
	case "pgup", "ctrl+b":
		m.bodyOff -= h
	case "pgdown", "ctrl+f", " ":
		m.bodyOff += h
	case "home", "g":
		m.bodyOff = 0
	case "end", "G":
		m.bodyOff = len(m.body)
	default:
		return
	}
	m.clampBody()
}

func (m *uiModel) clampBody() {
	maxOff := len(m.body) - m.listHeight()
	if m.bodyOff > maxOff {
		m.bodyOff = maxOff
	}
	if m.bodyOff < 0 {
		m.bodyOff = 0
	}
}

// --- форма проекта -----------------------------------------------------------

func (m *uiModel) keyForm(msg tea.KeyMsg) tea.Cmd {
	f := m.form
	if f == nil {
		m.pop()
		return nil
	}
	switch msg.String() {
	case "esc":
		m.form = nil
		m.pop()
		return nil
	case "ctrl+s":
		m.saveForm()
		return nil
	case "tab", "down":
		f.field++
		f.clampField()
		return nil
	case "shift+tab", "up":
		f.field--
		f.clampField()
		return nil
	case "ctrl+n":
		m.showPicker(&uiPicker{title: tr("Вид источника"),
			labels: []string{tr("репозиторий (git-ссылка)"), tr("каталог с кодом на этой машине"),
				tr("сайт или документ"), tr("просто текст")},
			values: uiSourceKinds, target: pickSourceKind})
		return nil
	case "ctrl+k":
		if i := f.currentSource(); i >= 0 {
			f.sources = append(f.sources[:i], f.sources[i+1:]...)
			f.field = uiFormFixed + i
			f.clampField()
		} else {
			m.fail(errors.New(tr("ctrl+k убирает источник — встань на строку источника")))
		}
		return nil
	case "ctrl+t":
		if i := f.currentSource(); i >= 0 {
			f.sources[i].kind = uiNextSourceKind(f.sources[i].kind)
		} else {
			m.fail(errors.New(tr("ctrl+t меняет вид источника — встань на строку источника")))
		}
		return nil
	}

	field := f.current()
	if field == nil {
		return nil
	}
	switch msg.String() {
	case "left":
		field.left()
	case "right":
		field.right()
	case "home", "ctrl+a":
		field.home()
	case "end", "ctrl+e":
		field.end()
	case "backspace":
		field.backspace()
	case "delete":
		field.del()
	case "ctrl+w":
		field.killWord()
	case "ctrl+u":
		field.clear()
	case "enter":
		f.field++
		f.clampField()
	default:
		if len(msg.Runes) > 0 {
			field.insert(msg.Runes)
		}
	}
	return nil
}

// --- каналы ------------------------------------------------------------------

func (m *uiModel) keyChannels(k string) tea.Cmd {
	switch k {
	case "enter", "e":
		if ch, ok := m.selectedChannel(); ok {
			m.chanForm = newChanForm(ch)
			m.push(scrChannel)
		}
	case " ", "x":
		// Включить и выключить — самое частое действие, и лезть ради него в
		// форму незачем.
		ch, ok := m.selectedChannel()
		if !ok {
			return nil
		}
		if err := uiSaveChannel(m.st, ch.Key, !ch.Enabled, ch.Values); err != nil {
			m.fail(err)
			return nil
		}
		m.afterChannelChange(ch.Key, !ch.Enabled)
	}
	return nil
}

func (m *uiModel) afterChannelChange(key string, enabled bool) {
	if err := m.reload(); err != nil {
		m.fail(err)
		return
	}
	m.selectChannel(key)
	name := key
	if d, ok := channelByKey(key); ok {
		name = d.name
	}
	word := tr("выключен")
	if enabled {
		word = tr("включён")
	}
	// Часть каналов слушает сеть с самого старта, и переключить их на ходу
	// нельзя: молчать об этом — значит оставить человека ждать того, чего не
	// будет, пока он не перезапустит сервис.
	tail := ""
	if d, ok := channelByKey(key); ok && !d.live {
		tail = tr("; применится после перезапуска serve")
	}
	m.say("«%s» %s%s", name, word, tail)
}

func (m *uiModel) selectChannel(key string) {
	for i, c := range m.visibleChannels() {
		if c.Key == key {
			m.cursor[tabChannels] = i
			m.clampCursor()
			return
		}
	}
}

func (m *uiModel) keyChannelForm(msg tea.KeyMsg) tea.Cmd {
	f := m.chanForm
	if f == nil {
		m.pop()
		return nil
	}
	row := f.current()
	switch msg.String() {
	case "esc":
		m.chanForm = nil
		m.pop()
		return nil
	case "ctrl+s":
		m.saveChannelForm()
		return nil
	case "tab", "down":
		f.cursor++
		f.clampCursor()
		return nil
	case "shift+tab", "up":
		f.cursor--
		f.clampCursor()
		return nil
	case "ctrl+n":
		field := row.field
		if field < 0 || f.fields[field].def.Kind != "list" {
			m.fail(errors.New(tr("ctrl+n добавляет значение в список — встань на строку списка")))
			return nil
		}
		f.addItem(field)
		return nil
	case "ctrl+k":
		if !f.removeItem(row.field, row.item) {
			m.fail(errors.New(tr("ctrl+k убирает значение списка — встань на такую строку")))
		}
		return nil
	}

	// Переключатели и «добавить значение» — по пробелу и enter. На строке с
	// текстом пробел остаётся пробелом: иначе его нельзя набрать.
	field := f.currentText()
	if field == nil {
		// На строке без текста стрелки вправо-влево тоже переключают: курсору
		// в этой строке ходить всё равно негде, а человек жмёт их первыми.
		if k := msg.String(); k == " " || k == "enter" || k == "left" || k == "right" {
			switch {
			case row.item == uiRowAdd:
				if k == "left" {
					return nil
				}
				f.addItem(row.field)
			case row.field < 0:
				f.enabled = !f.enabled
			case f.fields[row.field].def.Kind == "switch":
				f.fields[row.field].on = !f.fields[row.field].on
			}
		}
		return nil
	}
	switch msg.String() {
	case "left":
		field.left()
	case "right":
		field.right()
	case "home", "ctrl+a":
		field.home()
	case "end", "ctrl+e":
		field.end()
	case "backspace":
		field.backspace()
	case "delete":
		field.del()
	case "ctrl+w":
		field.killWord()
	case "ctrl+u":
		field.clear()
	case "enter":
		f.cursor++
		f.clampCursor()
	default:
		if len(msg.Runes) > 0 {
			field.insert(msg.Runes)
		}
	}
	return nil
}

func (m *uiModel) saveChannelForm() {
	f := m.chanForm
	if err := uiSaveChannel(m.st, f.key, f.enabled, f.values()); err != nil {
		m.fail(err)
		return
	}
	name, key, live := f.name, f.key, f.live
	m.chanForm = nil
	m.pop()
	if err := m.reload(); err != nil {
		m.fail(err)
		return
	}
	m.selectChannel(key)
	if live {
		m.say(tr("«%s» сохранён"), name)
		return
	}
	// Источники слушают сеть с самого старта, и переключить их на ходу нельзя.
	// Молчать об этом — значит оставить человека ждать того, чего не будет.
	m.say(tr("«%s» сохранён; сервис подхватит это после перезапуска serve"), name)
}

func uiNextSourceKind(kind string) string {
	for i, k := range uiSourceKinds {
		if k == kind {
			return uiSourceKinds[(i+1)%len(uiSourceKinds)]
		}
	}
	return uiSourceKinds[0]
}

func (m *uiModel) saveForm() {
	f := m.form
	name := strings.TrimSpace(f.name.String())
	if name == "" {
		m.fail(errors.New(tr("у проекта должно быть название")))
		f.field = uiFieldName
		return
	}
	var sources []Source
	for i, s := range f.sources {
		v := strings.TrimSpace(s.value.String())
		if v == "" {
			m.fail(fmt.Errorf(tr("источник «%s» пустой — заполни или убери (ctrl+k)"),
				uiSourceKindTitle(s.kind)))
			f.field = uiFormFixed + i
			return
		}
		if s.kind == "path" {
			v = expandHome(v)
		}
		sources = append(sources, Source{Kind: s.kind, Value: v})
	}
	p := Project{
		Name:       name,
		About:      strings.TrimSpace(f.about.String()),
		Aliases:    commaList(f.aliases.String()),
		People:     commaList(f.people.String()),
		Vocabulary: commaList(f.words.String()),
		Sources:    sources,
	}
	if err := uiRenameProject(m.st, f.old, p); err != nil {
		m.fail(err)
		return
	}
	old := f.old
	m.form = nil
	m.pop()
	if err := m.reload(); err != nil {
		m.fail(err)
		return
	}
	if m.projectName == old || m.projectName == "" {
		m.projectName = name
	}
	m.selectProject(name)
	m.rebuildBody()
	switch {
	case old == "":
		m.say(tr("проект «%s» заведён"), name)
	case old != name:
		m.say(tr("«%s» переименован в «%s» вместе с задачами"), old, name)
	default:
		m.say(tr("проект «%s» сохранён"), name)
	}
}

// selectProject ставит курсор списка на проект с этим именем: после правки
// человек должен видеть то, что правил, а не первую строку списка.
func (m *uiModel) selectProject(name string) {
	for i, p := range m.visibleProjects() {
		if p.Name == name {
			m.cursor[tabProjects] = i
			m.clampCursor()
			return
		}
	}
}
