package main

import (
	"time"

	"github.com/sur1cat/steno/internal/core"
)

// Демо-набор: правдоподобные созвоны, чтобы панель можно было посмотреть
// глазами до первого настоящего. Им пользуются `steno demo` и наполнение
// базы для картинок в README (seed_test.go).
//
// Русский и английский наборы — два разных, а не перевод одного: переводить
// на ходу значило бы каждый раз получать другой текст, а картинки в README
// сняты именно с этого.
func seedDemo(st *core.Store, lang string) (projects []core.Project, n int, err error) {
	if lang == "en" {
		return seedEN(st)
	}
	return seedRU(st)
}

func seedRU(st *core.Store) (projects []core.Project, n int, err error) {

	type line struct {
		speaker string
		text    string
		at      float64
	}
	type demo struct {
		id, title, status, left string
		ago, dur                time.Duration
		people                  []string
		f                       *core.Followup
		lines                   []line
	}

	all := []demo{{
		id: "2026-09-08-1100-a1b2", title: "Планёрка по релизу 2.4",
		ago: 3 * time.Hour, dur: 47 * time.Minute, status: "published", left: "остался один",
		people: []string{"Участник А", "Участник Б", "Участник В", "Участник Г"},
		f: &core.Followup{
			Title: "Релиз 2.4 сдвинули на пятницу из-за миграции",
			TLDR: []string{
				"Релиз переносится со среды на пятницу: миграция схемы не успевает пройти на стейджинге.",
				"Договорились не резать фичи, а сдвинуть дату — откат миграции дороже недели ожидания.",
				"Открытым остался вопрос, кто дежурит в выходные после выката.",
			},
			ActionItems: []core.ActionItem{
				{Owner: "Участник Б", What: "закончить миграцию схемы и прогнать её на стейджинге",
					Due: "2026-09-11", At: 412, Quote: "я закончу миграцию к четвергу, в пятницу утром прогоню на стейдже"},
				{Owner: "Участник В", What: "обновить changelog и письмо клиентам под новую дату",
					Due: "2026-09-10", At: 1105, Quote: "переписать письмо — это полчаса, сделаю завтра"},
				{Owner: "не назначен", What: "решить, кто дежурит в выходные после выката",
					At: 1890, Quote: "давайте про дежурство отдельно, сейчас не решим"},
				{Owner: "Участник Г", What: "проверить, что откат миграции отрабатывает на копии продовой базы",
					Due: "2026-09-04", At: 760, Quote: "я возьму копию прода и проверю откат"},
			},
			Decisions: []core.Decision{
				{What: "релиз в пятницу 11 сентября", Why: "миграция не успевает раньше, а резать фичи дороже", At: 380},
				{What: "фичи не режем", Why: "половина уже в бете у трёх клиентов", At: 640},
			},
			OpenQuestions: []core.OpenQuestion{
				{Question: "кто дежурит в выходные после выката", WaitingOn: "Участник А", At: 1890},
				{Question: "нужен ли отдельный прогон нагрузочных после миграции", WaitingOn: "не определено", At: 2210},
			},
			Risks: []string{
				"Если миграция не пройдёт на стейджинге в пятницу утром, релиз уедет на следующую неделю.",
				"Откат миграции ни разу не проверялся на объёме продовой базы.",
			},
		},
		lines: []line{
			{"Участник А", "Так, давайте начнём с релиза. Участник Б, что с миграцией?", 12},
			{"Участник Б", "Миграция готова процентов на восемьдесят, но я не успею прогнать её на стейджинге до среды.", 31},
			{"Участник А", "То есть в среду мы не выкатываемся.", 58},
			{"Участник Б", "Не выкатываемся. Либо режем миграцию из релиза, либо двигаем дату.", 66},
			{"Участник В", "Резать нельзя, половина фич в бете у трёх клиентов и они этого ждут.", 94},
			{"Участник Г", "Плюс если резать, придётся откатывать половину веток, это ещё день.", 120},
			{"Участник А", "Значит двигаем на пятницу. Возражения?", 370},
			{"Участник Б", "Я закончу миграцию к четвергу, в пятницу утром прогоню на стейдже.", 412},
			{"Участник Г", "Я возьму копию прода и проверю откат. Никто ведь ни разу не проверял на таком объёме.", 760},
			{"Участник В", "Переписать письмо — это полчаса, сделаю завтра.", 1105},
			{"Участник А", "Давайте про дежурство отдельно, сейчас не решим.", 1890},
			{"Участник Г", "Ещё вопрос — нужен ли отдельный прогон нагрузочных после миграции?", 2210},
		},
	}, {
		id: "2026-09-07-1530-c3d4", title: "Разбор инцидента с очередью",
		ago: 26 * time.Hour, dur: 62 * time.Minute, status: "published", left: "остался один",
		people: []string{"Участник А", "Участник Г", "Участник Е"},
		f: &core.Followup{
			Title: "Очередь встала из-за ретраев без ограничения",
			TLDR: []string{
				"Очередь встала на два часа: воркеры ретраили упавшие задачи без потолка и забили пул соединений.",
				"Чинили руками, автоматики на этот случай нет.",
			},
			ActionItems: []core.ActionItem{
				{Owner: "Участник Г", What: "добавить потолок ретраев и экспоненциальную паузу",
					Due: "2026-09-12", At: 900, Quote: "надо просто поставить максимум пять попыток и бэкофф"},
				{Owner: "Участник Е", What: "завести алерт на глубину очереди",
					Due: "2026-09-09", At: 1500, Quote: "алерт на глубину очереди сделаю на этой неделе"},
			},
			Decisions: []core.Decision{{What: "потолок ретраев — пять попыток", Why: "дальше задача всё равно не проходит", At: 940}},
			Risks:     []string{"Пока алерта нет, следующий такой инцидент заметим так же — по жалобам."},
		},
		lines: []line{
			{"Участник А", "Давайте разберём, что вчера случилось с очередью.", 8},
			{"Участник Г", "Воркеры ретраили упавшие задачи бесконечно и забили пул соединений к базе.", 40},
			{"Участник Е", "Мы это заметили только когда клиенты начали писать.", 180},
			{"Участник Г", "Надо просто поставить максимум пять попыток и бэкофф.", 900},
			{"Участник Е", "Алерт на глубину очереди сделаю на этой неделе.", 1500},
		},
	}, {
		id: "2026-09-05-0930-e5f6", title: "Синк с дизайном",
		ago: 3 * 24 * time.Hour, dur: 28 * time.Minute,
		status: "publish_failed", left: "бота вывели из звонка",
		people: []string{"Участник В", "Участник Д"},
		f: &core.Followup{
			Title: "Онбординг переделываем в три шага",
			TLDR:  []string{"Онбординг сокращаем с пяти экранов до трёх, спорный шаг с импортом убираем в настройки."},
			ActionItems: []core.ActionItem{
				{Owner: "Участник Д", What: "собрать прототип трёхшагового онбординга",
					Due: "2026-09-15", At: 300, Quote: "прототип соберу к середине следующей недели"},
			},
		},
		lines: []line{
			{"Участник В", "Пять экранов — это много, люди отваливаются на третьем.", 20},
			{"Участник Д", "Прототип соберу к середине следующей недели.", 300},
		},
	}, {
		id: "2026-09-08-1400-g7h8", title: "Звонок с подрядчиком",
		ago: 40 * time.Minute, status: "recording",
		people: []string{"Участник А"},
	}}

	for _, d := range all {
		start := time.Now().Add(-d.ago)
		m := &core.Meeting{
			ID: d.id, Title: d.title, MeetURL: "https://meet.google.com/abc-defg-hij",
			StartedAt: start, Participants: d.people, Invitees: d.people,
			Status: d.status, LeftReason: d.left,
		}
		if err := st.CreateMeeting(m); err != nil {
			return nil, 0, err
		}
		if d.dur > 0 {
			if err := st.FinishMeeting(d.id, start, start.Add(d.dur), d.people, d.status, "", d.left); err != nil {
				return nil, 0, err
			}
		}
		var segs []core.Segment
		for _, l := range d.lines {
			segs = append(segs, core.Segment{Start: l.at, End: l.at + 12, Speaker: l.speaker, Text: l.text})
		}
		if len(segs) > 0 {
			if err := st.SaveSegments(d.id, segs); err != nil {
				return nil, 0, err
			}
		}
		if d.f != nil {
			if err := st.SaveFollowup(d.id, "claude-opus-5", d.f); err != nil {
				return nil, 0, err
			}
			_ = st.SavePublication(d.id, "google_docs", "https://docs.google.com/document/d/1abc/edit", "")
			if d.status == "published" {
				_ = st.SavePublication(d.id, "slack", "https://team.slack.com/archives/C01/p1757", "")
				_ = st.SavePublication(d.id, "telegram", "", "")
			}
		}
	}
	// Живое состояние проектов: часть пунктов уже закрыта следующим созвоном.
	cfg := core.DefaultConfig()
	cfg.Projects = []core.Project{
		{Name: "Платежи", Aliases: []string{"биллинг"}, About: "приём денег"},
		{Name: "Инфраструктура", Aliases: []string{"инфра"}, About: "выкаты и очереди"},
		{Name: "Онбординг", Aliases: []string{"регистрация"}, About: "первые экраны"},
	}
	first := &core.Followup{
		ActionItems: []core.ActionItem{
			{Owner: "Участник Б", What: "закончить миграцию схемы", Due: "2026-09-11",
				Project: "Платежи", Quote: "я закончу миграцию к четвергу"},
			{Owner: "Участник Г", What: "проверить откат на копии прода", Due: "2026-09-04",
				Project: "Платежи", Quote: "я возьму копию прода"},
			{Owner: "Участник Г", What: "потолок ретраев и бэкофф", Due: "2026-09-12",
				Project: "инфра", Quote: "максимум пять попыток"},
			{Owner: "Участник Д", What: "прототип трёхшагового онбординга", Due: "2026-09-15",
				Project: "регистрация"},
		},
		Decisions: []core.Decision{
			{What: "релиз в пятницу 11 сентября", Why: "миграция не успевает раньше", Project: "Платежи"},
			{What: "потолок ретраев — пять попыток", Why: "дальше задача не проходит", Project: "инфра"},
		},
		OpenQuestions: []core.OpenQuestion{
			{Question: "кто дежурит в выходные после выката", WaitingOn: "Участник А", Project: "Платежи"},
			{Question: "нужен ли прогон нагрузочных после миграции", WaitingOn: "не определено", Project: ""},
		},
	}
	for i := range first.ActionItems {
		first.ActionItems[i].Project = core.MatchProject(cfg.Projects, first.ActionItems[i].Project)
	}
	for i := range first.Decisions {
		first.Decisions[i].Project = core.MatchProject(cfg.Projects, first.Decisions[i].Project)
	}
	for i := range first.OpenQuestions {
		first.OpenQuestions[i].Project = core.MatchProject(cfg.Projects, first.OpenQuestions[i].Project)
	}
	if _, _, err := core.ApplyFollowup(st, cfg.Projects, "2026-09-08-1100-a1b2", first); err != nil {
		return nil, 0, err
	}
	// Следующий созвон закрыл одну задачу — так и выглядит движение по проекту.
	if open, err := st.OpenItems("Платежи"); err == nil {
		for _, it := range open {
			if it.Kind == core.KindTask && it.Owner == "Участник Г" {
				_ = st.CloseItem(it.ID, "done", "откат отработал на копии прода", "2026-09-07-1530-c3d4")
			}
		}
	}
	return cfg.Projects, len(all), nil
}

