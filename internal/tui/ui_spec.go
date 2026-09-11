package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
	"github.com/sur1cat/steno/internal/spec"
)

// ТЗ в терминале: t на задаче открывает её задание, а если задания нет —
// собирает. Из карточки a отдаёт ТЗ агенту, после вопроса «точно?».
//
// Здесь та же граница, что у панели и у `steno spec run`: запуск — только по
// нажатию человека за клавиатурой. Имя того, кто нажал, берётся из окружения,
// как в терминальной команде.

// uiSpecDone — ТЗ собралось (или не собралось). Спокойное сообщение в строке
// состояния: человек мог уйти на другой раздел, и распахивать карточку из-под
// него не стоит.
type uiSpecDone struct {
	ItemID string
	Spec   *spec.Spec
	Err    error
}

// uiSpecRunDone — агент отработал. Ход работы к этому моменту уже в базе, и
// карточка, если она открыта, перечитывается.
type uiSpecRunDone struct {
	ID   string
	Spec *spec.Spec
	Err  error
}

func (m *uiModel) reloadSpecs() error {
	store, err := spec.Open(m.st)
	if err != nil {
		return err
	}
	latest, err := store.Latest("")
	if err != nil {
		return err
	}
	m.specs = latest
	// Открытая карточка перечитывается из базы: во время исполнения в ней
	// растёт журнал, а после — меняется статус.
	if m.spec != nil {
		if sp, err := store.Get(m.spec.ID); err == nil {
			m.spec = sp
		}
	}
	return nil
}

// specOf — последнее ТЗ по задаче, если есть.
func (m *uiModel) specOf(itemID string) *spec.Spec {
	if m.specs == nil {
		return nil
	}
	return m.specs[itemID]
}

// specForSelected — t на задаче: открыть задание или собрать.
func (m *uiModel) specForSelected() tea.Cmd {
	it, ok := m.selectedItem()
	if !ok {
		return nil
	}
	if it.Kind != core.KindTask {
		m.fail(fmt.Errorf(i18n.Tr("%s — не задача, а %s: исполнять нечего"), it.ID, uiKindWord(it.Kind)))
		return nil
	}
	if sp := m.specOf(it.ID); sp != nil {
		m.spec = sp
		m.push(scrSpec)
		return nil
	}
	return m.buildSpec(it)
}

// buildSpec собирает ТЗ в фоне: это разбор репозитория и запрос к модели,
// минута, и терминал на неё не замирает.
func (m *uiModel) buildSpec(it core.ProjectItem) tea.Cmd {
	if it.Status != "open" {
		m.fail(fmt.Errorf(i18n.Tr("%s закрыт — ТЗ собирают по открытым задачам"), it.ID))
		return nil
	}
	if it.Project == core.UnassignedProject || strings.TrimSpace(it.Project) == "" {
		m.fail(fmt.Errorf(i18n.Tr("у %s не определён проект — сначала отнеси задачу к проекту (панель или steno projects)"), it.ID))
		return nil
	}
	if m.specBuilding[it.ID] {
		m.say(i18n.Tr("ТЗ по %s уже собирается"), it.ID)
		return nil
	}
	m.specBuilding[it.ID] = true
	m.say(i18n.Tr("собираю ТЗ по %s — это чтение репозитория и запрос к модели, около минуты"), it.ID)
	cfg, st, id := m.cfg, m.st, it.ID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		store, err := spec.Open(st)
		if err != nil {
			return uiSpecDone{ItemID: id, Err: err}
		}
		sp, err := spec.Build(ctx, spec.Deps{Cfg: cfg, St: st, Sp: store}, id)
		return uiSpecDone{ItemID: id, Spec: sp, Err: err}
	}
}

func (m *uiModel) onSpecDone(msg uiSpecDone) {
	delete(m.specBuilding, msg.ItemID)
	if msg.Err != nil {
		m.fail(fmt.Errorf(i18n.Tr("ТЗ по %s: %v"), msg.ItemID, msg.Err))
		return
	}
	if err := m.reloadSpecs(); err != nil {
		m.fail(err)
		return
	}
	switch {
	case msg.Spec.Status == spec.StatusRejected:
		m.say(i18n.Tr("ТЗ по %s: задача не взята — %s"), msg.ItemID, i18n.Cut(msg.Spec.Reject, 80))
	case len(msg.Spec.Gate()) > 0:
		m.say(i18n.Tr("ТЗ по %s готово (%s), но работать по нему нельзя — t покажет почему"), msg.ItemID, msg.Spec.ID)
	default:
		m.say(i18n.Tr("ТЗ по %s готово: %s, вопросов — %d; t — открыть"), msg.ItemID, msg.Spec.ID, len(msg.Spec.Unknowns))
	}
}

