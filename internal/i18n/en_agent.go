package i18n

// Английский для выключателя агента и его поверхностей: `steno agent`,
// строка в doctor и мастере, ТЗ в `steno ui`, автосборка после разбора.
// Отдельным файлом по той же причине, что en_spec.go: хвост общего каталога —
// самое конфликтное место в репозитории.
func init() {
	for ru, en := range trAgentEN {
		TrEN[ru] = en
	}
}

var trAgentEN = map[string]string{
	// --- steno agent ---
	"steno agent             — задачи агенту: выключатель и состояние\n\n  steno agent                что включено, кто исполняет, куда кладутся ветки\n  steno agent on|off         разрешить или запретить исполнение ТЗ на этой\n                             машине: рабочая копия, ветка, коммит — никогда push\n  steno agent auto on|off    собирать ли ТЗ самому после каждого разбора\n                             (это только чтение кода и запрос к модели)\n\nФлаги:\n  -c <path>                  путь к steno.json\n": "steno agent             — tasks to an agent: the switch and its state\n\n  steno agent                what is on, who executes, where the branches go\n  steno agent on|off         allow or forbid running specs on this machine:\n                             a worktree, a branch, a commit — never a push\n  steno agent auto on|off    whether to write specs on its own after every write-up\n                             (that is only reading the code and one model call)\n\nFlags:\n  -c <path>                  path to steno.json\n",
	"steno agent · конфиг %s\n\n":                                       "steno agent · config %s\n\n",
	"включить исполнение:  steno agent on":                              "turn execution on:   steno agent on",
	"выключить исполнение:  steno agent off":                            "turn execution off:  steno agent off",
	"исполнение включено, но исполнителя нет — см. выше":                "execution is on, but there is nobody to execute — see above",
	"конфига %s нет — сначала steno setup":                              "there is no config %s — run steno setup first",
	"не понял «%s»: steno agent on | off | auto on | auto off | status": "did not understand “%s”: steno agent on | off | auto on | auto off | status",
	"не понял «auto %s»: steno agent auto on | off":                     "did not understand “auto %s”: steno agent auto on | off",
	"собирать ТЗ самому:   steno agent auto on":                         "write specs on its own:  steno agent auto on",
	"исполнение: включено — steno может писать файлы и выполнять команды на этой машине": "execution: on — steno may write files and run commands on this machine",
	"исполнение: выключено — ТЗ собираются, ветки не заводятся":                          "execution: off — specs are written, branches are not",
	"ТЗ: собираются сами после каждого разбора":                                          "specs: written on their own after every write-up",
	"ТЗ: по запросу (steno spec <задача>, кнопка в панели)":                              "specs: on request (steno spec <task>, the button in the panel)",
	"исполнитель: не выбран — ":                                                          "executor: not chosen — ",
	"исполнитель: %s — %s":              "executor: %s — %s",
	"исполнитель: %s":                   "executor: %s",
	"ветки: %s…, рабочие копии в %s":    "branches: %s…, worktrees in %s",
	"раздел agent в %s не читается: %w": "the agent section in %s cannot be read: %w",
	"ожидался объект JSON":              "expected a JSON object",
	"ожидался ключ":                     "expected a key",

	// --- doctor ---
	"агент":                "agent",
	", ТЗ собираются сами": ", specs are written on their own",
	"выключен — ТЗ собираются, ветки не заводятся": "off — specs are written, branches are not",
	"→ включить: steno agent on":                   "→ turn on: steno agent on",
	"включён, но исполнять некому":                 "on, but there is nobody to execute",
	"включён, исполняет %s, ветки %s…":             "on, %s executes, branches %s…",

	// --- мастер ---
	"Задачи — агенту": "Tasks to an agent",
	"Задачу с созвона steno умеет превратить в ТЗ по репозиторию проекта, а ТЗ —":      "steno can turn a task from a meeting into a spec written from the project's repository, and the spec —",
	"отдать Claude Code или Codex: отдельная рабочая копия, своя ветка, никогда push.": "hand to Claude Code or Codex: a worktree of its own, a branch of its own, never a push.",
	"Оба выключателя меняются потом: steno agent on|off, steno agent auto on|off.":     "Both switches can be changed later: steno agent on|off, steno agent auto on|off.",
	"Собирать ТЗ самому после каждого разбора? (чтение кода и запрос к модели)":        "Write specs on its own after every write-up? (reading the code and one model call)",
	"Разрешить агенту работать в репозитории на этой машине?":                          "Allow the agent to work in the repository on this machine?",
	"исполняет ":          "executed by ",
	"; ветки ":            "; branches ",
	"…, рабочие копии в ": "…, worktrees in ",

	// --- панель ---
	"уже собираю": "already building",

	// --- автосборка ---
	"ТЗ после разбора: %v":                                      "specs after the write-up: %v",
	"ТЗ после разбора: не прочитал открытые пункты: %v":         "specs after the write-up: could not read the open items: %v",
	"ТЗ после разбора: задач без проекта — %d, собирать нечего": "specs after the write-up: %d tasks without a project, nothing to write",
	"ТЗ после разбора: задач с проектом — %d, собираю":          "specs after the write-up: %d tasks with a project, writing",

	// --- steno ui ---
	"ТЗ":             "Spec",
	"ТЗ ":            "Spec ",
	"агенту":         "to agent",
	"перечитать":     "reload",
	"собрать заново": "rebuild",
	"ТЗ по задаче: открыть, а если его нет — собрать по репозиторию проекта":                 "the task's spec: open it, or write it from the project's repository if there is none",
	"отдать агенту: рабочая копия, ветка, коммит — после вопроса «точно?»":                   "hand to the agent: a worktree, a branch, a commit — after a “sure?”",
	"собрать заново — после того, как на вопросы ответили":                                   "rebuild — once the questions were answered",
	"перечитать: пока агент работает, растёт журнал":                                         "reload: while the agent works, the log grows",
	"%s — не задача, а %s: исполнять нечего":                                                 "%s is not a task but a %s: there is nothing to carry out",
	"%s закрыт — ТЗ собирают по открытым задачам":                                            "%s is closed — specs are written for open tasks",
	"у %s не определён проект — сначала отнеси задачу к проекту (панель или steno projects)": "%s has no project — assign the task to a project first (the panel or steno projects)",
	"ТЗ по %s уже собирается":                                                                "the spec for %s is already being written",
	"собираю ТЗ по %s — это чтение репозитория и запрос к модели, около минуты":              "writing the spec for %s — reading the repository and one model call, about a minute",
	"ТЗ по %s готово (%s), но работать по нему нельзя — t покажет почему":                    "the spec for %s is written (%s), but no agent may run from it — t shows why",
	"ТЗ по %s готово: %s, вопросов — %d; t — открыть":                                        "the spec for %s is written: %s, %d open questions; t opens it",
	"перечитал": "reloaded",
	"исполнение выключено — включить: steno agent on":                                             "execution is off — turn on: steno agent on",
	"по этому ТЗ нельзя работать: %s":                                                             "no agent may run from this spec: %s",
	"Отдать %s исполнителю %s? Отдельная рабочая копия, ветка %s…, коммит. Push не делает никто.": "Hand %s to %s? A worktree of its own, a branch %s…, a commit. Nobody pushes.",
	"терминал": "terminal",
	"отдал %s агенту — ход работы виден в карточке (r обновит), готовая ветка придёт сюда": "handed %s to the agent — progress shows in the card (r reloads), the branch will be announced here",
	"%s: готово, ветка %s в %s":                                          "%s: done, branch %s in %s",
	"%s: агент отработал":                                                "%s: the agent has finished",
	"ТЗ: собирается…":                                                    "spec: being written…",
	"ТЗ нет — t соберёт по репозиторию проекта":                          "no spec — t writes one from the project's repository",
	"ТЗ %s: задача не взята — %s":                                        "spec %s: task declined — %s",
	"ТЗ %s: агент работает, ветка %s — t покажет ход":                    "spec %s: the agent is working, branch %s — t shows progress",
	"ТЗ %s: сделано, ветка %s — t откроет":                               "spec %s: done, branch %s — t opens it",
	"ТЗ %s: сорвалось — %s":                                              "spec %s: failed — %s",
	"ТЗ %s есть, но работать по нему нельзя — t покажет почему":          "spec %s exists, but no agent may run from it — t shows why",
	"ТЗ %s готово, вопросов — %d; t откроет, a в карточке отдаст агенту": "spec %s is written, %d open questions; t opens it, a in the card hands it to the agent",
	"ТЗ не открыто":                                                      "no spec is open",
	"Задача не взята: ":                                                  "Task declined: ",
	"Исполнение":                                                         "Execution",
	" · ветка ":                                                          " · branch ",
	" · запустил: ":                                                      " · started by: ",
	"  посмотреть: ":                                                     "  review: ",
	"агент работает":                                                     "the agent is working",
	"агент отработал":                                                    "the agent has finished",
	"агент сорвался":                                                     "the agent failed",
}
