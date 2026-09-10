package main

import (
	"os"
	"testing"
	"time"

	"github.com/sur1cat/steno/internal/core"
)

// Наполняет базу правдоподобными данными, чтобы панель можно было посмотреть
// глазами. Верстать список созвонов на пустой базе бессмысленно.
//
//	SEED_DIR=./demo go test -run TestSeed .
//	STENO_PANEL_PASSWORD=... steno serve -c demo.json
func TestSeed(t *testing.T) {
	dir := os.Getenv("SEED_DIR")
	if dir == "" {
		t.Skip("нет SEED_DIR — это не проверка, а наполнение демо-базы")
	}
	st, err := core.OpenStore(dir)
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
			t.Fatal(err)
		}
		if d.dur > 0 {
			if err := st.FinishMeeting(d.id, start, start.Add(d.dur), d.people, d.status, "", d.left); err != nil {
				t.Fatal(err)
			}
		}
		var segs []core.Segment
		for _, l := range d.lines {
			segs = append(segs, core.Segment{Start: l.at, End: l.at + 12, Speaker: l.speaker, Text: l.text})
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
		t.Fatal(err)
	}
	// Следующий созвон закрыл одну задачу — так и выглядит движение по проекту.
	if open, err := st.OpenItems("Платежи"); err == nil {
		for _, it := range open {
			if it.Kind == core.KindTask && it.Owner == "Участник Г" {
				_ = st.CloseItem(it.ID, "done", "откат отработал на копии прода", "2026-09-07-1530-c3d4")
			}
		}
	}

	t.Logf("наполнено: %d созвонов в %s", len(all), dir)
}
