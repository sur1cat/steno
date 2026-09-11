package spec

import (
	"context"
	"log"
	"strings"

	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
)

// Автоматическая сборка ТЗ — после разбора созвона или заметки.
//
// Разделение здесь то же, что и во всём пакете, и оно принципиальное: ТЗ
// собирается само, исполнение — по кнопке. Сборка ничего не пишет в
// репозиторий и ничего не запускает: это чтение кода и запрос к модели, и
// цена ей — центы. Поэтому её можно доверить автомату; а вот ветку заводит
// только человек, и AutoBuild к Run не прикасается — за этим следит
// TestRunUnreachableFromInbound, в список которого входят и конвейеры.
//
// Что берётся: задачи, появившиеся на этом созвоне и легшие в проект с
// репозиторием на этой машине. «Не определён» пропускается молча — по нему
// Build и так откажет, а платить за отказ по каждой задаче без проекта после
// каждого созвона незачем.

// AutoBuild собирает ТЗ по задачам, открытым созвоном meetingID. Возвращает,
// сколько собрано и сколько задач не взято. Ошибки не роняют разбор: созвон
// уже опубликован, и сорванное ТЗ — не повод помечать его сорвавшимся.
func AutoBuild(ctx context.Context, d Deps, set Settings, meetingID string, lg *log.Logger) (built, skipped int) {
	if !set.AutoSpec || strings.TrimSpace(meetingID) == "" {
		return 0, 0
	}
	if lg == nil {
		lg = log.Default()
	}
	items, err := d.St.OpenItems("")
	if err != nil {
		lg.Printf(i18n.Tr("ТЗ после разбора: не прочитал открытые пункты: %v"), err)
		return 0, 0
	}
	var todo []core.ProjectItem
	for _, it := range items {
		if it.OpenedIn != meetingID || it.Kind != core.KindTask {
			continue
		}
		if it.Project == core.UnassignedProject || strings.TrimSpace(it.Project) == "" {
			skipped++
			continue
		}
		todo = append(todo, it)
	}
	if len(todo) == 0 {
		if skipped > 0 {
			lg.Printf(i18n.Tr("ТЗ после разбора: задач без проекта — %d, собирать нечего"), skipped)
		}
		return 0, skipped
	}
	SortItems(todo)
	lg.Printf(i18n.Tr("ТЗ после разбора: задач с проектом — %d, собираю"), len(todo))
	for _, it := range todo {
		if ctx.Err() != nil {
			return built, skipped
		}
		sp, err := Build(ctx, d, it.ID)
		switch {
		case err != nil:
			lg.Printf(i18n.Tr("ТЗ по %s: %v"), it.ID, err)
		case sp.Status == StatusRejected:
			skipped++
			lg.Printf(i18n.Tr("ТЗ по %s: задача не взята — %s"), it.ID, sp.Reject)
		default:
			built++
			lg.Printf(i18n.Tr("ТЗ по %s готово: %s, вопросов — %d"), it.ID, sp.ID, len(sp.Unknowns))
		}
	}
	return built, skipped
}

// AfterFollowup — то, что зовут конвейеры созвона и заметки, когда разбор
// сохранён и разослан. Всё, что нужно, собирает сам: настройки — из того
// конфига, с которым идёт разбор, хранилище — над той же базой.
func AfterFollowup(ctx context.Context, cfg *core.Config, st *core.Store, meetingID string) {
	set := SettingsFor(cfg)
	if !set.AutoSpec {
		return
	}
	store, err := Open(st)
	if err != nil {
		log.Printf(i18n.Tr("ТЗ после разбора: %v"), err)
		return
	}
	AutoBuild(ctx, Deps{Cfg: cfg, St: st, Sp: store}, set, meetingID, log.Default())
}
