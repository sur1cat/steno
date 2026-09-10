package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
	"github.com/sur1cat/steno/internal/panel"
	"github.com/sur1cat/steno/internal/publish"
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

// uiStatusWord — те же слова, что в панели
// (internal/panel/web/app/src/lib/fmt.ts). Один и тот же созвон не должен в
// терминале и в браузере называться по-разному: человек смотрит то туда, то
// сюда и сверяет глазами.
func uiStatusWord(status string) string {
	switch status {
	case "uploading":
		return i18n.Tr("разбираю файл")
	case "recording":
		return i18n.Tr("идёт запись")
	case "recorded":
		return i18n.Tr("записан")
	case "transcribed":
		return i18n.Tr("расшифрован")
	case "summarized":
		return i18n.Tr("есть follow-up")
	case "published":
		return i18n.Tr("разослан")
	case "publish_failed":
		return i18n.Tr("не разослан")
	case "failed":
		return i18n.Tr("сорвался")
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

func uiKindWord(k core.ItemKind) string {
	switch k {
	case core.KindTask:
		return i18n.Tr("задача")
	case core.KindQuestion:
		return i18n.Tr("вопрос")
	case core.KindDecision:
		return i18n.Tr("решение")
	}
	return string(k)
}

func uiKindLetter(k core.ItemKind) string {
	switch k {
	case core.KindTask:
		return i18n.Tr("З")
	case core.KindQuestion:
		return i18n.Tr("В")
	case core.KindDecision:
		return i18n.Tr("Р")
	}
	return "·"
}

func uiDur(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	m := int(d.Minutes())
	if m < 60 {
		return fmt.Sprintf(i18n.Tr("%d мин"), m)
	}
	return fmt.Sprintf(i18n.Tr("%d ч %02d"), m/60, m%60)
}

// uiHighlight превращает разметку сниппета в подсветку. В индексе лежит сырой
// текст, обрамлённый U+0002/U+0003, — те же символы, что разбирает панель.
func uiHighlight(s string) string {
	var b strings.Builder
	for {
		before, rest, found := strings.Cut(s, core.MarkStart)
		b.WriteString(before)
		if !found {
			return b.String()
		}
		inside, after, ok := strings.Cut(rest, core.MarkEnd)
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
	for _, target := range core.SortedKeys(links) {
		url := links[target]
		if url == "" {
			url = uiDim.Render(i18n.Tr("отправлено"))
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
		return []string{uiDim.Render(i18n.Tr("созвон не открыт"))}
	}
	mt := m.meeting
	var out []string
	add := func(s string) { out = append(out, s) }
	head := func(s string) { add(""); add(uiSection.Render(s)) }

	title := mt.Title
	if m.followup != nil && strings.TrimSpace(m.followup.Title) != "" {
		title = m.followup.Title
	}
	out = append(out, uiWrapLines("", uiBold.Render(core.OrDash(title)), w)...)

	meta := mt.StartedAt.Format("02.01.2006 15:04")
	if mt.EndedAt != nil {
		meta += " · " + uiDur(mt.EndedAt.Sub(mt.StartedAt))
	}
	meta += " · " + uiStatusWord(mt.Status)
	add(uiDim.Render(meta))
	if len(mt.Participants) > 0 {
		out = append(out, uiWrapLines(uiDim.Render(i18n.Tr("участники: ")),
			strings.Join(mt.Participants, ", "), w)...)
	}
	if mt.Error != "" {
		out = append(out, uiWrapLines(uiErrStyle.Render(i18n.Tr("сбой: ")), mt.Error, w)...)
	}
	if mt.LeftReason != "" {
		add(uiDim.Render(i18n.Tr("бот вышел: ") + mt.LeftReason))
	}
	out = append(out, uiLinkLines(m.links)...)

	f := m.followup
	if f == nil {
		head(i18n.Tr("Follow-up ещё нет"))
		out = append(out, uiWrapLines("", i18n.Tr("Созвон записан, но не разобран. Сделать разбор:  steno process ")+mt.ID, w)...)
		if len(m.segments) > 0 {
			add(uiDim.Render(i18n.Tr("расшифровка есть — t покажет её целиком")))
		}
		return out
	}

	if len(f.TLDR) > 0 {
		head(i18n.Tr("Коротко"))
		for _, s := range f.TLDR {
			out = append(out, uiWrapLines("  • ", s, w)...)
		}
	}
	if len(f.ActionItems) > 0 {
		head(i18n.Tr("Задачи"))
		for _, a := range f.ActionItems {
			out = append(out, uiWrapLines("  • ", a.What, w)...)
			tail := uiAccent.Render(core.OrDash(a.Owner)) + uiDim.Render(i18n.Tr(" · срок ")+publish.DueOr(a.Due, i18n.Tr("не назван")))
			if a.Project != "" {
				tail += uiDim.Render(" · " + a.Project)
			}
			tail += uiDim.Render(" · " + core.Clock(a.At))
			add(strings.Repeat(" ", uiItemIndent) + tail)
		}
	}
	if len(f.Decisions) > 0 {
		head(i18n.Tr("Решения"))
		for _, d := range f.Decisions {
			out = append(out, uiWrapLines("  • ", d.What, w)...)
			if d.Why != "" {
				out = append(out, uiWrapLines(uiDim.Render(strings.Repeat(" ", uiItemIndent)+i18n.Tr("почему: ")), d.Why, w)...)
			}
			add(uiDim.Render(strings.Repeat(" ", uiItemIndent) + core.Clock(d.At)))
		}
	}
	if len(f.OpenQuestions) > 0 {
		head(i18n.Tr("Открытые вопросы"))
		for _, q := range f.OpenQuestions {
			out = append(out, uiWrapLines("  • ", q.Question, w)...)
			add(uiDim.Render(strings.Repeat(" ", uiItemIndent) + i18n.Tr("ждём: ") +
				publish.DueOr(q.WaitingOn, i18n.Tr("не определено")) + " · " + core.Clock(q.At)))
		}
	}
	if len(f.Risks) > 0 {
		head(i18n.Tr("Риски"))
		for _, r := range f.Risks {
			out = append(out, uiWrapLines("  "+uiWarn.Render("!")+" ", r, w)...)
		}
	}
	if len(f.Timeline) > 0 {
		head(i18n.Tr("Как шёл разговор"))
		for _, t := range f.Timeline {
			out = append(out, uiWrapLines("  "+uiDim.Render(core.Clock(t.At))+" ", uiBold.Render(t.Title), w)...)
			if t.Summary != "" {
				out = append(out, uiWrapLines(strings.Repeat(" ", uiItemIndent), t.Summary, w)...)
			}
		}
	}
	if f.Empty() {
		head(i18n.Tr("Пусто"))
		out = append(out, uiWrapLines("", i18n.Tr("Разбор есть, но в нём ничего не оказалось: ни задач, ни решений. Обычно так выходит с очень короткой записью."), w)...)
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
			uiDim.Render(i18n.Tr("расшифровки нет")),
			"",
			i18n.Tr("Созвон записан, но не расшифрован. Расшифровать:  steno process ") + id,
		}
	}
	var out []string
	for i, sg := range m.segments {
		m.segLines[i] = len(out)
		prefix := uiDim.Render(core.Clock(sg.Start)) + " "
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
		return []string{uiDim.Render(i18n.Tr("проекта больше нет"))}
	}
	var out []string
	add := func(s string) { out = append(out, s) }
	head := func(s string) { add(""); add(uiSection.Render(s)) }

	add(uiBold.Render(i18n.Tr(p.Name)))
	if !p.Registered {
		out = append(out, uiWrapLines(uiWarn.Render("  "), i18n.Tr("Проекта нет в реестре: он встречается в задачах, но не заведён. Заведи с тем же названием (n), чтобы приложить репозиторий и описание."), w)...)
	}
	if p.About != "" {
		out = append(out, uiWrapLines("", p.About, w)...)
	}
	if len(p.Aliases) > 0 {
		out = append(out, uiWrapLines(uiDim.Render(i18n.Tr("зовут также: ")), strings.Join(p.Aliases, ", "), w)...)
	}

	head(i18n.Tr("Что звучит вслух"))
	if len(p.People) == 0 && len(p.Vocabulary) == 0 {
		add(uiDim.Render(i18n.Tr("  пусто — а без имён задача уезжает не тому (e — правка)")))
	}
	if len(p.People) > 0 {
		out = append(out, uiWrapLines(uiDim.Render(fmt.Sprintf("  %-13s ", i18n.Tr("люди"))),
			strings.Join(p.People, ", "), w)...)
	}
	if other := p.project().OtherWords(); len(other) > 0 {
		out = append(out, uiWrapLines(uiDim.Render(fmt.Sprintf("  %-13s ", i18n.Tr("сервисы"))),
			strings.Join(other, ", "), w)...)
	}

	head(i18n.Tr("Источники"))
	if len(p.Sources) == 0 {
		add(uiDim.Render(i18n.Tr("  нет — справку собрать не из чего (e — правка, ctrl+n — добавить)")))
	}
	for _, s := range p.Sources {
		out = append(out, uiWrapLines(fmt.Sprintf("  %-13s ", uiSourceKindTitle(s.Kind)), s.Value, w)...)
	}

	head(i18n.Tr("Справка по коду"))
	switch {
	case m.building[p.Name]:
		add(uiDim.Render(i18n.Tr("  собирается…")))
	case p.ContextAt.IsZero() || strings.TrimSpace(p.Primer) == "":
		add(uiDim.Render(i18n.Tr("  не собрана — s соберёт (это поход в Claude)")))
	default:
		add(uiDim.Render(i18n.Tr("  собрана ") + p.ContextAt.Format("02.01.2006 15:04")))
		add("")
		for _, line := range strings.Split(strings.TrimSpace(p.Primer), "\n") {
			out = append(out, uiWrapLines("  ", line, w)...)
		}
	}

	// Пункты берём из уже прочитанного, а не запросом: перерисовка бывает на
	// каждое изменение размера окна, и ходить за этим в базу незачем.
	var items []core.ProjectItem
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
	head(fmt.Sprintf(i18n.Tr("Открыто — %d"), open))
	if open == 0 {
		add(uiDim.Render(i18n.Tr("  ничего не висит")))
	}
	for _, it := range items {
		if it.Status != "open" {
			continue
		}
		out = append(out, m.itemLines(it, w)...)
	}
	if closed := len(items) - open; closed > 0 {
		head(fmt.Sprintf(i18n.Tr("Закрыто — %d"), closed))
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

func (m *uiModel) itemLines(it core.ProjectItem, w int) []string {
	prefix := "  " + uiDim.Render(uiFit(it.ID, 7)) + uiAccent.Render(uiKindLetter(it.Kind)) + " "
	out := uiWrapLines(prefix, it.Text, w)
	var tail []string
	if it.Owner != "" {
		tail = append(tail, it.Owner)
	}
	if it.Due != "" {
		tail = append(tail, i18n.Tr("до ")+it.Due)
	}
	if it.Status != "open" {
		word := i18n.Tr("сделано")
		if it.Status == "dropped" {
			word = i18n.Tr("снято")
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
				p[inner-1] = uiDim.Render(i18n.Tr("  … enter — целиком"))
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
		return fmt.Sprintf("%s · %d", uiTabTitles()[m.tab], n)
	}
	return uiTabTitles()[m.tab]
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
		return i18n.Tr("Расшифровка")
	case scrProject:
		return i18n.Tr("Проект")
	case scrHelp:
		return i18n.Tr("Клавиши")
	case scrForm:
		if m.form != nil && m.form.old != "" {
			return i18n.Tr("Правка проекта")
		}
		return i18n.Tr("Новый проект")
	case scrChannel:
		return i18n.Tr("Настройка канала")
	case scrPicker:
		return i18n.Tr("Выбор")
	case scrConfirm:
		return i18n.Tr("Подтверждение")
	}
	return uiTabTitles()[m.tab]
}

func (m *uiModel) tabBar() string {
	left := " "
	for i := uiTab(0); i < tabCount; i++ {
		switch {
		case i == m.tab && m.screen() == scrList:
			// Выбранная и мы в ней: залитый блок. Номер внутри блока не глушим —
			// на залитом фоне приглушение выглядит грязью, а не подсказкой.
			left += uiTabOn.Render(fmt.Sprintf(" %d %s ", i+1, uiTabTitles()[i]))
		case i == m.tab:
			// Выбранная, но человек ушёл вглубь — в карточку, форму, справку.
			// Рамка вместо заливки: место помнится, но сейчас оно не здесь.
			left += uiAccent.Render(fmt.Sprintf("[%d %s]", i+1, uiTabTitles()[i]))
		default:
			left += uiTabNum.Render(fmt.Sprintf(" %d ", i+1)) +
				uiTabOff.Render(uiTabTitles()[i]) + " "
		}
		left += " "
	}
	// Стрелки названы прямо в шапке: это первое, что человек нажимает, и
	// искать их в справке он не пойдёт. На узком окне подсказка укорачивается,
	// но не пропадает — пропадать ей нельзя, она тут главная.
	for _, hint := range []string{i18n.Tr("← → разделы · ? клавиши "), i18n.Tr("← → разделы "), "← → "} {
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
		return fmt.Sprintf(i18n.Tr("созвонов %d"), len(m.meetings))
	case tabTasks:
		open := 0
		for _, it := range m.items {
			if it.Status == "open" {
				open++
			}
		}
		return fmt.Sprintf(i18n.Tr("открыто %d"), open)
	case tabProjects:
		return fmt.Sprintf(i18n.Tr("проектов %d"), len(m.projects))
	case tabSearch:
		if strings.TrimSpace(m.query.String()) == "" {
			return ""
		}
		return fmt.Sprintf(i18n.Tr("найдено %d"), len(m.hits))
	case tabChannels:
		on := 0
		for _, c := range m.channels {
			if c.Enabled {
				on++
			}
		}
		return fmt.Sprintf(i18n.Tr("включено %d из %d"), on, len(m.channels))
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
				uiDim.Render(i18n.Tr("   ctrl+s сохранит, esc отменит"))
		}
		return pad + uiDim.Render(i18n.Tr("ctrl+s сохранит, esc отменит"))
	case scrChannel:
		if m.chanForm != nil {
			return pad + uiBold.Render("«"+m.chanForm.name+"»") +
				uiDim.Render(i18n.Tr("   ctrl+s сохранит, esc отменит"))
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
		return pad + uiDim.Render(i18n.Tr("запрос: ")) + m.query.view(min(m.w-12, 60), m.searchFocus)
	}
	if m.filtering || !m.filter[m.tab].empty() {
		return pad + uiDim.Render(i18n.Tr("фильтр: ")) + m.filter[m.tab].view(min(m.w-12, 60), m.filtering)
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
			title = core.OrDash(m.meeting.Title)
		}
	case scrProject:
		title = m.projectName
	case scrHelp:
		title = i18n.Tr("что нажимать")
	}
	left := strings.Repeat(" ", uiGutter) + uiBold.Render(uiTrunc(title, max(m.w-24, 10)))
	right := ""
	if n := len(m.body); n > m.listHeight() {
		right = uiDim.Render(fmt.Sprintf(i18n.Tr("%d–%d из %d"), m.bodyOff+1,
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
func uiMeetingTitle(r core.MeetingRow) string {
	t := core.OrDash(r.Title)
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
	return uiNatural(i18n.Tr("название"), vals, avail), when, status, tasks
}

func (m *uiModel) colsTasks() (what, owner, due, project, id int) {
	owner, due, project, id = 16, 12, 16, 8
	rows := m.visibleItems()
	vals := make([]string, 0, len(rows))
	for _, it := range rows {
		if it.Kind != core.KindTask {
			vals = append(vals, uiKindWord(it.Kind)+": "+it.Text)
			continue
		}
		vals = append(vals, it.Text)
	}
	avail := uiFlex(m.tableWidth(), owner, due, project, id)
	return uiNatural(i18n.Tr("что"), vals, avail), owner, due, project, id
}

func (m *uiModel) colsProjects() (name, tasks, questions, decisions, sources int) {
	tasks, questions, decisions, sources = 7, 7, 7, 9
	rows := m.visibleProjects()
	vals := make([]string, 0, len(rows))
	for _, p := range rows {
		vals = append(vals, p.Name)
	}
	avail := uiFlex(m.tableWidth(), tasks, questions, decisions, sources, 14)
	return uiNatural(i18n.Tr("проект"), vals, avail), tasks, questions, decisions, sources
}

func (m *uiModel) colsChannels() (name, state, what int) {
	state, what = 12, 22
	rows := m.visibleChannels()
	vals := make([]string, 0, len(rows))
	for _, c := range rows {
		vals = append(vals, c.Name)
	}
	avail := uiFlex(m.tableWidth(), state, what, 16)
	return uiNatural(i18n.Tr("канал"), vals, avail), state, what
}

func (m *uiModel) columns() string {
	// Ширины повторяют раскладку строк ниже: заголовок, сдвинутый на две
	// колонки от своей колонки, читается как чужой.
	pad := strings.Repeat(" ", uiGutter)
	switch m.tab {
	case tabMeetings:
		name, when, status, tasks := m.colsMeetings()
		return pad + uiCell(i18n.Tr("название"), name) + uiCell(i18n.Tr("когда"), when) +
			uiCell(i18n.Tr("статус"), status) + uiCell(i18n.Tr("зад."), tasks)
	case tabTasks:
		what, owner, due, project, id := m.colsTasks()
		return pad + uiCell(i18n.Tr("что"), what) + uiCell(i18n.Tr("кто"), owner) +
			uiCell(i18n.Tr("срок"), due) + uiCell(i18n.Tr("проект"), project) + uiCell("id", id)
	case tabProjects:
		name, tasks, questions, decisions, sources := m.colsProjects()
		return pad + uiCell(i18n.Tr("проект"), name) + uiCell(i18n.Tr("задач"), tasks) +
			uiCell(i18n.Tr("вопр."), questions) + uiCell(i18n.Tr("реш."), decisions) +
			uiCell(i18n.Tr("источн."), sources) + i18n.Tr("справка")
	case tabChannels:
		name, state, what := m.colsChannels()
		return pad + uiCell(i18n.Tr("канал"), name) + uiCell(i18n.Tr("состояние"), state) +
			uiCell(i18n.Tr("что делает"), what) + i18n.Tr("настроено")
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
			return uiEmpty(m.w, i18n.Tr("Ничего не подошло под фильтр"),
				i18n.Tr("esc снимет фильтр, / наберёт другой."))
		}
		return uiEmpty(m.w, i18n.Tr("Созвонов пока нет"),
			i18n.Tr("Бот запишет созвон и разберёт его сам:  steno join <ссылка>"),
			i18n.Tr("Чтобы он ходил на встречи из календаря без напоминаний:  steno serve"))
	}
	from, to := m.window(len(rows))
	out := make([]string, 0, to-from)
	for i := from; i < to; i++ {
		r := rows[i]
		nameW, whenW, statusW, tasksW := m.colsMeetings()
		// Название — то, ради чего человек сюда смотрит, поэтому оно первое и
		// занимает весь запас ширины. Участники дописываются в ту же колонку и
		// обрезаются вместе с ней.
		title := core.OrDash(r.Title)
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
			return uiEmpty(m.w, i18n.Tr("Задач пока нет"),
				i18n.Tr("Они появляются сами: бот разбирает созвон и раскладывает, кто что взял."),
				i18n.Tr("Записать созвон:  steno join <ссылка>"))
		case !m.itemFilter.empty() || !m.filter[tabTasks].empty():
			return uiEmpty(m.w, i18n.Tr("Под фильтр ничего не подошло"),
				i18n.Tr("c снимет все фильтры, a покажет и закрытое."))
		default:
			return uiEmpty(m.w, i18n.Tr("Всё закрыто"),
				i18n.Tr("Ни одной открытой задачи. a покажет закрытые."))
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
		if it.Kind != core.KindTask {
			text = uiKindWord(it.Kind) + ": " + text
		}
		if it.Status == "open" {
			line = " " + uiCell(text, whatW)
		} else {
			line = " " + uiDim.Render(uiCell(text, whatW))
		}
		line += uiCell(core.OrDash(it.Owner), ownerW)
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
			return uiEmpty(m.w, i18n.Tr("Ничего не подошло под фильтр"),
				i18n.Tr("esc снимет фильтр."))
		}
		return uiEmpty(m.w, i18n.Tr("Проектов нет"),
			i18n.Tr("n заведёт первый: название, одна строка о том, что это, и репозиторий или каталог с кодом."),
			i18n.Tr("По ним бот понимает, к чему относится сказанное на созвоне."))
	}
	from, to := m.window(len(rows))
	out := make([]string, 0, to-from)
	for i := from; i < to; i++ {
		p := rows[i]
		nameW, tasksW, questionsW, decisionsW, sourcesW := m.colsProjects()
		line := " " + uiCell(i18n.Tr(p.Name), nameW)
		line += uiDim.Render(uiFit(fmt.Sprintf("%d", p.Tasks), tasksW))
		line += uiDim.Render(uiFit(fmt.Sprintf("%d", p.Questions), questionsW))
		line += uiDim.Render(uiFit(fmt.Sprintf("%d", p.Decisions), decisionsW))
		line += uiDim.Render(uiFit(fmt.Sprintf("%d", len(p.Sources)), sourcesW))
		switch {
		case m.building[p.Name]:
			line += i18n.Tr("собирается…")
		case !p.Registered:
			// Значка вроде звёздочки у названия человек не расшифрует, а место
			// в этой колонке всё равно пустое: у незаведённого проекта нет ни
			// источников, ни справки.
			line += uiWarn.Render(i18n.Tr("не заведён"))
		case !p.ContextAt.IsZero():
			line += p.ContextAt.Format("02.01.2006")
		case len(p.Sources) == 0:
			line += uiDim.Render(i18n.Tr("нет источников"))
		default:
			line += uiDim.Render(i18n.Tr("не собрана"))
		}
		out = append(out, m.rowStyle(i)(line))
	}
	return out
}

func (m *uiModel) searchView() []string {
	if strings.TrimSpace(m.query.String()) == "" {
		return uiEmpty(m.w, i18n.Tr("Поиск по всему, что накоплено"),
			i18n.Tr("Наберите слово — ищется и в расшифровках, и в follow-up."),
			i18n.Tr("Ищется по началу слова: «релиз» найдёт и «релиза», и «релизом»."))
	}
	if len(m.hits) == 0 {
		return uiEmpty(m.w, i18n.Tr("Ничего не нашлось"),
			i18n.Tr("Слова короче двух букв не ищутся. Попробуйте другое слово или его начало."))
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
		who := core.OrDash(h.Title)
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
		return uiEmpty(m.w, i18n.Tr("Ничего не подошло под фильтр"), i18n.Tr("esc снимет фильтр."))
	}
	from, to := m.window(len(rows))
	out := make([]string, 0, to-from)
	for i := from; i < to; i++ {
		c := rows[i]
		nameW, stateW, whatW := m.colsChannels()
		line := " " + uiCell(c.Name, nameW)
		if c.Enabled {
			line += uiOKStyle.Render(uiCell(i18n.Tr("включён"), stateW))
		} else {
			line += uiDim.Render(uiCell(i18n.Tr("выключен"), stateW))
		}
		line += uiDim.Render(uiCell(uiChannelWhat(c), whatW))
		if s := strings.TrimSpace(c.Summary); s != "" {
			line += s
		} else {
			line += uiDim.Render(i18n.Tr("не настроено"))
		}
		out = append(out, m.rowStyle(i)(line))
	}
	return out
}

// channelFormView рисует форму по описанию полей канала, а не по своему списку:
// новое поле в ChannelDefs появляется здесь само. Возвращает ещё и номер строки
// под курсором — по нему форма прокручивается за курсором.
func (m *uiModel) channelFormView() ([]string, int) {
	f := m.chanForm
	if f == nil {
		return nil, 0
	}
	// Колонка подписей — по самой длинной подписи этого канала, а не наугад:
	// «Писать в личку тем, на ком задача» — тридцать три колонки, и на
	// фиксированных двадцати она наезжала на значение справа.
	lw := ansi.StringWidth(i18n.Tr("канал включён"))
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
			return uiOKStyle.Render(i18n.Tr("[×] да"))
		}
		return uiDim.Render(i18n.Tr("[ ] нет"))
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
			row(here, i18n.Tr("канал включён"), check(f.enabled))
			if !f.live {
				note(uiDim.Render(i18n.Tr("сервис подхватит это после перезапуска serve")))
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
			name, text := "", i18n.Tr("добавить значение")
			if len(fs.items) == 0 {
				name, text = fs.def.Label, i18n.Tr("пока ни одного — enter добавит")
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
		i18n.Tr("Токенов и паролей здесь нет и не будет: их задаёт steno setup, и лежат они в .env.")), m.w-2)...)
	return out, active
}

// googleLines — состояние доступа в Google. Кнопки здесь быть не может: за
// согласием ходят браузером, а терминалу его открыть нечем.
func (m *uiModel) googleLines(def core.ChannelField, lw int) []string {
	g := panel.GoogleStatus(m.cfg)
	var out []string
	head := "  " + uiDim.Render(uiFit(def.Label, lw))
	switch {
	case g.Connected && g.Account != "":
		out = append(out, head+uiOKStyle.Render(i18n.Tr("подключено"))+uiDim.Render(" · "+g.Account))
	case g.Connected:
		out = append(out, head+uiOKStyle.Render(i18n.Tr("подключено")))
	default:
		out = append(out, head+uiWarn.Render(i18n.Tr("не подключено")))
	}
	if g.Why != "" {
		style := uiDim
		if g.Severity == panel.GoogleWarn {
			style = uiWarn
		}
		out = append(out, uiWrapLines("  "+uiPad("", lw), style.Render(g.Why), m.w-2)...)
	}
	if !g.Connected {
		out = append(out, uiWrapLines("  "+uiPad("", lw), uiDim.Render(
			i18n.Tr("Подключение идёт через браузер, из терминала его не нажать: запусти steno serve ")+
				i18n.Tr("и подключись в панели, в разделе «Настройки».")), m.w-2)...)
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
	row(uiFieldName, i18n.Tr("название"), f.name.view(w, f.field == uiFieldName))
	row(uiFieldAbout, i18n.Tr("о чём проект"), f.about.view(w, f.field == uiFieldAbout))
	row(uiFieldAliases, i18n.Tr("зовут также"), f.aliases.view(w, f.field == uiFieldAliases))
	out = append(out, "", "  "+uiSection.Render(i18n.Tr("Что звучит вслух"))+
		uiDim.Render(i18n.Tr("  через запятую")))
	row(uiFieldPeople, i18n.Tr("кто участвует"), f.people.view(w, f.field == uiFieldPeople))
	row(uiFieldWords, i18n.Tr("сервисы, слова"), f.words.view(w, f.field == uiFieldWords))
	out = append(out, "", "  "+uiSection.Render(i18n.Tr("Источники"))+uiDim.Render(i18n.Tr("  ctrl+n добавить · ctrl+t сменить вид · ctrl+k убрать")))
	if len(f.sources) == 0 {
		out = append(out, uiWrapLines("  ", uiDim.Render(i18n.Tr("Пока ни одного. Без них справку по коду собрать не из чего, но проект всё равно заведётся.")), m.w-2)...)
	}
	for i, s := range f.sources {
		row(uiFormFixed+i, uiSourceKindTitle(s.kind), s.value.view(w, f.field == uiFormFixed+i))
	}
	out = append(out, "")
	out = append(out, uiWrapLines("  ", uiDim.Render(
		i18n.Tr("Псевдонимы — через запятую: как проект называют вслух. По ним бот понимает, что «биллинг» и «платежи» — одно и то же.")), m.w-2)...)
	out = append(out, "")
	out = append(out, uiWrapLines("  ", uiDim.Render(
		i18n.Tr("Имена людей — теми, которыми их зовут на созвоне, а не подписью в git. Их не угадать по коду, а без них задача уезжает не тому. Сервисы и сокращения — рядом, отдельной строкой: по ней бот знает, что «Сапар» это не человек.")), m.w-2)...)
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
	// По ширине текста, а не окна. Раньше здесь стояло m.w-2, и вопрос длиннее
	// экрана box обрезал многоточием — ровно посередине, где стоит цена.
	// uiProseWidth — та же мера, что у карточек: строка в сто с лишним колонок
	// читается хуже, а на узком окне ещё и не влезает в рамку (внутрь неё
	// помещается m.w-4, не больше).
	out = append(out, uiWrapLines("  ", m.confirm.text,
		min(uiProseWidth(m.w)+uiGutter, m.w-4))...)
	out = append(out, "", "  "+uiWarn.Render("y")+uiDim.Render(i18n.Tr(" — удалить, любая другая клавиша — отмена")))
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
		return uiDim.Render(i18n.Tr(" собираю справку: ") + strings.Join(names, ", "))
	}
	return ""
}

func (m *uiModel) filterSummary() string {
	var parts []string
	if m.itemFilter.Project != "" {
		parts = append(parts, i18n.Tr("проект: ")+m.itemFilter.Project)
	}
	if m.itemFilter.Owner != "" {
		parts = append(parts, i18n.Tr("исполнитель: ")+m.itemFilter.Owner)
	}
	if m.itemFilter.Kind != "" {
		parts = append(parts, i18n.Tr("вид: ")+uiKindWord(m.itemFilter.Kind))
	}
	if m.itemFilter.Closed {
		parts = append(parts, i18n.Tr("с закрытыми"))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " · ") + i18n.Tr("   (c — снять)")
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
		return join(key("↑↓", i18n.Tr("листать")), key("←", i18n.Tr("назад")))
	case scrForm:
		return join(key("tab", i18n.Tr("поле")), key("ctrl+s", i18n.Tr("сохранить")), key("ctrl+n", i18n.Tr("источник")),
			key("ctrl+k", i18n.Tr("убрать")), key("esc", i18n.Tr("отмена")))
	case scrChannel:
		return join(key("tab", i18n.Tr("поле")), key("space", i18n.Tr("переключить")), key("enter", i18n.Tr("добавить")),
			key("ctrl+k", i18n.Tr("убрать")), key("ctrl+s", i18n.Tr("сохранить")), key("esc", i18n.Tr("отмена")))
	case scrPicker:
		return join(key("↑↓", i18n.Tr("выбрать")), key("→ enter", i18n.Tr("принять")), key("← esc", i18n.Tr("отмена")))
	case scrConfirm:
		return join(key("y", i18n.Tr("да")), key("esc", i18n.Tr("нет")))
	case scrMeeting:
		return join(key("↑↓", i18n.Tr("листать")), key("t", i18n.Tr("расшифровка")), key("D", i18n.Tr("удалить")),
			key("←", i18n.Tr("назад")), key("?", i18n.Tr("клавиши")), key("q", i18n.Tr("выход")))
	case scrTranscript:
		return join(key("↑↓", i18n.Tr("листать")), key("g/G", i18n.Tr("начало/конец")),
			key("D", i18n.Tr("удалить")), key("←", i18n.Tr("назад")), key("q", i18n.Tr("выход")))
	case scrProject:
		return join(key("↑↓", i18n.Tr("листать")), key("e", i18n.Tr("правка")), key("s", i18n.Tr("справка")),
			key("D", i18n.Tr("удалить")), key("←", i18n.Tr("назад")))
	}

	if m.editing() {
		return join(key("enter", i18n.Tr("принять")), key("esc", i18n.Tr("снять")), key("↑↓", i18n.Tr("выбрать")),
			key("← →", i18n.Tr("раздел")), key("?", i18n.Tr("клавиши")))
	}
	switch m.tab {
	case tabMeetings:
		return join(key("↑↓", i18n.Tr("выбрать")), key("enter", "follow-up"), key("t", i18n.Tr("расшифровка")),
			key("D", i18n.Tr("удалить")), key("/", i18n.Tr("фильтр")), key("q", i18n.Tr("выход")))
	case tabTasks:
		return join(key("enter", i18n.Tr("созвон")), key("d", i18n.Tr("сделана")), key("x", i18n.Tr("снять")),
			key("p", i18n.Tr("проект")), key("o", i18n.Tr("кто")), key("v", i18n.Tr("вид")), key("a", i18n.Tr("закрытые")))
	case tabProjects:
		return join(key("enter", i18n.Tr("открыть")), key("n", i18n.Tr("новый")), key("e", i18n.Tr("правка")),
			key("D", i18n.Tr("удалить")), key("s", i18n.Tr("справка")))
	case tabSearch:
		return join(key("i", i18n.Tr("ввод")), key("↑↓", i18n.Tr("выбрать")), key("enter", i18n.Tr("открыть")),
			key("esc", i18n.Tr("очистить")))
	case tabChannels:
		return join(key("enter", i18n.Tr("настроить")), key("space", i18n.Tr("вкл/выкл")),
			key("/", i18n.Tr("фильтр")), key("q", i18n.Tr("выход")))
	}
	return ""
}

func uiHelpBody(w int) []string {
	type row struct{ keys, what string }
	sections := []struct {
		title string
		rows  []row
	}{
		{i18n.Tr("Везде"), []row{
			{"1…5", i18n.Tr("созвоны, задачи, проекты, поиск, каналы")},
			{"← →", i18n.Tr("предыдущий и следующий раздел; ← в карточке — назад")},
			{"tab / shift+tab", i18n.Tr("то же самое")},
			{i18n.Tr("↑ ↓ или k j"), i18n.Tr("движение по списку и по тексту карточки")},
			{"pgup pgdn", i18n.Tr("страница вверх и вниз")},
			{"g / G", i18n.Tr("в начало и в конец")},
			{"/", i18n.Tr("фильтр по подстроке в текущем списке")},
			{"esc", i18n.Tr("назад; в списке — снять фильтр")},
			{"r", i18n.Tr("перечитать из базы")},
			{"?", i18n.Tr("эта справка")},
			{i18n.Tr("q или ctrl+c"), i18n.Tr("выход")},
		}},
		{i18n.Tr("Созвоны"), []row{
			{"enter", i18n.Tr("follow-up: задачи, решения, вопросы, риски, ход разговора")},
			{"t", i18n.Tr("расшифровка целиком")},
			{"D", i18n.Tr("удалить созвон целиком: запись, расшифровку, follow-up ") +
				i18n.Tr("и задачи, которые из него вышли. Чужие пункты, закрытые на нём, ") +
				i18n.Tr("вернутся в работу")},
		}},
		{i18n.Tr("Задачи"), []row{
			{"enter", i18n.Tr("созвон, на котором пункт появился")},
			{"d", i18n.Tr("отметить сделанной")},
			{"x", i18n.Tr("снять — решили не делать")},
			{"u", i18n.Tr("вернуть в работу закрытый пункт")},
			{"p", i18n.Tr("фильтр по проекту")},
			{"o", i18n.Tr("фильтр по исполнителю")},
			{"v", i18n.Tr("фильтр по виду: задачи, вопросы, решения")},
			{"a", i18n.Tr("показывать и закрытые")},
			{"c", i18n.Tr("снять все фильтры")},
		}},
		{i18n.Tr("Проекты"), []row{
			{"enter", i18n.Tr("карточка: описание, источники, справка, что открыто")},
			{"n", i18n.Tr("завести новый")},
			{"e", i18n.Tr("править: название, описание, псевдонимы, источники")},
			{"D", i18n.Tr("удалить из реестра; задачи и решения останутся")},
			{"s", i18n.Tr("собрать справку по коду (поход в Claude)")},
		}},
		{i18n.Tr("Форма проекта"), []row{
			{"tab / shift+tab", i18n.Tr("следующее и предыдущее поле")},
			{"ctrl+s", i18n.Tr("сохранить")},
			{"ctrl+n", i18n.Tr("добавить источник")},
			{"ctrl+t", i18n.Tr("сменить вид источника")},
			{"ctrl+k", i18n.Tr("убрать источник под курсором")},
			{"ctrl+w", i18n.Tr("стереть слово, ctrl+u — всё поле")},
			{"esc", i18n.Tr("отменить правку")},
		}},
		{i18n.Tr("Каналы"), []row{
			{"enter", i18n.Tr("настроить: поля рисуются по описанию канала")},
			{"space", i18n.Tr("включить или выключить прямо в списке")},
			{i18n.Tr("space в форме"), i18n.Tr("переключатель под курсором")},
			{i18n.Tr("enter в форме"), i18n.Tr("на строке «+» — добавить значение в список")},
			{"ctrl+k", i18n.Tr("убрать значение списка под курсором")},
			{"ctrl+s", i18n.Tr("сохранить")},
			{"", i18n.Tr("Доступ в Google подключается только в веб-панели: за согласием ") +
				i18n.Tr("ходят браузером. Токенов и паролей в интерфейсе нет — их задаёт steno setup.")},
		}},
		{i18n.Tr("Поиск"), []row{
			{i18n.Tr("i или /"), i18n.Tr("вернуться к вводу запроса")},
			{i18n.Tr("enter в вводе"), i18n.Tr("перейти к найденному")},
			{i18n.Tr("enter в списке"), i18n.Tr("открыть созвон на этом месте")},
			{"esc", i18n.Tr("очистить запрос")},
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
		i18n.Tr("Всё это работает без запущенного serve: интерфейс читает ту же базу, что и панель.")), w)...)
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
		return i18n.Tr("Созвон")
	case tabTasks:
		return i18n.Tr("Пункт")
	case tabProjects:
		return i18n.Tr("Проект")
	case tabSearch:
		return i18n.Tr("Найдено")
	case tabChannels:
		return i18n.Tr("Канал")
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
		out = append(out, uiWrapLines(uiDim.Render(i18n.Tr("участники: ")),
			strings.Join(r.Participants, ", "), w)...)
	}

	f := m.peek
	if f == nil {
		add("")
		out = append(out, uiWrapLines("", i18n.Tr("Follow-up ещё нет: созвон записан, но не разобран. ")+
			i18n.Tr("Разобрать:  steno process ")+r.ID, w)...)
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
			meta = append(meta, i18n.Tr("срок ")+a.Due)
		}
		if len(meta) > 0 {
			t += uiDim.Render("  (" + strings.Join(meta, " · ") + ")")
		}
		tasks = append(tasks, t)
	}
	sect(i18n.Tr("Задачи"), tasks)
	var decisions []string
	for _, d := range f.Decisions {
		decisions = append(decisions, d.What)
	}
	sect(i18n.Tr("Решения"), decisions)
	var questions []string
	for _, q := range f.OpenQuestions {
		questions = append(questions, q.Question)
	}
	sect(i18n.Tr("Открытые вопросы"), questions)
	sect(i18n.Tr("Риски"), f.Risks)
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
			m.peekTags = append(m.peekTags, i18n.Plural(n, one, few, many))
		}
	}
	count(len(m.peek.ActionItems), i18n.Tr("задача"), i18n.Tr("задачи"), i18n.Tr("задач"))
	count(len(m.peek.Decisions), i18n.Tr("решение"), i18n.Tr("решения"), i18n.Tr("решений"))
	count(len(m.peek.OpenQuestions), i18n.Tr("вопрос"), i18n.Tr("вопроса"), i18n.Tr("вопросов"))
	count(len(m.peek.Risks), i18n.Tr("риск"), i18n.Tr("риска"), i18n.Tr("рисков"))
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
		meta += i18n.Tr(" · срок ") + it.Due
	}
	if it.Project != "" {
		meta += " · " + it.Project
	}
	out = append(out, uiDim.Render("  "+meta))
	if it.Status != "open" {
		tail := i18n.Tr("закрыт: ") + it.Status
		if it.Note != "" {
			tail += " — " + it.Note
		}
		out = append(out, uiWrapLines(uiDim.Render("  "), tail, w)...)
	}
	if it.Quote != "" {
		out = append(out, "")
		out = append(out, uiWrapLines(uiDim.Render(i18n.Tr("  из разговора: ")), "«"+it.Quote+"»", w)...)
	}
	out = append(out, "", uiDim.Render(i18n.Tr("  enter — созвон, на котором это появилось")))
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
		out = append(out, uiDim.Render(i18n.Tr("описания нет — e добавит")))
	}
	out = append(out, "")
	if len(p.Aliases) > 0 {
		out = append(out, uiWrapLines(uiDim.Render(i18n.Tr("  зовут ещё: ")), strings.Join(p.Aliases, ", "), w)...)
	}
	if len(p.People) > 0 {
		out = append(out, uiWrapLines(uiDim.Render(i18n.Tr("  люди: ")), strings.Join(p.People, ", "), w)...)
	}
	if len(p.Sources) == 0 {
		out = append(out, uiDim.Render(i18n.Tr("  источников нет — без них справку по коду собрать не из чего")))
	} else {
		for _, src := range p.Sources {
			out = append(out, uiWrapLines(uiDim.Render("  "+uiFit(uiSourceKindTitle(src.Kind), 14)),
				src.Value, w)...)
		}
	}
	out = append(out, "")
	if p.ContextAt.IsZero() {
		out = append(out, uiDim.Render(i18n.Tr("  справка не собрана — s соберёт")))
	} else {
		out = append(out, uiDim.Render(i18n.Tr("  справка собрана ")+p.ContextAt.Format("02.01.2006")))
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
	out = append(out, "", uiDim.Render(i18n.Tr("  enter — открыть на этом месте")))
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
	state := uiDim.Render(i18n.Tr("  выключен"))
	if c.Enabled {
		state = uiOKStyle.Render(i18n.Tr("  включён"))
	}
	out = append(out, state+uiDim.Render(" · "+uiChannelWhat(c)))
	if s := strings.TrimSpace(c.Summary); s != "" {
		out = append(out, uiDim.Render(i18n.Tr("  настроено: ")+s))
	} else {
		out = append(out, uiDim.Render(i18n.Tr("  ещё не настроено")))
	}
	out = append(out, "", uiDim.Render(i18n.Tr("  enter — настроить, space — включить или выключить")))
	return out
}
