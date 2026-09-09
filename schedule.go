package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"google.golang.org/api/calendar/v3"
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
	_, err := s.db.Exec(`INSERT INTO schedule
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
	rows, err := s.db.Query(`
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
		_, err := s.db.Exec(`DELETE FROM schedule_override WHERE key=?`, key)
		return err
	}
	if decision != "skip" && decision != "attend" {
		return fmt.Errorf("непонятное решение %q", decision)
	}
	_, err := s.db.Exec(`INSERT INTO schedule_override (key,decision,created_at) VALUES (?,?,?)
		ON CONFLICT(key) DO UPDATE SET decision=excluded.decision, created_at=excluded.created_at`,
		key, decision, time.Now().Unix())
	return err
}

func (s *Store) ScheduleOverride(key string) string {
	var d string
	_ = s.db.QueryRow(`SELECT decision FROM schedule_override WHERE key=?`, key).Scan(&d)
	return d
}

// PruneSchedule убирает прошедшее: расписание нужно на несколько дней вперёд,
// а не как второй архив созвонов.
func (s *Store) PruneSchedule(before time.Time) error {
	_, err := s.db.Exec(`DELETE FROM schedule WHERE starts_at < ?`, before.Unix())
	return err
}

// --- опрос ------------------------------------------------------------------

type schedulePoller struct {
	cfg *Config
	st  *Store
	log *log.Logger
	src *calendarSource // ради кеша клиентов Google
}

func (s *schedulePoller) Name() string { return "расписание" }

