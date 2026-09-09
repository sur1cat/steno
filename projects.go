package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
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

// matchProject приводит то, как проект назвали вслух, к тому, как он записан.
// «биллинг», «платежи» и «payments» — один проект, и решает это не модель, а
// список псевдонимов: так ответ не зависит от того, что она сегодня придумала.
func matchProject(projects []Project, said string) string {
	said = strings.ToLower(strings.TrimSpace(said))
	if said == "" || said == unassignedProject {
		return unassignedProject
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
	return unassignedProject
}

const unassignedProject = "не определён"

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
	rows, err := s.db.Query(q, args...)
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

func (s *Store) AddItem(it ProjectItem) error {
	now := time.Now().Unix()
	_, err := s.db.Exec(`INSERT INTO project_items
		(id,project,kind,text,owner,due,status,quote,opened_at,updated_at,opened_in,closed_in,note)
		VALUES (?,?,?,?,?,?,'open',?,?,?,?,'','')`,
		it.ID, it.Project, string(it.Kind), it.Text, it.Owner, it.Due, it.Quote,
		now, now, it.OpenedIn)
	return err
}

func (s *Store) CloseItem(id, status, note, meetingID string) error {
	if status != "done" && status != "dropped" {
		return fmt.Errorf("непонятный статус %q", status)
	}
	_, err := s.db.Exec(`UPDATE project_items SET status=?, note=?, closed_in=?, updated_at=?
		WHERE id=? AND status='open'`, status, note, meetingID, time.Now().Unix(), id)
	return err
}

// KnownProjects — проекты, по которым что-то накопилось. Нужен панели: там
// показываются те, где есть содержимое, а не весь список из конфига.
func (s *Store) KnownProjects() ([]string, error) {
	rows, err := s.db.Query(`SELECT project, COUNT(*) FROM project_items GROUP BY project`)
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
		if a, b := out[i] == unassignedProject, out[j] == unassignedProject; a != b {
			return b
		}
		return out[i] < out[j]
	})
	return out, nil
}

// applyFollowup переносит разобранный созвон в состояние проектов: закрывает
// то, что закрылось, и заводит то, что появилось.
func applyFollowup(st *Store, projects []Project, meetingID string, f *Followup) (added, closed int, err error) {
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
			ID: newItemID(kind), Project: matchProject(projects, project),
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
	err = s.db.QueryRow(`SELECT doc_id, url FROM project_docs WHERE project=?`,
		project).Scan(&docID, &url)
	return
}

func (s *Store) SaveProjectDoc(project, docID, url string) error {
	_, err := s.db.Exec(`INSERT INTO project_docs (project,doc_id,url,updated_at) VALUES (?,?,?,?)
		ON CONFLICT(project) DO UPDATE SET doc_id=excluded.doc_id, url=excluded.url,
		updated_at=excluded.updated_at`, project, docID, url, time.Now().Unix())
	return err
}

// touchedProjects — проекты, которых коснулся этот созвон. Только их документы
// и надо переписывать.
func touchedProjects(f *Followup, st *Store, meetingID string) []string {
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

// renderOpenItems — то, что уходит в промпт следующего созвона. Без этого
// модель не знает, что уже висит, и заводит копии тех же задач каждый раз.
func renderOpenItems(items []ProjectItem) string {
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
	b.WriteString("Что уже висит открытым по проектам:\n\n")
	names := map[ItemKind]string{KindTask: "задача", KindDecision: "решение", KindQuestion: "вопрос"}
	for _, p := range order {
		fmt.Fprintf(&b, "%s:\n", p)
		for _, it := range byProject[p] {
			fmt.Fprintf(&b, "  [%s] %s: %s", it.ID, names[it.Kind], it.Text)
			if it.Owner != "" {
				fmt.Fprintf(&b, " (на ком: %s", it.Owner)
				if it.Due != "" {
					fmt.Fprintf(&b, ", срок %s", it.Due)
				}
				b.WriteString(")")
			}
			fmt.Fprintf(&b, " — с %s\n", it.OpenedAt.Format("2006-01-02"))
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

func (s *Store) Projects() ([]Project, error) {
	rows, err := s.db.Query(`SELECT name, aliases, about, sources FROM projects ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var p Project
		var aliases, sources string
		if err := rows.Scan(&p.Name, &aliases, &p.About, &sources); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(aliases), &p.Aliases)
		_ = json.Unmarshal([]byte(sources), &p.Sources)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) Project(name string) (Project, error) {
	var p Project
	var aliases, sources string
	err := s.db.QueryRow(`SELECT name, aliases, about, sources FROM projects WHERE name=?`, name).
		Scan(&p.Name, &aliases, &p.About, &sources)
	if err != nil {
		return p, err
	}
	_ = json.Unmarshal([]byte(aliases), &p.Aliases)
	_ = json.Unmarshal([]byte(sources), &p.Sources)
	return p, nil
}

func (s *Store) SaveProject(p Project) error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("у проекта должно быть название")
	}
	aliases, _ := json.Marshal(p.Aliases)
	sources, _ := json.Marshal(p.Sources)
	now := time.Now().Unix()
	_, err := s.db.Exec(`INSERT INTO projects (name,aliases,about,sources,created_at,updated_at)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET aliases=excluded.aliases, about=excluded.about,
		sources=excluded.sources, updated_at=excluded.updated_at`,
		p.Name, string(aliases), p.About, string(sources), now, now)
	return err
}

// DeleteProject убирает описание, но не трогает накопленное состояние: задачи
// и решения — это история, и терять её из-за переименования проекта нельзя.
func (s *Store) DeleteProject(name string) error {
	_, err := s.db.Exec(`DELETE FROM projects WHERE name=?`, name)
	return err
}

// importProjects переносит проекты из конфига в базу при первом запуске.
func importProjects(st *Store, cfg *Config) (int, error) {
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

// activeProjects — то, по чему работает сервис. База главнее конфига: в панели
// проект правят на ходу, и перечитывать файл ради этого не должно быть нужно.
func activeProjects(st *Store, cfg *Config) []Project {
	if ps, err := st.Projects(); err == nil && len(ps) > 0 {
		return ps
	}
	return cfg.Projects
}
