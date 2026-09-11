package i18n

// Английский для той части steno, которая делает ТЗ по задачам с созвонов и
// отдаёт их агенту (internal/spec).
//
// Отдельным файлом с init(), а не строками в i18n_en.go. Каталог там один на
// всех, правят его сразу несколько человек, и хвост общего файла — самое
// конфликтное место в репозитории. Отдельный файл добавляет свои строки в ту же
// карту и не отнимает ни одной чужой.
//
// Перевод здесь только для того, что видит человек: сообщения об ошибках и
// заголовки готового ТЗ. Сами промпты (internal/spec/prompt.go) через Tr() не
// идут вовсе — их читает модель, и они написаны по-русски.
func init() {
	for ru, en := range trSpecEN {
		TrEN[ru] = en
	}
}

var trSpecEN = map[string]string{
	// --- отбор и сборка ---
	"это не задача, а решение или вопрос — исполнять нечего":                 "this is a decision or a question, not a task — there is nothing to carry out",
	"у задачи не определён проект — непонятно, в каком репозитории работать": "the task has no project — there is no telling which repository to work in",
	"проект %q не заведён":                             "no project named %q",
	"у проекта %q нет каталога с кодом на этой машине": "project %q has no code directory on this machine",
	"по формулировке не видно, что это делается кодом": "the wording does not show that this is done by changing code",
	"разбор ТЗ: %w\nответ: %.400s":                     "parsing the spec: %w\nanswer: %.400s",
	"разбор отбора: %w\nответ: %.200s":                 "parsing the triage: %w\nanswer: %.200s",
	"нет базы":               "no database",
	"нет открытой задачи %s": "no open task %s",

	// --- сам документ ---
	"Задание без названия":                  "Untitled spec",
	"**Проект:** %s  \n":                    "**Project:** %s  \n",
	"**Задача:** %s  \n":                    "**Task:** %s  \n",
	"**Репозиторий:** %s\n":                 "**Repository:** %s\n",
	"## Что известно":                       "## What is known",
	"\n## Где это в коде\n\n":               "\n## Where this lives in the code\n\n",
	"  ← **такого пути в репозитории нет**": "  ← **no such path in the repository**",
	"## Что сделать":                        "## What to do",
	"## Чем проверить":                      "## How to check it",
	"## Чего в этой задаче делать не надо":  "## What this task does not include",
	"\n## На чём это держится\n\n":          "\n## What this rests on\n\n",
	"Ответов не было, поэтому ТЗ предполагает вот что. Если предположение неверно — переделывать придётся отсюда.\n\n": "Nobody answered, so the spec assumes the following. If an assumption is wrong, the rework starts here.\n\n",
	"  Если не так: %s\n":                         "  If it is not so: %s\n",
	"\n## Чего не хватает, чтобы это сделать\n\n": "\n## What is missing before this can be done\n\n",
	"Ни одного вопроса не названо — это само по себе повод не доверять этому ТЗ.\n": "Not a single question was named — that alone is reason enough to distrust this spec.\n",
	"  Спросить: %s\n": "  Ask: %s\n",

	// --- почему по ТЗ нельзя работать ---
	"> **По этому ТЗ нельзя запускать агента.**\n":                            "> **No agent may be run from this spec.**\n",
	"в ТЗ нет ни одного открытого вопроса — по фразе с созвона так не бывает": "the spec names no open question at all — that never happens with a sentence from a call",
	"ТЗ не называет ни одного места в коде":                                   "the spec names no place in the code",
	"ни один путь из ТЗ не нашёлся в репозитории":                             "not one path from the spec was found in the repository",
	"в ТЗ нет ни одного шага работы":                                          "the spec has no step of work in it",
	"без ответов на вопросы начинать нельзя":                                  "the work cannot start before the questions are answered",

	// --- исполнение ---
	"исполнение выключено: поставь \"agent\": {\"enabled\": true} в steno.json": "running agents is off: put \"agent\": {\"enabled\": true} into steno.json",
	"исполнение запускается только человеком — некому приписать запуск":         "only a person starts a run — there is nobody to attribute this one to",
	"нет ТЗ %s: %w": "no spec %s: %w",
	"по этому ТЗ уже идёт работа":                                 "work on this spec is already under way",
	"это не ТЗ, а отказ: %s":                                      "this is a refusal rather than a spec: %s",
	"по этому ТЗ нельзя работать:\n  · %s":                        "this spec cannot be worked from:\n  · %s",
	"у ТЗ не записан репозиторий":                                 "the spec has no repository recorded",
	"agent.base_branch=%q — такой ветки в репозитории нет":        "agent.base_branch=%q — the repository has no such branch",
	"%s не похож на репозиторий git: %s":                          "%s does not look like a git repository: %s",
	"каталог %s уже занят — убери его и повтори":                  "the directory %s is taken already — remove it and try again",
	"ветка %s уже есть — повтори запуск, имя берётся со временем": "branch %s exists already — start again, the name carries the time",
	"не удалось завести рабочую копию: %s":                        "could not create the working copy: %s",
	"не удалось записать правила доступа, запуск отменён: %w":     "could not write the permission rules, the run is off: %w",
	"неизвестный исполнитель %q":                                  "unknown runner %q",
	"не удалось запустить %s: %w":                                 "could not start %s: %w",
	"поток агента не удалось прочитать (%v) — запуск остановлен":  "the agent's stream could not be read (%v) — the run was stopped",
	"агент не уложился в отведённое время (agent.timeout)":        "the agent did not finish within its time (agent.timeout)",
	"не удалось сохранить изменения: %v":                          "could not save the changes: %v",
	"агент не изменил ни одного файла":                            "the agent changed no files at all",
	"сохранено в ветку %s: файлов — %d":                           "saved to branch %s: %d files",
	"рабочая копия: %s":                                           "working copy: %s",
	"ветка %s от %s":                                              "branch %s off %s",
	"исполняет %s":                                                "run by %s",

	// --- ход работы агента ---
	"читает %s":     "reads %s",
	"правит %s":     "edits %s",
	"пишет %s":      "writes %s",
	"выполняет: %s": "runs: %s",
	"ищет %s":       "looks for %s",
	"работает: %s":  "working: %s",

	// --- выбор исполнителя ---
	"исполнять некому: %s":                              "there is nobody to run it: %s",
	"agent.provider=%q — допустимы auto, claude, codex": "agent.provider=%q — auto, claude and codex are the allowed values",
	"разбор идёт через %s, а этот путь умеет только текст: файлы он не правит.\n":                          "the analysis goes through %s, and that path only speaks text: it does not edit files.\n",
	"  → поставь agent.provider = \"claude\" или \"codex\" — это те два, что умеют работать в репозитории": "  → set agent.provider = \"claude\" or \"codex\" — those two can work inside a repository",

	// --- команда steno spec ---
	"только по этому проекту":                     "only for this project",
	"собрать ТЗ по всем открытым задачам проекта": "build a spec for every open task of the project",
	"открытых задач нет":                          "there are no open tasks",
	"ТЗ нет — сделать: steno spec %s":             "no spec yet — build one: steno spec %s",
	"не взято: %s":                                "not taken: %s",
	"идёт работа, ветка %s":                       "work under way, branch %s",
	"сделано, ветка %s":                           "done, branch %s",
	"сорвалось: %s":                               "it fell through: %s",
	"ТЗ есть, но работать по нему нельзя: %s":     "there is a spec, but it cannot be worked from: %s",
	"ТЗ готово, вопросов в нём — %d":              "spec ready, %d questions in it",
	"%s — задача не взята: %s\n":                  "%s — task not taken: %s\n",
	"\n%s · %s · $%.4f\n":                         "\n%s · %s · $%.4f\n",
	"\nвсего потрачено $%.4f\n":                   "\n$%.4f spent in total\n",
	"задача не взята: %s\n":                       "task not taken: %s\n",
	"\n## Что делал агент (ветка %s)\n\n%s\n":     "\n## What the agent did (branch %s)\n\n%s\n",
	"исполнение выключено. Это осознанная настройка: по ней steno получает право писать файлы и выполнять команды на этой машине.\n  → добавь в steno.json: \"agent\": {\"enabled\": true}": "running agents is off. That switch is meant to be thrown deliberately: it gives steno the right to write files and run commands on this machine.\n  → add to steno.json: \"agent\": {\"enabled\": true}",
	"\nготово: ветка %s в %s\n":         "\ndone: branch %s in %s\n",
	"посмотреть: git -C %s diff ..%s\n": "take a look: git -C %s diff ..%s\n",

	// --- ручки панели ---
	"нет такого ТЗ":    "no such spec",
	"нет такой задачи": "no such task",
	"исполнение выключено в настройках сервиса": "running agents is off in the service settings",
	"по этому ТЗ нельзя работать":               "this spec cannot be worked from",
	"начал":        "started",
	"панель (%s)":  "panel (%s)",
	"ТЗ по %s: %v": "spec for %s: %v",
	"ТЗ по %s: задача не взята — %s":     "spec for %s: task not taken — %s",
	"ТЗ по %s готово: %s, вопросов — %d": "spec for %s is ready: %s, %d questions",
	"работа по %s: %v":                   "work on %s: %v",

	// --- справка команды ---
	"steno spec              — ТЗ по задачам с созвонов\n\n  steno spec                 показать, что уже собрано\n  steno spec <id-задачи>     собрать ТЗ по задаче (T-3f2a из steno projects)\n  steno spec show <id-ТЗ>    показать ТЗ целиком\n  steno spec run <id-ТЗ>     отдать ТЗ агенту: рабочая копия, ветка, коммит\n\nФлаги:\n  --project <имя>            только по этому проекту\n  --all                      собрать ТЗ по всем открытым задачам проекта\n  -c <path>                  путь к steno.json\n": "steno spec              — specs for tasks from calls\n\n  steno spec                 show what has been built already\n  steno spec <task-id>       build a spec for a task (T-3f2a from steno projects)\n  steno spec show <spec-id>  print the spec in full\n  steno spec run <spec-id>   hand the spec to an agent: working copy, branch, commit\n\nFlags:\n  --project <name>           only for this project\n  --all                      build a spec for every open task of the project\n  -c <path>                  path to steno.json\n",
}