// keySpec — клавиши на карточке ТЗ.
func (m *uiModel) keySpec(k string) tea.Cmd {
	switch k {
	case "q":
		return tea.Quit
	case "esc", "left":
		m.spec = nil
		m.pop()
		return nil
	case "?":
		m.push(scrHelp)
		return nil
	case "a":
		m.askRunSpec()
		return nil
	case "r":
		// Перечитать: пока агент работает, журнал в базе растёт, и r здесь
		// значит то же, что и в списках, — «покажи свежее».
		if err := m.reloadSpecs(); err != nil {
			m.fail(err)
			return nil
		}
		m.rebuildBody()
		m.sayText(i18n.Tr("перечитал"))
		return nil
	case "b":
		if m.spec == nil {
			return nil
		}
		it, err := spec.FindItem(m.st, m.spec.ItemID)
		if err != nil {
			m.fail(err)
			return nil
		}
		return m.buildSpec(it)
	}
	m.scrollBody(k)
	return nil
}

// askRunSpec — вопрос перед запуском. Отказы, которые известны заранее, — до
// вопроса: спрашивать «точно?», чтобы потом ответить «нельзя», — это два
// нажатия ради одного отказа.
func (m *uiModel) askRunSpec() {
	sp := m.spec
	if sp == nil {
		return
	}
	set := spec.SettingsFor(m.cfg)
	switch {
	case !set.Enabled:
		m.fail(errors.New(i18n.Tr("исполнение выключено — включить: steno agent on")))
		return
	case sp.Status == spec.StatusRejected:
		m.fail(fmt.Errorf(i18n.Tr("это не ТЗ, а отказ: %s"), sp.Reject))
		return
	case sp.Status == spec.StatusRunning || m.specRunning[sp.ID]:
		m.fail(errors.New(i18n.Tr("по этому ТЗ уже идёт работа")))
		return
	}
	if reasons := sp.Gate(); len(reasons) > 0 {
		m.fail(fmt.Errorf(i18n.Tr("по этому ТЗ нельзя работать: %s"), reasons[0]))
		return
	}
	r := spec.Check(m.cfg, set, false)
	if r.Executor == "" {
		m.fail(errors.New(r.Why))
		return
	}
	m.confirm = &uiConfirm{
		text: fmt.Sprintf(i18n.Tr("Отдать %s исполнителю %s? Отдельная рабочая копия, ветка %s…, коммит. Push не делает никто."),
			sp.ID, r.Executor, r.BranchPrefix),
		action: actRunSpec, arg: sp.ID,
	}
	m.push(scrConfirm)
}

// runSpec — исполнение в фоне. by — из окружения, как у `steno spec run`.
func (m *uiModel) runSpec(id string) tea.Cmd {
	set := spec.SettingsFor(m.cfg)
	by := core.FirstNonEmpty(os.Getenv("USER"), os.Getenv("USERNAME"), i18n.Tr("терминал"))
	m.specRunning[id] = true
	m.say(i18n.Tr("отдал %s агенту — ход работы виден в карточке (r обновит), готовая ветка придёт сюда"), id)
	cfg, st := m.cfg, m.st
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		store, err := spec.Open(st)
		if err != nil {
			return uiSpecRunDone{ID: id, Err: err}
		}
		sp, err := spec.Run(ctx, spec.Deps{Cfg: cfg, St: st, Sp: store}, set, id, by, nil)
		return uiSpecRunDone{ID: id, Spec: sp, Err: err}
	}
}

func (m *uiModel) onSpecRunDone(msg uiSpecRunDone) {
	delete(m.specRunning, msg.ID)
	if err := m.reloadSpecs(); err != nil {
		m.fail(err)
		return
	}
	m.rebuildBody()
	if msg.Err != nil {
		m.fail(fmt.Errorf(i18n.Tr("работа по %s: %v"), msg.ID, msg.Err))
		return
	}
	if msg.Spec != nil && msg.Spec.Branch != "" {
		m.say(i18n.Tr("%s: готово, ветка %s в %s"), msg.ID, msg.Spec.Branch, msg.Spec.Repo)
		return
	}
	m.say(i18n.Tr("%s: агент отработал"), msg.ID)
}