func seedEN(st *core.Store) (projects []core.Project, n int, err error) {

	type line struct {
		speaker string
		text    string
		at      float64
	}
	type demo struct {
		id, title, status, left string
		ago, dur                time.Duration
		people                  []string
		f                       *core.Followup
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
		f: &core.Followup{
			Title: "Release 2.4 moves to Friday because the migration will not make it",
			TLDR: []string{
				"The release moves from Wednesday to Friday: the schema migration will not get through staging in time.",
				"Agreed not to cut features but to move the date — rolling the migration back costs more than a week of waiting.",
				"Left open: who is on call over the weekend after the rollout.",
			},
			ActionItems: []core.ActionItem{
				{Owner: marek, What: "finish the schema migration and run it against staging",
					Due: "2026-09-11", At: 412, Quote: "I'll have the migration done by Thursday and run it on staging Friday morning"},
				{Owner: priya, What: "update the changelog and the customer email for the new date",
					Due: "2026-09-10", At: 1105, Quote: "rewriting the email is half an hour, I'll do it tomorrow"},
				{Owner: "unassigned", What: "decide who is on call over the weekend after the rollout",
					At: 1890, Quote: "let's take on-call separately, we won't settle it now"},
				{Owner: tom, What: "check that the migration rollback works on a copy of the production database",
					Due: "2026-09-04", At: 760, Quote: "I'll take a copy of prod and test the rollback"},
			},
			Decisions: []core.Decision{
				{What: "release on Friday, 11 September", Why: "the migration cannot land sooner, and cutting features costs more", At: 380},
				{What: "no features get cut", Why: "half of them are already in beta with three customers", At: 640},
			},
			OpenQuestions: []core.OpenQuestion{
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
		f: &core.Followup{
			Title: "The queue stalled because retries had no ceiling",
			TLDR: []string{
				"The queue stalled for two hours: workers retried failed jobs without a cap and exhausted the connection pool.",
				"Fixed by hand; there is no automation for this case.",
			},
			ActionItems: []core.ActionItem{
				{Owner: tom, What: "add a retry ceiling and exponential backoff",
					Due: "2026-09-12", At: 900, Quote: "we just need a max of five attempts and a backoff"},
				{Owner: jonas, What: "add an alert on queue depth",
					Due: "2026-09-09", At: 1500, Quote: "I'll do the queue-depth alert this week"},
			},
			Decisions: []core.Decision{{What: "retry ceiling — five attempts", Why: "beyond that the job does not go through anyway", At: 940}},
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
		f: &core.Followup{
			Title: "Onboarding gets rebuilt as three steps",
			TLDR:  []string{"Onboarding shrinks from five screens to three; the contested import step moves into settings."},
			ActionItems: []core.ActionItem{
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
		m := &core.Meeting{
			ID: d.id, Title: d.title, MeetURL: "https://meet.google.com/abc-defg-hij",
			StartedAt: start, Participants: d.people, Invitees: d.people,
			Status: d.status, LeftReason: d.left,
		}
		if err := st.CreateMeeting(m); err != nil {
			return nil, 0, err
		}
		if d.dur > 0 {
			if err := st.FinishMeeting(d.id, start, start.Add(d.dur), d.people, d.status, "", d.left); err != nil {
				return nil, 0, err
			}
		}
		var segs []core.Segment
		for _, l := range d.lines {
			segs = append(segs, core.Segment{Start: l.at, End: l.at + 12, Speaker: l.speaker, Text: l.text})
		}
		if len(segs) > 0 {
			if err := st.SaveSegments(d.id, segs); err != nil {
				return nil, 0, err
			}
		}
		if d.f != nil {
			if err := st.SaveFollowup(d.id, "claude-opus-5", d.f); err != nil {
				return nil, 0, err
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
	for _, e := range []core.ScheduleEntry{
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
			return nil, 0, err
		}
	}

	// Живое состояние проектов: часть пунктов уже закрыта следующим созвоном.
	cfg := core.DefaultConfig()
	cfg.Projects = []core.Project{
		{Name: "Payments", Aliases: []string{"billing"}, About: "taking money in"},
		{Name: "Infrastructure", Aliases: []string{"infra"}, About: "rollouts and queues"},
		{Name: "Onboarding", Aliases: []string{"signup"}, About: "the first screens"},
	}
	first := &core.Followup{
		ActionItems: []core.ActionItem{
			{Owner: marek, What: "finish the schema migration", Due: "2026-09-11",
				Project: "Payments", Quote: "I'll have the migration done by Thursday"},
			{Owner: tom, What: "test the rollback on a copy of prod", Due: "2026-09-04",
				Project: "Payments", Quote: "I'll take a copy of prod"},
			{Owner: tom, What: "retry ceiling and backoff", Due: "2026-09-12",
				Project: "infra", Quote: "a max of five attempts"},
			{Owner: jonas, What: "prototype of the three-step onboarding", Due: "2026-09-15",
				Project: "signup"},
		},
		Decisions: []core.Decision{
			{What: "release on Friday, 11 September", Why: "the migration cannot land sooner", Project: "Payments"},
			{What: "retry ceiling — five attempts", Why: "beyond that the job does not go through", Project: "infra"},
		},
		OpenQuestions: []core.OpenQuestion{
			{Question: "who is on call over the weekend after the rollout", WaitingOn: ana, Project: "Payments"},
			{Question: "does the migration need a load test after it", WaitingOn: "not decided", Project: ""},
		},
	}
	for i := range first.ActionItems {
		first.ActionItems[i].Project = core.MatchProject(cfg.Projects, first.ActionItems[i].Project)
	}
	for i := range first.Decisions {
		first.Decisions[i].Project = core.MatchProject(cfg.Projects, first.Decisions[i].Project)
	}
	for i := range first.OpenQuestions {
		first.OpenQuestions[i].Project = core.MatchProject(cfg.Projects, first.OpenQuestions[i].Project)
	}
	if _, _, err := core.ApplyFollowup(st, cfg.Projects, "2026-09-09-1100-a1b2", first); err != nil {
		return nil, 0, err
	}
	// Следующий созвон закрыл одну задачу — так и выглядит движение по проекту.
	if open, err := st.OpenItems("Payments"); err == nil {
		for _, it := range open {
			if it.Kind == core.KindTask && it.Owner == tom {
				_ = st.CloseItem(it.ID, "done", "the rollback worked on a copy of prod", "2026-09-08-1530-c3d4")
			}
		}
	}
	return cfg.Projects, len(all), nil
}
