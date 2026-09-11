package demo

import (
	"strings"
	"time"

	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/spec"
)

// ТЗ в демо-наборе: одно готовое к запуску, с вопросами, и одно уже
// исполненное, с веткой и журналом. Без них витрина не показывает главного
// отличия — что задача с созвона доезжает до ветки, — а «кнопка ТЗ» на пустом
// месте объясняет меньше, чем готовое задание, которое можно прочитать.
//
// Задачи находятся по тексту, а не по id: id им раздаёт ApplyFollowup случайно.
func seedSpecs(st *core.Store, lang string) error {
	store, err := spec.Open(st)
	if err != nil {
		return err
	}
	items, err := st.OpenItems("")
	if err != nil {
		return err
	}
	find := func(prefix string) (core.ProjectItem, bool) {
		for _, it := range items {
			if it.Kind == core.KindTask && strings.HasPrefix(it.Text, prefix) {
				return it, true
			}
		}
		return core.ProjectItem{}, false
	}
	var draft, done *spec.Spec
	if lang == "en" {
		draft, done = specsEN(find)
	} else {
		draft, done = specsRU(find)
	}
	for _, sp := range []*spec.Spec{draft, done} {
		if sp == nil {
			continue
		}
		if err := store.Save(sp); err != nil {
			return err
		}
	}
	return nil
}

