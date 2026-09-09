package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Отрисовка. Ничего не решает и ничего не меняет: читает модель и складывает
// строки. Цвета — базовые ANSI, а не подобранные оттенки: терминал у человека
// уже настроен, и лезть в его тему своей палитрой — верный способ получить
// серое на сером.

// uiChromeLines — сколько строк занимает обвязка: имя и разделы (2), пустая
// строка, верх рамки, строка заголовков внутри неё, низ рамки, снизу состояние
// и подсказка (2).
const uiChromeLines = 8

// uiProseWidth — предел ширины для сплошного текста. На широком окне строка в
// двести колонок нечитаема: глаз теряет начало следующей, пока доходит до конца
// текущей. Таблицам это ограничение, наоборот, вредит — они занимают всё окно.
func uiProseWidth(w int) int {
	if w-uiGutter*2 < 92 {
		return max(w-uiGutter*2, 20)
	}
	return 92
}

// uiGutter — отступ слева у текста и списков. Строка, начинающаяся вплотную к
// краю окна, читается тяжелее и выглядит теснее, чем она есть.
const uiGutter = 2

var (
	uiDim      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	uiAccent   = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	uiBold     = lipgloss.NewStyle().Bold(true)
	uiHeadName = lipgloss.NewStyle().Bold(true).Reverse(true)
	// Вкладки. Выбранная — заливкой, а не подчёркиванием: подчёркнутый текст в
	// ряду такого же текста глаз находит не сразу, а залитый блок читается как
	// нажатая кнопка с одного взгляда. Цвет фона берётся из палитры терминала
	// (Reverse), чтобы не спорить с темой человека.
	uiTabOn = lipgloss.NewStyle().Bold(true).Reverse(true)
	// Соседняя вкладка не серая, а обычного цвета: серым помечено то, что
	// сейчас недоступно, и вкладки в этот ряд не входят — на них можно перейти.
	uiTabOff = lipgloss.NewStyle()
	// Номер вкладки — подсказка, а не название. Приглушён, чтобы в ряду читались
	// слова, а цифры находились, когда их ищут.
	uiTabNum   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	uiSelected = lipgloss.NewStyle().Reverse(true)
	uiErrStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	uiOKStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	uiWarn     = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	uiSection  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("4"))
	uiHit      = lipgloss.NewStyle().Reverse(true)

	uiCursorStyle = lipgloss.NewStyle().Reverse(true)

	// Полоса заголовка. Цвет фона — из палитры терминала, а не свой: тема у
	// человека уже подобрана, и вставлять в неё чужой оттенок незачем.
	uiTitleBar = lipgloss.NewStyle().Bold(true).Reverse(true)
	uiColHead  = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Bold(true)

	// Метка строки под курсором. Одной инверсии мало: на светлой теме полоса
	// бледная, и глазу не за что зацепиться.
	uiCursorMark = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)

	// Нижняя полоса подсказок. Приглушённая: это опора, а не то, что читают.
	uiHintKey = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)

	// Рамка поля. Тускло: она задаёт границы, а не привлекает внимание.
	uiFrame = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

// --- строковые мелочи --------------------------------------------------------

func uiTrunc(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= width {
		return s
	}
	return ansi.Truncate(s, width, "…")
}

