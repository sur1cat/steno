package tui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/sur1cat/steno/internal/brain"
	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
)

// Данные для терминального интерфейса. Своих запросов здесь почти нет: всё
// читается теми же выборками, которыми живёт панель, — ListMeetings, Followup,
// Segments, ProjectItems, Projects, Search. Вторая копия тех же запросов
// разошлась бы с первой на первой же правке, и терминал начал бы показывать
// не то же самое, что панель.

// uiProjectRow — строка списка проектов: реестр и накопленное состояние вместе.
// Проект бывает заведён, но пуст (его только что создали), и бывает наоборот —
// упомянут в задачах, но в реестре его нет. Показывать надо оба: человек,
// заведший проект минуту назад, не должен читать «проектов нет».
type uiProjectRow struct {
	Name       string
	Registered bool // есть запись в реестре, а не только пункты по проекту
	About      string
	Aliases    []string
	Sources    []core.Source
	People     []string
	Vocabulary []string // весь словарь, включая имена людей
	Tasks      int      // открытых задач
	Questions  int
	Decisions  int
	Closed     int
	ContextAt  time.Time // когда собрана справка; ноль — не собрана
	Primer     string
}

func (p uiProjectRow) openCount() int { return p.Tasks + p.Questions + p.Decisions }

// project — то, что уходит в сборку справки и в форму правки.
func (p uiProjectRow) project() core.Project {
	return core.Project{Name: p.Name, About: p.About, Aliases: p.Aliases, Sources: p.Sources,
		People: p.People, Vocabulary: p.Vocabulary}
}

// uiLoadProjects собирает список так же, как это делает `steno projects`:
// объединяет реестр и проекты, по которым что-то накопилось.
func uiLoadProjects(st *core.Store) ([]uiProjectRow, error) {
	registered, err := st.Projects()
	if err != nil {
		return nil, err
	}
	known, err := st.KnownProjects()
	if err != nil {
		return nil, err
	}

	byName := map[string]*uiProjectRow{}
	var order []string
	add := func(name string) *uiProjectRow {
		if r, ok := byName[name]; ok {
			return r
		}
		r := &uiProjectRow{Name: name}
		byName[name] = r
		order = append(order, name)
		return r
	}
	for _, p := range registered {
		r := add(p.Name)
		r.Registered = true
		r.About = p.About
		r.Aliases = p.Aliases
		r.Sources = p.Sources
		r.People = p.People
		r.Vocabulary = p.Vocabulary
	}
	for _, n := range known {
		add(n)
	}

	for _, name := range order {
		r := byName[name]
		items, err := st.ProjectItems(name)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			if it.Status != "open" {
				r.Closed++
				continue
			}
			switch it.Kind {
			case core.KindTask:
				r.Tasks++
			case core.KindQuestion:
				r.Questions++
			case core.KindDecision:
				r.Decisions++
			}
		}
		if c, err := st.ProjectContext(name); err == nil {
			r.ContextAt = c.BuiltAt
			r.Primer = c.Primer
		}
	}

	out := make([]uiProjectRow, 0, len(order))
	for _, name := range order {
		out = append(out, *byName[name])
	}
	sort.SliceStable(out, func(i, j int) bool {
		// «не определён» — свалка, а не проект: ему место в конце списка.
		if a, b := out[i].Name == core.UnassignedProject, out[j].Name == core.UnassignedProject; a != b {
			return b
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// uiLoadItems — все пункты по всем проектам. Собирается из ProjectItems по
// каждому известному проекту: отдельного «дай всё» в хранилище нет, и заводить
// его ради одного экрана — плодить запрос, который разойдётся с остальными.
func uiLoadItems(st *core.Store) ([]core.ProjectItem, error) {
	names, err := st.KnownProjects()
	if err != nil {
		return nil, err
	}
	var out []core.ProjectItem
	for _, n := range names {
		items, err := st.ProjectItems(n)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	uiSortItems(out)
	return out, nil
}

// uiSortItems: сначала открытое, внутри — по сроку. Пункт без срока идёт после
// всех со сроком: «сделать когда-нибудь» не должно стоять выше «до завтра».
func uiSortItems(items []core.ProjectItem) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if (a.Status == "open") != (b.Status == "open") {
			return a.Status == "open"
		}
		if (a.Due == "") != (b.Due == "") {
			return a.Due != ""
		}
		if a.Due != b.Due {
			return a.Due < b.Due
		}
		return a.OpenedAt.Before(b.OpenedAt)
	})
}

// uiItemFilter — состояние фильтров вкладки задач.
type uiItemFilter struct {
	Project string        // "" — все
	Owner   string        // "" — все
	Kind    core.ItemKind // "" — все виды
	Closed  bool          // показывать закрытые
	Text    string        // подстрока по тексту, исполнителю и проекту
}

func (f uiItemFilter) empty() bool {
	return f.Project == "" && f.Owner == "" && f.Kind == "" && !f.Closed && f.Text == ""
}

func uiFilterItems(items []core.ProjectItem, f uiItemFilter) []core.ProjectItem {
	needle := strings.ToLower(strings.TrimSpace(f.Text))
	out := make([]core.ProjectItem, 0, len(items))
	for _, it := range items {
		if !f.Closed && it.Status != "open" {
			continue
		}
		if f.Project != "" && it.Project != f.Project {
			continue
		}
		if f.Owner != "" && it.Owner != f.Owner {
			continue
		}
		if f.Kind != "" && it.Kind != f.Kind {
			continue
		}
		if needle != "" && !strings.Contains(
			strings.ToLower(it.Text+" "+it.Owner+" "+it.Project+" "+it.ID), needle) {
			continue
		}
		out = append(out, it)
	}
	return out
}

// uiOwners — исполнители, встречающиеся в пунктах. Пустой владелец в список не
// попадает: фильтровать «по никому» смысла нет.
func uiOwners(items []core.ProjectItem) []string {
	seen := map[string]bool{}
	var out []string
	for _, it := range items {
		o := strings.TrimSpace(it.Owner)
		if o == "" || seen[o] {
			continue
		}
		seen[o] = true
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return core.LessOwner(out[i], out[j]) })
	return out
}