func specsRU(find func(string) (core.ProjectItem, bool)) (draft, done *spec.Spec) {
	if it, ok := find("закончить миграцию"); ok {
		draft = &spec.Spec{
			ID: "S-7c1e", ItemID: it.ID, Project: it.Project, Repo: "~/work/payments",
			Status: spec.StatusDraft, Model: "claude-sonnet-5", USD: 0.041,
			CreatedAt: time.Now().Add(-26 * time.Hour),
			Title:     "Довести миграцию схемы подписок до прода",
			Summary: []string{
				"Миграция 0042 добавляет таблицу subscription_periods и переносит в неё сроки из subscriptions.",
				"На созвоне договорились закончить к четвергу: без неё релиз 2.4 не выкатить.",
			},
			Known: []string{
				"Миграции лежат в billing/migrations, последняя применённая — 0041_invoice_status.",
				"Перенос данных в 0042 написан, но не прогонялся на объёме прода (оценка — 3,2 млн строк).",
				"Откат проверен на копии прода 7 сентября — см. закрытую задачу.",
			},
			Places: []spec.Place{
				{Path: "billing/migrations/0042_subscription_periods.py", Why: "сама миграция и перенос данных", Found: true},
				{Path: "billing/models.py", Why: "модель SubscriptionPeriod, на которую переезжает срок", Found: true},
				{Path: "billing/services/renewal.py", Why: "читает срок из subscriptions — после переноса должен читать из periods", Found: true},
				{Path: "billing/tests/test_renewal.py", Why: "тесты продления, их надо переписать под новую таблицу", Found: true},
			},
			Steps: []string{
				"Разбить перенос данных в 0042 на пачки по 50 000 строк: одним UPDATE он держит блокировку дольше минуты.",
				"Переключить renewal.py на SubscriptionPeriod и оставить чтение из subscriptions только под флагом на один релиз.",
				"Переписать test_renewal.py: продление, истечение, льготный период.",
				"Прогнать миграцию на копии прода и записать время.",
			},
			Checks: []string{
				"pytest billing/tests — зелёный.",
				"На копии прода миграция проходит быстрее 10 минут, ни одной блокировки дольше 5 секунд.",
				"После миграции число строк в subscription_periods равно числу активных подписок.",
			},
			NotHere: []string{
				"Не трогать выставление счетов: invoice_status ушёл в 0041 и к этой задаче не относится.",
				"Не удалять старые колонки из subscriptions — это следующий релиз.",
			},
			Guesses: []spec.Assumption{
				{What: "Перенос идёт в той же миграции, а не отдельной командой.", IfWrong: "0042 остаётся только схемой, перенос выносится в management-команду — шаг 1 переписывается."},
			},
			Unknowns: []spec.Unknown{
				{Question: "Нужен ли прогон нагрузочных после миграции?", Why: "На созвоне вопрос остался открытым, а от ответа зависит, входит ли в задачу подготовка стенда.", Ask: "Участник А"},
				{Question: "Какие подписки считать активными при переносе — только paid или и trial?", Why: "В коде оба статуса читаются одинаково, а в разговоре про trial не было ни слова.", Ask: "Участник Б"},
			},
		}
	}
	if it, ok := find("потолок ретраев"); ok {
		done = &spec.Spec{
			ID: "S-2a9d", ItemID: it.ID, Project: it.Project, Repo: "~/work/infra",
			Status: spec.StatusDone, Model: "claude-sonnet-5", USD: 0.033,
			CreatedAt: time.Now().Add(-49 * time.Hour),
			Title:     "Потолок ретраев и экспоненциальный бэкофф в воркере очереди",
			Summary: []string{
				"Воркер повторяет упавшую задачу без ограничения; договорились: максимум пять попыток, пауза растёт вдвое.",
			},
			Known: []string{
				"Повтор задачи живёт в queue/worker.py, функция retry(); задержка сейчас постоянная — 30 секунд.",
				"Число попыток нигде не считается: у задачи нет поля attempts.",
			},
			Places: []spec.Place{
				{Path: "queue/worker.py", Why: "retry() — здесь и потолок, и бэкофф", Found: true},
				{Path: "queue/models.py", Why: "поле attempts у задачи", Found: true},
				{Path: "queue/tests/test_worker.py", Why: "тесты повтора", Found: true},
			},
			Steps: []string{
				"Добавить задаче поле attempts и увеличивать его в retry().",
				"Задержку считать как 30 × 2^attempts, после пятой попытки переводить задачу в failed.",
				"Тест: пять попыток, шестой нет; задержки 30, 60, 120, 240, 480.",
			},
			Checks: []string{"pytest queue/tests — зелёный.", "В логе воркера видно номер попытки и задержку."},
			Unknowns: []spec.Unknown{
				{Question: "Куда сообщать о задаче, которая упала пять раз?", Why: "На созвоне не сказали; пока — только статус failed в базе.", Ask: "Участник Г"},
			},
			Branch: "steno/potolok-retraev-i-bekoff-0910-1412", Worktree: "~/steno/data/agent/infra/S-2a9d",
			RunBy: "панель (127.0.0.1)", StartedAt: time.Now().Add(-47 * time.Hour),
			RunLog: strings.Join([]string{
				"14:12:03 note рабочая копия: ~/steno/data/agent/infra/S-2a9d",
				"14:12:03 note ветка steno/potolok-retraev-i-bekoff-0910-1412 от 8f1c2e0",
				"14:12:04 note исполняет Claude Code",
				"14:12:09 do   читает worker.py",
				"14:12:11 do   читает models.py",
				"14:12:20 say  Добавляю поле attempts и потолок в retry(), задержку считаю от номера попытки.",
				"14:12:31 do   правит models.py",
				"14:12:44 do   правит worker.py",
				"14:13:02 do   правит test_worker.py",
				"14:13:10 do   выполняет: pytest queue/tests -q",
				"14:13:38 say  Тесты зелёные: пять попыток, шестой нет, задержки удваиваются.",
				"14:13:39 note сохранено в ветку steno/potolok-retraev-i-bekoff-0910-1412: файлов — 3",
			}, "\n"),
		}
	}
	return draft, done
}

