package core

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/sur1cat/steno/internal/i18n"
)

// Живое состояние проекта. На одном созвоне обсуждают три-четыре проекта, и
// если follow-up остаётся плоским списком, через месяц по нему невозможно
// понять, что относилось к чему и что из этого уже закрыто.
//
// Поэтому у каждого пункта есть проект, а сами пункты живут не внутри
// отдельного созвона, а отдельно и переживают его. Следующий созвон получает
// их на вход и говорит, что закрылось, — так документ по проекту обновляется
// по ходу задач, а не переписывается заново каждый раз.

type ItemKind string

const (
	KindTask     ItemKind = "task"
	KindDecision ItemKind = "decision"
	KindQuestion ItemKind = "question"
)

type ProjectItem struct {
	ID        string
	Project   string
	Kind      ItemKind
	Text      string
	Owner     string
	Due       string
	Status    string // open | done | dropped
	Quote     string
	OpenedAt  time.Time
	UpdatedAt time.Time
	OpenedIn  string
	ClosedIn  string
	Note      string
}

// Короткий читаемый идентификатор: он попадает в промпт и в документ, и
// «T-3f2a» человек глазами сверит, а UUID — нет.
func newItemID(kind ItemKind) string {
	var b [2]byte
	_, _ = rand.Read(b[:])
	prefix := map[ItemKind]string{KindTask: "T", KindDecision: "D", KindQuestion: "Q"}[kind]
	return prefix + "-" + hex.EncodeToString(b[:])
}

// MatchProject приводит то, как проект назвали вслух, к тому, как он записан.
// «биллинг», «платежи» и «payments» — один проект, и решает это не модель, а
// список псевдонимов: так ответ не зависит от того, что она сегодня придумала.
func MatchProject(projects []Project, said string) string {
	said = strings.ToLower(strings.TrimSpace(said))
	if said == "" || said == UnassignedProject {
		return UnassignedProject
	}
	for _, p := range projects {
		if strings.EqualFold(p.Name, said) {
			return p.Name
		}
		for _, a := range p.Aliases {
			if strings.EqualFold(a, said) {
				return p.Name
			}
		}
	}
	return UnassignedProject
}

const UnassignedProject = "не определён"

// --- хранилище --------------------------------------------------------------

func (s *Store) OpenItems(project string) ([]ProjectItem, error) {
	q := `SELECT id,project,kind,text,owner,due,status,quote,opened_at,updated_at,opened_in,closed_in,note
	      FROM project_items WHERE status='open'`
	args := []any{}
	if project != "" {
		q += ` AND project=?`
		args = append(args, project)
	}
	q += ` ORDER BY opened_at`
	return s.queryItems(q, args...)
}

func (s *Store) ProjectItems(project string) ([]ProjectItem, error) {
	return s.queryItems(`SELECT id,project,kind,text,owner,due,status,quote,opened_at,updated_at,opened_in,closed_in,note
		FROM project_items WHERE project=? ORDER BY status, opened_at DESC`, project)
}

func (s *Store) queryItems(q string, args ...any) ([]ProjectItem, error) {
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProjectItem
	for rows.Next() {
		var it ProjectItem
		var opened, updated int64
		var kind string
		if err := rows.Scan(&it.ID, &it.Project, &kind, &it.Text, &it.Owner, &it.Due,
			&it.Status, &it.Quote, &opened, &updated, &it.OpenedIn, &it.ClosedIn, &it.Note); err != nil {
			return nil, err
		}
		it.Kind = ItemKind(kind)
		it.OpenedAt = time.Unix(opened, 0)
		it.UpdatedAt = time.Unix(updated, 0)
		out = append(out, it)
	}
	return out, rows.Err()
}

// Item — один пункт по идентификатору. Нужен тому, кто закрывает пункт снаружи
// созвона — из MCP: CloseItem молчит, если такого id нет или он уже закрыт,
// а ответить «закрыл» на опечатку в id — значит соврать.
func (s *Store) Item(id string) (ProjectItem, error) {
	items, err := s.queryItems(`SELECT id,project,kind,text,owner,due,status,quote,opened_at,updated_at,opened_in,closed_in,note
		FROM project_items WHERE id=?`, id)
	if err != nil {
		return ProjectItem{}, err
	}
	if len(items) == 0 {
		return ProjectItem{}, sql.ErrNoRows
	}
	return items[0], nil
}