func uiPad(s string, width int) string {
	if n := width - ansi.StringWidth(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// uiFit — обрезать и добить до ровно width колонок.
func uiFit(s string, width int) string { return uiPad(uiTrunc(s, width), width) }

// uiCell — ячейка таблицы. Всегда оставляет колонку под пробел: без неё
// обрезанное значение упирается многоточием прямо в соседнюю колонку, и на
// узком окне строка склеивается в кашу.
func uiCell(s string, width int) string { return uiPad(uiTrunc(s, width-1), width) }

// uiWrapLines — перенос по словам с отступом под первую строку. Длинные имена
// проектов и тексты задач регулярно длиннее экрана, и обрезать их молча значит
// потерять ровно ту часть, ради которой человек сюда смотрит.
func uiWrapLines(prefix, text string, width int) []string {
	pw := ansi.StringWidth(prefix)
	avail := width - pw
	if avail < 8 {
		avail = 8
	}
	text = strings.TrimRight(text, " \t")
	if text == "" {
		return []string{prefix}
	}
	parts := strings.Split(ansi.Wrap(text, avail, " -/"), "\n")
	out := make([]string, 0, len(parts))
	pad := strings.Repeat(" ", pw)
	for i, p := range parts {
		if i == 0 {
			out = append(out, prefix+p)
		} else {
			out = append(out, pad+p)
		}
	}
	return out
}

// uiStatusWord — те же слова, что в панели (web/app/src/lib/fmt.ts). Один и тот
// же созвон не должен в терминале и в браузере называться по-разному: человек
// смотрит то туда, то сюда и сверяет глазами.
func uiStatusWord(status string) string {
	switch status {
	case "uploading":
		return "разбираю файл"
	case "recording":
		return "идёт запись"
	case "recorded":
		return "записан"
	case "transcribed":
		return "расшифрован"
	case "summarized":
		return "есть follow-up"
	case "published":
		return "разослан"
	case "publish_failed":
		return "не разослан"
	case "failed":
		return "сорвался"
	}
	return status
}

// uiStatusWidth — ширина колонки статуса. Самое длинное — «есть follow-up».
const uiStatusWidth = 14

// uiStatusCell — статус так, как он стоит в строке списка. Сорвавшееся и
// незаконченное выделено: обрезанная запись снаружи выглядит как обычная, и
// это надо видеть, не вчитываясь в каждую строку.
func uiStatusCell(status string, width int) string {
	// Здесь uiFit, а не uiCell: uiStatusWidth — ровно длина самого длинного
	// слова, а зазор до соседней колонки даёт её ширина в раскладке раздела.
	s := uiFit(uiStatusWord(status), width)
	switch status {
	case "failed", "publish_failed":
		return uiErrStyle.Render(s)
	case "recording", "uploading":
		return uiWarn.Render(s)
	}
	return s
}

// uiDueCell — срок с пометкой о просрочке. Цвет здесь несёт смысл, а не
// украшает: просроченное надо видеть, не сверяя каждую дату с календарём.
func uiDueCell(due string, width int) string {
	cell := uiFit(due, width)
	day, err := time.Parse("2006-01-02", due)
	if err != nil {
		return cell
	}
	today := time.Now().Truncate(24 * time.Hour)
	switch d := day.Sub(today); {
	case d < 0:
		return uiErrStyle.Render(cell)
	case d < 48*time.Hour:
		return uiWarn.Render(cell)
	}
	return cell
}

func uiKindWord(k ItemKind) string {
	switch k {
	case KindTask:
		return "задача"
	case KindQuestion:
		return "вопрос"
	case KindDecision:
		return "решение"
	}
	return string(k)
}

func uiKindLetter(k ItemKind) string {
	switch k {
	case KindTask:
		return "З"
	case KindQuestion:
		return "В"
	case KindDecision:
		return "Р"
	}
	return "·"
}

func uiDur(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	m := int(d.Minutes())
	if m < 60 {
		return fmt.Sprintf("%d мин", m)
	}
	return fmt.Sprintf("%d ч %02d", m/60, m%60)
}

// uiHighlight превращает разметку сниппета в подсветку. В индексе лежит сырой
// текст, обрамлённый U+0002/U+0003, — те же символы, что разбирает панель.
func uiHighlight(s string) string {
	var b strings.Builder
	for {
		before, rest, found := strings.Cut(s, markStart)
		b.WriteString(before)
		if !found {
			return b.String()
		}
		inside, after, ok := strings.Cut(rest, markEnd)
		b.WriteString(uiHit.Render(inside))
		if !ok {
			return b.String()
		}
		s = after
	}
}

// uiLinkLines — куда follow-up разослан. Порядок задан сортировкой, а не
// обходом map: иначе ссылки прыгали бы местами на каждой перерисовке. Пустая
// ссылка — это Telegram и Slack без постоянного адреса: опубликовано там всё
// равно, а «telegram» с пустотой после него читается как сбой рассылки.
func uiLinkLines(links map[string]string) []string {
	out := make([]string, 0, len(links))
	for _, target := range sortedKeys(links) {
		url := links[target]
		if url == "" {
			url = uiDim.Render("отправлено")
		}
		out = append(out, uiDim.Render(uiFit(target, 12))+url)
	}
	return out
}

// --- тело карточек -----------------------------------------------------------

// rebuildBody пересобирает строки открытой карточки. Вызывается при открытии и
// при изменении размера окна: перенос по словам зависит от ширины, и без этого
// после растягивания терминала текст остался бы свёрнут по-старому.
func (m *uiModel) rebuildBody() {
	w := uiProseWidth(m.w)
	var body []string
	switch m.screen() {
	case scrMeeting:
		body = m.meetingBody(w)
	case scrTranscript:
		body = m.transcriptBody(w)
	case scrProject:
		body = m.projectBody(w)
	case scrHelp:
		body = uiHelpBody(w)
	default:
		return
	}
	// Отступ добавляем здесь, а не в каждой строке: перенос по словам считался
	// от ширины текста, и вмешиваться в него отступом уже поздно.
	pad := strings.Repeat(" ", uiGutter)
	m.body = make([]string, len(body))
	for i, l := range body {
		if l == "" {
			m.body[i] = ""
			continue
		}
		m.body[i] = pad + l
	}
	m.clampBody()
}

func (m *uiModel) meetingBody(w int) []string {
	if m.meeting == nil {
		return []string{uiDim.Render("созвон не открыт")}
	}
	mt := m.meeting
	var out []string
	add := func(s string) { out = append(out, s) }
	head := func(s string) { add(""); add(uiSection.Render(s)) }

	title := mt.Title
	if m.followup != nil && strings.TrimSpace(m.followup.Title) != "" {
		title = m.followup.Title
	}
	out = append(out, uiWrapLines("", uiBold.Render(orDash(title)), w)...)

	meta := mt.StartedAt.Format("02.01.2006 15:04")
	if mt.EndedAt != nil {
		meta += " · " + uiDur(mt.EndedAt.Sub(mt.StartedAt))
	}
	meta += " · " + uiStatusWord(mt.Status)
	add(uiDim.Render(meta))
	if len(mt.Participants) > 0 {
		out = append(out, uiWrapLines(uiDim.Render("участники: "),
			strings.Join(mt.Participants, ", "), w)...)
	}
	if mt.Error != "" {
		out = append(out, uiWrapLines(uiErrStyle.Render("сбой: "), mt.Error, w)...)
	}
	if mt.LeftReason != "" {
		add(uiDim.Render("бот вышел: " + mt.LeftReason))
	}
	out = append(out, uiLinkLines(m.links)...)

	f := m.followup
	if f == nil {
		head("Follow-up ещё нет")
		out = append(out, uiWrapLines("", "Созвон записан, но не разобран. Сделать разбор:  steno process "+mt.ID, w)...)
		if len(m.segments) > 0 {
			add(uiDim.Render("расшифровка есть — t покажет её целиком"))
		}
		return out
	}

	if len(f.TLDR) > 0 {
		head("Коротко")
		for _, s := range f.TLDR {
			out = append(out, uiWrapLines("  • ", s, w)...)
		}
	}
	if len(f.ActionItems) > 0 {
		head("Задачи")
		for _, a := range f.ActionItems {
			out = append(out, uiWrapLines("  • ", a.What, w)...)
			tail := uiAccent.Render(orDash(a.Owner)) + uiDim.Render(" · срок "+dueOr(a.Due, "не назван"))
			if a.Project != "" {
				tail += uiDim.Render(" · " + a.Project)
			}
			tail += uiDim.Render(" · " + clock(a.At))
			add(strings.Repeat(" ", uiItemIndent) + tail)
		}
	}
	if len(f.Decisions) > 0 {
		head("Решения")
		for _, d := range f.Decisions {
			out = append(out, uiWrapLines("  • ", d.What, w)...)
			if d.Why != "" {
				out = append(out, uiWrapLines(uiDim.Render(strings.Repeat(" ", uiItemIndent)+"почему: "), d.Why, w)...)
			}
			add(uiDim.Render(strings.Repeat(" ", uiItemIndent) + clock(d.At)))
		}
	}
	if len(f.OpenQuestions) > 0 {
		head("Открытые вопросы")
		for _, q := range f.OpenQuestions {
			out = append(out, uiWrapLines("  • ", q.Question, w)...)
			add(uiDim.Render(strings.Repeat(" ", uiItemIndent) + "ждём: " +
				dueOr(q.WaitingOn, "не определено") + " · " + clock(q.At)))
		}
	}
	if len(f.Risks) > 0 {
		head("Риски")
		for _, r := range f.Risks {
			out = append(out, uiWrapLines("  "+uiWarn.Render("!")+" ", r, w)...)
		}
	}
	if len(f.Timeline) > 0 {
		head("Как шёл разговор")
		for _, t := range f.Timeline {
			out = append(out, uiWrapLines("  "+uiDim.Render(clock(t.At))+" ", uiBold.Render(t.Title), w)...)
			if t.Summary != "" {
				out = append(out, uiWrapLines(strings.Repeat(" ", uiItemIndent), t.Summary, w)...)
			}
		}
	}
	if f.empty() {
		head("Пусто")
		out = append(out, uiWrapLines("", "Разбор есть, но в нём ничего не оказалось: ни задач, ни решений. Обычно так выходит с очень короткой записью.", w)...)
	}
	return out
}

func (m *uiModel) transcriptBody(w int) []string {
	m.segLines = make([]int, len(m.segments))
	if len(m.segments) == 0 {
		id := ""
		if m.meeting != nil {
			id = m.meeting.ID
		}
		return []string{
			uiDim.Render("расшифровки нет"),
			"",
			"Созвон записан, но не расшифрован. Расшифровать:  steno process " + id,
		}
	}
	var out []string
	for i, sg := range m.segments {
		m.segLines[i] = len(out)
		prefix := uiDim.Render(clock(sg.Start)) + " "
		if sg.Speaker != "" {
			prefix += uiAccent.Render(sg.Speaker) + ": "
		}
		out = append(out, uiWrapLines(prefix, sg.Text, w)...)
	}
	return out
}

func (m *uiModel) projectBody(w int) []string {
	p, ok := m.openProjectRow()
	if !ok {
		return []string{uiDim.Render("проекта больше нет")}
	}
	var out []string
	add := func(s string) { out = append(out, s) }
	head := func(s string) { add(""); add(uiSection.Render(s)) }

	add(uiBold.Render(p.Name))
	if !p.Registered {
		out = append(out, uiWrapLines(uiWarn.Render("  "), "Проекта нет в реестре: он встречается в задачах, но не заведён. Заведи с тем же названием (n), чтобы приложить репозиторий и описание.", w)...)
	}
	if p.About != "" {
		out = append(out, uiWrapLines("", p.About, w)...)
	}
	if len(p.Aliases) > 0 {
		out = append(out, uiWrapLines(uiDim.Render("зовут также: "), strings.Join(p.Aliases, ", "), w)...)
	}

	head("Источники")
	if len(p.Sources) == 0 {
		add(uiDim.Render("  нет — справку собрать не из чего (e — правка, ctrl+n — добавить)"))
	}
	for _, s := range p.Sources {
		out = append(out, uiWrapLines(fmt.Sprintf("  %-13s ", uiSourceKindTitle(s.Kind)), s.Value, w)...)
	}

	head("Справка по коду")
	switch {
	case m.building[p.Name]:
		add(uiDim.Render("  собирается…"))
	case p.ContextAt.IsZero() || strings.TrimSpace(p.Primer) == "":
		add(uiDim.Render("  не собрана — s соберёт (это поход в Claude)"))
	default:
		add(uiDim.Render("  собрана " + p.ContextAt.Format("02.01.2006 15:04")))
		add("")
		for _, line := range strings.Split(strings.TrimSpace(p.Primer), "\n") {
			out = append(out, uiWrapLines("  ", line, w)...)
		}
	}

	// Пункты берём из уже прочитанного, а не запросом: перерисовка бывает на
	// каждое изменение размера окна, и ходить за этим в базу незачем.
	var items []ProjectItem
	for _, it := range m.items {
		if it.Project == p.Name {
			items = append(items, it)
		}
	}
	open := 0
	for _, it := range items {
		if it.Status == "open" {
			open++
		}
	}
	head(fmt.Sprintf("Открыто — %d", open))
	if open == 0 {
		add(uiDim.Render("  ничего не висит"))
	}
	for _, it := range items {
		if it.Status != "open" {
			continue
		}
		out = append(out, m.itemLines(it, w)...)
	}
	if closed := len(items) - open; closed > 0 {
		head(fmt.Sprintf("Закрыто — %d", closed))
		for _, it := range items {
			if it.Status == "open" {
				continue
			}
			out = append(out, m.itemLines(it, w)...)
		}
	}
	return out
}

// uiItemIndent — ширина «  T-0001 З »: под неё уходят и перенос текста, и
// строка с исполнителем и сроком, иначе они разъезжаются на разных id.
// uiItemIndent — отступ пояснения под пунктом. Ровно под текстом пункта, а не
// под его маркером: четыре пробела — это «  • » впереди.
const uiItemIndent = 4

func (m *uiModel) itemLines(it ProjectItem, w int) []string {
	prefix := "  " + uiDim.Render(uiFit(it.ID, 7)) + uiAccent.Render(uiKindLetter(it.Kind)) + " "
	out := uiWrapLines(prefix, it.Text, w)
	var tail []string
	if it.Owner != "" {
		tail = append(tail, it.Owner)
	}
	if it.Due != "" {
		tail = append(tail, "до "+it.Due)
	}
	if it.Status != "open" {
		word := "сделано"
		if it.Status == "dropped" {
			word = "снято"
		}
		tail = append(tail, word)
	}
	if len(tail) > 0 {
		out = append(out, uiDim.Render(strings.Repeat(" ", uiItemIndent)+strings.Join(tail, " · ")))
	}
	return out
}

// --- экран -------------------------------------------------------------------

func (m *uiModel) View() string {
	h := m.h
	if h < uiChromeLines+1 {
		h = uiChromeLines + 1
	}
	lines := make([]string, 0, h)
	lines = append(lines, m.titleBar(), m.tabBar(), "")

	// Поле в рамке. Без неё пустота ничем не ограничена и читается как ничто:
	// на сорока четырёх строках с одним созвоном человек видел содержимое в
	// пять строк и провал в тридцать восемь под ним.
	room := h - len(lines) - 2 // снизу состояние и подсказка
	body := m.content()
	head := m.contextLine()
	listH := min(len(body)+2+btoi(head != ""), room)

	// Нижняя панель забирает всё, что списку не понадобилось, и растягивается
	// до низа: недотянутая до края панель оставляет под собой тот же провал,
	// ради которого всё и затевалось. Если список занимает экран сам — панели
	// нет, и это правильно: место уходит строкам.
	var below []string
	if free := room - listH - 1; free >= uiPreviewMin {
		if p := m.preview(m.boxWidth() - 6); len(p) > 0 {
			inner := free - 2
			for i, l := range p {
				if l != "" {
					p[i] = "  " + l
				}
			}
			cut := len(p) > inner
			for len(p) < inner {
				p = append(p, "")
			}
			p = p[:inner]
			if cut && inner > 0 {
				p[inner-1] = uiDim.Render("  … enter — целиком")
			}
			below = append([]string{""}, m.box(m.previewTitle(), "", p)...)
		}
	}

	if keep := room - len(below) - 2 - btoi(head != ""); keep >= 0 && keep < len(body) {
		body = body[:keep]
	}
	lines = append(lines, m.box(m.boxTitle(), head, body)...)
	lines = append(lines, below...)

	for len(lines) < h-2 {
		lines = append(lines, "")
	}
	lines = append(lines, m.statusLine(), m.hintLine())
	for i, l := range lines {
		lines[i] = uiTrunc(l, m.w)
	}
	return strings.Join(lines[:h], "\n")
}

// uiPreviewMin — меньше этого нижнюю панель показывать незачем: в трёх строках
// сути не расскажешь, а место у списка она отнимет.
const uiPreviewMin = 6

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// box — рамка вокруг поля. Высота по содержимому, а не во весь экран: коробка,
// растянутая на сорок строк ради одной, — та же пустота, только обведённая.
// Подсказка идёт сразу под рамкой, и всё вместе читается одним блоком.
func (m *uiModel) box(title, head string, body []string) []string {
	inner := m.boxWidth() - 2
	edge := func(left, right string, fill string) string {
		return uiFrame.Render(left + strings.Repeat(fill, inner) + right)
	}
	row := func(s string) string {
		return uiFrame.Render("│") + uiFit(s, inner) + uiFrame.Render("│")
	}

	top := edge("╭", "╮", "─")
	if title != "" {
		t := uiFit("─ "+title+" ", inner)
		if ansi.StringWidth(title)+4 <= inner {
			t = "─ " + uiBold.Render(title) + " " +
				strings.Repeat("─", inner-ansi.StringWidth(title)-3)
		}
		top = uiFrame.Render("╭") + t + uiFrame.Render("╮")
	}

	out := []string{" " + top}
	if head != "" {
		out = append(out, " "+row(head))
	}
	for _, l := range body {
		out = append(out, " "+row(l))
	}
	return append(out, " "+edge("╰", "╯", "─"))
}

// boxWidth — внешняя ширина рамки вместе с краями: по самому широкому, что
// внутри. Рамка во всё окно ради одной короткой строки — та же пустота, только
// обведённая.
func (m *uiModel) boxWidth() int {
	w := m.rowWidth()
	if t := ansi.StringWidth(m.boxTitle()) + 6; t > w {
		w = t
	}
	if h := ansi.StringWidth(ansi.Strip(m.contextLine())); h > w {
		w = h
	}
	for _, l := range m.content() {
		if n := ansi.StringWidth(l); n > w {
			w = n
		}
	}
	// Но и в обтяжку рамку делать нельзя: коробка в шестьдесят колонок посреди
	// стосемидесятиколоночного окна — это ровно то «всё маленькое», с которого
	// начался разговор. Панель занимает две трети окна, а колонки внутри
	// по-прежнему держатся содержимого.
	w = max(w+2, m.w*2/3)
	return max(min(w, min(m.w-2, 152)), 40)
}

func (m *uiModel) boxTitle() string {
	if m.screen() != scrList {
		return m.placeName()
	}
	if n := m.rowCount(); n > 0 {
		return fmt.Sprintf("%s · %d", uiTabTitles[m.tab], n)
	}
	return uiTabTitles[m.tab]
}

// titleBar — где я нахожусь и сколько тут всего. Полоса во всю ширину: на
// широком окне тонкая строчка в углу теряется, а сплошная полоса сразу говорит,
// где верх экрана.
func (m *uiModel) titleBar() string {
	left := " steno"
	if name := m.placeName(); name != "" {
		left += " · " + name
	}
	left += " "
	right := m.counters()
	if right != "" {
		right += " "
	}
	gap := m.w - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		return uiTitleBar.Render(uiFit(left, m.w))
	}
	return uiTitleBar.Render(left + strings.Repeat(" ", gap) + right)
}

// placeName — название текущего места словами. Человек читает его, а не считает
// подсвеченную вкладку.
func (m *uiModel) placeName() string {
	switch m.screen() {
	case scrMeeting:
		return "Follow-up"
	case scrTranscript:
		return "Расшифровка"
	case scrProject:
		return "Проект"
	case scrHelp:
		return "Клавиши"
	case scrForm:
		if m.form != nil && m.form.old != "" {
			return "Правка проекта"
		}
		return "Новый проект"
	case scrChannel:
		return "Настройка канала"
	case scrPicker:
		return "Выбор"
	case scrConfirm:
		return "Подтверждение"
	}
	return uiTabTitles[m.tab]
}

func (m *uiModel) tabBar() string {
	left := " "
	for i := uiTab(0); i < tabCount; i++ {
		switch {
		case i == m.tab && m.screen() == scrList:
			// Выбранная и мы в ней: залитый блок. Номер внутри блока не глушим —
			// на залитом фоне приглушение выглядит грязью, а не подсказкой.
			left += uiTabOn.Render(fmt.Sprintf(" %d %s ", i+1, uiTabTitles[i]))
		case i == m.tab:
			// Выбранная, но человек ушёл вглубь — в карточку, форму, справку.
			// Рамка вместо заливки: место помнится, но сейчас оно не здесь.
			left += uiAccent.Render(fmt.Sprintf("[%d %s]", i+1, uiTabTitles[i]))
		default:
			left += uiTabNum.Render(fmt.Sprintf(" %d ", i+1)) +
				uiTabOff.Render(uiTabTitles[i]) + " "
		}
		left += " "
	}
	// Стрелки названы прямо в шапке: это первое, что человек нажимает, и
	// искать их в справке он не пойдёт. На узком окне подсказка укорачивается,
	// но не пропадает — пропадать ей нельзя, она тут главная.
	for _, hint := range []string{"← → разделы · ? клавиши ", "← → разделы ", "← → "} {
		gap := m.w - ansi.StringWidth(left) - ansi.StringWidth(hint)
		if gap >= 1 {
			return left + strings.Repeat(" ", gap) + uiDim.Render(hint)
		}
	}
	return left
}

func (m *uiModel) counters() string {
	switch m.tab {
	case tabMeetings:
		return fmt.Sprintf("созвонов %d", len(m.meetings))
	case tabTasks:
		open := 0
		for _, it := range m.items {
			if it.Status == "open" {
				open++
			}
		}
		return fmt.Sprintf("открыто %d", open)
	case tabProjects:
		return fmt.Sprintf("проектов %d", len(m.projects))
	case tabSearch:
		if strings.TrimSpace(m.query.String()) == "" {
			return ""
		}
		return fmt.Sprintf("найдено %d", len(m.hits))
	case tabChannels:
		on := 0
		for _, c := range m.channels {
			if c.Enabled {
				on++
			}
		}
		return fmt.Sprintf("включено %d из %d", on, len(m.channels))
	}
	return ""
}

// contextLine — вторая строка: заголовки колонок, поле ввода или название
// открытой карточки. Одна строка на всё, потому что каждая отнятая у списка
// строка — это минус один созвон на экране.
func (m *uiModel) contextLine() string {
	pad := strings.Repeat(" ", uiGutter)
	switch m.screen() {
	case scrMeeting, scrTranscript, scrProject, scrHelp:
		return m.cardContext()
	case scrForm:
		if m.form != nil && m.form.old != "" {
			return pad + uiBold.Render("«"+m.form.old+"»") +
				uiDim.Render("   ctrl+s сохранит, esc отменит")
		}
		return pad + uiDim.Render("ctrl+s сохранит, esc отменит")
	case scrChannel:
		if m.chanForm != nil {
			return pad + uiBold.Render("«"+m.chanForm.name+"»") +
				uiDim.Render("   ctrl+s сохранит, esc отменит")
		}
		return ""
	case scrPicker:
		if m.picker != nil {
			return pad + uiBold.Render(m.picker.title)
		}
		return ""
	case scrConfirm:
		return ""
	}

	if m.tab == tabSearch {
		return pad + uiDim.Render("запрос: ") + m.query.view(min(m.w-12, 60), m.searchFocus)
	}
	if m.filtering || !m.filter[m.tab].empty() {
		return pad + uiDim.Render("фильтр: ") + m.filter[m.tab].view(min(m.w-12, 60), m.filtering)
	}
	// Заголовки колонок над пустым списком объясняют пустоту хуже, чем ничего:
	// человек читает «что · кто · срок» и ищет строки, которых нет.
	if m.rowCount() == 0 {
		return ""
	}
	return uiColHead.Render(uiFit(m.columns(), m.rowWidth()))
}

func (m *uiModel) cardContext() string {
	title := ""
	switch m.screen() {
	case scrMeeting, scrTranscript:
		if m.meeting != nil {
			title = orDash(m.meeting.Title)
		}
	case scrProject:
		title = m.projectName
	case scrHelp:
		title = "что нажимать"
	}
	left := strings.Repeat(" ", uiGutter) + uiBold.Render(uiTrunc(title, max(m.w-24, 10)))
	right := ""
	if n := len(m.body); n > m.listHeight() {
		right = uiDim.Render(fmt.Sprintf("%d–%d из %d", m.bodyOff+1,
			min(m.bodyOff+m.listHeight(), n), n))
	}
	gap := m.w - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

// uiFlex — сколько остаётся главной колонке после колонок постоянной ширины.
// Главная идёт первой и забирает весь запас: на широком окне растёт название, а
// не пустота между колонками.
func uiFlex(total int, fixed ...int) int {
	w := total - uiGutter
	for _, f := range fixed {
		w -= f
	}
	return max(w, 12)
}

// uiTableWidth — ширина таблицы. Растягивать её на всё окно нельзя: на двухстах
// колонках между текстом задачи и её сроком получается сотня пробелов, и строка
// перестаёт читаться как одна строка. Дальше предела таблица просто кончается —
// так же, как кончается страница.
func (m *uiModel) tableWidth() int { return min(m.w-4, 150) }

// uiNatural — ширина колонки по самому длинному значению в ней: не уже
// заголовка и не шире отведённого. Колонка, растянутая на пол-экрана ради двух
// коротких строк, читается хуже плотной — глаз идёт по пустоте от текста задачи
// до её срока и теряет строку.
func uiNatural(head string, values []string, avail int) int {
	w := ansi.StringWidth(head)
	for _, v := range values {
		w = max(w, ansi.StringWidth(v))
	}
	return min(w+2, avail)
}

// uiMeetingTitle — название созвона с участниками, без разметки: по нему
// считается ширина колонки, а цвета в подсчёт лезть не должны.
func uiMeetingTitle(r MeetingRow) string {
	t := orDash(r.Title)
	if len(r.Participants) > 0 {
		t += " · " + strings.Join(r.Participants, ", ")
	}
	return t
}

// Ширины колонок по разделам. Одна функция на заголовок и на строки — иначе они
// разъезжаются на первой же правке.
func (m *uiModel) colsMeetings() (name, when, status, tasks int) {
	when, status, tasks = 13, uiStatusWidth+2, 6
	rows := m.visibleMeetings()
	vals := make([]string, 0, len(rows))
	for _, r := range rows {
		vals = append(vals, uiMeetingTitle(r))
	}
	avail := uiFlex(m.tableWidth(), when, status, tasks)
	return uiNatural("название", vals, avail), when, status, tasks
}

func (m *uiModel) colsTasks() (what, owner, due, project, id int) {
	owner, due, project, id = 16, 12, 16, 8
	rows := m.visibleItems()
	vals := make([]string, 0, len(rows))
	for _, it := range rows {
		if it.Kind != KindTask {
			vals = append(vals, uiKindWord(it.Kind)+": "+it.Text)
			continue
		}
		vals = append(vals, it.Text)
	}
	avail := uiFlex(m.tableWidth(), owner, due, project, id)
	return uiNatural("что", vals, avail), owner, due, project, id
}

func (m *uiModel) colsProjects() (name, tasks, questions, decisions, sources int) {
	tasks, questions, decisions, sources = 7, 7, 7, 9
	rows := m.visibleProjects()
	vals := make([]string, 0, len(rows))
	for _, p := range rows {
		vals = append(vals, p.Name)
	}
	avail := uiFlex(m.tableWidth(), tasks, questions, decisions, sources, 14)
	return uiNatural("проект", vals, avail), tasks, questions, decisions, sources
}

func (m *uiModel) colsChannels() (name, state, what int) {
	state, what = 12, 22
	rows := m.visibleChannels()
	vals := make([]string, 0, len(rows))
	for _, c := range rows {
		vals = append(vals, c.Name)
	}
	avail := uiFlex(m.tableWidth(), state, what, 16)
	return uiNatural("канал", vals, avail), state, what
}

func (m *uiModel) columns() string {
	// Ширины повторяют раскладку строк ниже: заголовок, сдвинутый на две
	// колонки от своей колонки, читается как чужой.
	pad := strings.Repeat(" ", uiGutter)
	switch m.tab {
	case tabMeetings:
		name, when, status, tasks := m.colsMeetings()
		return pad + uiCell("название", name) + uiCell("когда", when) +
			uiCell("статус", status) + uiCell("зад.", tasks)
	case tabTasks:
		what, owner, due, project, id := m.colsTasks()
		return pad + uiCell("что", what) + uiCell("кто", owner) +
			uiCell("срок", due) + uiCell("проект", project) + uiCell("id", id)
	case tabProjects:
		name, tasks, questions, decisions, sources := m.colsProjects()
		return pad + uiCell("проект", name) + uiCell("задач", tasks) +
			uiCell("вопр.", questions) + uiCell("реш.", decisions) +
			uiCell("источн.", sources) + "справка"
	case tabChannels:
		name, state, what := m.colsChannels()
		return pad + uiCell("канал", name) + uiCell("состояние", state) +
			uiCell("что делает", what) + "настроено"
	}
	return ""
}

func (m *uiModel) content() []string {
	switch m.screen() {
	case scrMeeting, scrTranscript, scrProject, scrHelp:
		return m.bodyWindow()
	case scrForm:
		return uiWindowAround(m.formView, m.listHeight())
	case scrChannel:
		return uiWindowAround(m.channelFormView, m.listHeight())
	case scrPicker:
		return m.pickerView()
	case scrConfirm:
		return m.confirmView()
	}
	switch m.tab {
	case tabMeetings:
		return m.meetingsView()
	case tabTasks:
		return m.tasksView()
	case tabProjects:
		return m.projectsView()
	case tabSearch:
		return m.searchView()
	case tabChannels:
		return m.channelsView()
	}
	return nil
}

// uiWindowAround — окно строк формы, в котором обязательно видна строка под
// курсором и пара строк под ней: у поля бывает пояснение следующей строкой, и
// прокрутка ровно по курсору прятала бы именно его.
func uiWindowAround(build func() ([]string, int), h int) []string {
	lines, active := build()
	if len(lines) <= h {
		return lines
	}
	from := 0
	if active+3 > h {
		from = active + 3 - h
	}
	if from > len(lines)-h {
		from = len(lines) - h
	}
	if from > active {
		from = active
	}
	if from < 0 {
		from = 0
	}
	return lines[from:min(from+h, len(lines))]
}

func (m *uiModel) bodyWindow() []string {
	h := m.listHeight()
	from := m.bodyOff
	if from > len(m.body) {
		from = len(m.body)
	}
	if from < 0 {
		from = 0
	}
	to := min(from+h, len(m.body))
	return m.body[from:to]
}

// uiEmpty — экран без содержимого. Пустой список без объяснения человек читает
// как поломку, поэтому пустого экрана здесь не бывает нигде.
func uiEmpty(w int, title string, hints ...string) []string {
	out := []string{"", "  " + uiBold.Render(title)}
	for _, h := range hints {
		out = append(out, uiWrapLines("  ", uiDim.Render(h), w)...)
	}
	return out
}

func (m *uiModel) rowStyle(i int) func(string) string {
	w := m.rowWidth()
	if i == m.cursor[m.tab] {
		return func(s string) string {
			return uiCursorMark.Render("▌") + uiSelected.Render(uiFit(s, w-1))
		}
	}
	return func(s string) string { return " " + s }
}

// rowWidth — докуда доходит последняя колонка. Полоса под курсором, тянущаяся
// на полсотни колонок за конец таблицы, выглядит сломанной.
func (m *uiModel) rowWidth() int {
	sum := func(v ...int) int {
		w := uiGutter
		for _, x := range v {
			w += x
		}
		return min(w, m.tableWidth())
	}
	switch m.tab {
	case tabMeetings:
		return sum(m.colsMeetings())
	case tabTasks:
		return sum(m.colsTasks())
	case tabProjects:
		name, t, q, d, src := m.colsProjects()
		return sum(name, t, q, d, src, 14)
	case tabChannels:
		name, state, what := m.colsChannels()
		return sum(name, state, what, 16)
	case tabSearch:
		return m.tableWidth()
	}
	return m.tableWidth()
}

func (m *uiModel) meetingsView() []string {
	rows := m.visibleMeetings()
	if len(rows) == 0 {
		if !m.filter[tabMeetings].empty() {
			return uiEmpty(m.w, "Ничего не подошло под фильтр",
				"esc снимет фильтр, / наберёт другой.")
		}
		return uiEmpty(m.w, "Созвонов пока нет",
			"Бот запишет созвон и разберёт его сам:  steno join <ссылка>",
			"Чтобы он ходил на встречи из календаря без напоминаний:  steno serve")
	}
	from, to := m.window(len(rows))
	out := make([]string, 0, to-from)
	for i := from; i < to; i++ {
		r := rows[i]
		nameW, whenW, statusW, tasksW := m.colsMeetings()
		// Название — то, ради чего человек сюда смотрит, поэтому оно первое и
		// занимает весь запас ширины. Участники дописываются в ту же колонку и
		// обрезаются вместе с ней.
		title := orDash(r.Title)
		if len(r.Participants) > 0 && ansi.StringWidth(title)+3 < nameW {
			title = title + uiDim.Render(" · "+strings.Join(r.Participants, ", "))
		}
		line := " " + uiCell(title, nameW)
		line += uiDim.Render(uiCell(r.StartedAt.Format("02.01 15:04"), whenW))
		line += uiStatusCell(r.Status, statusW)
		line += uiDim.Render(uiCell(fmt.Sprintf("%d", r.Tasks), tasksW))
		out = append(out, m.rowStyle(i)(line))
	}
	return out
}

func (m *uiModel) tasksView() []string {
	rows := m.visibleItems()
	if len(rows) == 0 {
		switch {
		case len(m.items) == 0:
			return uiEmpty(m.w, "Задач пока нет",
				"Они появляются сами: бот разбирает созвон и раскладывает, кто что взял.",
				"Записать созвон:  steno join <ссылка>")
		case !m.itemFilter.empty() || !m.filter[tabTasks].empty():
			return uiEmpty(m.w, "Под фильтр ничего не подошло",
				"c снимет все фильтры, a покажет и закрытое.")
		default:
			return uiEmpty(m.w, "Всё закрыто",
				"Ни одной открытой задачи. a покажет закрытые.")
		}
	}
	from, to := m.window(len(rows))
	out := make([]string, 0, to-from)
	for i := from; i < to; i++ {
		it := rows[i]
		var line string
		whatW, ownerW, dueW, projectW, idW := m.colsTasks()
		due := it.Due
		if due == "" {
			due = "—"
		}
		// Текст задачи — первым и во всю оставшуюся ширину. Идентификатор ушёл
		// в конец и потускнел: он нужен, только когда задачу ищут командой в
		// терминале, а читают строку не ради него.
		text := it.Text
		if it.Kind != KindTask {
			text = uiKindWord(it.Kind) + ": " + text
		}
		if it.Status == "open" {
			line = " " + uiCell(text, whatW)
		} else {
			line = " " + uiDim.Render(uiCell(text, whatW))
		}
		line += uiCell(orDash(it.Owner), ownerW)
		if it.Status != "open" {
			line += uiDim.Render(uiCell(due, dueW))
		} else {
			line += uiDueCell(due, dueW)
		}
		line += uiDim.Render(uiCell(it.Project, projectW))
		line += uiDim.Render(uiCell(it.ID, idW))
		out = append(out, m.rowStyle(i)(line))
	}
	return out
}

func (m *uiModel) projectsView() []string {
	rows := m.visibleProjects()
	if len(rows) == 0 {
		if !m.filter[tabProjects].empty() {
			return uiEmpty(m.w, "Ничего не подошло под фильтр",
				"esc снимет фильтр.")
		}
		return uiEmpty(m.w, "Проектов нет",
			"n заведёт первый: название, одна строка о том, что это, и репозиторий или каталог с кодом.",
			"По ним бот понимает, к чему относится сказанное на созвоне.")
	}
	from, to := m.window(len(rows))
	out := make([]string, 0, to-from)
	for i := from; i < to; i++ {
		p := rows[i]
		nameW, tasksW, questionsW, decisionsW, sourcesW := m.colsProjects()
		line := " " + uiCell(p.Name, nameW)
		line += uiDim.Render(uiFit(fmt.Sprintf("%d", p.Tasks), tasksW))
		line += uiDim.Render(uiFit(fmt.Sprintf("%d", p.Questions), questionsW))
		line += uiDim.Render(uiFit(fmt.Sprintf("%d", p.Decisions), decisionsW))
		line += uiDim.Render(uiFit(fmt.Sprintf("%d", len(p.Sources)), sourcesW))
		switch {
		case m.building[p.Name]:
			line += "собирается…"
		case !p.Registered:
			// Значка вроде звёздочки у названия человек не расшифрует, а место
			// в этой колонке всё равно пустое: у незаведённого проекта нет ни
			// источников, ни справки.
			line += uiWarn.Render("не заведён")
		case !p.ContextAt.IsZero():
			line += p.ContextAt.Format("02.01.2006")
		case len(p.Sources) == 0:
			line += uiDim.Render("нет источников")
		default:
			line += uiDim.Render("не собрана")
		}
		out = append(out, m.rowStyle(i)(line))
	}
	return out
}

func (m *uiModel) searchView() []string {
	if strings.TrimSpace(m.query.String()) == "" {
		return uiEmpty(m.w, "Поиск по всему, что накоплено",
			"Наберите слово — ищется и в расшифровках, и в follow-up.",
			"Ищется по началу слова: «релиз» найдёт и «релиза», и «релизом».")
	}
	if len(m.hits) == 0 {
		return uiEmpty(m.w, "Ничего не нашлось",
			"Слова короче двух букв не ищутся. Попробуйте другое слово или его начало.")
	}
	from, to := m.window(len(m.hits))
	out := make([]string, 0, (to-from)*2)
	for i := from; i < to; i++ {
		h := m.hits[i]
		// Найденный кусок — первым: ради него и искали. Кто и когда сказал —
		// справа, тускло; на широком окне растёт именно фрагмент.
		whoW, whenW := 18, 13
		snips := make([]string, 0, len(m.hits))
		for _, x := range m.hits {
			snips = append(snips, ansi.Strip(uiHighlight(strings.ReplaceAll(x.Snippet, "\n", " "))))
		}
		snipW := uiNatural("", snips, uiFlex(m.tableWidth(), whoW, whenW))
		who := orDash(h.Title)
		if h.Speaker != "" {
			who = h.Speaker
		}
		line := " " + uiCell(uiHighlight(strings.ReplaceAll(h.Snippet, "\n", " ")), snipW)
		line += uiAccent.Render(uiCell(who, whoW))
		line += uiDim.Render(uiCell(time.Unix(h.StartedAt, 0).Format("02.01 15:04"), whenW))
		out = append(out, m.rowStyle(i)(line))
	}
	return out
}

func (m *uiModel) channelsView() []string {
	rows := m.visibleChannels()
	if len(rows) == 0 {
		return uiEmpty(m.w, "Ничего не подошло под фильтр", "esc снимет фильтр.")
	}
	from, to := m.window(len(rows))
	out := make([]string, 0, to-from)
	for i := from; i < to; i++ {
		c := rows[i]
		nameW, stateW, whatW := m.colsChannels()
		line := " " + uiCell(c.Name, nameW)
		if c.Enabled {
			line += uiOKStyle.Render(uiCell("включён", stateW))
		} else {
			line += uiDim.Render(uiCell("выключен", stateW))
		}
		line += uiDim.Render(uiCell(uiChannelWhat(c), whatW))
		if s := strings.TrimSpace(c.Summary); s != "" {
			line += s
		} else {
			line += uiDim.Render("не настроено")
		}
		out = append(out, m.rowStyle(i)(line))
	}
	return out
}

// channelFormView рисует форму по описанию полей канала, а не по своему списку:
// новое поле в channelDefs появляется здесь само. Возвращает ещё и номер строки
// под курсором — по нему форма прокручивается за курсором.
func (m *uiModel) channelFormView() ([]string, int) {
	f := m.chanForm
	if f == nil {
		return nil, 0
	}
	// Колонка подписей — по самой длинной подписи этого канала, а не наугад:
	// «Писать в личку тем, на ком задача» — тридцать три колонки, и на
	// фиксированных двадцати она наезжала на значение справа.
	lw := ansi.StringWidth("канал включён")
	for _, fs := range f.fields {
		if n := ansi.StringWidth(fs.def.Label); n > lw {
			lw = n
		}
	}
	lw = min(lw+1, max(m.w/2, 14))
	w := min(m.w-lw-4, 56)
	if w < 12 {
		w = 12
	}
	check := func(on bool) string {
		if on {
			return uiOKStyle.Render("[×] да")
		}
		return uiDim.Render("[ ] нет")
	}

	var out []string
	active := 0
	// Строка формы и строка в rows() — одно и то же место, поэтому идём по
	// rows(): курсор и отрисовка не могут разъехаться.
	row := func(on bool, name, value string) {
		if on {
			active = len(out)
			out = append(out, uiAccent.Render("▸ "+uiFit(name, lw))+value)
			return
		}
		out = append(out, "  "+uiDim.Render(uiFit(name, lw))+value)
	}
	note := func(text string) {
		out = append(out, uiWrapLines("  "+uiPad("", lw), text, m.w-2)...)
	}

	out = append(out, uiWrapLines("  ", uiDim.Render(f.about), m.w-2)...)
	out = append(out, "")

	for i, r := range f.rows() {
		here := i == f.cursor
		switch {
		case r.field < 0:
			row(here, "канал включён", check(f.enabled))
			if !f.live {
				note(uiDim.Render("сервис подхватит это после перезапуска serve"))
			}
			// Поля вида «google» правке не подлежат: подключение делается
			// браузером. Место им — рядом с каналом, а не отдельным разделом.
			for _, fs := range f.fields {
				if fs.def.Kind == "google" {
					out = append(out, m.googleLines(fs.def, lw)...)
				}
			}
			out = append(out, "")
		case r.item == uiRowAdd:
			fs := f.fields[r.field]
			name, text := "", "добавить значение"
			if len(fs.items) == 0 {
				name, text = fs.def.Label, "пока ни одного — enter добавит"
			}
			row(here, name, uiAccent.Render("+ ")+uiDim.Render(text))
			if fs.def.Hint != "" {
				note(uiDim.Render(fs.def.Hint))
			}
			out = append(out, "")
		case r.item >= 0:
			fs := &f.fields[r.field]
			name := ""
			if r.item == 0 {
				name = fs.def.Label
			}
			row(here, name, fs.items[r.item].view(w, here))
		default:
			fs := &f.fields[r.field]
			if fs.def.Kind == "switch" {
				row(here, fs.def.Label, check(fs.on))
			} else {
				row(here, fs.def.Label, fs.text.view(w, here))
			}
			if fs.def.Hint != "" {
				note(uiDim.Render(fs.def.Hint))
			}
		}
	}
	out = append(out, "")
	out = append(out, uiWrapLines("  ", uiDim.Render(
		"Токенов и паролей здесь нет и не будет: их задаёт steno setup, и лежат они в .env."), m.w-2)...)
	return out, active
}

// googleLines — состояние доступа в Google. Кнопки здесь быть не может: за
// согласием ходят браузером, а терминалу его открыть нечем.
func (m *uiModel) googleLines(def ChannelField, lw int) []string {
	g := googleStatus(m.cfg)
	var out []string
	head := "  " + uiDim.Render(uiFit(def.Label, lw))
	switch {
	case g.Connected && g.Account != "":
		out = append(out, head+uiOKStyle.Render("подключено")+uiDim.Render(" · "+g.Account))
	case g.Connected:
		out = append(out, head+uiOKStyle.Render("подключено"))
	default:
		out = append(out, head+uiWarn.Render("не подключено"))
	}
	if g.Why != "" {
		style := uiDim
		if g.Severity == googleWarn {
			style = uiWarn
		}
		out = append(out, uiWrapLines("  "+uiPad("", lw), style.Render(g.Why), m.w-2)...)
	}
	if !g.Connected {
		out = append(out, uiWrapLines("  "+uiPad("", lw), uiDim.Render(
			"Подключение идёт через браузер, из терминала его не нажать: запусти steno serve "+
				"и подключись в панели, в разделе «Настройки»."), m.w-2)...)
	}
	return out
}

// window — какие строки списка видны при текущей прокрутке.
func (m *uiModel) window(n int) (int, int) {
	h := m.listHeight()
	from := m.listOff[m.tab]
	if from > n-h {
		from = n - h
	}
	if from < 0 {
		from = 0
	}
	return from, min(from+h, n)
}

// formView возвращает строки формы и номер той из них, на которой стоит
// курсор: длинная форма не помещается на экран, и прокручивать её надо за
// курсором, а не с начала.
func (m *uiModel) formView() ([]string, int) {
	f := m.form
	if f == nil {
		return nil, 0
	}
	w := min(m.w-20, 60)
	if w < 12 {
		w = 12
	}
	out := []string{""}
	active := 0
	row := func(i int, name, value string) {
		if f.field == i {
			active = len(out)
			out = append(out, uiAccent.Render("▸ "+uiPad(name, 14))+value)
			return
		}
		out = append(out, "  "+uiDim.Render(uiPad(name, 14))+value)
	}
	row(0, "название", f.name.view(w, f.field == 0))
	row(1, "о чём проект", f.about.view(w, f.field == 1))
	row(2, "зовут также", f.aliases.view(w, f.field == 2))
	out = append(out, "", "  "+uiSection.Render("Источники")+uiDim.Render("  ctrl+n добавить · ctrl+t сменить вид · ctrl+k убрать"))
	if len(f.sources) == 0 {
		out = append(out, uiWrapLines("  ", uiDim.Render("Пока ни одного. Без них справку по коду собрать не из чего, но проект всё равно заведётся."), m.w-2)...)
	}
	for i, s := range f.sources {
		row(3+i, uiSourceKindTitle(s.kind), s.value.view(w, f.field == 3+i))
	}
	out = append(out, "")
	out = append(out, uiWrapLines("  ", uiDim.Render(
		"Псевдонимы — через запятую: как проект называют вслух. По ним бот понимает, что «биллинг» и «платежи» — одно и то же."), m.w-2)...)
	return out, active
}

func (m *uiModel) pickerView() []string {
	p := m.picker
	if p == nil {
		return nil
	}
	out := []string{""}
	for i, l := range p.labels {
		mark := "  "
		if p.values[i] == p.current {
			mark = uiAccent.Render(" ✓")
		}
		line := mark + " " + l
		if i == p.cursor {
			line = uiSelected.Render(uiFit(line, min(m.w, 60)))
		}
		out = append(out, line)
	}
	return out
}

func (m *uiModel) confirmView() []string {
	if m.confirm == nil {
		return nil
	}
	out := []string{""}
	out = append(out, uiWrapLines("  ", m.confirm.text, m.w-2)...)
	out = append(out, "", "  "+uiWarn.Render("y")+uiDim.Render(" — удалить, любая другая клавиша — отмена"))
	return out
}

func (m *uiModel) statusLine() string {
	if m.status != "" {
		style := uiDim
		switch m.statusIs {
		case uiErr:
			style = uiErrStyle
		case uiOK:
			style = uiOKStyle
		}
		return style.Render(uiTrunc(" "+m.status, m.w))
	}
	if m.screen() == scrList && m.tab == tabTasks {
		if s := m.filterSummary(); s != "" {
			return uiDim.Render(" " + s)
		}
	}
	if len(m.building) > 0 {
		var names []string
		for n := range m.building {
			names = append(names, n)
		}
		return uiDim.Render(" собираю справку: " + strings.Join(names, ", "))
	}
	return ""
}

func (m *uiModel) filterSummary() string {
	var parts []string
	if m.itemFilter.Project != "" {
		parts = append(parts, "проект: "+m.itemFilter.Project)
	}
	if m.itemFilter.Owner != "" {
		parts = append(parts, "исполнитель: "+m.itemFilter.Owner)
	}
	if m.itemFilter.Kind != "" {
		parts = append(parts, "вид: "+uiKindWord(m.itemFilter.Kind))
	}
	if m.itemFilter.Closed {
		parts = append(parts, "с закрытыми")
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " · ") + "   (c — снять)"
}

// hintLine — подсказка по клавишам того экрана, который сейчас открыт. Она
// всегда на месте: интерфейс, где надо помнить клавиши, до конца никто не
// осваивает.
// hintLine — нижняя полоса. Фон дорисовывается до края отдельным куском, а не
// одной обёрткой поверх готовой строки: внутри уже есть свои сбросы цвета, и
// обёртка гасла на первом же из них — полоса обрывалась на середине экрана.
func (m *uiModel) hintLine() string {
	return uiTrunc(" "+m.hints(), m.w)
}

func (m *uiModel) hints() string {
	key := func(k, what string) string { return uiHintKey.Render(k) + uiDim.Render(" "+what) }
	join := func(parts ...string) string { return " " + strings.Join(parts, uiDim.Render(" · ")) }

	switch m.screen() {
	case scrHelp:
		return join(key("↑↓", "листать"), key("←", "назад"))
	case scrForm:
		return join(key("tab", "поле"), key("ctrl+s", "сохранить"), key("ctrl+n", "источник"),
			key("ctrl+k", "убрать"), key("esc", "отмена"))
	case scrChannel:
		return join(key("tab", "поле"), key("space", "переключить"), key("enter", "добавить"),
			key("ctrl+k", "убрать"), key("ctrl+s", "сохранить"), key("esc", "отмена"))
	case scrPicker:
		return join(key("↑↓", "выбрать"), key("→ enter", "принять"), key("← esc", "отмена"))
	case scrConfirm:
		return join(key("y", "да"), key("esc", "нет"))
	case scrMeeting:
		return join(key("↑↓", "листать"), key("t", "расшифровка"), key("←", "назад"),
			key("?", "клавиши"), key("q", "выход"))
	case scrTranscript:
		return join(key("↑↓", "листать"), key("g/G", "начало/конец"), key("←", "назад"),
			key("q", "выход"))
	case scrProject:
		return join(key("↑↓", "листать"), key("e", "правка"), key("s", "справка"),
			key("D", "удалить"), key("←", "назад"))
	}

	if m.editing() {
		return join(key("enter", "принять"), key("esc", "снять"), key("↑↓", "выбрать"),
			key("← →", "раздел"), key("?", "клавиши"))
	}
	switch m.tab {
	case tabMeetings:
		return join(key("↑↓", "выбрать"), key("enter", "follow-up"), key("t", "расшифровка"),
			key("/", "фильтр"), key("q", "выход"))
	case tabTasks:
		return join(key("enter", "созвон"), key("d", "сделана"), key("x", "снять"),
			key("p", "проект"), key("o", "кто"), key("v", "вид"), key("a", "закрытые"))
	case tabProjects:
		return join(key("enter", "открыть"), key("n", "новый"), key("e", "правка"),
			key("D", "удалить"), key("s", "справка"))
	case tabSearch:
		return join(key("i", "ввод"), key("↑↓", "выбрать"), key("enter", "открыть"),
			key("esc", "очистить"))
	case tabChannels:
		return join(key("enter", "настроить"), key("space", "вкл/выкл"),
			key("/", "фильтр"), key("q", "выход"))
	}
	return ""
}

func uiHelpBody(w int) []string {
	type row struct{ keys, what string }
	sections := []struct {
		title string
		rows  []row
	}{
		{"Везде", []row{
			{"1…5", "созвоны, задачи, проекты, поиск, каналы"},
			{"← →", "предыдущий и следующий раздел; ← в карточке — назад"},
			{"tab / shift+tab", "то же самое"},
			{"↑ ↓ или k j", "движение по списку и по тексту карточки"},
			{"pgup pgdn", "страница вверх и вниз"},
			{"g / G", "в начало и в конец"},
			{"/", "фильтр по подстроке в текущем списке"},
			{"esc", "назад; в списке — снять фильтр"},
			{"r", "перечитать из базы"},
			{"?", "эта справка"},
			{"q или ctrl+c", "выход"},
		}},
		{"Созвоны", []row{
			{"enter", "follow-up: задачи, решения, вопросы, риски, ход разговора"},
			{"t", "расшифровка целиком"},
		}},
		{"Задачи", []row{
			{"enter", "созвон, на котором пункт появился"},
			{"d", "отметить сделанной"},
			{"x", "снять — решили не делать"},
			{"u", "вернуть в работу закрытый пункт"},
			{"p", "фильтр по проекту"},
			{"o", "фильтр по исполнителю"},
			{"v", "фильтр по виду: задачи, вопросы, решения"},
			{"a", "показывать и закрытые"},
			{"c", "снять все фильтры"},
		}},
		{"Проекты", []row{
			{"enter", "карточка: описание, источники, справка, что открыто"},
			{"n", "завести новый"},
			{"e", "править: название, описание, псевдонимы, источники"},
			{"D", "удалить из реестра; задачи и решения останутся"},
			{"s", "собрать справку по коду (поход в Claude)"},
		}},
		{"Форма проекта", []row{
			{"tab / shift+tab", "следующее и предыдущее поле"},
			{"ctrl+s", "сохранить"},
			{"ctrl+n", "добавить источник"},
			{"ctrl+t", "сменить вид источника"},
			{"ctrl+k", "убрать источник под курсором"},
			{"ctrl+w", "стереть слово, ctrl+u — всё поле"},
			{"esc", "отменить правку"},
		}},
		{"Каналы", []row{
			{"enter", "настроить: поля рисуются по описанию канала"},
			{"space", "включить или выключить прямо в списке"},
			{"space в форме", "переключатель под курсором"},
			{"enter в форме", "на строке «+» — добавить значение в список"},
			{"ctrl+k", "убрать значение списка под курсором"},
			{"ctrl+s", "сохранить"},
			{"", "Доступ в Google подключается только в веб-панели: за согласием " +
				"ходят браузером. Токенов и паролей в интерфейсе нет — их задаёт steno setup."},
		}},
		{"Поиск", []row{
			{"i или /", "вернуться к вводу запроса"},
			{"enter в вводе", "перейти к найденному"},
			{"enter в списке", "открыть созвон на этом месте"},
			{"esc", "очистить запрос"},
		}},
	}

	var out []string
	for _, s := range sections {
		out = append(out, "", "  "+uiSection.Render(s.title))
		for _, r := range s.rows {
			out = append(out, uiWrapLines("  "+uiAccent.Render(uiPad(r.keys, 17))+" ", r.what, w)...)
		}
	}
	out = append(out, "")
	out = append(out, uiWrapLines("  ", uiDim.Render(
		"Всё это работает без запущенного serve: интерфейс читает ту же базу, что и панель."), w)...)
	return out
}

// --- нижняя панель ------------------------------------------------------------

// Список на высоком экране занимает несколько строк из сорока, и всё остальное
// раньше было пустотой: человек видел содержимое в пять строк и провал в
// тридцать восемь под ним. Нижняя панель показывает суть выделенной строки —
// место занято тем, ради чего в список и смотрят.

// previewTitle — заголовок нижней панели по текущему разделу.
func (m *uiModel) previewTitle() string {
	switch m.tab {
	case tabMeetings:
		return "Созвон"
	case tabTasks:
		return "Пункт"
	case tabProjects:
		return "Проект"
	case tabSearch:
		return "Найдено"
	case tabChannels:
		return "Канал"
	}
	return ""
}

// preview — строки нижней панели. Пусто, если выделять нечего.
func (m *uiModel) preview(w int) []string {
	if m.screen() != scrList || m.rowCount() == 0 {
		return nil
	}
	switch m.tab {
	case tabMeetings:
		return m.previewMeeting(w)
	case tabTasks:
		return m.previewItem(w)
	case tabProjects:
		return m.previewProject(w)
	case tabSearch:
		return m.previewHit(w)
	case tabChannels:
		return m.previewChannel(w)
	}
	return nil
}

func (m *uiModel) previewMeeting(w int) []string {
	r, ok := m.selectedMeeting()
	if !ok {
		return nil
	}
	m.loadPeek(r.ID)
	var out []string
	add := func(s string) { out = append(out, s) }

	when := r.StartedAt.Format("02.01.2006 15:04")
	if r.Duration > 0 {
		when += " · " + uiDur(r.Duration)
	}
	add(uiDim.Render(when + " · " + uiStatusWord(r.Status)))
	if len(r.Participants) > 0 {
		out = append(out, uiWrapLines(uiDim.Render("участники: "),
			strings.Join(r.Participants, ", "), w)...)
	}

	f := m.peek
	if f == nil {
		add("")
		out = append(out, uiWrapLines("", "Follow-up ещё нет: созвон записан, но не разобран. "+
			"Разобрать:  steno process "+r.ID, w)...)
		return out
	}
	if t := strings.TrimSpace(f.Title); t != "" {
		add("")
		out = append(out, uiWrapLines("", uiBold.Render(t), w)...)
	}
	for _, s := range f.TLDR {
		out = append(out, uiWrapLines("  • ", s, w)...)
	}
	// Место есть — показываем разбор, а не пересказ о нём. Лишнее обрежет
	// раскладка, и она же скажет, что обрезала.
	sect := func(title string, items []string) {
		if len(items) == 0 {
			return
		}
		add("")
		add(uiSection.Render(title))
		for _, it := range items {
			out = append(out, uiWrapLines("  • ", it, w)...)
		}
	}
	var tasks []string
	for _, a := range f.ActionItems {
		t := a.What
		var meta []string
		if a.Owner != "" {
			meta = append(meta, a.Owner)
		}
		if a.Due != "" {
			meta = append(meta, "срок "+a.Due)
		}
		if len(meta) > 0 {
			t += uiDim.Render("  (" + strings.Join(meta, " · ") + ")")
		}
		tasks = append(tasks, t)
	}
	sect("Задачи", tasks)
	var decisions []string
	for _, d := range f.Decisions {
		decisions = append(decisions, d.What)
	}
	sect("Решения", decisions)
	var questions []string
	for _, q := range f.OpenQuestions {
		questions = append(questions, q.Question)
	}
	sect("Открытые вопросы", questions)
	sect("Риски", f.Risks)
	return out
}

// loadPeek подтягивает follow-up выделенного созвона. Один запрос на смену
// строки, а не на каждый кадр.
func (m *uiModel) loadPeek(id string) {
	if m.peekID == id {
		return
	}
	m.peekID, m.peekTags = id, nil
	m.peek, _ = m.st.Followup(id)
	if m.peek == nil {
		return
	}
	count := func(n int, one, few, many string) {
		if n > 0 {
			m.peekTags = append(m.peekTags, plural(n, one, few, many))
		}
	}
	count(len(m.peek.ActionItems), "задача", "задачи", "задач")
	count(len(m.peek.Decisions), "решение", "решения", "решений")
	count(len(m.peek.OpenQuestions), "вопрос", "вопроса", "вопросов")
	count(len(m.peek.Risks), "риск", "риска", "рисков")
}

func (m *uiModel) previewItem(w int) []string {
	it, ok := m.selectedItem()
	if !ok {
		return nil
	}
	var out []string
	out = append(out, uiWrapLines("", uiBold.Render(it.Text), w)...)
	out = append(out, "")

	meta := uiKindWord(it.Kind)
	if it.Owner != "" {
		meta += " · " + it.Owner
	}
	if it.Due != "" {
		meta += " · срок " + it.Due
	}
	if it.Project != "" {
		meta += " · " + it.Project
	}
	out = append(out, uiDim.Render("  "+meta))
	if it.Status != "open" {
		tail := "закрыт: " + it.Status
		if it.Note != "" {
			tail += " — " + it.Note
		}
		out = append(out, uiWrapLines(uiDim.Render("  "), tail, w)...)
	}
	if it.Quote != "" {
		out = append(out, "")
		out = append(out, uiWrapLines(uiDim.Render("  из разговора: "), "«"+it.Quote+"»", w)...)
	}
	out = append(out, "", uiDim.Render("  enter — созвон, на котором это появилось"))
	return out
}

func (m *uiModel) previewProject(w int) []string {
	p, ok := m.selectedProject()
	if !ok {
		return nil
	}
	var out []string
	if p.About != "" {
		out = append(out, uiWrapLines("", p.About, w)...)
	} else {
		out = append(out, uiDim.Render("описания нет — e добавит"))
	}
	out = append(out, "")
	if len(p.Aliases) > 0 {
		out = append(out, uiWrapLines(uiDim.Render("  зовут ещё: "), strings.Join(p.Aliases, ", "), w)...)
	}
	if len(p.Sources) == 0 {
		out = append(out, uiDim.Render("  источников нет — без них справку по коду собрать не из чего"))
	} else {
		for _, src := range p.Sources {
			out = append(out, uiWrapLines(uiDim.Render("  "+uiFit(uiSourceKindTitle(src.Kind), 14)),
				src.Value, w)...)
		}
	}
	out = append(out, "")
	if p.ContextAt.IsZero() {
		out = append(out, uiDim.Render("  справка не собрана — s соберёт"))
	} else {
		out = append(out, uiDim.Render("  справка собрана "+p.ContextAt.Format("02.01.2006")))
	}
	return out
}

func (m *uiModel) previewHit(w int) []string {
	if m.cursor[tabSearch] < 0 || m.cursor[tabSearch] >= len(m.hits) {
		return nil
	}
	h := m.hits[m.cursor[tabSearch]]
	var out []string
	out = append(out, uiWrapLines("", uiHighlight(strings.ReplaceAll(h.Snippet, "\n", " ")), w)...)
	out = append(out, "")
	where := time.Unix(h.StartedAt, 0).Format("02.01.2006 15:04")
	if h.Title != "" {
		where = h.Title + " · " + where
	}
	if h.Speaker != "" {
		where = h.Speaker + " · " + where
	}
	out = append(out, uiDim.Render("  "+where))
	out = append(out, "", uiDim.Render("  enter — открыть на этом месте"))
	return out
}

func (m *uiModel) previewChannel(w int) []string {
	c, ok := m.selectedChannel()
	if !ok {
		return nil
	}
	var out []string
	out = append(out, uiWrapLines("", c.About, w)...)
	out = append(out, "")
	state := uiDim.Render("  выключен")
	if c.Enabled {
		state = uiOKStyle.Render("  включён")
	}
	out = append(out, state+uiDim.Render(" · "+uiChannelWhat(c)))
	if s := strings.TrimSpace(c.Summary); s != "" {
		out = append(out, uiDim.Render("  настроено: "+s))
	} else {
		out = append(out, uiDim.Render("  ещё не настроено"))
	}
	out = append(out, "", uiDim.Render("  enter — настроить, space — включить или выключить"))
	return out
}