func uiFilterMeetings(rows []core.MeetingRow, text string) []core.MeetingRow {
	needle := strings.ToLower(strings.TrimSpace(text))
	if needle == "" {
		return rows
	}
	out := make([]core.MeetingRow, 0, len(rows))
	for _, r := range rows {
		hay := strings.ToLower(r.Title + " " + r.ID + " " + r.Status + " " +
			strings.Join(r.Participants, " "))
		if strings.Contains(hay, needle) {
			out = append(out, r)
		}
	}
	return out
}

func uiFilterProjects(rows []uiProjectRow, text string) []uiProjectRow {
	needle := strings.ToLower(strings.TrimSpace(text))
	if needle == "" {
		return rows
	}
	out := make([]uiProjectRow, 0, len(rows))
	for _, r := range rows {
		// Словарь тоже ищется: «где у нас Сапар?» — это вопрос, на который
		// список проектов должен отвечать, а не заставлять открывать каждый.
		hay := strings.ToLower(r.Name + " " + r.About + " " +
			strings.Join(r.Aliases, " ") + " " + strings.Join(r.Vocabulary, " "))
		if strings.Contains(hay, needle) {
			out = append(out, r)
		}
	}
	return out
}

// uiRenameProject переносит проект целиком: и запись в реестре, и накопленное
// состояние. Панель на переименовании оставляет задачи под старым именем —
// проект «Платежи» после правки названия выглядит пустым, а его задачи висят
// под именем, которого больше нет ни в одном списке. Переименование — не
// удаление, и терять на нём историю нельзя.
func uiRenameProject(st *core.Store, old string, p core.Project) error {
	old = strings.TrimSpace(old)
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return errors.New(i18n.Tr("у проекта должно быть название"))
	}
	if old == "" || old == name {
		return st.SaveProject(p)
	}
	if _, err := st.Project(name); err == nil {
		return fmt.Errorf(i18n.Tr("проект «%s» уже есть — выбери другое название"), name)
	}

	tx, err := st.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Таблицы с проектом в первичном ключе: если под новым именем что-то уже
	// лежит (например, справка от одноимённого проекта из прошлого), UPDATE
	// упал бы на конфликте ключа. Старое здесь главнее — его и переносим.
	for _, table := range []string{"project_context", "project_docs"} {
		if _, err := tx.Exec(`DELETE FROM `+table+` WHERE project=?`, name); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM repo_state WHERE project=?`, name); err != nil {
		return err
	}
	for _, table := range []string{"project_items", "project_context", "project_docs", "repo_state"} {
		if _, err := tx.Exec(`UPDATE `+table+` SET project=? WHERE project=?`, name, old); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM projects WHERE name=?`, old); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return st.SaveProject(p)
}

// uiSourceKinds — виды источников в том же порядке, в каком их предлагает
// панель. Список закрытый: непонятный вид GatherSources молча пропустит.
var uiSourceKinds = []string{"repo", "path", "url", "text"}

func uiSourceKindTitle(kind string) string {
	switch kind {
	case "repo":
		return i18n.Tr("репозиторий")
	case "path":
		return i18n.Tr("каталог")
	case "url":
		return i18n.Tr("ссылка")
	case "text":
		return i18n.Tr("текст")
	}
	return kind
}

// uiContextDone — ответ фоновой сборки справки о проекте.
type uiContextDone struct {
	Project string
	Spend   core.Spend
	Err     error
}

// uiBuildContext собирает справку тем же способом, что и панель: сходить в
// репозиторий и на сайт, попросить Claude выжимку, положить в базу.
func uiBuildContext(ctx context.Context, cfg *core.Config, st *core.Store, p core.Project) uiContextDone {
	material, fp, err := brain.GatherSources(ctx, cfg.DataDir, p)
	if err != nil {
		return uiContextDone{Project: p.Name, Err: err}
	}
	primer, spend, err := brain.BuildPrimer(ctx, cfg, p, material)
	if err != nil {
		return uiContextDone{Project: p.Name, Err: err}
	}
	err = st.SaveProjectContext(core.ProjectContext{
		Project: p.Name, Primer: primer, Fingerprint: fp, Sources: core.SourcesSummary(p),
	})
	return uiContextDone{Project: p.Name, Spend: spend, Err: err}
}