func (s *schedulePoller) Run(ctx context.Context) error {
	every := 15 * time.Minute
	s.log.Printf("расписание: собираю на %d дней вперёд, опрос раз в %s",
		s.horizonDays(), every)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		s.once(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

func (s *schedulePoller) horizonDays() int {
	if d := s.cfg.Calendar.ScheduleDays; d > 0 {
		return d
	}
	return 7
}

func (s *schedulePoller) once(ctx context.Context) {
	from := time.Now().Add(-2 * time.Hour)
	to := time.Now().AddDate(0, 0, s.horizonDays())

	for _, calID := range s.cfg.Calendar.Calendars {
		events, err := s.src.upcoming(ctx, calID, from, to)
		if err != nil {
			s.log.Printf("расписание: %s: %v", calID, err)
			continue
		}
		for _, ev := range events {
			e, ok := s.entry(calID, ev)
			if !ok {
				continue
			}
			if err := s.st.SaveScheduled(e); err != nil {
				s.log.Printf("расписание: %v", err)
			}
		}
	}
	if err := s.st.PruneSchedule(time.Now().AddDate(0, 0, -2)); err != nil {
		s.log.Printf("расписание: уборка: %v", err)
	}
}

// entry решает, пойдёт ли бот, и главное — объясняет, почему нет. Молчаливое
// «бот не пришёл» разбирать невозможно; названная причина чинится за минуту.
func (s *schedulePoller) entry(calID string, ev *calendar.Event) (ScheduleEntry, bool) {
	if ev.Status == "cancelled" {
		return ScheduleEntry{}, false
	}
	if ev.Start == nil || ev.Start.DateTime == "" {
		return ScheduleEntry{}, false // встреча на весь день — не созвон
	}
	start, err := time.Parse(time.RFC3339, ev.Start.DateTime)
	if err != nil {
		return ScheduleEntry{}, false
	}
	e := ScheduleEntry{
		CalendarID: calID,
		Title:      ev.Summary,
		StartsAt:   start,
		Attendees:  acceptedAttendees(ev),
	}
	if ev.End != nil && ev.End.DateTime != "" {
		if end, err := time.Parse(time.RFC3339, ev.End.DateTime); err == nil {
			e.EndsAt = end
		}
	}

	e.MeetURL = meetLink(ev)
	switch {
	case e.MeetURL == "":
		e.Skip = "нет ссылки на Meet"
	case skipMarked(ev, s.cfg.Calendar.SkipMarkers):
		e.Skip = "стоит метка «не записывать»"
	case len(e.Attendees) < s.cfg.Calendar.MinAttendees:
		e.Skip = fmt.Sprintf("участников %d, нужно хотя бы %d",
			len(e.Attendees), s.cfg.Calendar.MinAttendees)
	}

	// Ключ тот же, что у дедупликации: по нему видно, записан ли уже созвон, и
	// по нему же человек отменяет поход.
	if e.MeetURL != "" {
		e.Key = plannedKey(e.MeetURL, start)
	} else {
		e.Key = "no-meet:" + calID + "@" + start.UTC().Format(time.RFC3339)
	}
	return e, true
}

// --- приглашение из панели --------------------------------------------------

// inviteToCall заводит бота на созвон по ссылке. Тот же путь, что у Telegram и
// HTTP, только повод — кнопка в панели.
func inviteToCall(ctx context.Context, d *Dispatcher, meetURL, title, why string) (string, StartResult, error) {
	u := findMeetURL(meetURL)
	if u == "" {
		return "", StartError, fmt.Errorf("это не похоже на ссылку Google Meet")
	}
	m := &Meeting{
		ID:        newID(time.Now()),
		Title:     firstNonEmpty(strings.TrimSpace(title), "Созвон по ссылке из панели"),
		MeetURL:   u,
		StartedAt: time.Now(),
		Status:    "recording",
	}
	return m.ID, d.Start(ctx, adHocKey(u, time.Now()), m, why), nil
}

// --- напоминания -------------------------------------------------------------
//
// Почту читают не все и не всегда, а пропущенная встреча стоит дороже одного
// сообщения в чат. Напоминание приходит туда же, куда потом придёт follow-up,
// и говорит заодно, придёт ли бот, — если нет, ещё есть время это поправить.

func (s *Store) MarkReminded(key string) (bool, error) {
	res, err := s.db.Exec(`INSERT OR IGNORE INTO schedule_reminded (key,reminded_at) VALUES (?,?)`,
		key, time.Now().Unix())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// PruneReminded убирает отметки о прошедшем: таблица не должна расти вечно.
func (s *Store) PruneReminded(before time.Time) error {
	_, err := s.db.Exec(`DELETE FROM schedule_reminded WHERE reminded_at < ?`, before.Unix())
	return err
}

type reminder struct {
	cfg *Config
	st  *Store
	log *log.Logger
}

func (r *reminder) Name() string { return "напоминания" }

func (r *reminder) Run(ctx context.Context) error {
	before := r.cfg.Calendar.RemindBefore.D()
	if before <= 0 {
		before = 10 * time.Minute
	}
	r.log.Printf("напоминания: за %s до начала", before)

	// Раз в минуту: напоминание за десять минут, пришедшее за четыре, уже
	// бесполезно.
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		r.once(ctx, before)
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

func (r *reminder) once(ctx context.Context, before time.Duration) {
	now := time.Now()
	entries, err := r.st.Schedule(now, now.Add(before))
	if err != nil {
		r.log.Printf("напоминания: %v", err)
		return
	}
	for _, e := range entries {
		if e.Recorded != "" {
			continue // уже записывается — напоминать не о чем
		}
		fresh, err := r.st.MarkReminded(e.Key)
		if err != nil {
			r.log.Printf("напоминания: %v", err)
			continue
		}
		if !fresh {
			continue
		}
		r.send(ctx, e, now)
	}
	_ = r.st.PruneReminded(now.AddDate(0, 0, -2))
}

func (r *reminder) send(ctx context.Context, e ScheduleEntry, now time.Time) {
	text := remindText(e, now)
	if r.cfg.Telegram.Enabled && r.cfg.Telegram.ChatID != "" {
		if err := sendTelegramText(ctx, r.cfg, r.cfg.Telegram.ChatID, text); err != nil {
			r.log.Printf("напоминания: telegram: %v", err)
		}
	}
	if r.cfg.Slack.Enabled && r.cfg.Slack.Channel != "" {
		if err := sendSlackText(ctx, r.cfg, r.cfg.Slack.Channel, text); err != nil {
			r.log.Printf("напоминания: slack: %v", err)
		}
	}
}

func remindText(e ScheduleEntry, now time.Time) string {
	var b strings.Builder
	mins := int(e.StartsAt.Sub(now).Minutes())
	switch {
	case mins <= 0:
		b.WriteString("Сейчас начинается")
	case mins == 1:
		b.WriteString("Через минуту")
	default:
		fmt.Fprintf(&b, "Через %d мин", mins)
	}
	fmt.Fprintf(&b, " — %s\n%s", orDash(e.Title), e.StartsAt.Format("15:04"))
	if len(e.Attendees) > 0 {
		fmt.Fprintf(&b, " · %s", strings.Join(e.Attendees, ", "))
	}
	b.WriteString("\n")

	// Про бота говорим всегда: «не придёт, потому что нет ссылки» — это ещё
	// можно успеть поправить, а молчание разбирать потом уже поздно.
	switch {
	case e.WillAttend() && e.MeetURL != "":
		fmt.Fprintf(&b, "Бот придёт. %s", e.MeetURL)
	case e.Override == "skip":
		b.WriteString("Бот не придёт: отменили в панели")
	case e.Skip != "":
		fmt.Fprintf(&b, "Бот не придёт: %s", e.Skip)
	default:
		b.WriteString("Бот придёт")
	}
	return b.String()
}
