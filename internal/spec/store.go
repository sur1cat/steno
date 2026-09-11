package spec

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
)

// Хранение ТЗ.
//
// Таблица заводится отсюда, а не из общей схемы в core: пакет должен уметь
// подняться на базе, которая про него ничего не знает, — иначе steno, собранный
// без этой части, оставляет за собой таблицу-сироту, а собранный с ней падает
// на старой базе. CREATE TABLE IF NOT EXISTS при первом обращении решает оба
// случая и стоит один запрос за запуск.
//
// Тело ТЗ лежит одним JSON, а не разложено по колонкам. Разложить стоило бы
// того, если бы по этим полям искали; ищут же по задаче и проекту, а само
// задание всегда читают целиком.

const schema = `
CREATE TABLE IF NOT EXISTS specs (
  id         TEXT PRIMARY KEY,
  item_id    TEXT NOT NULL,
  project    TEXT NOT NULL,
  repo       TEXT NOT NULL DEFAULT '',
  status     TEXT NOT NULL,
  reject     TEXT NOT NULL DEFAULT '',
  payload    TEXT NOT NULL DEFAULT '{}',
  model      TEXT NOT NULL DEFAULT '',
  usd        REAL NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  branch     TEXT NOT NULL DEFAULT '',
  worktree   TEXT NOT NULL DEFAULT '',
  run_log    TEXT NOT NULL DEFAULT '',
  run_error  TEXT NOT NULL DEFAULT '',
  run_by     TEXT NOT NULL DEFAULT '',
  started_at INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS specs_item ON specs(item_id, created_at);
`

type Store struct{ db *sql.DB }

// Open берёт ту же базу, в которой живёт всё остальное: ТЗ без задачи не имеет
// смысла, а задача лежит там.
func Open(st *core.Store) (*Store, error) {
	if st == nil || st.DB == nil {
		return nil, errors.New(i18n.Tr("нет базы"))
	}
	if _, err := st.DB.Exec(schema); err != nil {
		return nil, err
	}
	return &Store{db: st.DB}, nil
}

// newID — читаемый идентификатор рядом с T-3f2a у задач. Буква своя, чтобы в
// разговоре «S-91c4» нельзя было спутать с задачей, из которой оно выросло.
func newID() string {
	var b [2]byte
	_, _ = rand.Read(b[:])
	return "S-" + hex.EncodeToString(b[:])
}

// body — то, что уезжает в payload. Отдельный тип, а не сам Spec: колонки и
// JSON не должны хранить одно и то же дважды, иначе они разъедутся ровно тогда,
// когда кто-то поправит одну из копий.
type body struct {
	Title    string       `json:"title"`
	Summary  []string     `json:"summary"`
	Known    []string     `json:"known"`
	Places   []Place      `json:"places"`
	Steps    []string     `json:"steps"`
	Checks   []string     `json:"checks"`
	Unknowns []Unknown    `json:"unknowns"`
	Guesses  []Assumption `json:"guesses"`
	NotHere  []string     `json:"not_here"`
	Blocked  bool         `json:"blocked"`
	Why      string       `json:"why"`
	// Found у Place по JSON не ходит (тег "-"), а сохранить его надо: проверка
	// путей делается один раз, при сборке, и повторять её при каждом чтении
	// значило бы ходить на диск ради уже известного ответа.
	Found []bool `json:"found"`
}

func (s *Store) Save(sp *Spec) error {
	if sp.ID == "" {
		sp.ID = newID()
	}
	if sp.CreatedAt.IsZero() {
		sp.CreatedAt = time.Now()
	}
	found := make([]bool, len(sp.Places))
	for i, p := range sp.Places {
		found[i] = p.Found
	}
	raw, err := json.Marshal(body{
		Title: sp.Title, Summary: sp.Summary, Known: sp.Known, Places: sp.Places,
		Steps: sp.Steps, Checks: sp.Checks, Unknowns: sp.Unknowns,
		Guesses: sp.Guesses, NotHere: sp.NotHere, Blocked: sp.Blocked,
		Why: sp.Why, Found: found,
	})
	if err != nil {
		return err
	}
	started := int64(0)
	if !sp.StartedAt.IsZero() {
		started = sp.StartedAt.Unix()
	}
	_, err = s.db.Exec(`INSERT INTO specs
		(id,item_id,project,repo,status,reject,payload,model,usd,created_at,
		 branch,worktree,run_log,run_error,run_by,started_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET status=excluded.status, reject=excluded.reject,
		  payload=excluded.payload, model=excluded.model, usd=excluded.usd,
		  branch=excluded.branch, worktree=excluded.worktree, run_log=excluded.run_log,
		  run_error=excluded.run_error, run_by=excluded.run_by, started_at=excluded.started_at`,
		sp.ID, sp.ItemID, sp.Project, sp.Repo, sp.Status, sp.Reject, string(raw),
		sp.Model, sp.USD, sp.CreatedAt.Unix(),
		sp.Branch, sp.Worktree, sp.RunLog, sp.RunError, sp.RunBy, started)
	return err
}

