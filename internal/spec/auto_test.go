package spec

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/sur1cat/steno/internal/core"
)

// Автосборка берёт только то, по чему ТЗ вообще возможно: задачи этого
// созвона, с проектом, у проекта есть каталог с кодом. Всё остальное
// пропускается без единого запроса к модели — за отказ по каждой задаче без
// проекта после каждого созвона платить незачем.
func TestAutoBuildPicksOnlyTasksWithAProject(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("скрипт модели на sh")
	}
	d := testDeps(t)
	// Модель — скрипт, который на всё отвечает «не про код»: так Build
	// доходит до отбора и отказывает дёшево, а тест видит, сколько раз к
	// модели вообще сходили.
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho x >> " + strconv.Quote(calls) + "\n" +
		`echo '{"text":"{\"verdict\":\"talk\",\"why\":\"это разговор, не код\"}"}'` + "\n"
	must(t, os.WriteFile(filepath.Join(dir, "model.sh"), []byte(script), 0o755))
	d.Cfg.Brain.Provider = core.ProviderCommand
	d.Cfg.LLM.Cmd = []string{filepath.Join(dir, "model.sh")}

	repo := t.TempDir()
	must(t, d.St.SaveProject(core.Project{Name: "такси",
		Sources: []core.Source{{Kind: "path", Value: repo}}}))
	items := []core.ProjectItem{
		{ID: "T-1", Project: "такси", Kind: core.KindTask, Text: "закончить сапар", OpenedIn: "m-1"},
		{ID: "T-2", Project: core.UnassignedProject, Kind: core.KindTask, Text: "без проекта", OpenedIn: "m-1"},
		{ID: "T-3", Project: "такси", Kind: core.KindTask, Text: "с другого созвона", OpenedIn: "m-0"},
		{ID: "Q-1", Project: "такси", Kind: core.KindQuestion, Text: "вопрос", OpenedIn: "m-1"},
	}
	for _, it := range items {
		must(t, d.St.AddItem(it))
	}
	lg := log.New(io.Discard, "", 0)

	// Выключено — ничего не происходит, к модели не ходим.
	if built, skipped := AutoBuild(context.Background(), d, Settings{}, "m-1", lg); built != 0 || skipped != 0 {
		t.Fatalf("при выключенной автосборке что-то произошло: %d/%d", built, skipped)
	}
	if _, err := os.Stat(calls); err == nil {
		t.Fatal("к модели сходили при выключенной автосборке")
	}

	built, skipped := AutoBuild(context.Background(), d, Settings{AutoSpec: true}, "m-1", lg)
	// T-1 — единственная задача этого созвона с проектом: по ней сходили к
	// модели и получили отказ; T-2 без проекта пропущена молча; T-3 с другого
	// созвона и вопрос Q-1 не трогаются.
	if built != 0 || skipped != 2 {
		t.Errorf("собрано %d, пропущено %d; ждали 0 и 2", built, skipped)
	}
	raw, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("к модели не сходили ни разу: %v", err)
	}
	if n := len(raw); n != 2 {
		t.Errorf("обращений к модели %d, ждали 1 (одна задача — один отбор)", n)
	}
	latest, err := d.Sp.Latest("")
	if err != nil {
		t.Fatal(err)
	}
	if sp := latest["T-1"]; sp == nil || sp.Status != StatusRejected {
		t.Errorf("отказ по T-1 не сохранён: %+v", latest)
	}
	if latest["T-2"] != nil || latest["T-3"] != nil {
		t.Errorf("ТЗ завелось по тому, что брать нельзя: %+v", latest)
	}
}

// AfterFollowup читает настройку из того же файла, что назван в конфиге,
// и без auto_spec не делает ничего — даже базу не трогает.
func TestAfterFollowupReadsSettingsFromConfigFile(t *testing.T) {
	d := testDeps(t)
	path := filepath.Join(t.TempDir(), "steno.json")
	write(t, path, `{"agent": {"auto_spec": false}}`)
	d.Cfg.Path = path
	AfterFollowup(context.Background(), d.Cfg, d.St, "m-1")
	if set := SettingsFor(d.Cfg); set.AutoSpec {
		t.Fatalf("настройка прочиталась не из файла: %+v", set)
	}
	write(t, path, `{"agent": {"auto_spec": true}}`)
	if set := SettingsFor(d.Cfg); !set.AutoSpec {
		t.Fatalf("правка файла не подхватилась без перезапуска: %+v", set)
	}
}
