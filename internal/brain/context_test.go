package brain

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sur1cat/steno/internal/core"
)

// Сбор материала — половина справки, и он работает без ключа Claude.
// Проверяем на настоящем репозитории: этом самом.
func TestGatherSourcesFromRepo(t *testing.T) {
	dir := t.TempDir()
	p := core.Project{
		Name:    "steno",
		Aliases: []string{"стено"},
		Sources: []core.Source{
			{Kind: "text", Value: "Заметки и follow-up с созвонов"},
			// Корень репозитория: тест смотрит на этот самый проект, а сам
			// лежит на два каталога глубже.
			{Kind: "path", Value: "../.."},
		},
	}
	material, fp, err := GatherSources(context.Background(), dir, p)
	if err != nil {
		t.Fatal(err)
	}
	if fp == "" {
		t.Error("нет отпечатка материала — пересборка не сможет понять, что менялось")
	}

	for _, want := range []string{
		"Заметки и follow-up с созвонов", // текстовый источник
		"### README", // README подхватился
		"### go.mod", // манифест
		"### Состав", // структура каталогов
		// Раздела коммитов здесь нет намеренно: этот каталог — не git-репозиторий,
		// и такой случай должен обрабатываться молча. Коммиты проверяет
		// TestGatherSourcesReadsCommits.
	} {
		if !strings.Contains(material, want) {
			t.Errorf("в материале нет раздела %q", want)
		}
	}
	// Мусор в справку попадать не должен.
	for _, bad := range []string{"node_modules", "/.git/"} {
		if strings.Contains(material, bad) {
			t.Errorf("в материал попал %q", bad)
		}
	}
	// Объём ограничен: четыре проекта по мегабайту не влезут в промпт.
	if n := len([]rune(material)); n > 40000 {
		t.Errorf("материал %d символов — слишком много для промпта", n)
	}
	t.Logf("материал: %d символов, отпечаток %s", len([]rune(material)), fp)
}

// Темы коммитов — самая полезная часть материала: в них лежит словарь, которым
// команда говорит о проекте вслух. Каталог без git обрабатывается молча, без
// этого раздела, — но там, где git есть, раздел обязан появиться.
func TestGatherSourcesReadsCommits(t *testing.T) {
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "README.md"),
		[]byte("# Платежи\n\nПриём денег и подписки."), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "вебхуки провайдера: разбор повторной доставки")
	run("commit", "-q", "--allow-empty", "-m", "миграция схемы подписок")

	material, _, err := GatherSources(context.Background(), t.TempDir(),
		core.Project{Name: "Платежи", Sources: []core.Source{{Kind: "path", Value: repo}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(material, "### Последние коммиты") {
		t.Fatal("раздел коммитов не появился на настоящем репозитории")
	}
	for _, want := range []string{"вебхуки провайдера", "миграция схемы подписок", "Приём денег"} {
		if !strings.Contains(material, want) {
			t.Errorf("в материале нет %q", want)
		}
	}
}

// Отпечаток должен меняться вместе с материалом — иначе справка застынет.
func TestFingerprintTracksSources(t *testing.T) {
	dir := t.TempDir()
	a := core.Project{Name: "x", Sources: []core.Source{{Kind: "text", Value: "первое"}}}
	b := core.Project{Name: "x", Sources: []core.Source{{Kind: "text", Value: "второе"}}}
	_, fpA, err := GatherSources(context.Background(), dir, a)
	if err != nil {
		t.Fatal(err)
	}
	_, fpB, err := GatherSources(context.Background(), dir, b)
	if err != nil {
		t.Fatal(err)
	}
	if fpA == fpB {
		t.Error("отпечаток не изменился при смене материала")
	}
}

func TestGatherSourcesRejectsUnknownKind(t *testing.T) {
	_, _, err := GatherSources(context.Background(), t.TempDir(),
		core.Project{Name: "x", Sources: []core.Source{{Kind: "магия", Value: "?"}}})
	if err == nil {
		t.Fatal("непонятный источник принят молча")
	}
}