func (s *Store) AddItem(it ProjectItem) error {
	now := time.Now().Unix()
	_, err := s.DB.Exec(`INSERT INTO project_items
		(id,project,kind,text,owner,due,status,quote,opened_at,updated_at,opened_in,closed_in,note)
		VALUES (?,?,?,?,?,?,'open',?,?,?,?,'','')`,
		it.ID, it.Project, string(it.Kind), it.Text, it.Owner, it.Due, it.Quote,
		now, now, it.OpenedIn)
	return err
}

func (s *Store) CloseItem(id, status, note, meetingID string) error {
	if status != "done" && status != "dropped" {
		return fmt.Errorf(i18n.Tr("непонятный статус %q"), status)
	}
	_, err := s.DB.Exec(`UPDATE project_items SET status=?, note=?, closed_in=?, updated_at=?
		WHERE id=? AND status='open'`, status, note, meetingID, time.Now().Unix(), id)
	return err
}

// ReopenItem возвращает задачу в работу. Нужен, когда коммит закрыл её
// ошибочно: без этого ошибка автоматики была бы необратимой.
func (s *Store) ReopenItem(id string) error {
	_, err := s.DB.Exec(`UPDATE project_items SET status='open', closed_in='', note='',
		updated_at=? WHERE id=?`, time.Now().Unix(), id)
	return err
}

// KnownProjects — проекты, по которым что-то накопилось. Нужен панели: там
// показываются те, где есть содержимое, а не весь список из конфига.
func (s *Store) KnownProjects() ([]string, error) {
	rows, err := s.DB.Query(`SELECT project, COUNT(*) FROM project_items GROUP BY project`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		var n int
		if err := rows.Scan(&p, &n); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := out[i] == UnassignedProject, out[j] == UnassignedProject; a != b {
			return b
		}
		return out[i] < out[j]
	})
	return out, nil
}

// ApplyFollowup переносит разобранный созвон в состояние проектов: закрывает
// то, что закрылось, и заводит то, что появилось.
func ApplyFollowup(st *Store, projects []Project, meetingID string, f *Followup) (added, closed int, err error) {
	for _, u := range f.Updates {
		if u.Status == "done" || u.Status == "dropped" {
			if err := st.CloseItem(u.ID, u.Status, u.Note, meetingID); err != nil {
				return added, closed, err
			}
			closed++
		}
	}
	add := func(kind ItemKind, project, text, owner, due, quote string) error {
		it := ProjectItem{
			ID: newItemID(kind), Project: MatchProject(projects, project),
			Kind: kind, Text: text, Owner: owner, Due: due, Quote: quote,
			OpenedIn: meetingID,
		}
		added++
		return st.AddItem(it)
	}
	for _, a := range f.ActionItems {
		if err := add(KindTask, a.Project, a.What, a.Owner, a.Due, a.Quote); err != nil {
			return added, closed, err
		}
	}
	for _, d := range f.Decisions {
		if err := add(KindDecision, d.Project, d.What, "", "", d.Why); err != nil {
			return added, closed, err
		}
	}
	for _, q := range f.OpenQuestions {
		if err := add(KindQuestion, q.Project, q.Question, q.WaitingOn, "", ""); err != nil {
			return added, closed, err
		}
	}
	return added, closed, nil
}

func (s *Store) ProjectDoc(project string) (docID, url string, err error) {
	err = s.DB.QueryRow(`SELECT doc_id, url FROM project_docs WHERE project=?`,
		project).Scan(&docID, &url)
	return
}

func (s *Store) SaveProjectDoc(project, docID, url string) error {
	_, err := s.DB.Exec(`INSERT INTO project_docs (project,doc_id,url,updated_at) VALUES (?,?,?,?)
		ON CONFLICT(project) DO UPDATE SET doc_id=excluded.doc_id, url=excluded.url,
		updated_at=excluded.updated_at`, project, docID, url, time.Now().Unix())
	return err
}

