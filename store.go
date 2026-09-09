package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db  *sql.DB
	dir string
}

const schema = `
CREATE TABLE IF NOT EXISTS meetings (
  id            TEXT PRIMARY KEY,
  title         TEXT NOT NULL DEFAULT '',
  meet_url      TEXT NOT NULL,
  calendar_id   TEXT NOT NULL DEFAULT '',
  started_at    INTEGER NOT NULL,
  ended_at      INTEGER,
  audio_path    TEXT NOT NULL DEFAULT '',
  captions_path TEXT NOT NULL DEFAULT '',
  invitees      TEXT NOT NULL DEFAULT '[]',
  participants  TEXT NOT NULL DEFAULT '[]',
  status        TEXT NOT NULL DEFAULT 'recording',
  error         TEXT NOT NULL DEFAULT '',
  reply_to      TEXT NOT NULL DEFAULT '',
  left_reason   TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS segments (
  meeting_id TEXT NOT NULL,
  idx        INTEGER NOT NULL,
  start_s    REAL NOT NULL,
  end_s      REAL NOT NULL,
  speaker    TEXT NOT NULL DEFAULT '',
  text       TEXT NOT NULL,
  PRIMARY KEY (meeting_id, idx)
);

CREATE TABLE IF NOT EXISTS followups (
  meeting_id TEXT PRIMARY KEY,
  created_at INTEGER NOT NULL,
  model      TEXT NOT NULL DEFAULT '',
  payload    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS publications (
  meeting_id TEXT NOT NULL,
  target     TEXT NOT NULL,
  url        TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  error      TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (meeting_id, target)
);

-- Живое состояние проекта: задачи, решения и открытые вопросы, накопленные
-- за все созвоны. Именно это, а не отдельный follow-up, отвечает на вопрос
-- «что у нас сейчас по проекту» через полгода.
CREATE TABLE IF NOT EXISTS project_items (
  id          TEXT PRIMARY KEY,
  project     TEXT NOT NULL,
  kind        TEXT NOT NULL,              -- task | decision | question
  text        TEXT NOT NULL,
  owner       TEXT NOT NULL DEFAULT '',
  due         TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL DEFAULT 'open', -- open | done | dropped
  quote       TEXT NOT NULL DEFAULT '',
  opened_at   INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL,
  -- Созвон, где вопрос возник, и созвон, где закрылся.
  opened_in   TEXT NOT NULL DEFAULT '',
  closed_in   TEXT NOT NULL DEFAULT '',
  note        TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS project_items_state ON project_items(project, kind, status);

-- Докуда репозиторий проекта был прочитан в прошлый раз. Без этого каждая
-- синхронизация заново разбирала бы всю историю, а новые коммиты — те самые,
-- по которым видно, что задача закрыта, — было бы не отличить от старых.
CREATE TABLE IF NOT EXISTS repo_state (
  project   TEXT NOT NULL,
  source    TEXT NOT NULL,
  head      TEXT NOT NULL DEFAULT '',
  synced_at INTEGER NOT NULL,
  PRIMARY KEY (project, source)
);

-- Проекты живут в базе, а не в конфиге: их заводит и правит человек в панели,
-- а не разработчик в JSON с перезапуском сервиса. Из конфига они переезжают
-- один раз при первом запуске.
CREATE TABLE IF NOT EXISTS projects (
  name       TEXT PRIMARY KEY,
  aliases    TEXT NOT NULL DEFAULT '[]',
  about      TEXT NOT NULL DEFAULT '',
  sources    TEXT NOT NULL DEFAULT '[]',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

-- Справка о проекте: выжимка из репозитория, сайта и описания. Собирается
-- редко, а читается на каждом созвоне, поэтому лежит готовой.
CREATE TABLE IF NOT EXISTS project_context (
  project    TEXT PRIMARY KEY,
  primer     TEXT NOT NULL,
  sources    TEXT NOT NULL DEFAULT '',
  built_at   INTEGER NOT NULL,
  fingerprint TEXT NOT NULL DEFAULT ''
);

-- Документ проекта живёт по одной ссылке и обновляется на месте. Заводить
-- новый документ на каждый созвон — значит через месяц иметь тридцать
-- документов и ни одного актуального.
CREATE TABLE IF NOT EXISTS project_docs (
  project    TEXT PRIMARY KEY,
  doc_id     TEXT NOT NULL,
  url        TEXT NOT NULL DEFAULT '',
  updated_at INTEGER NOT NULL
);

-- Одна встреча лежит в календарях всех участников. Ключ по ссылке и времени
-- начала не даёт завести на неё нескольких ботов.
CREATE TABLE IF NOT EXISTS seen_events (
  key        TEXT PRIMARY KEY,
  meeting_id TEXT NOT NULL,
  created_at INTEGER NOT NULL
);

-- Задачи вынесены из JSON follow-up в таблицу: страница «кто что должен»
-- иначе означала бы разбор всех follow-up на каждый показ.
CREATE TABLE IF NOT EXISTS tasks (
  meeting_id TEXT NOT NULL,
  idx        INTEGER NOT NULL,
  owner      TEXT NOT NULL,
  what       TEXT NOT NULL,
  due        TEXT NOT NULL DEFAULT '',
  quote      TEXT NOT NULL DEFAULT '',
  at         REAL NOT NULL DEFAULT 0,
  PRIMARY KEY (meeting_id, idx)
);

CREATE INDEX IF NOT EXISTS tasks_owner ON tasks(owner);
CREATE INDEX IF NOT EXISTS segments_text ON segments(meeting_id, start_s);
`