func specsEN(find func(string) (core.ProjectItem, bool)) (draft, done *spec.Spec) {
	if it, ok := find("finish the schema migration"); ok {
		draft = &spec.Spec{
			ID: "S-7c1e", ItemID: it.ID, Project: it.Project, Repo: "~/work/payments",
			Status: spec.StatusDraft, Model: "claude-sonnet-5", USD: 0.041,
			CreatedAt: time.Now().Add(-26 * time.Hour),
			Title:     "Land the subscription schema migration in production",
			Summary: []string{
				"Migration 0042 adds subscription_periods and moves the period dates out of subscriptions.",
				"The call agreed on Thursday: release 2.4 cannot ship without it.",
			},
			Known: []string{
				"Migrations live in billing/migrations; the last applied one is 0041_invoice_status.",
				"The data move in 0042 is written but has never run at production size (about 3.2M rows).",
				"The rollback was tested on a copy of prod on 7 September — see the closed task.",
			},
			Places: []spec.Place{
				{Path: "billing/migrations/0042_subscription_periods.py", Why: "the migration and the data move", Found: true},
				{Path: "billing/models.py", Why: "SubscriptionPeriod, where the period date moves to", Found: true},
				{Path: "billing/services/renewal.py", Why: "reads the period from subscriptions — must read from periods after the move", Found: true},
				{Path: "billing/tests/test_renewal.py", Why: "renewal tests, to be rewritten for the new table", Found: true},
			},
			Steps: []string{
				"Split the data move in 0042 into batches of 50,000 rows: as one UPDATE it holds the lock for over a minute.",
				"Switch renewal.py to SubscriptionPeriod and keep the old read behind a flag for one release.",
				"Rewrite test_renewal.py: renewal, expiry, grace period.",
				"Run the migration on a copy of prod and record the time.",
			},
			Checks: []string{
				"pytest billing/tests is green.",
				"On a copy of prod the migration finishes under 10 minutes with no lock held longer than 5 seconds.",
				"After the migration, subscription_periods has one row per active subscription.",
			},
			NotHere: []string{
				"Do not touch invoicing: invoice_status went into 0041 and is not part of this task.",
				"Do not drop the old columns from subscriptions — that is the next release.",
			},
			Guesses: []spec.Assumption{
				{What: "The data move runs inside the same migration, not as a separate command.", IfWrong: "0042 stays schema-only and the move becomes a management command — step 1 is rewritten."},
			},
			Unknowns: []spec.Unknown{
				{Question: "Does the migration need a load test after it?", Why: "The question stayed open on the call, and the answer decides whether preparing a stand is part of this task.", Ask: "Ana"},
				{Question: "Which subscriptions count as active for the move — paid only, or trial too?", Why: "The code reads both statuses the same way, and trial was never mentioned on the call.", Ask: "Marek"},
			},
		}
	}
	if it, ok := find("retry ceiling"); ok {
		done = &spec.Spec{
			ID: "S-2a9d", ItemID: it.ID, Project: it.Project, Repo: "~/work/infra",
			Status: spec.StatusDone, Model: "claude-sonnet-5", USD: 0.033,
			CreatedAt: time.Now().Add(-49 * time.Hour),
			Title:     "Retry ceiling and exponential backoff in the queue worker",
			Summary: []string{
				"The worker retries a failed job without limit; agreed: at most five attempts, the pause doubling each time.",
			},
			Known: []string{
				"The retry lives in queue/worker.py, retry(); the delay is a constant 30 seconds.",
				"Attempts are not counted anywhere: the job has no attempts field.",
			},
			Places: []spec.Place{
				{Path: "queue/worker.py", Why: "retry() — the ceiling and the backoff go here", Found: true},
				{Path: "queue/models.py", Why: "the attempts field on the job", Found: true},
				{Path: "queue/tests/test_worker.py", Why: "retry tests", Found: true},
			},
			Steps: []string{
				"Add an attempts field to the job and increment it in retry().",
				"Compute the delay as 30 × 2^attempts; after the fifth attempt mark the job failed.",
				"Test: five attempts, no sixth; delays 30, 60, 120, 240, 480.",
			},
			Checks: []string{"pytest queue/tests is green.", "The worker log shows the attempt number and the delay."},
			Unknowns: []spec.Unknown{
				{Question: "Where to report a job that failed five times?", Why: "Not said on the call; for now only the failed status in the database.", Ask: "Tom"},
			},
			Branch: "steno/retry-ceiling-and-backoff-0910-1412", Worktree: "~/steno/data/agent/infrastructure/S-2a9d",
			RunBy: "panel (127.0.0.1)", StartedAt: time.Now().Add(-47 * time.Hour),
			RunLog: strings.Join([]string{
				"14:12:03 note worktree: ~/steno/data/agent/infrastructure/S-2a9d",
				"14:12:03 note branch steno/retry-ceiling-and-backoff-0910-1412 from 8f1c2e0",
				"14:12:04 note executed by Claude Code",
				"14:12:09 do   reads worker.py",
				"14:12:11 do   reads models.py",
				"14:12:20 say  Adding the attempts field and the ceiling in retry(); the delay is derived from the attempt number.",
				"14:12:31 do   edits models.py",
				"14:12:44 do   edits worker.py",
				"14:13:02 do   edits test_worker.py",
				"14:13:10 do   runs: pytest queue/tests -q",
				"14:13:38 say  Tests are green: five attempts, no sixth, delays double.",
				"14:13:39 note saved to branch steno/retry-ceiling-and-backoff-0910-1412: 3 files",
			}, "\n"),
		}
	}
	return draft, done
}
