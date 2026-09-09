package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitRepo(t *testing.T) (string, func(...string)) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Боря", "GIT_AUTHOR_EMAIL=b@t",
			"GIT_COMMITTER_NAME=Боря", "GIT_COMMITTER_EMAIL=b@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	run("init", "-q")
	return dir, run
}

// Разово прочитанный репозиторий устаревает: бот начинает рассуждать о проекте
// по прошлогоднему коду, а новых коммитов — тех, по которым видно закрытые
// задачи, — не видит вовсе.
func TestSyncSeesOnlyNewCommits(t *testing.T) {
	repo, run := gitRepo(t)
	run("commit", "-q", "--allow-empty", "-m", "первый, до того как steno узнал о проекте")

	st, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	src := Source{Kind: "path", Value: repo}
	ctx := context.Background()

	// Первая сверка: историю задним числом не разбираем.
	res, err := syncRepo(ctx, t.TempDir(), "Платежи", src, st)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Commits) != 0 {
		t.Fatalf("на первой сверке разобрали старую историю: %+v", res.Commits)
	}
	if res.Head == "" {
		t.Fatal("не запомнили, докуда прочитали")
	}

	// Ничего не поменялось — и сверять нечего.
	if res, err := syncRepo(ctx, t.TempDir(), "Платежи", src, st); err != nil {
		t.Fatal(err)
	} else if len(res.Commits) != 0 {
		t.Fatalf("без новых коммитов вернулось %d", len(res.Commits))
	}

	// Появились два коммита — ровно они и должны приехать.
	run("commit", "-q", "--allow-empty", "-m", "миграция схемы подписок")
	run("commit", "-q", "--allow-empty", "-m", "вебхуки: разбор повторной доставки")

	res, err = syncRepo(ctx, t.TempDir(), "Платежи", src, st)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Commits) != 2 {
		t.Fatalf("ожидали 2 новых коммита, получили %d: %+v", len(res.Commits), res.Commits)
	}
	subjects := res.Commits[0].Subject + " | " + res.Commits[1].Subject
	if !strings.Contains(subjects, "миграция схемы подписок") ||
		!strings.Contains(subjects, "вебхуки") {
		t.Fatalf("темы коммитов: %q", subjects)
	}
	if res.Commits[0].Author != "Боря" {
		t.Errorf("автор потерялся: %q", res.Commits[0].Author)
	}
	if len(res.Commits[0].Short()) != 8 {
		t.Errorf("короткий хеш: %q", res.Commits[0].Short())
	}

	// И снова ничего: отметка сдвинулась.
	if res, err := syncRepo(ctx, t.TempDir(), "Платежи", src, st); err != nil {
		t.Fatal(err)
	} else if len(res.Commits) != 0 {
		t.Fatalf("те же коммиты приехали повторно: %d", len(res.Commits))
	}
}

// Локальный каталог — рабочая копия человека, и делать в ней pull значит
// влезать в чужую работу. Читаем как есть.
func TestSyncDoesNotTouchLocalWorkingCopy(t *testing.T) {
	repo, run := gitRepo(t)
	run("commit", "-q", "--allow-empty", "-m", "первый")
	dirty := filepath.Join(repo, "черновик.txt")
	if err := os.WriteFile(dirty, []byte("несохранённая работа"), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, err := syncRepo(context.Background(), t.TempDir(), "X",
		Source{Kind: "path", Value: repo}, st); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dirty); err != nil {
		t.Fatal("сверка снесла несохранённую работу в рабочей копии")
	}
}

func TestSyncOnNonRepoIsAnError(t *testing.T) {
	st, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := syncRepo(context.Background(), t.TempDir(), "X",
		Source{Kind: "path", Value: t.TempDir()}, st); err == nil {
		t.Fatal("каталог без git принят молча")
	}
}

// Сводка — то, что человек читает раз в сутки.
func TestSyncDigestText(t *testing.T) {
	d := syncDigest{
		Project: "Платежи", Commits: 12, StillOpen: 3,
		Closed: []string{"закончить миграцию — Боря\n    коммит a1b2c3d4 «миграция схемы»\n    коммит делает ровно это"},
		Maybe:  []string{"проверить откат — Дима\n    коммит e5f6a7b8 «начал проверку»\n    работа начата, но не закончена"},
	}
	out := d.text()
	for _, want := range []string{
		"Платежи — за сутки 12 коммитов",
		"Закрыто коммитами:",
		"Похоже, сделано — но не закрывал:",
		"Ещё открыто: 3 задачи",
		"a1b2c3d4",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("в сводке нет %q:\n%s", want, out)
		}
	}
	if (syncDigest{Project: "X", Commits: 5}).empty() != true {
		t.Error("сводку без находок надо считать пустой и не слать")
	}
}

func TestPluralAgreement(t *testing.T) {
	for n, want := range map[int]string{
		1: "1 коммит", 2: "2 коммита", 4: "4 коммита", 5: "5 коммитов",
		11: "11 коммитов", 21: "21 коммит", 22: "22 коммита", 25: "25 коммитов",
		0: "0 коммитов",
	} {
		if got := plural(n, "коммит", "коммита", "коммитов"); got != want {
			t.Errorf("plural(%d) = %q, ожидали %q", n, got, want)
		}
	}
}