func openStore(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "recordings"), 0o755); err != nil {
		return nil, err
	}
	// WAL пускает читателей параллельно с писателем, busy_timeout заставляет
	// ждать блокировку вместо мгновенного SQLITE_BUSY. Один коннект — потому
	// что писать в базу одновременно могут четыре записи и четыре источника,
	// а database/sql иначе раздаёт неограниченный пул.
	dsn := filepath.Join(dir, "steno.db") +
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("схема: %w", err)
	}
	if _, err := db.Exec(searchSchema); err != nil {
		return nil, fmt.Errorf("индекс поиска: %w", err)
	}
	if err := migrate(db); err != nil {
		return nil, err
	}
	return &Store{db: db, dir: dir}, nil
}

// migrate догоняет базы, созданные предыдущими версиями. CREATE TABLE IF NOT
// EXISTS новых колонок не добавляет, поэтому смотрим, чего не хватает.
func migrate(db *sql.DB) error {
	want := map[string]map[string]string{
		"meetings": {
			"reply_to":    "TEXT NOT NULL DEFAULT ''",
			"left_reason": "TEXT NOT NULL DEFAULT ''",
		},
		"followups": {
			"input_tokens":  "INTEGER NOT NULL DEFAULT 0",
			"output_tokens": "INTEGER NOT NULL DEFAULT 0",
			"cache_read":    "INTEGER NOT NULL DEFAULT 0",
			"cache_write":   "INTEGER NOT NULL DEFAULT 0",
			"cost_usd":      "REAL NOT NULL DEFAULT 0",
		},
	}
	for table, cols := range want {
		rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
		if err != nil {
			return err
		}
		missing := map[string]string{}
		for k, v := range cols {
			missing[k] = v
		}
		for rows.Next() {
			var cid int
			var name, typ string
			var notnull int
			var dflt any
			var pk int
			if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
				rows.Close()
				return err
			}
			delete(missing, name)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for name, decl := range missing {
			if _, err := db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + name + ` ` + decl); err != nil {
				return fmt.Errorf("миграция %s.%s: %w", table, name, err)
			}
		}
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

// RecordingDir — папка под записи одного созвона.
func (s *Store) RecordingDir(id string) string {
	return filepath.Join(s.dir, "recordings", id)
}

type Meeting struct {
	ID           string
	Title        string
	MeetURL      string
	CalendarID   string
	StartedAt    time.Time
	EndedAt      *time.Time
	AudioPath    string
	CaptionsPath string
	Invitees     []string
	Participants []string
	Status       string // recording | recorded | transcribed | summarized | published | failed
	Error        string
	// Почему бот вышел из звонка. Нужно, чтобы обрезанную запись было видно:
	// «остался один» — норма, «страница закрылась» — повод посмотреть.
	LeftReason string
	// Откуда пришла просьба записать созвон. Тот, кто попросил, получает
	// follow-up там же, где спросил, — независимо от того, куда сервис
	// публикует по умолчанию.
	ReplyTo ReplyTo
}

// ReplyTo — обратный адрес: канал и адрес внутри него.
type ReplyTo struct {
	Kind string `json:"kind"` // telegram | slack
	Addr string `json:"addr"`
}

func (r ReplyTo) empty() bool { return r.Kind == "" || r.Addr == "" }

func (s *Store) CreateMeeting(m *Meeting) error {
	inv, _ := json.Marshal(m.Invitees)
	par, _ := json.Marshal(m.Participants)
	reply := ""
	if !m.ReplyTo.empty() {
		b, _ := json.Marshal(m.ReplyTo)
		reply = string(b)
	}
	_, err := s.db.Exec(`INSERT INTO meetings
		(id,title,meet_url,calendar_id,started_at,audio_path,captions_path,invitees,participants,status,reply_to)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.Title, m.MeetURL, m.CalendarID, m.StartedAt.Unix(),
		m.AudioPath, m.CaptionsPath, string(inv), string(par), m.Status, reply)
	return err
}

// FinishMeeting уточняет и время начала: строка заводится в момент, когда про
// созвон узнали, а запись стартует после ожидания впуска — иногда через
// несколько минут. Без поправки длительность в панели и в документе завышена
// на всё это ожидание, хотя таймкоды расшифровки считаются от начала файла.
func (s *Store) FinishMeeting(id string, startedAt, endedAt time.Time, participants []string, status, errMsg, leftReason string) error {
	par, _ := json.Marshal(participants)
	if startedAt.IsZero() {
		_, err := s.db.Exec(`UPDATE meetings SET ended_at=?, participants=?, status=?, error=?, left_reason=?
			WHERE id=?`, endedAt.Unix(), string(par), status, errMsg, leftReason, id)
		return err
	}
	_, err := s.db.Exec(`UPDATE meetings SET started_at=?, ended_at=?, participants=?, status=?,
		error=?, left_reason=? WHERE id=?`,
		startedAt.Unix(), endedAt.Unix(), string(par), status, errMsg, leftReason, id)
	return err
}

func (s *Store) SetStatus(id, status, errMsg string) error {
	_, err := s.db.Exec(`UPDATE meetings SET status=?, error=? WHERE id=?`, status, errMsg, id)
	return err
}

func (s *Store) Meeting(id string) (*Meeting, error) {
	row := s.db.QueryRow(`SELECT id,title,meet_url,calendar_id,started_at,ended_at,
		audio_path,captions_path,invitees,participants,status,error,reply_to,left_reason
		FROM meetings WHERE id=?`, id)
	var m Meeting
	var started int64
	var ended sql.NullInt64
	var inv, par, reply string
	err := row.Scan(&m.ID, &m.Title, &m.MeetURL, &m.CalendarID, &started, &ended,
		&m.AudioPath, &m.CaptionsPath, &inv, &par, &m.Status, &m.Error, &reply, &m.LeftReason)
	if err != nil {
		return nil, err
	}
	m.StartedAt = time.Unix(started, 0)
	if ended.Valid {
		t := time.Unix(ended.Int64, 0)
		m.EndedAt = &t
	}
	_ = json.Unmarshal([]byte(inv), &m.Invitees)
	_ = json.Unmarshal([]byte(par), &m.Participants)
	if reply != "" {
		_ = json.Unmarshal([]byte(reply), &m.ReplyTo)
	}
	return &m, nil
}

// Segment — одна реплика: текст от whisper, имя от субтитров Meet.
type Segment struct {
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Speaker string  `json:"speaker"`
	Text    string  `json:"text"`
}

func (s *Store) SaveSegments(meetingID string, segs []Segment) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM segments WHERE meeting_id=?`, meetingID); err != nil {
		return err
	}
	st, err := tx.Prepare(`INSERT INTO segments (meeting_id,idx,start_s,end_s,speaker,text) VALUES (?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer st.Close()
	for i, sg := range segs {
		if _, err := st.Exec(meetingID, i, sg.Start, sg.End, sg.Speaker, sg.Text); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.reindexSegments(meetingID, segs)
}

func (s *Store) Segments(meetingID string) ([]Segment, error) {
	rows, err := s.db.Query(`SELECT start_s,end_s,speaker,text FROM segments WHERE meeting_id=? ORDER BY idx`, meetingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Segment
	for rows.Next() {
		var sg Segment
		if err := rows.Scan(&sg.Start, &sg.End, &sg.Speaker, &sg.Text); err != nil {
			return nil, err
		}
		out = append(out, sg)
	}
	return out, rows.Err()
}

func (s *Store) SaveFollowup(meetingID, model string, f *Followup) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if _, err = s.db.Exec(`INSERT INTO followups (meeting_id,created_at,model,payload) VALUES (?,?,?,?)
		ON CONFLICT(meeting_id) DO UPDATE SET created_at=excluded.created_at,
		model=excluded.model, payload=excluded.payload`,
		meetingID, time.Now().Unix(), model, string(b)); err != nil {
		return err
	}
	if err := s.saveTasks(meetingID, f.ActionItems); err != nil {
		return err
	}
	return s.reindexFollowup(meetingID, f)
}

func (s *Store) saveTasks(meetingID string, items []ActionItem) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM tasks WHERE meeting_id=?`, meetingID); err != nil {
		return err
	}
	st, err := tx.Prepare(`INSERT INTO tasks (meeting_id,idx,owner,what,due,quote,at)
		VALUES (?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer st.Close()
	for i, a := range items {
		if _, err := st.Exec(meetingID, i, a.Owner, a.What, a.Due, a.Quote, a.At); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) SaveSpend(meetingID string, sp Spend) error {
	_, err := s.db.Exec(`UPDATE followups SET input_tokens=?, output_tokens=?,
		cache_read=?, cache_write=?, cost_usd=? WHERE meeting_id=?`,
		sp.Input, sp.Output, sp.CacheRead, sp.CacheWrite, sp.USD, meetingID)
	return err
}

func (s *Store) Spend(meetingID string) (Spend, error) {
	var sp Spend
	err := s.db.QueryRow(`SELECT model, input_tokens, output_tokens, cache_read, cache_write, cost_usd
		FROM followups WHERE meeting_id=?`, meetingID).
		Scan(&sp.Model, &sp.Input, &sp.Output, &sp.CacheRead, &sp.CacheWrite, &sp.USD)
	sp.PriceKnown = sp.USD > 0
	return sp, err
}

// TotalSpend — сколько всего потрачено на follow-up за период. Ради этого
// числа учёт и заводился: оценки «пара центов» расходятся с правдой в разы.
func (s *Store) TotalSpend(since time.Time) (float64, int64, int64, int, error) {
	var usd float64
	var in, out int64
	var n int
	err := s.db.QueryRow(`SELECT COALESCE(SUM(f.cost_usd),0), COALESCE(SUM(f.input_tokens),0),
		COALESCE(SUM(f.output_tokens),0), COUNT(*)
		FROM followups f JOIN meetings m ON m.id = f.meeting_id
		WHERE m.started_at >= ?`, since.Unix()).Scan(&usd, &in, &out, &n)
	return usd, in, out, n, err
}

func (s *Store) Followup(meetingID string) (*Followup, error) {
	var payload string
	err := s.db.QueryRow(`SELECT payload FROM followups WHERE meeting_id=?`, meetingID).Scan(&payload)
	if err != nil {
		return nil, err
	}
	var f Followup
	if err := json.Unmarshal([]byte(payload), &f); err != nil {
		return nil, err
	}
	return &f, nil
}

func (s *Store) SavePublication(meetingID, target, url, errMsg string) error {
	_, err := s.db.Exec(`INSERT INTO publications (meeting_id,target,url,created_at,error) VALUES (?,?,?,?,?)
		ON CONFLICT(meeting_id,target) DO UPDATE SET url=excluded.url,
		created_at=excluded.created_at, error=excluded.error`,
		meetingID, target, url, time.Now().Unix(), errMsg)
	return err
}

func (s *Store) Publications(meetingID string) (map[string]string, error) {
	rows, err := s.db.Query(`SELECT target,url FROM publications WHERE meeting_id=? AND error=''`, meetingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var t, u string
		if err := rows.Scan(&t, &u); err != nil {
			return nil, err
		}
		out[t] = u
	}
	return out, rows.Err()
}

func (s *Store) EventSeen(key string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM seen_events WHERE key=?`, key).Scan(&n)
	return n > 0, err
}

// UnmarkEvent снимает отметку. Нужен, когда созвон не поехал в работу по
// причине, которая пройдёт: например, не было свободного слота. Иначе встреча
// оказалась бы забанена навсегда.
func (s *Store) UnmarkEvent(key string) error {
	_, err := s.db.Exec(`DELETE FROM seen_events WHERE key=?`, key)
	return err
}

// MarkEventSeen возвращает, удалось ли занять ключ. Без этого проверка через
// EventSeen и последующая вставка — это два шага, между которыми пролезает
// второй источник: оба читают «не видели», оба заводят бота.
func (s *Store) MarkEventSeen(key, meetingID string) (bool, error) {
	res, err := s.db.Exec(`INSERT OR IGNORE INTO seen_events (key,meeting_id,created_at) VALUES (?,?,?)`,
		key, meetingID, time.Now().Unix())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// --- то, что нужно панели ---------------------------------------------------

// MeetingRow — строка списка созвонов. Считать задачи отдельным запросом на
// каждую строку — это N+1, поэтому счётчик приезжает вместе со строкой.
type MeetingRow struct {
	ID           string
	Title        string
	StartedAt    time.Time
	Duration     time.Duration
	Participants []string
	Status       string
	LeftReason   string
	Tasks        int
	HasAudio     bool
}

func (s *Store) ListMeetings(limit, offset int) ([]MeetingRow, error) {
	rows, err := s.db.Query(`
		SELECT m.id, m.title, m.started_at, COALESCE(m.ended_at,0), m.participants,
		       m.status, m.left_reason, m.audio_path,
		       (SELECT COUNT(*) FROM tasks t WHERE t.meeting_id = m.id)
		FROM meetings m
		ORDER BY m.started_at DESC
		LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MeetingRow
	for rows.Next() {
		var r MeetingRow
		var started, ended int64
		var par, audio string
		if err := rows.Scan(&r.ID, &r.Title, &started, &ended, &par,
			&r.Status, &r.LeftReason, &audio, &r.Tasks); err != nil {
			return nil, err
		}
		r.StartedAt = time.Unix(started, 0)
		if ended > started {
			r.Duration = time.Duration(ended-started) * time.Second
		}
		_ = json.Unmarshal([]byte(par), &r.Participants)
		r.HasAudio = audio != ""
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) CountMeetings() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM meetings`).Scan(&n)
	return n, err
}

// TaskRow — задача вместе с созвоном, на котором она возникла: без этого
// контекста строчка «доделать миграцию» ничего не значит.
type TaskRow struct {
	ActionItem
	MeetingID    string
	MeetingTitle string
	MeetingAt    time.Time
}

func (s *Store) Tasks(owner string) ([]TaskRow, error) {
	q := `SELECT t.owner, t.what, t.due, t.quote, t.at, t.meeting_id,
	             COALESCE(m.title,''), COALESCE(m.started_at,0)
	      FROM tasks t LEFT JOIN meetings m ON m.id = t.meeting_id`
	args := []any{}
	if owner != "" {
		q += ` WHERE t.owner = ?`
		args = append(args, owner)
	}
	q += ` ORDER BY m.started_at DESC, t.idx LIMIT 2000`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TaskRow
	for rows.Next() {
		var t TaskRow
		var at int64
		if err := rows.Scan(&t.Owner, &t.What, &t.Due, &t.Quote, &t.At,
			&t.MeetingID, &t.MeetingTitle, &at); err != nil {
			return nil, err
		}
		t.MeetingAt = time.Unix(at, 0)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) TaskOwners() ([]string, error) {
	rows, err := s.db.Query(`SELECT owner, COUNT(*) c FROM tasks GROUP BY owner`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var o string
		var c int
		if err := rows.Scan(&o, &c); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return lessOwner(out[i], out[j]) })
	return out, nil
}