// specLine — состояние ТЗ одной строкой для нижней панели задачи.
func (m *uiModel) specLine(it core.ProjectItem) string {
	if m.specBuilding[it.ID] {
		return i18n.Tr("ТЗ: собирается…")
	}
	sp := m.specOf(it.ID)
	if sp == nil {
		if it.Kind != core.KindTask || it.Status != "open" {
			return ""
		}
		return i18n.Tr("ТЗ нет — t соберёт по репозиторию проекта")
	}
	switch sp.Status {
	case spec.StatusRejected:
		return i18n.Trf("ТЗ %s: задача не взята — %s", sp.ID, i18n.Cut(sp.Reject, 60))
	case spec.StatusRunning:
		return i18n.Trf("ТЗ %s: агент работает, ветка %s — t покажет ход", sp.ID, sp.Branch)
	case spec.StatusDone:
		return i18n.Trf("ТЗ %s: сделано, ветка %s — t откроет", sp.ID, sp.Branch)
	case spec.StatusFailed:
		return i18n.Trf("ТЗ %s: сорвалось — %s", sp.ID, i18n.Cut(sp.RunError, 60))
	}
	if r := sp.Gate(); len(r) > 0 {
		return i18n.Trf("ТЗ %s есть, но работать по нему нельзя — t покажет почему", sp.ID)
	}
	return i18n.Trf("ТЗ %s готово, вопросов — %d; t откроет, a в карточке отдаст агенту", sp.ID, len(sp.Unknowns))
}

// specBody — карточка ТЗ: та же разметка, что печатает `steno spec show`,
// построчно и с переносом по ширине. Разметку не разбираем: заголовки и
// списки читаются и так, а второй рисовальщик разошёлся бы с первым.
func (m *uiModel) specBody(w int) []string {
	sp := m.spec
	if sp == nil {
		return []string{uiDim.Render(i18n.Tr("ТЗ не открыто"))}
	}
	var out []string
	if sp.Status == spec.StatusRejected {
		out = append(out, uiWrapLines("", i18n.Tr("Задача не взята: ")+sp.Reject, w)...)
		return out
	}
	for _, line := range strings.Split(strings.TrimRight(sp.Render(), "\n"), "\n") {
		line = uiUnmark(line)
		switch {
		case strings.HasPrefix(line, "# "):
			out = append(out, uiWrapLines("", uiBold.Render(strings.TrimPrefix(line, "# ")), w)...)
		case strings.HasPrefix(line, "## "):
			out = append(out, uiSection.Render(strings.TrimPrefix(line, "## ")))
		case strings.HasPrefix(line, "> "):
			out = append(out, uiWrapLines(uiDim.Render("│ "), strings.TrimPrefix(line, "> "), w)...)
		case line == "":
			out = append(out, "")
		default:
			out = append(out, uiWrapLines("", line, w)...)
		}
	}
	if sp.Status != spec.StatusDraft {
		out = append(out, "", uiSection.Render(i18n.Tr("Исполнение")))
		meta := uiStatusWordSpec(sp.Status)
		if sp.Branch != "" {
			meta += i18n.Tr(" · ветка ") + sp.Branch
		}
		if sp.RunBy != "" {
			meta += i18n.Tr(" · запустил: ") + sp.RunBy
		}
		out = append(out, uiWrapLines("", meta, w)...)
		if sp.RunError != "" {
			out = append(out, uiWrapLines(uiDim.Render("  "), sp.RunError, w)...)
		}
		if sp.Status == spec.StatusDone && sp.Repo != "" {
			out = append(out, uiWrapLines(uiDim.Render(i18n.Tr("  посмотреть: ")),
				"git -C "+sp.Repo+" diff .."+sp.Branch, w)...)
		}
		if sp.RunLog != "" {
			out = append(out, "")
			for _, l := range strings.Split(sp.RunLog, "\n") {
				out = append(out, uiWrapLines("  ", uiDim.Render(l), w)...)
			}
		}
	}
	return out
}

// uiUnmark снимает разметку, которая в терминале читается как мусор: жирное
// становится жирным, обратные кавычки вокруг путей исчезают, «  \n» в конце
// строки — тоже. Заголовки и списки остаются как есть: их видно и так.
func uiUnmark(line string) string {
	line = strings.TrimRight(line, " ")
	line = strings.ReplaceAll(line, "`", "")
	for {
		i := strings.Index(line, "**")
		if i < 0 {
			break
		}
		j := strings.Index(line[i+2:], "**")
		if j < 0 {
			break
		}
		line = line[:i] + uiBold.Render(line[i+2:i+2+j]) + line[i+2+j+2:]
	}
	return line
}

func uiStatusWordSpec(status string) string {
	switch status {
	case spec.StatusRunning:
		return i18n.Tr("агент работает")
	case spec.StatusDone:
		return i18n.Tr("агент отработал")
	case spec.StatusFailed:
		return i18n.Tr("агент сорвался")
	}
	return status
}