// TouchedProjects — проекты, которых коснулся этот созвон. Только их документы
// и надо переписывать.
func TouchedProjects(f *Followup, st *Store, meetingID string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, a := range f.ActionItems {
		add(a.Project)
	}
	for _, d := range f.Decisions {
		add(d.Project)
	}
	for _, q := range f.OpenQuestions {
		add(q.Project)
	}
	// Закрытые пункты тоже меняют документ, даже если нового по проекту не было.
	for _, u := range f.Updates {
		if u.Status == "done" || u.Status == "dropped" {
			if items, err := st.queryItems(
				`SELECT id,project,kind,text,owner,due,status,quote,opened_at,updated_at,opened_in,closed_in,note
				 FROM project_items WHERE id=?`, u.ID); err == nil && len(items) == 1 {
				add(items[0].Project)
			}
		}
	}
	sort.Strings(out)
	return out
}

// RenderOpenItems — то, что уходит в промпт следующего созвона. Без этого
// модель не знает, что уже висит, и заводит копии тех же задач каждый раз.
func RenderOpenItems(items []ProjectItem) string {
	if len(items) == 0 {
		return ""
	}
	byProject := map[string][]ProjectItem{}
	var order []string
	for _, it := range items {
		if _, ok := byProject[it.Project]; !ok {
			order = append(order, it.Project)
		}
		byProject[it.Project] = append(byProject[it.Project], it)
	}
	sort.Strings(order)

	var b strings.Builder
	b.WriteString(i18n.Tr("Что уже висит открытым по проектам:\n\n"))
	names := map[ItemKind]string{KindTask: i18n.Tr("задача"), KindDecision: i18n.Tr("решение"), KindQuestion: i18n.Tr("вопрос")}
	for _, p := range order {
		fmt.Fprintf(&b, "%s:\n", p)
		for _, it := range byProject[p] {
			fmt.Fprintf(&b, "  [%s] %s: %s", it.ID, names[it.Kind], it.Text)
			if it.Owner != "" {
				fmt.Fprintf(&b, i18n.Tr(" (на ком: %s"), it.Owner)
				if it.Due != "" {
					fmt.Fprintf(&b, i18n.Tr(", срок %s"), it.Due)
				}
				b.WriteString(")")
			}
			fmt.Fprintf(&b, i18n.Tr(" — с %s\n"), it.OpenedAt.Format("2006-01-02"))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// --- проекты в базе ---------------------------------------------------------
//
// Проекты заводит человек в панели, а не разработчик в конфиге: описать проект,
// приложить сайт и репозиторий — это работа того, кто в проекте разбирается, и
// требовать за неё правку JSON с перезапуском сервиса неправильно.
//
// Из конфига проекты переезжают один раз, при первом запуске: у тех, кто уже
// описал их файлом, ничего не пропадёт.

const projectColumns = `name, aliases, about, sources, people, vocabulary`

func (s *Store) Projects() ([]Project, error) {
	rows, err := s.DB.Query(`SELECT ` + projectColumns + ` FROM projects ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		p, err := scanProject(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) Project(name string) (Project, error) {
	row := s.DB.QueryRow(`SELECT `+projectColumns+` FROM projects WHERE name=?`, name)
	return scanProject(row.Scan)
}

// scanProject — одна строка таблицы проектов. Вынесено, потому что колонок
// стало шесть, а мест, которые их читают, два: разъехавшись, они дали бы
// проект, у которого словарь есть в списке и нет в карточке.
func scanProject(scan func(...any) error) (Project, error) {
	var p Project
	var aliases, sources, people, vocabulary string
	if err := scan(&p.Name, &aliases, &p.About, &sources, &people, &vocabulary); err != nil {
		return p, err
	}
	_ = json.Unmarshal([]byte(aliases), &p.Aliases)
	_ = json.Unmarshal([]byte(sources), &p.Sources)
	_ = json.Unmarshal([]byte(people), &p.People)
	_ = json.Unmarshal([]byte(vocabulary), &p.Vocabulary)
	return p, nil
}

func (s *Store) SaveProject(p Project) error {
	if strings.TrimSpace(p.Name) == "" {
		return errors.New(i18n.Tr("у проекта должно быть название"))
	}
	aliases, _ := json.Marshal(p.Aliases)
	sources, _ := json.Marshal(p.Sources)
	people, _ := json.Marshal(trimWords(p.People))
	// Единственное место, где держится обязательство «имена людей лежат и в
	// общем словаре». Форм правки три — терминал, ui, панель, — и если следить
	// за этим в каждой, то в одной из них рано или поздно забудут, а пропажа
	// имени из словаря whisper тихая: видно её через месяц, в задаче, уехавшей
	// не тому человеку.
	vocabulary, _ := json.Marshal(MergeWords(p.Vocabulary, p.People))
	now := time.Now().Unix()
	_, err := s.DB.Exec(`INSERT INTO projects (name,aliases,about,sources,people,vocabulary,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET aliases=excluded.aliases, about=excluded.about,
		sources=excluded.sources, people=excluded.people, vocabulary=excluded.vocabulary,
		updated_at=excluded.updated_at`,
		p.Name, string(aliases), p.About, string(sources),
		string(people), string(vocabulary), now, now)
	return err
}

// --- словарь проекта --------------------------------------------------------
//
// Два списка вместо одного: люди и всё остальное. Деление ровно одно, и оно не
// про порядок полей в форме, а про то, что модель делает со словом. Имя
// человека может стать владельцем задачи, название сервиса — нет, и ошибка в
// любую сторону стоит одинаково: задачи, которую никто не делает.
//
// Дальше не делим. «Сервисы», «сокращения», «клиенты» — это ещё три поля, из
// которых заполняют ноль, а модели разница между ними ничего не даёт: ей
// достаточно знать, что это слово команды, а не оговорка расшифровки.

// trimWords чистит список, набранный руками: пустые строки и повторы. Регистр
// не различаем — «Сапар» и «сапар» одно слово, а два одинаковых чипа в панели
// выглядят как недосмотр интерфейса.
func trimWords(words []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range words {
		w = strings.TrimSpace(w)
		if w == "" {
			continue
		}
		k := strings.ToLower(w)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, w)
	}
	return out
}

// MergeWords дописывает к списку то, чего в нём ещё нет, сохраняя порядок.
func MergeWords(base, extra []string) []string {
	return trimWords(append(append([]string{}, base...), extra...))
}

// withoutWords — список без указанных слов. Нужен формам: в поле «другие
// слова» человек должен видеть то, что он туда написал, а не свой же список
// людей, приклеенный к нему при сохранении.
func withoutWords(base, drop []string) []string {
	skip := map[string]bool{}
	for _, w := range drop {
		skip[strings.ToLower(strings.TrimSpace(w))] = true
	}
	var out []string
	for _, w := range trimWords(base) {
		if !skip[strings.ToLower(w)] {
			out = append(out, w)
		}
	}
	return out
}

// OtherWords — словарь проекта без имён людей: то, что показывают в формах и
// что уходит в промпт отдельной строкой «сервисы и сокращения».
func (p Project) OtherWords() []string { return withoutWords(p.Vocabulary, p.People) }

// DeleteProject убирает описание, но не трогает накопленное состояние: задачи
// и решения — это история, и терять её из-за переименования проекта нельзя.
func (s *Store) DeleteProject(name string) error {
	_, err := s.DB.Exec(`DELETE FROM projects WHERE name=?`, name)
	return err
}

// ImportProjects переносит проекты из конфига в базу при первом запуске.
func ImportProjects(st *Store, cfg *Config) (int, error) {
	if len(cfg.Projects) == 0 {
		return 0, nil
	}
	existing, err := st.Projects()
	if err != nil {
		return 0, err
	}
	if len(existing) > 0 {
		return 0, nil // база уже главнее конфига
	}
	for _, p := range cfg.Projects {
		if err := st.SaveProject(p); err != nil {
			return 0, err
		}
	}
	return len(cfg.Projects), nil
}

// ActiveProjects — то, по чему работает сервис. База главнее конфига: в панели
// проект правят на ходу, и перечитывать файл ради этого не должно быть нужно.
func ActiveProjects(st *Store, cfg *Config) []Project {
	if ps, err := st.Projects(); err == nil && len(ps) > 0 {
		return ps
	}
	return cfg.Projects
}

func SourcesSummary(p Project) string {
	var out []string
	for _, s := range p.Sources {
		out = append(out, s.Kind+":"+s.Value)
	}
	return strings.Join(out, ", ")
}
