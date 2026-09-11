package spec

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Требования 3 и 4 целиком: рабочая копия отдельно от каталога, где человек
// работает, и коммит только в свою ветку.
//
// Проверяется на настоящем git в временном каталоге, а не на подделке: вся суть
// требования в том, что делает git, и подделка проверяла бы наши представления
// о нём.

func repoWithCommit(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("нет git")
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
	} {
		if out, err := git(dir, args...); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	write(t, filepath.Join(dir, "readme.md"), "исходное\n")
	if out, err := git(dir, "add", "-A"); err != nil {
		t.Fatal(out)
	}
	if out, err := git(dir, "commit", "-m", "первый"); err != nil {
		t.Fatal(out)
	}
	return dir
}

func TestWorktreeIsSeparateAndBranchIsNew(t *testing.T) {
	repo := repoWithCommit(t)
	base, err := baseCommit(repo, "")
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(t.TempDir(), "work")
	if err := makeWorktree(repo, "steno/proba", wt, base); err != nil {
		t.Fatal(err)
	}

	// Копия отдельная: правка в ней не видна в основном каталоге.
	write(t, filepath.Join(wt, "readme.md"), "правка агента\n")
	got, err := os.ReadFile(filepath.Join(repo, "readme.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "исходное\n" {
		t.Fatalf("основной каталог задет: %q", got)
	}

	sp := &Spec{ID: "S-1", ItemID: "T-1", Title: "проба"}
	files, err := commit(wt, sp)
	if err != nil || files != 1 {
		t.Fatalf("commit = %d, %v", files, err)
	}

	// main остался там же, где был; работа легла в свою ветку.
	head, _ := git(repo, "rev-parse", "main")
	if strings.TrimSpace(head) != base {
		t.Fatalf("main сдвинулся: %s → %s", base, strings.TrimSpace(head))
	}
	branch, _ := git(repo, "rev-parse", "steno/proba")
	if strings.TrimSpace(branch) == base {
		t.Fatal("коммит не попал в ветку задачи")
	}
	log, _ := git(repo, "log", "--oneline", "steno/proba")
	if !strings.Contains(log, "проба") {
		t.Fatalf("в ветке нет нашего коммита:\n%s", log)
	}
}

// Ветку с тем же именем не перезаводим и чужую рабочую копию не сносим: в
// radio так можно — там ветки заводит он сам, — а здесь репозиторий чужой, и
// человек в нём работает прямо сейчас.
func TestMakeWorktreeRefusesToReuseBranch(t *testing.T) {
	repo := repoWithCommit(t)
	base, _ := baseCommit(repo, "")
	first := filepath.Join(t.TempDir(), "a")
	if err := makeWorktree(repo, "steno/proba", first, base); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(t.TempDir(), "b")
	err := makeWorktree(repo, "steno/proba", second, base)
	if err == nil {
		t.Fatal("вторая копия заняла ту же ветку")
	}
	// git отказал бы и сам, но своими словами: «branch already used by
	// worktree». Проверяем именно нашу формулировку — она говорит человеку,
	// что делать, и она же пропадёт первой, если проверку однажды уберут.
	if !strings.Contains(err.Error(), "уже есть") {
		t.Fatalf("отказ пришёл не от нас: %v", err)
	}
	if _, err := os.Stat(filepath.Join(first, "readme.md")); err != nil {
		t.Fatalf("первую копию задели: %v", err)
	}
}

func TestMakeWorktreeRefusesOccupiedDir(t *testing.T) {
	repo := repoWithCommit(t)
	base, _ := baseCommit(repo, "")
	wt := filepath.Join(t.TempDir(), "занято")
	must(t, os.MkdirAll(wt, 0o755))
	write(t, filepath.Join(wt, "чужое.txt"), "не наше")
	err := makeWorktree(repo, "steno/proba", wt, base)
	if err == nil {
		t.Fatal("заняли чужой каталог")
	}
	if !strings.Contains(err.Error(), "уже занят") {
		t.Fatalf("отказ пришёл не от нас: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wt, "чужое.txt")); err != nil {
		t.Fatalf("чужой файл пропал: %v", err)
	}
}

// Пустая работа не коммитится: агент, ничего не изменивший, не должен оставлять
// за собой коммит с описанием того, чего не сделал.
func TestCommitSkipsEmptyWork(t *testing.T) {
	repo := repoWithCommit(t)
	base, _ := baseCommit(repo, "")
	wt := filepath.Join(t.TempDir(), "work")
	if err := makeWorktree(repo, "steno/пусто", wt, base); err != nil {
		t.Fatal(err)
	}
	files, err := commit(wt, &Spec{ID: "S-1", Title: "ничего"})
	if err != nil || files != 0 {
		t.Fatalf("commit на пустой работе = %d, %v", files, err)
	}
	log, _ := git(repo, "log", "--oneline", "steno/пусто")
	if strings.Count(strings.TrimSpace(log), "\n") != 0 {
		t.Fatalf("завёлся лишний коммит:\n%s", log)
	}
}

// База — снимок, а не имя ветки: пока агент работает, человек в основном
// каталоге успевает закоммитить, и «та же ветка» через полчаса означала бы уже
// другое состояние.
func TestBaseCommitIsSnapshot(t *testing.T) {
	repo := repoWithCommit(t)
	base, err := baseCommit(repo, "")
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(repo, "readme.md"), "человек дописал\n")
	git(repo, "add", "-A")
	git(repo, "commit", "-m", "второй")

	again, _ := baseCommit(repo, "")
	if again == base {
		t.Fatal("HEAD не сдвинулся — тест сторожит пустоту")
	}
	wt := filepath.Join(t.TempDir(), "work")
	if err := makeWorktree(repo, "steno/snapshot", wt, base); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(wt, "readme.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "исходное\n" {
		t.Fatalf("копия взята не от снимка, а от текущего состояния: %q", got)
	}
}

func TestBaseCommitRefusesUnknownBranch(t *testing.T) {
	repo := repoWithCommit(t)
	if _, err := baseCommit(repo, "нет-такой-ветки"); err == nil {
		t.Fatal("несуществующая ветка принята за основу")
	}
}
