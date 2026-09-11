package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
	"github.com/sur1cat/steno/internal/spec"
)

// `steno spec` — ТЗ по задаче с созвона и, отдельной командой, работа по нему.
//
// Отдельным файлом, чтобы main.go не разрастался: там сейчас пишут ещё двое.
// Всё, что нужно добавить туда, — одна строка в switch и три в справке.
//
// Почему исполнение живёт в терминале и в панели, но не в источниках: запускать
// агента имеет право только человек. Команда, набранная руками, и кнопка,
// нажатая в панели с паролем, — это человек. Сообщение в телеграме, письмо и
// запрос на HTTP-вход — нет, кто бы их ни отправил.

const specUsage = `steno spec              — ТЗ по задачам с созвонов

  steno spec                 показать, что уже собрано
  steno spec <id-задачи>     собрать ТЗ по задаче (T-3f2a из steno projects)
  steno spec show <id-ТЗ>    показать ТЗ целиком
  steno spec run <id-ТЗ>     отдать ТЗ агенту: рабочая копия, ветка, коммит

Флаги:
  --project <имя>            только по этому проекту
  --all                      собрать ТЗ по всем открытым задачам проекта
  -c <path>                  путь к steno.json
`

func cmdSpec(ctx context.Context, args []string) error {
	fs := newFlagSet("spec")
	cfgPath := setupFlags(fs)
	project := fs.String("project", "", i18n.Tr("только по этому проекту"))
	all := fs.Bool("all", false, i18n.Tr("собрать ТЗ по всем открытым задачам проекта"))
	rest, err := parseArgs(fs, args)
	if err != nil {
		return err
	}

	cfg, st, err := open(*cfgPath)
	if err != nil {
		return err
	}
	defer st.Close()
	store, err := spec.Open(st)
	if err != nil {
		return err
	}
	d := spec.Deps{Cfg: cfg, St: st, Sp: store}

	switch {
	case len(rest) >= 1 && (rest[0] == "help" || rest[0] == "-h"):
		fmt.Print(i18n.Tr(specUsage))
		return nil
	case len(rest) >= 2 && rest[0] == "show":
		return specShow(store, rest[1])
	case len(rest) >= 2 && rest[0] == "run":
		return specRun(ctx, d, *cfgPath, rest[1])
	case len(rest) >= 1:
		return specBuild(ctx, d, rest[0])
	case *all:
		return specBuildAll(ctx, d, *project)
	}
	return specList(st, store, *project)
}

// specList — что уже есть. Первое, что человек делает, вернувшись к этой части
// через неделю: смотрит, по каким задачам ТЗ собрано и что с ним.
func specList(st *core.Store, store *spec.Store, project string) error {
	items, err := st.OpenItems(project)
	if err != nil {
		return err
	}
	latest, err := store.Latest(project)
	if err != nil {
		return err
	}
	spec.SortItems(items)
	shown := 0
	for _, it := range items {
		if it.Kind != core.KindTask {
			continue
		}
		shown++
		fmt.Printf("%-8s %-14s %s\n", it.ID, i18n.Cut(it.Project, 14), i18n.Cut(it.Text, 70))
		sp, ok := latest[it.ID]
		if !ok {
			fmt.Printf("         %s\n", i18n.Trf("ТЗ нет — сделать: steno spec %s", it.ID))
			continue
		}
		fmt.Printf("         %s %s\n", sp.ID, specLine(sp))
	}
	if shown == 0 {
		fmt.Println(i18n.Tr("открытых задач нет"))
	}
	return nil
}

func specLine(sp *spec.Spec) string {
	switch sp.Status {
	case spec.StatusRejected:
		return i18n.Trf("не взято: %s", i18n.Cut(sp.Reject, 90))
	case spec.StatusRunning:
		return i18n.Trf("идёт работа, ветка %s", sp.Branch)
	case spec.StatusDone:
		return i18n.Trf("сделано, ветка %s", sp.Branch)
	case spec.StatusFailed:
		return i18n.Trf("сорвалось: %s", i18n.Cut(sp.RunError, 90))
	}
	if r := sp.Gate(); len(r) > 0 {
		return i18n.Trf("ТЗ есть, но работать по нему нельзя: %s", r[0])
	}
	return i18n.Trf("ТЗ готово, вопросов в нём — %d", len(sp.Unknowns))
}

func specBuild(ctx context.Context, d spec.Deps, itemID string) error {
	sp, err := spec.Build(ctx, d, itemID)
	if err != nil {
		return err
	}
	if sp.Status == spec.StatusRejected {
		fmt.Printf(i18n.Tr("%s — задача не взята: %s\n"), itemID, sp.Reject)
		return nil
	}
	fmt.Print(sp.Render())
	fmt.Printf(i18n.Tr("\n%s · %s · $%.4f\n"), sp.ID, sp.Model, sp.USD)
	return nil
}

// specBuildAll — ТЗ по всем открытым задачам. Дорогая команда, поэтому отбор в
// ней и важен: за задачу, которая кодом не делается, платится только короткий
// запрос отбора, а не полный разбор репозитория.
func specBuildAll(ctx context.Context, d spec.Deps, project string) error {
	items, err := d.St.OpenItems(project)
	if err != nil {
		return err
	}
	spec.SortItems(items)
	var total float64
	for _, it := range items {
		if it.Kind != core.KindTask {
			continue
		}
		sp, err := spec.Build(ctx, d, it.ID)
		if err != nil {
			fmt.Printf("%s: %v\n", it.ID, err)
			continue
		}
		total += sp.USD
		fmt.Printf("%-8s %s\n", it.ID, specLine(sp))
	}
	fmt.Printf(i18n.Tr("\nвсего потрачено $%.4f\n"), total)
	return nil
}

func specShow(store *spec.Store, id string) error {
	sp, err := store.Get(id)
	if err != nil {
		return err
	}
	if sp.Status == spec.StatusRejected {
		fmt.Printf(i18n.Tr("задача не взята: %s\n"), sp.Reject)
		return nil
	}
	fmt.Print(sp.Render())
	if sp.RunLog != "" {
		fmt.Printf(i18n.Tr("\n## Что делал агент (ветка %s)\n\n%s\n"), sp.Branch, sp.RunLog)
	}
	return nil
}

// specRun — единственный путь к исполнению из терминала.
//
// Имя того, кто запустил, берётся из окружения, а не из аргумента: это ответ на
// вопрос «кто нажал», и подставлять его из командной строки — значит разрешить
// подставить что угодно. В журнале и в базе останется тот, кто сидел за этой
// машиной.
func specRun(ctx context.Context, d spec.Deps, cfgPath, id string) error {
	set := spec.LoadSettings(core.ResolveConfigPath(cfgPath))
	if !set.Enabled {
		return errors.New(i18n.Tr("исполнение выключено. Это осознанная настройка: по ней steno получает право писать файлы и выполнять команды на этой машине.\n  → добавь в steno.json: \"agent\": {\"enabled\": true}"))
	}
	by := core.FirstNonEmpty(os.Getenv("USER"), os.Getenv("USERNAME"), "терминал")
	sp, err := spec.Run(ctx, d, set, id, by, func(kind, text string) {
		fmt.Printf("  %-5s %s\n", kind, strings.TrimSpace(text))
	})
	if err != nil {
		return err
	}
	fmt.Printf(i18n.Tr("\nготово: ветка %s в %s\n"), sp.Branch, sp.Repo)
	fmt.Printf(i18n.Tr("посмотреть: git -C %s diff ..%s\n"), sp.Repo, sp.Branch)
	return nil
}