const specColumns = `id,item_id,project,repo,status,reject,payload,model,usd,created_at,
	branch,worktree,run_log,run_error,run_by,started_at`

func (s *Store) Get(id string) (*Spec, error) {
	row := s.db.QueryRow(`SELECT `+specColumns+` FROM specs WHERE id=?`, id)
	return scanSpec(row.Scan)
}

// ForItem — все ТЗ по одной задаче, свежие сверху. Их бывает несколько: первое
// собрали, прочитали, задали вопросы, собрали второе. Перезаписывать прошлое
// нельзя — по нему уже могли запустить агента, и ветка ссылается именно на него.
func (s *Store) ForItem(itemID string) ([]*Spec, error) {
	rows, err := s.db.Query(`SELECT `+specColumns+` FROM specs WHERE item_id=? ORDER BY created_at DESC`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Spec
	for rows.Next() {
		sp, err := scanSpec(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

// Latest — последнее ТЗ по каждой задаче. Панели нужно именно это: список
// задач, у каждой отметка, есть ли задание и что с ним.
func (s *Store) Latest(project string) (map[string]*Spec, error) {
	q := `SELECT ` + specColumns + ` FROM specs`
	args := []any{}
	if project != "" {
		q += ` WHERE project=?`
		args = append(args, project)
	}
	q += ` ORDER BY created_at`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*Spec{}
	for rows.Next() {
		sp, err := scanSpec(rows.Scan)
		if err != nil {
			return nil, err
		}
		out[sp.ItemID] = sp // порядок по возрастанию — последнее затирает прежние
	}
	return out, rows.Err()
}

func scanSpec(scan func(...any) error) (*Spec, error) {
	var sp Spec
	var payload string
	var created, started int64
	if err := scan(&sp.ID, &sp.ItemID, &sp.Project, &sp.Repo, &sp.Status, &sp.Reject,
		&payload, &sp.Model, &sp.USD, &created,
		&sp.Branch, &sp.Worktree, &sp.RunLog, &sp.RunError, &sp.RunBy, &started); err != nil {
		return nil, err
	}
	var b body
	if err := json.Unmarshal([]byte(payload), &b); err != nil {
		return nil, err
	}
	sp.Title, sp.Summary, sp.Known = b.Title, b.Summary, b.Known
	sp.Places, sp.Steps, sp.Checks = b.Places, b.Steps, b.Checks
	sp.Unknowns, sp.Guesses, sp.NotHere = b.Unknowns, b.Guesses, b.NotHere
	sp.Blocked, sp.Why = b.Blocked, b.Why
	for i := range sp.Places {
		if i < len(b.Found) {
			sp.Places[i].Found = b.Found[i]
		}
	}
	sp.CreatedAt = time.Unix(created, 0)
	if started > 0 {
		sp.StartedAt = time.Unix(started, 0)
	}
	return &sp, nil
}

// FindItem достаёт задачу по её идентификатору.
//
// В core нет чтения одного пункта по id — там есть список открытых, и этого
// хватало всем, кто читал. Заводить ради ТЗ метод в чужом пакете незачем:
// открытых пунктов у команды десятки, а не миллионы, и перебрать их дешевле,
// чем развести два места, которые собирают ProjectItem из строки.
func FindItem(st *core.Store, id string) (core.ProjectItem, error) {
	items, err := st.OpenItems("")
	if err != nil {
		return core.ProjectItem{}, err
	}
	for _, it := range items {
		if it.ID == id {
			return it, nil
		}
	}
	return core.ProjectItem{}, fmt.Errorf(i18n.Tr("нет открытой задачи %s"), id)
}

// SortItems — задачи в том порядке, в каком их показывают: сначала со сроком,
// потом остальные, внутри — по времени появления.
func SortItems(items []core.ProjectItem) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i].Due != "", items[j].Due != ""
		if a != b {
			return a
		}
		return items[i].OpenedAt.Before(items[j].OpenedAt)
	})
}
