package sources

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/sur1cat/steno/internal/bot"
	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
	"github.com/sur1cat/steno/internal/publish"
	"google.golang.org/api/calendar/v3"
)

// --- опрос ------------------------------------------------------------------

type SchedulePoller struct {
	Cfg *core.Config
	St  *core.Store
	Log *log.Logger
	Src *CalendarSource // ради кеша клиентов Google
}

func (s *SchedulePoller) Name() string { return i18n.Tr("расписание") }

func (s *SchedulePoller) Run(ctx context.Context) error {
	every := 15 * time.Minute
	s.Log.Printf(i18n.Tr("расписание: собираю на %d дней вперёд, опрос раз в %s"),
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

func (s *SchedulePoller) horizonDays() int {
	if d := s.Cfg.Calendar.ScheduleDays; d > 0 {
		return d
	}
	return 7
}

func (s *SchedulePoller) once(ctx context.Context) {
	from := time.Now().Add(-2 * time.Hour)
	to := time.Now().AddDate(0, 0, s.horizonDays())

	for _, calID := range s.Cfg.Calendar.Calendars {
		events, err := s.Src.upcoming(ctx, calID, from, to)
		if err != nil {
			s.Log.Printf(i18n.Tr("расписание: %s: %v"), calID, err)
			continue
		}
		for _, ev := range events {
			e, ok := s.entry(calID, ev)
			if !ok {
				continue
			}
			if err := s.St.SaveScheduled(e); err != nil {
				s.Log.Printf(i18n.Tr("расписание: %v"), err)
			}
		}
	}
	if err := s.St.PruneSchedule(time.Now().AddDate(0, 0, -2)); err != nil {
		s.Log.Printf(i18n.Tr("расписание: уборка: %v"), err)
	}
}

// entry решает, пойдёт ли бот, и главное — объясняет, почему нет. Молчаливое
// «бот не пришёл» разбирать невозможно; названная причина чинится за минуту.
func (s *SchedulePoller) entry(calID string, ev *calendar.Event) (core.ScheduleEntry, bool) {
	if ev.Status == "cancelled" {
		return core.ScheduleEntry{}, false
	}
	if ev.Start == nil || ev.Start.DateTime == "" {
		return core.ScheduleEntry{}, false // встреча на весь день — не созвон
	}
	start, err := time.Parse(time.RFC3339, ev.Start.DateTime)
	if err != nil {
		return core.ScheduleEntry{}, false
	}
	e := core.ScheduleEntry{
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
		// Отдельно называем узнанную, но неподдержанную площадку: «нет ссылки»
		// и «встреча в Zoom» — это разные причины не пойти, и вторая видна в
		// расписании как повод позвать людей в другую комнату.
		if hint := bot.LinkHint(eventLinkText(ev)); hint != "" {
			e.Skip = hint
		} else {
			e.Skip = i18n.Tr("нет ссылки на созвон")
		}
	case skipMarked(ev, s.Cfg.Calendar.SkipMarkers):
		e.Skip = i18n.Tr("стоит метка «не записывать»")
	case len(e.Attendees) < s.Cfg.Calendar.MinAttendees:
		e.Skip = fmt.Sprintf(i18n.Tr("участников %d, нужно хотя бы %d"),
			len(e.Attendees), s.Cfg.Calendar.MinAttendees)
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

// eventLinkText — всё, где у события календаря может лежать ссылка на созвон.
// Нужен, только чтобы объяснить человеку, почему бот не пойдёт: сам поход
// решается строгой проверкой в meetLink.
func eventLinkText(ev *calendar.Event) string {
	parts := []string{ev.HangoutLink, ev.Location, ev.Description}
	if ev.ConferenceData != nil {
		for _, ep := range ev.ConferenceData.EntryPoints {
			parts = append(parts, ep.Uri)
		}
	}
	return strings.Join(parts, " ")
}

// InviteToCall заводит бота на созвон по ссылке. Тот же путь, что у Telegram и
// HTTP, только повод — кнопка в панели.
func InviteToCall(ctx context.Context, d *Dispatcher, meetURL, title, why string) (string, StartResult, error) {
	u := bot.FindMeetURL(meetURL)
	if u == "" {
		return "", StartError, bot.MeetingLinkError(meetURL)
	}
	m := &core.Meeting{
		ID:        core.NewID(time.Now()),
		Title:     core.FirstNonEmpty(strings.TrimSpace(title), i18n.Tr("Созвон по ссылке из панели")),
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

type Reminder struct {
	Cfg *core.Config
	St  *core.Store
	Log *log.Logger
}

func (r *Reminder) Name() string { return i18n.Tr("напоминания") }

func (r *Reminder) Run(ctx context.Context) error {
	before := r.Cfg.Calendar.RemindBefore.D()
	if before <= 0 {
		before = 10 * time.Minute
	}
	r.Log.Printf(i18n.Tr("напоминания: за %s до начала"), before)

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

func (r *Reminder) once(ctx context.Context, before time.Duration) {
	now := time.Now()
	entries, err := r.St.Schedule(now, now.Add(before))
	if err != nil {
		r.Log.Printf(i18n.Tr("напоминания: %v"), err)
		return
	}
	for _, e := range entries {
		if e.Recorded != "" {
			continue // уже записывается — напоминать не о чем
		}
		fresh, err := r.St.MarkReminded(e.Key)
		if err != nil {
			r.Log.Printf(i18n.Tr("напоминания: %v"), err)
			continue
		}
		if !fresh {
			continue
		}
		r.send(ctx, e, now)
	}
	_ = r.St.PruneReminded(now.AddDate(0, 0, -2))
}

func (r *Reminder) send(ctx context.Context, e core.ScheduleEntry, now time.Time) {
	text := remindText(e, now)
	if r.Cfg.Telegram.Enabled && r.Cfg.Telegram.ChatID != "" {
		if err := publish.SendTelegramText(ctx, r.Cfg, r.Cfg.Telegram.ChatID, text); err != nil {
			r.Log.Printf(i18n.Tr("напоминания: telegram: %v"), err)
		}
	}
	if r.Cfg.Slack.Enabled && r.Cfg.Slack.Channel != "" {
		if err := publish.SendSlackText(ctx, r.Cfg, r.Cfg.Slack.Channel, text); err != nil {
			r.Log.Printf(i18n.Tr("напоминания: slack: %v"), err)
		}
	}
}

func remindText(e core.ScheduleEntry, now time.Time) string {
	var b strings.Builder
	mins := int(e.StartsAt.Sub(now).Minutes())
	switch {
	case mins <= 0:
		b.WriteString(i18n.Tr("Сейчас начинается"))
	case mins == 1:
		b.WriteString(i18n.Tr("Через минуту"))
	default:
		fmt.Fprintf(&b, i18n.Tr("Через %d мин"), mins)
	}
	fmt.Fprintf(&b, " — %s\n%s", core.OrDash(e.Title), e.StartsAt.Format("15:04"))
	if len(e.Attendees) > 0 {
		fmt.Fprintf(&b, " · %s", strings.Join(e.Attendees, ", "))
	}
	b.WriteString("\n")

	// Про бота говорим всегда: «не придёт, потому что нет ссылки» — это ещё
	// можно успеть поправить, а молчание разбирать потом уже поздно.
	switch {
	case e.WillAttend() && e.MeetURL != "":
		fmt.Fprintf(&b, i18n.Tr("Бот придёт. %s"), e.MeetURL)
	case e.Override == "skip":
		b.WriteString(i18n.Tr("Бот не придёт: отменили в панели"))
	case e.Skip != "":
		fmt.Fprintf(&b, i18n.Tr("Бот не придёт: %s"), e.Skip)
	default:
		b.WriteString(i18n.Tr("Бот придёт"))
	}
	return b.String()
}
