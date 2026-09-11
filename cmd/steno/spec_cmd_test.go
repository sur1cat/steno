package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/spec"
)

// specCLI разворачивает конфиг и базу так, как их видит команда: `steno spec`
// ходит через open(), а не через переданное хранилище.
func specCLI(t *testing.T, agentSection string) (cfgPath string, st *core.Store) {
	t.Helper()
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	cfgPath = filepath.Join(dir, "steno.json")
	body := `{"data_dir":` + quote(data) + agentSection + `}`
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := core.OpenStore(data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return cfgPath, st
}

func TestSpecListShowsTasksWithoutSpec(t *testing.T) {
	cfgPath, st := specCLI(t, "")
	if err := st.AddItem(core.ProjectItem{
		ID: "T-1", Project: "такси", Kind: core.KindTask,
		Text: "Закончить сапар", Owner: "Ануар"}); err != nil {
		t.Fatal(err)
	}
	// Решение — не задача: исполнять в нём нечего, и в списке его быть не должно.
	if err := st.AddItem(core.ProjectItem{
		ID: "D-1", Project: "такси", Kind: core.KindDecision, Text: "Берём sqlite"}); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if err := cmdSpec(context.Background(), []string{"-c", cfgPath}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "T-1") || !strings.Contains(out, "ТЗ нет") {
		t.Fatalf("задача без ТЗ не показана:\n%s", out)
	}
	if strings.Contains(out, "D-1") {
		t.Fatalf("решение попало в список задач:\n%s", out)
	}
}

// Требование первое, со стороны терминала: пока не включено, `steno spec run`
// объясняет, что это осознанная настройка, и не запускает ничего.
func TestSpecRunRefusedWhenDisabled(t *testing.T) {
	cfgPath, st := specCLI(t, "")
	store, err := spec.Open(st)
	if err != nil {
		t.Fatal(err)
	}
	sp := &spec.Spec{
		ID: "S-1", ItemID: "T-1", Project: "такси", Repo: t.TempDir(),
		Status: spec.StatusDraft, Title: "проба",
		Places:   []spec.Place{{Path: "a.py", Found: true}},
		Steps:    []string{"сделать"},
		Unknowns: []spec.Unknown{{Question: "что именно?", Why: "не сказано", Ask: "автор"}},
	}
	if err := store.Save(sp); err != nil {
		t.Fatal(err)
	}
	err = cmdSpec(context.Background(), []string{"-c", cfgPath, "run", "S-1"})
	if err == nil || !strings.Contains(err.Error(), "исполнение выключено") {
		t.Fatalf("запуск при выключенной настройке: %v", err)
	}
}

// А при включённой — упирается в само ТЗ, а не в настройку: у этого нет ни
// одного открытого вопроса, значит, оно придумано.
func TestSpecRunRefusesUngatedSpec(t *testing.T) {
	cfgPath, st := specCLI(t, `,"agent":{"enabled":true}`)
	store, err := spec.Open(st)
	if err != nil {
		t.Fatal(err)
	}
	sp := &spec.Spec{
		ID: "S-2", ItemID: "T-2", Project: "такси", Repo: t.TempDir(),
		Status: spec.StatusDraft, Title: "проба",
		Places: []spec.Place{{Path: "a.py", Found: true}},
		Steps:  []string{"сделать"},
	}
	if err := store.Save(sp); err != nil {
		t.Fatal(err)
	}
	err = cmdSpec(context.Background(), []string{"-c", cfgPath, "run", "S-2"})
	if err == nil || !strings.Contains(err.Error(), "открытого вопроса") {
		t.Fatalf("запуск по придуманному ТЗ: %v", err)
	}
}

func TestSpecShowPrintsMissingSection(t *testing.T) {
	cfgPath, st := specCLI(t, "")
	store, err := spec.Open(st)
	if err != nil {
		t.Fatal(err)
	}
	sp := &spec.Spec{ID: "S-3", ItemID: "T-3", Project: "такси",
		Status: spec.StatusDraft, Title: "проба"}
	if err := store.Save(sp); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if err := cmdSpec(context.Background(), []string{"-c", cfgPath, "show", "S-3"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "Чего не хватает") {
		t.Fatalf("обязательный раздел не напечатан:\n%s", out)
	}
	if !strings.Contains(out, "нельзя запускать агента") {
		t.Fatalf("негодность ТЗ не названа:\n%s", out)
	}
}
