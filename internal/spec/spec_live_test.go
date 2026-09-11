//go:build live

package spec

// Проверка на настоящих задачах с настоящих созвонов. Обычные прогоны её не
// видят: тег live.
//
//	STENO_LIVE_CONFIG=~/steno/steno.json \
//	  go test -tags live -run TestLiveSpec -v -timeout 20m ./internal/spec/
//
// STENO_LIVE_CONFIG — конфиг с настоящим доступом к модели.
// STENO_LIVE_ITEM   — задача, по которой делать ТЗ. Пусто — первая открытая.
// STENO_LIVE_FORCE  — приписать задаче этот проект. Нужно ровно для одного:
//                     посмотреть, каким выходит ТЗ, когда проект у задачи
//                     проставлен, а разбор оставил её в «не определён».
//
// База копируется целиком в каталог теста и правится только там. Живую базу
// владельца этот тест не открывает вовсе — ни на запись, ни на чтение сверх
// одного `.backup`.

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sur1cat/steno/internal/core"
	_ "modernc.org/sqlite"
)

func TestLiveSpec(t *testing.T) {
	cfgPath := os.Getenv("STENO_LIVE_CONFIG")
	if cfgPath == "" {
		t.Skip("нужен STENO_LIVE_CONFIG")
	}
	cfg, err := core.LoadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	// Копия базы: живую трогать нельзя, там идёт работа.
	dir := t.TempDir()
	// Только на чтение: живая база владельца открыта сервисом, и единственное,
	// что нам от неё нужно, — снимок в свой каталог. Приставка file: тут не
	// украшение: без неё mode=ro молча не применяется, и база открывается на
	// запись — не написав в неё ни байта, но и не запретив себе этого.
	src, err := sql.Open("sqlite",
		"file:"+filepath.Join(cfg.DataDir, "steno.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.Exec(`VACUUM INTO ?`, filepath.Join(dir, "steno.db")); err != nil {
		t.Fatal(err)
	}
	src.Close()

	st, err := core.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg.DataDir = dir

	sp, err := Open(st)
	if err != nil {
		t.Fatal(err)
	}
	d := Deps{Cfg: cfg, St: st, Sp: sp}

	items, err := st.OpenItems("")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Skip("в базе нет открытых задач")
	}
	id := os.Getenv("STENO_LIVE_ITEM")
	if id == "" {
		id = items[0].ID
	}
	if force := os.Getenv("STENO_LIVE_FORCE"); force != "" {
		if _, err := st.DB.Exec(`UPDATE project_items SET project=? WHERE id=?`, force, id); err != nil {
			t.Fatal(err)
		}
	}

	it, err := FindItem(st, id)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("\n=== задача %s ===\nпроект: %s\nна ком: %s\nтекст:  %s\nцитата: %s\n",
		it.ID, it.Project, it.Owner, it.Text, it.Quote)

	// Материал по коду виден отдельно: если ТЗ выйдет пустым, надо понимать,
	// нашёлся ли код или модель его не увидела.
	if p, err := st.Project(it.Project); err == nil {
		if repo, err := RepoOf(cfg.DataDir, p); err == nil {
			fmt.Println(Collect(context.Background(), repo, it).Render())
		}
	}

	out, err := Build(context.Background(), d, id)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status == StatusRejected {
		fmt.Printf("\n=== задача не взята ===\n%s\n", out.Reject)
		return
	}
	fmt.Printf("\n=== ТЗ %s, %s, $%.4f ===\n\n%s\n", out.ID, out.Model, out.USD, out.Render())
	if r := out.Gate(); len(r) > 0 {
		fmt.Printf("=== по этому ТЗ работать нельзя ===\n%v\n", r)
	}
}

// Исполнение целиком: рабочая копия, настоящий агент, поток, ветка.
//
//	STENO_LIVE_CONFIG=~/steno/steno.json \
//	  go test -tags live -run TestLiveRun -v -timeout 20m ./internal/spec/
//
// Репозиторий тест заводит свой, во временном каталоге. Ни один настоящий
// репозиторий здесь не задет: проверяется механика, а не чужой код.
func TestLiveRun(t *testing.T) {
	cfgPath := os.Getenv("STENO_LIVE_CONFIG")
	if cfgPath == "" {
		t.Skip("нужен STENO_LIVE_CONFIG")
	}
	cfg, err := core.LoadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cfg.DataDir = dir
	st, err := core.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sp, err := Open(st)
	if err != nil {
		t.Fatal(err)
	}
	d := Deps{Cfg: cfg, St: st, Sp: sp}

	repo := repoWithCommit(t)
	if err := os.WriteFile(filepath.Join(repo, "greet.py"),
		[]byte("def greet(name):\n    return \"Привет, \" + name\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := git(repo, "add", "-A"); err != nil {
		t.Fatal(out)
	}
	if out, err := git(repo, "commit", "-m", "greet"); err != nil {
		t.Fatal(out)
	}

	task := &Spec{
		ItemID: "T-live", Project: "проба", Repo: repo, Status: StatusDraft,
		Title:   "Здороваться без имени",
		Summary: []string{"greet() падает на пустом имени — пусть отвечает «Привет»."},
		Known:   []string{"Функция greet живёт в greet.py и склеивает строки."},
		Places:  []Place{{Path: "greet.py", Why: "тут вся функция", Found: true}},
		Steps:   []string{"Обработать пустое имя в greet.py"},
		Checks:  []string{"Автопроверок нет — перечитать глазами"},
		Unknowns: []Unknown{{Question: "Что отвечать на None, а не на пустую строку?",
			Why: "поведение не задано", Ask: "тот, кто просил"}},
	}
	if err := sp.Save(task); err != nil {
		t.Fatal(err)
	}

	set := Settings{Enabled: true, MaxUSD: 1}
	out, err := Run(context.Background(), d, set, task.ID, "Рустем", func(kind, text string) {
		fmt.Printf("  [%s] %s\n", kind, text)
	})
	if err != nil {
		t.Fatalf("исполнение: %v", err)
	}
	fmt.Printf("\nстатус: %s\nветка: %s\nкопия: %s\n", out.Status, out.Branch, out.Worktree)

	// main не тронут, работа в своей ветке, push никуда не делался.
	head, _ := git(repo, "rev-parse", "main")
	branch, _ := git(repo, "rev-parse", out.Branch)
	if head == branch {
		t.Fatal("в ветке ничего не появилось")
	}
	log, _ := git(repo, "log", "--oneline", "-1", out.Branch)
	diff, _ := git(repo, "diff", "main.."+out.Branch)
	fmt.Printf("\nкоммит: %s\nдифф:\n%s\n", strings.TrimSpace(log), diff)
	if strings.TrimSpace(diff) == "" {
		t.Error("агент ничего не изменил")
	}
	if _, err := git(repo, "rev-parse", "--verify", "origin/main"); err == nil {
		t.Error("появился origin — push быть не должно")
	}
}
