package core

import (
	"fmt"
	"time"

	"github.com/sur1cat/steno/internal/i18n"
)

// Расписание созвонов.
//
// Календарный источник смотрит на две-три минуты вперёд и ничего не хранит:
// ему достаточно понять, что созвон вот-вот начнётся. Человеку этого мало — он
// хочет заранее видеть, куда бот пойдёт сегодня, а куда нет и почему, и уметь
// передумать до начала встречи, а не выгонять бота из звонка руками.
//
// Поэтому расписание собирается отдельно и на несколько дней вперёд, а решение
// «идти или нет» считается теми же функциями, что и в календарном источнике, —
// иначе предсказание в панели и поведение бота разъехались бы.

type ScheduleEntry struct {
	Key        string // ссылка + время начала: тот же ключ, что у дедупликации
	CalendarID string
	Title      string
	MeetURL    string
	StartsAt   time.Time
	EndsAt     time.Time
	Attendees  []string
	// Пойдёт ли бот и почему нет. Пустая причина — пойдёт.
	Skip     string
	Override string // "" | "skip" | "attend" — что человек решил вручную
	Recorded string // id созвона, если уже записан
}

// WillAttend — итоговое решение с учётом ручной правки.
func (e ScheduleEntry) WillAttend() bool {
	switch e.Override {
	case "skip":
		return false
	case "attend":
		return true
	}
	return e.Skip == ""
}

const scheduleSchema = `
CREATE TABLE IF NOT EXISTS schedule (
  key         TEXT PRIMARY KEY,
  calendar_id TEXT NOT NULL DEFAULT '',
  title       TEXT NOT NULL DEFAULT '',
  meet_url    TEXT NOT NULL DEFAULT '',
  starts_at   INTEGER NOT NULL,
  ends_at     INTEGER NOT NULL DEFAULT 0,
  attendees   TEXT NOT NULL DEFAULT '[]',
  skip        TEXT NOT NULL DEFAULT '',
  seen_at     INTEGER NOT NULL
);

-- Ручное решение живёт отдельно от того, что видно в календаре: опрос
-- перезаписывает строку расписания целиком, а «не ходить сюда» должно это
-- пережить.
-- О каком созвоне уже напомнили. Отдельной таблицей, а не колонкой в
-- расписании: опрос календаря перезаписывает строку целиком, и отметка о
-- напоминании этого не пережила бы — человек получал бы одно и то же каждую
-- минуту.
CREATE TABLE IF NOT EXISTS schedule_reminded (
  key         TEXT PRIMARY KEY,
  reminded_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS schedule_override (
  key        TEXT PRIMARY KEY,
  decision   TEXT NOT NULL,
  created_at INTEGER NOT NULL
);`

func (s *Store) SaveScheduled(e ScheduleEntry) error {
	att, _ := jsonMarshal(e.Attendees)
	_, err := s.DB.Exec(`INSERT INTO schedule
		(key,calendar_id,title,meet_url,starts_at,ends_at,attendees,skip,seen_at)
		VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT(key) DO UPDATE SET calendar_id=excluded.calendar_id,
		title=excluded.title, meet_url=excluded.meet_url, starts_at=excluded.starts_at,
		ends_at=excluded.ends_at, attendees=excluded.attendees, skip=excluded.skip,
		seen_at=excluded.seen_at`,
		e.Key, e.CalendarID, e.Title, e.MeetURL, e.StartsAt.Unix(), e.EndsAt.Unix(),
		att, e.Skip, time.Now().Unix())
	return err
}

func (s *Store) Schedule(from, to time.Time) ([]ScheduleEntry, error) {
	rows, err := s.DB.Query(`
		SELECT s.key, s.calendar_id, s.title, s.meet_url, s.starts_at, s.ends_at,
		       s.attendees, s.skip,
		       COALESCE(o.decision,''), COALESCE(e.meeting_id,'')
		FROM schedule s
		LEFT JOIN schedule_override o ON o.key = s.key
		LEFT JOIN seen_events e ON e.key = s.key
		WHERE s.starts_at >= ? AND s.starts_at < ?
		ORDER BY s.starts_at`, from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScheduleEntry
	for rows.Next() {
		var e ScheduleEntry
		var start, end int64
		var att string
		if err := rows.Scan(&e.Key, &e.CalendarID, &e.Title, &e.MeetURL,
			&start, &end, &att, &e.Skip, &e.Override, &e.Recorded); err != nil {
			return nil, err
		}
		e.StartsAt = time.Unix(start, 0)
		if end > 0 {
			e.EndsAt = time.Unix(end, 0)
		}
		_ = jsonUnmarshal(att, &e.Attendees)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) SetScheduleOverride(key, decision string) error {
	if decision == "" {
		_, err := s.DB.Exec(`DELETE FROM schedule_override WHERE key=?`, key)
		return err
	}
	if decision != "skip" && decision != "attend" {
		return fmt.Errorf(i18n.Tr("непонятное решение %q"), decision)
	}
	_, err := s.DB.Exec(`INSERT INTO schedule_override (key,decision,created_at) VALUES (?,?,?)
		ON CONFLICT(key) DO UPDATE SET decision=excluded.decision, created_at=excluded.created_at`,
		key, decision, time.Now().Unix())
	return err
}

func (s *Store) ScheduleOverride(key string) string {
	var d string
	_ = s.DB.QueryRow(`SELECT decision FROM schedule_override WHERE key=?`, key).Scan(&d)
	return d
}

// PruneSchedule убирает прошедшее: расписание нужно на несколько дней вперёд,
// а не как второй архив созвонов.
func (s *Store) PruneSchedule(before time.Time) error {
	_, err := s.DB.Exec(`DELETE FROM schedule WHERE starts_at < ?`, before.Unix())
	return err
}

func (s *Store) MarkReminded(key string) (bool, error) {
	res, err := s.DB.Exec(`INSERT OR IGNORE INTO schedule_reminded (key,reminded_at) VALUES (?,?)`,
		key, time.Now().Unix())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// PruneReminded убирает отметки о прошедшем: таблица не должна расти вечно.
func (s *Store) PruneReminded(before time.Time) error {
	_, err := s.DB.Exec(`DELETE FROM schedule_reminded WHERE reminded_at < ?`, before.Unix())
	return err
}
