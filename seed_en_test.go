package main

import (
	"os"
	"testing"
	"time"
)

// Английский демо-набор. Тот же смысл, что у TestSeed, но на языке, на котором
// сняты картинки в README: пустая база на витрине не показывает ничего, а
// переводить русскую на ходу — значит каждый раз получать другой текст.
//
//	SEED_DIR=./demo STENO_LANG=en go test -run TestSeedEN .
//	STENO_LANG=en STENO_PANEL_PASSWORD=… steno serve -c demo.json
func TestSeedEN(t *testing.T) {
	dir := os.Getenv("SEED_DIR")
	if dir == "" {
		t.Skip("нет SEED_DIR — это не проверка, а наполнение демо-базы")
	}
	st, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	type line struct {
		speaker string
		text    string
		at      float64
	}
	type demo struct {
		id, title, status, left string
		ago, dur                time.Duration
		people                  []string
		f                       *Followup
		lines                   []line
	}

	const (
		ana   = "Ana Ribeiro"
		marek = "Marek Nowak"
		priya = "Priya Nair"
		tom   = "Tom Ellis"
		jonas = "Jonas Weber"
	)

	all := []demo{{
		id: "2026-09-09-1100-a1b2", title: "Release planning",
		ago: 3 * time.Hour, dur: 47 * time.Minute, status: "published", left: "left alone",
		people: []string{ana, marek, priya, tom},
		f: &Followup{
			Title: "Release 2.4 moves to Friday because the migration will not make it",
			TLDR: []string{
				"The release moves from Wednesday to Friday: the schema migration will not get through staging in time.",
				"Agreed not to cut features but to move the date — rolling the migration back costs more than a week of waiting.",
				"Left open: who is on call over the weekend after the rollout.",
			},
			ActionItems: []ActionItem{
				{Owner: marek, What: "finish the schema migration and run it against staging",
					Due: "2026-09-11", At: 412, Quote: "I'll have the migration done by Thursday and run it on staging Friday morning"},
				{Owner: priya, What: "update the changelog and the customer email for the new date",
					Due: "2026-09-10", At: 1105, Quote: "rewriting the email is half an hour, I'll do it tomorrow"},
				{Owner: "unassigned", What: "decide who is on call over the weekend after the rollout",
					At: 1890, Quote: "let's take on-call separately, we won't settle it now"},
				{Owner: tom, What: "check that the migration rollback works on a copy of the production database",
					Due: "2026-09-04", At: 760, Quote: "I'll take a copy of prod and test the rollback"},
			},
			Decisions: []Decision{
				{What: "release on Friday, 11 September", Why: "the migration cannot land sooner, and cutting features costs more", At: 380},
				{What: "no features get cut", Why: "half of them are already in beta with three customers", At: 640},
			},
			OpenQuestions: []OpenQuestion{
				{Question: "who is on call over the weekend after the rollout", WaitingOn: ana, At: 1890},
				{Question: "does the migration need a separate load test after it", WaitingOn: "not decided", At: 2210},
			},
			Risks: []string{
				"If the migration does not pass staging on Friday morning, the release slips a week.",
				"The migration rollback has never been tested at production data volume.",
			},
		},
		lines: []line{
			{ana, "Right, let's start with the release. Marek, where is the migration?", 12},
			{marek, "The migration is about eighty percent done, but I will not get it through staging before Wednesday.", 31},
			{ana, "So we are not shipping on Wednesday.", 58},
			{marek, "We are not. Either we cut the migration out of the release, or we move the date.", 66},
			{priya, "We cannot cut it, half the features are in beta with three customers and they are waiting.", 94},
			{tom, "And if we cut it, we have to roll back half the branches — that is another day.", 120},
			{ana, "Friday then. Any objections?", 370},
			{marek, "I'll have the migration done by Thursday and run it on staging Friday morning.", 412},
			{tom, "I'll take a copy of prod and test the rollback. Nobody has ever tried it at that volume.", 760},
			{priya, "Rewriting the email is half an hour, I'll do it tomorrow.", 1105},
			{ana, "Let's take on-call separately, we won't settle it now.", 1890},
			{tom, "One more thing — does the migration need a separate load test after it?", 2210},
		},
	}, {
		id: "2026-09-08-1530-c3d4", title: "Queue incident review",
		ago: 26 * time.Hour, dur: 62 * time.Minute, status: "published", left: "left alone",
		people: []string{ana, tom, jonas},
		f: &Followup{
			Title: "The queue stalled because retries had no ceiling",
			TLDR: []string{
				"The queue stalled for two hours: workers retried failed jobs without a cap and exhausted the connection pool.",
				"Fixed by hand; there is no automation for this case.",
			},
			ActionItems: []ActionItem{
				{Owner: tom, What: "add a retry ceiling and exponential backoff",
					Due: "2026-09-12", At: 900, Quote: "we just need a max of five attempts and a backoff"},
				{Owner: jonas, What: "add an alert on queue depth",
					Due: "2026-09-09", At: 1500, Quote: "I'll do the queue-depth alert this week"},
			},
			Decisions: []Decision{{What: "retry ceiling — five attempts", Why: "beyond that the job does not go through anyway", At: 940}},
			Risks:     []string{"Until the alert exists, we will notice the next incident the same way — from complaints."},
		},
		lines: []line{
			{ana, "Let's go through what happened to the queue yesterday.", 8},
			{tom, "Workers retried failed jobs forever and exhausted the connection pool to the database.", 40},
			{jonas, "We only noticed when customers started writing in.", 180},
			{tom, "We just need a max of five attempts and a backoff.", 900},
			{jonas, "I'll do the queue-depth alert this week.", 1500},
		},
	}, {
		id: "2026-09-06-0930-e5f6", title: "Design sync",
		ago: 3 * 24 * time.Hour, dur: 28 * time.Minute,
		status: "publish_failed", left: "the bot was removed from the call",
		people: []string{priya, jonas},
		f: &Followup{
			Title: "Onboarding gets rebuilt as three steps",
			TLDR:  []string{"Onboarding shrinks from five screens to three; the contested import step moves into settings."},
			ActionItems: []ActionItem{
				{Owner: jonas, What: "put together a prototype of the three-step onboarding",
					Due: "2026-09-15", At: 300, Quote: "I'll have a prototype by the middle of next week"},
			},
		},
		lines: []line{
			{priya, "Five screens is a lot, people drop off on the third one.", 20},
			{jonas, "I'll have a prototype by the middle of next week.", 300},
		},
	}, {
		id: "2026-09-09-1400-g7h8", title: "Call with the contractor",
		ago: 40 * time.Minute, status: "recording",
		people: []string{ana},
	}}

	for _, d := range all {
		start := time.Now().Add(-d.ago)
		m := &Meeting{
			ID: d.id, Title: d.title, MeetURL: "https://meet.google.com/abc-defg-hij",
			StartedAt: start, Participants: d.people, Invitees: d.people,
			Status: d.status, LeftReason: d.left,
		}
		if err := st.CreateMeeting(m); err != nil {
			t.Fatal(err)
		}
		if d.dur > 0 {
			if err := st.FinishMeeting(d.id, start, start.Add(d.dur), d.people, d.status, "", d.left); err != nil {
				t.Fatal(err)
			}
		}
		var segs []Segment
		for _, l := range d.lines {
			segs = append(segs, Segment{Start: l.at, End: l.at + 12, Speaker: l.speaker, Text: l.text})
		}
		if len(segs) > 0 {
			if err := st.SaveSegments(d.id, segs); err != nil {
				t.Fatal(err)
			}
		}
		if d.f != nil {
			if err := st.SaveFollowup(d.id, "claude-opus-5", d.f); err != nil {
				t.Fatal(err)
			}
			_ = st.SavePublication(d.id, "google_docs", "https://docs.google.com/document/d/1abc/edit", "")
			if d.status == "published" {
				_ = st.SavePublication(d.id, "slack", "https://team.slack.com/archives/C01/p1757", "")
				_ = st.SavePublication(d.id, "telegram", "", "")
			}
		}
	}

	// Расписание на несколько дней вперёд: часть встреч бот пропустит, и
	// пропуск назван словами. Пустое расписание не показывает главного —
	// что решение видно заранее и его можно поменять.
	day := func(d, h, m int) time.Time {
		t := time.Now().AddDate(0, 0, d)
		return time.Date(t.Year(), t.Month(), t.Day(), h, m, 0, 0, t.Location())
	}
	for _, e := range []ScheduleEntry{
		{Key: "s1", Title: "Payments sync", StartsAt: day(0, 15, 0), EndsAt: day(0, 15, 30),
			Attendees: []string{ana, marek, priya}},
		{Key: "s2", Title: "1:1 — Tom", StartsAt: day(0, 17, 0), EndsAt: day(0, 17, 30),
			Attendees: []string{ana, tom}, Skip: "2 participants, at least 3 are needed"},
		{Key: "s3", Title: "Release 2.4 retro", StartsAt: day(1, 10, 0), EndsAt: day(1, 11, 0),
			Attendees: []string{ana, marek, priya, tom, jonas}},
		{Key: "s4", Title: "Design review", StartsAt: day(1, 14, 0), EndsAt: day(1, 15, 0),
			Attendees: []string{priya, jonas, ana}},
		{Key: "s5", Title: "Team coffee", StartsAt: day(1, 16, 30), EndsAt: day(1, 17, 0),
			Attendees: []string{ana, tom, jonas}, Skip: "no meeting link"},
		{Key: "s6", Title: "Board sync [nosteno]", StartsAt: day(2, 11, 0), EndsAt: day(2, 12, 0),
			Attendees: []string{ana, marek}, Skip: "carries a “do not record” marker"},
	} {
		e.CalendarID = "team@company.com"
		if e.Skip == "" {
			e.MeetURL = "https://meet.google.com/abc-defg-hij"
		}
		if err := st.SaveScheduled(e); err != nil {
			t.Fatal(err)
		}
	}

	// Живое состояние проектов: часть пунктов уже закрыта следующим созвоном.
	cfg := defaultConfig()
	cfg.Projects = []Project{
		{Name: "Payments", Aliases: []string{"billing"}, About: "taking money in"},
		{Name: "Infrastructure", Aliases: []string{"infra"}, About: "rollouts and queues"},
		{Name: "Onboarding", Aliases: []string{"signup"}, About: "the first screens"},
	}
	first := &Followup{
		ActionItems: []ActionItem{
			{Owner: marek, What: "finish the schema migration", Due: "2026-09-11",
				Project: "Payments", Quote: "I'll have the migration done by Thursday"},
			{Owner: tom, What: "test the rollback on a copy of prod", Due: "2026-09-04",
				Project: "Payments", Quote: "I'll take a copy of prod"},
			{Owner: tom, What: "retry ceiling and backoff", Due: "2026-09-12",
				Project: "infra", Quote: "a max of five attempts"},
			{Owner: jonas, What: "prototype of the three-step onboarding", Due: "2026-09-15",
				Project: "signup"},
		},
		Decisions: []Decision{
			{What: "release on Friday, 11 September", Why: "the migration cannot land sooner", Project: "Payments"},
			{What: "retry ceiling — five attempts", Why: "beyond that the job does not go through", Project: "infra"},
		},
		OpenQuestions: []OpenQuestion{
			{Question: "who is on call over the weekend after the rollout", WaitingOn: ana, Project: "Payments"},
			{Question: "does the migration need a load test after it", WaitingOn: "not decided", Project: ""},
		},
	}
	for i := range first.ActionItems {
		first.ActionItems[i].Project = matchProject(cfg.Projects, first.ActionItems[i].Project)
	}
	for i := range first.Decisions {
		first.Decisions[i].Project = matchProject(cfg.Projects, first.Decisions[i].Project)
	}
	for i := range first.OpenQuestions {
		first.OpenQuestions[i].Project = matchProject(cfg.Projects, first.OpenQuestions[i].Project)
	}
	if _, _, err := applyFollowup(st, cfg.Projects, "2026-09-09-1100-a1b2", first); err != nil {
		t.Fatal(err)
	}
	// Следующий созвон закрыл одну задачу — так и выглядит движение по проекту.
	if open, err := st.OpenItems("Payments"); err == nil {
		for _, it := range open {
			if it.Kind == KindTask && it.Owner == tom {
				_ = st.CloseItem(it.ID, "done", "the rollback worked on a copy of prod", "2026-09-08-1530-c3d4")
			}
		}
	}

	t.Logf("наполнено: %d созвонов в %s", len(all), dir)
}
