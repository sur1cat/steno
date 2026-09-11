package sources

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/sur1cat/steno/internal/bot"
	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/google"
	"github.com/sur1cat/steno/internal/i18n"
	"google.golang.org/api/calendar/v3"
)

// Источник созвонов номер один: календари сотрудников.
//
// Каждый календарь читается от имени его владельца — domain-wide delegation
// позволяет service-account'у представиться любым сотрудником домена. Поэтому
// отдельного OAuth-потока на каждого человека не нужно: достаточно один раз
// выдать делегирование в админке Workspace.

type CalendarSource struct {
	Cfg *core.Config
	D   *Dispatcher
	Log *log.Logger

	// По клиенту на сотрудника. Иначе каждые две минуты на каждый календарь
	// перечитывался бы файл ключа и заново обменивался OAuth-токен.
	mu   sync.Mutex
	svcs map[string]*calendar.Service
}

func (s *CalendarSource) Name() string { return i18n.Tr("календарь") }

func (s *CalendarSource) Run(ctx context.Context) error {
	if len(s.Cfg.Calendar.Calendars) == 0 {
		return errors.New(i18n.Tr("не указан ни один календарь в calendar.calendars"))
	}
	every := s.Cfg.Calendar.PollEvery.D()
	if every <= 0 {
		every = 2 * time.Minute
	}
	s.Log.Printf(i18n.Tr("календарь: слежу за %d календарями, опрос раз в %s"),
		len(s.Cfg.Calendar.Calendars), every)

	t := time.NewTicker(every)
	defer t.Stop()
	s.poll(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			s.poll(ctx)
		}
	}
}

func (s *CalendarSource) poll(ctx context.Context) {
	now := time.Now()
	horizon := now.Add(s.Cfg.Calendar.PollEvery.D() + s.Cfg.Calendar.JoinBefore.D() + time.Minute)

	for _, calID := range s.Cfg.Calendar.Calendars {
		events, err := s.upcoming(ctx, calID, now.Add(-2*time.Minute), horizon)
		if err != nil {
			s.Log.Printf(i18n.Tr("календарь %s: %v"), calID, err)
			continue
		}
		for _, ev := range events {
			s.consider(ctx, calID, ev, now)
		}
	}
}

func (s *CalendarSource) service(ctx context.Context, subject string) (*calendar.Service, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if svc, ok := s.svcs[subject]; ok {
		return svc, nil
	}
	opt, err := google.GoogleClient(ctx, s.Cfg,
		google.CredentialsFile(s.Cfg.Calendar.CredentialsFile, s.Cfg.GoogleDocs.CredentialsFile),
		subject, calendar.CalendarReadonlyScope)
	if err != nil {
		return nil, err
	}
	svc, err := calendar.NewService(ctx, opt)
	if err != nil {
		return nil, err
	}
	if s.svcs == nil {
		s.svcs = map[string]*calendar.Service{}
	}
	s.svcs[subject] = svc
	return svc, nil
}

func (s *CalendarSource) upcoming(ctx context.Context, calID string, from, to time.Time) ([]*calendar.Event, error) {
	srv, err := s.service(ctx, calID)
	if err != nil {
		return nil, err
	}
	res, err := srv.Events.List("primary").
		TimeMin(from.Format(time.RFC3339)).
		TimeMax(to.Format(time.RFC3339)).
		SingleEvents(true).
		OrderBy("startTime").
		MaxResults(50).
		Context(ctx).Do()
	if err != nil {
		return nil, err
	}
	return res.Items, nil
}

func (s *CalendarSource) consider(ctx context.Context, calID string, ev *calendar.Event, now time.Time) {
	meetURL := meetLink(ev)
	if meetURL == "" || ev.Status == "cancelled" {
		return
	}
	if skipMarked(ev, s.Cfg.Calendar.SkipMarkers) {
		return
	}
	if ev.Start == nil || ev.Start.DateTime == "" {
		return // встреча на весь день — не созвон
	}
	start, err := time.Parse(time.RFC3339, ev.Start.DateTime)
	if err != nil {
		return
	}
	attendees := acceptedAttendees(ev)
	if len(attendees) < s.Cfg.Calendar.MinAttendees {
		return
	}
	if now.Before(start.Add(-s.Cfg.Calendar.JoinBefore.D())) {
		return // ещё рано, вернёмся на следующем опросе
	}
	if now.After(start.Add(30 * time.Minute)) {
		return // встреча идёт больше получаса, заходить уже поздно
	}

	// Одна и та же встреча лежит в календарях всех участников. Ключ по ссылке
	// и времени начала, а не по id события: так бот заходит один раз.
	key := plannedKey(meetURL, start)
	// «Не ходить» из расписания. Смысл кнопки в том, чтобы передумать заранее,
	// а не выгонять бота из уже идущего звонка на глазах у всех.
	if s.D.st.ScheduleOverride(key) == "skip" {
		return
	}
	s.D.StartOnce(ctx, key, &core.Meeting{
		ID:         core.NewID(start),
		Title:      ev.Summary,
		MeetURL:    meetURL,
		CalendarID: calID,
		StartedAt:  time.Now(),
		Invitees:   attendees,
		Status:     "recording",
	}, i18n.Tr("календарь ")+calID)
}

// meetLink пропускает ссылку через ту же строгую проверку, что и остальные
// источники. Раньше здесь стояло strings.Contains(uri, "meet.google.com") —
// и приглашение в календарь с ссылкой вида https://evil.example/?x=meet.google.com
// уводило браузер бота на чужую страницу. А это Chromium с --no-sandbox,
// автоматически выданными микрофоном и камерой и примонтированным
// корпоративным профилем.
//
// Календарь — единственный источник, до которого посторонний дотягивается
// простым приглашением, так что проверка здесь нужна не меньше, чем в почте.
func meetLink(ev *calendar.Event) string {
	if u := bot.FindMeetURL(ev.HangoutLink); u != "" {
		return u
	}
	if ev.ConferenceData == nil {
		return ""
	}
	for _, ep := range ev.ConferenceData.EntryPoints {
		if ep.EntryPointType != "video" {
			continue
		}
		if u := bot.FindMeetURL(ep.Uri); u != "" {
			return u
		}
	}
	return ""
}

func skipMarked(ev *calendar.Event, markers []string) bool {
	hay := strings.ToLower(ev.Summary + " " + ev.Description)
	for _, m := range markers {
		if m != "" && strings.Contains(hay, strings.ToLower(m)) {
			return true
		}
	}
	return false
}

// acceptedAttendees — те, кто не отказался. Отказавшихся не считаем: встреча
// на десять приглашённых, из которых девять сказали «нет», — это не созвон.
func acceptedAttendees(ev *calendar.Event) []string {
	var out []string
	for _, a := range ev.Attendees {
		if a.ResponseStatus == "declined" || a.Resource {
			continue
		}
		out = append(out, core.FirstNonEmpty(a.DisplayName, a.Email))
	}
	return out
}
