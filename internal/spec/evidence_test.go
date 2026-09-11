package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sur1cat/steno/internal/core"
)

// Транслитерация — не украшение, а весь смысл поиска по коду. На созвоне
// сказали «сапар», в репозитории лежит sapar/: без перевода в латиницу ТЗ не
// найдёт ни одного файла и начнёт их придумывать.
func TestNeedlesTranslitAndStem(t *testing.T) {
	got := Needles("Анвару нужно закончить сапар, разобрать на синхронные запросы")
	if !contains(got, "sapar") {
		t.Fatalf("не вышло sapar из «сапар»: %v", got)
	}
	// Стоп-слова не должны съедать место в списке.
	for _, bad := range []string{"nuzhno", "zakonchit"} {
		if contains(got, bad) {
			t.Errorf("стоп-слово попало в поиск: %q в %v", bad, got)
		}
	}
}

func TestNeedlesCutsRussianEndings(t *testing.T) {
	cases := map[string]string{
		"сапаром":   "sapar",
		"конкурсам": "konkurs",
		"водители":  "voditel",
	}
	for word, want := range cases {
		got := Needles(word)
		if len(got) != 1 || got[0] != want {
			t.Errorf("Needles(%q) = %v, ждали [%s]", word, got, want)
		}
	}
}

// Латиницу трогать нельзя: «worker_sapar», сказанное вслух как есть, обязано
// искаться как есть.
func TestNeedlesKeepsLatin(t *testing.T) {
	got := Needles("посмотри worker_sapar")
	if !contains(got, "worker_sapar") {
		t.Fatalf("латинское слово потерялось: %v", got)
	}
}

// Слишком короткая основа — это шум: «мдс» найдётся в половине файлов.
func TestNeedlesDropsShort(t *testing.T) {
	if got := Needles("мдс и ws"); len(got) != 0 {
		t.Fatalf("короткие слова попали в поиск: %v", got)
	}
}

func TestMatchPathsShallowFirstAndNoNoise(t *testing.T) {
	dir := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(dir, "sapar", "migrations"), 0o755))
	must(t, os.MkdirAll(filepath.Join(dir, "__pycache__"), 0o755))
	must(t, os.MkdirAll(filepath.Join(dir, ".claude", "worktrees"), 0o755))
	must(t, os.MkdirAll(filepath.Join(dir, "apps"), 0o755))
	// Лежит глубже, а по алфавиту раньше: без сортировки по глубине всплыл бы
	// он, а не сам модуль.
	write(t, filepath.Join(dir, "apps", "sapar_config.py"), "x")
	write(t, filepath.Join(dir, "sapar", "tasks.py"), "x")
	write(t, filepath.Join(dir, "sapar", "migrations", "0007_sapardoc.py"), "x")
	write(t, filepath.Join(dir, "__pycache__", "sapar.pyc"), "x")
	write(t, filepath.Join(dir, ".claude", "worktrees", "sapar-old.txt"), "x")

	got := matchPaths(dir, []string{"sapar"})
	if len(got) == 0 || got[0] != "sapar/" {
		t.Fatalf("самый мелкий путь должен быть первым, вышло %v", got)
	}
	for _, p := range got {
		if strings.Contains(p, "__pycache__") || strings.Contains(p, ".claude") {
			t.Errorf("в материал попал мусор: %s", p)
		}
	}
}

// Пустой результат поиска — сведение, а не пустое место: ТЗ, написанное поверх
// него, не опирается на код вообще, и об этом обязаны узнать и модель, и человек.
func TestEvidenceRenderSaysWhenNothingFound(t *testing.T) {
	ev := Evidence{Repo: "/tmp/x", Needles: []string{"sapar"}}
	if !strings.Contains(ev.Render(), "не нашлось НИЧЕГО") {
		t.Fatalf("пустой поиск не назван вслух:\n%s", ev.Render())
	}
}

func TestVerifyWaysOnlyReportsWhatExists(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "manage.py"), "x")
	write(t, filepath.Join(dir, "Makefile"), "test:\n\tpytest\nlint:\n\truff\n")
	got := strings.Join(verifyWays(dir), "\n")
	if !strings.Contains(got, "manage.py") || !strings.Contains(got, "test, lint") {
		t.Fatalf("не нашли то, что лежит на диске: %q", got)
	}
	if strings.Contains(got, "go.mod") || strings.Contains(got, "package.json") {
		t.Fatalf("выдумали проверки, которых нет: %q", got)
	}
}

// Проект без каталога с кодом — повод отказаться от ТЗ, а не писать его по
// одной справке.
func TestRepoOfRefusesMissingDir(t *testing.T) {
	p := core.Project{Name: "нет", Sources: []core.Source{{Kind: "path", Value: "/no/such/dir/here"}}}
	if _, err := RepoOf(t.TempDir(), p); err == nil {
		t.Fatal("несуществующий каталог принят за репозиторий")
	}
}

func TestRepoOfTakesExistingPath(t *testing.T) {
	dir := t.TempDir()
	p := core.Project{Name: "есть", Sources: []core.Source{
		{Kind: "path", Value: "/no/such/dir"},
		{Kind: "path", Value: dir},
	}}
	got, err := RepoOf(t.TempDir(), p)
	if err != nil || got != dir {
		t.Fatalf("RepoOf = %q, %v; ждали %q", got, err, dir)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	must(t, os.WriteFile(path, []byte(body), 0o644))
}
