package spec

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sur1cat/steno/internal/core"
)

func testDeps(t *testing.T) Deps {
	t.Helper()
	dir := t.TempDir()
	st, err := core.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	sp, err := Open(st)
	if err != nil {
		t.Fatal(err)
	}
	cfg := core.DefaultConfig()
	cfg.DataDir = dir
	return Deps{Cfg: cfg, St: st, Sp: sp}
}

func saved(t *testing.T, d Deps, sp *Spec) *Spec {
	t.Helper()
	sp.Status = StatusDraft
	sp.Repo = t.TempDir()
	if err := d.Sp.Save(sp); err != nil {
		t.Fatal(err)
	}
	return sp
}

// Требование первое: выключено по умолчанию. Настройки из пустого конфига не
// должны давать права запускать что-либо на чужой машине.
func TestRunRefusesWhenDisabled(t *testing.T) {
	d := testDeps(t)
	sp := saved(t, d, good())
	_, err := Run(context.Background(), d, Settings{}, sp.ID, "Рустем", nil)
	if err == nil || !strings.Contains(err.Error(), "выключено") {
		t.Fatalf("запуск при выключенной настройке: %v", err)
	}
}

// Требование второе: запускает человек. Пустой by — это и есть «пришло из
// телеграма»: у входящего сообщения отправитель есть, а человека за этой
// машиной нет.
func TestRunRefusesWithoutPerson(t *testing.T) {
	d := testDeps(t)
	sp := saved(t, d, good())
	_, err := Run(context.Background(), d, Settings{Enabled: true}, sp.ID, "  ", nil)
	if err == nil || !strings.Contains(err.Error(), "только человеком") {
		t.Fatalf("запуск без человека: %v", err)
	}
}

// И главное: негодное ТЗ не исполняется, сколько бы кнопок ни нажали.
func TestRunRefusesUngatedSpec(t *testing.T) {
	d := testDeps(t)
	bad := good()
	bad.Unknowns = nil
	sp := saved(t, d, bad)
	_, err := Run(context.Background(), d, Settings{Enabled: true}, sp.ID, "Рустем", nil)
	if err == nil || !strings.Contains(err.Error(), "нельзя работать") {
		t.Fatalf("запуск по ТЗ без открытых вопросов: %v", err)
	}
}

func TestRunRefusesRejectedSpec(t *testing.T) {
	d := testDeps(t)
	sp := good()
	sp.Status, sp.Reject, sp.Repo = StatusRejected, "это не про код", t.TempDir()
	if err := d.Sp.Save(sp); err != nil {
		t.Fatal(err)
	}
	_, err := Run(context.Background(), d, Settings{Enabled: true}, sp.ID, "Рустем", nil)
	if err == nil || !strings.Contains(err.Error(), "это не про код") {
		t.Fatalf("запуск по отказу: %v", err)
	}
}

// Требование второе, вторым слоем. Правило «исполнение только по действию
// человека» держится не на памяти того, кто будет дописывать код через полгода,
// а на этом обходе: всё, что принимает данные снаружи, не имеет права звать
// исполнение вообще.
//
// Созвон — тоже чужой ввод: на звонке кто угодно может сказать «снеси
// репозиторий», и это доедет сюда задачей. Поэтому в списке и bot.
func TestRunUnreachableFromInbound(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	// Конвейеры — тоже вход снаружи: созвон и заметка приходят от людей на
	// звонке, и то, что после разбора собирается само (ТЗ), не имеет права
	// само же и запускать.
	inbound := []string{"internal/sources", "internal/bot", "internal/pipeline", "internal/note"}
	found := 0
	for _, rel := range inbound {
		dir := filepath.Join(root, rel)
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("не нашли %s — обход сторожит пустоту", rel)
		}
		err := filepath.WalkDir(dir, func(path string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() || !strings.HasSuffix(path, ".go") {
				return err
			}
			found++
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(raw), "spec.Run(") {
				t.Errorf("%s зовёт исполнение — а это вход снаружи, "+
					"запуск агента оттуда запрещён", path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if found < 10 {
		t.Fatalf("просмотрели всего %d файлов — обход сломался", found)
	}
}

// Имя ветки набирают руками в терминале. Кириллица в нём — это «й», разложенная
// по-разному, и команда, которая не находится по Tab.
func TestSlug(t *testing.T) {
	cases := map[string]string{
		"Разобрать sapar на запросы": "razobrat-sapar-na-zaprosy",
		"  ...  ": "task",
		"T-a99f":  "t-a99f",
	}
	for in, want := range cases {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, ждали %q", in, got, want)
		}
	}
	long := slug(strings.Repeat("длинное название задачи ", 10))
	if len(long) > 40 {
		t.Errorf("длина ветки не обрезана: %d символов", len(long))
	}
}

// Правила запретов — не пожелание промпта, а файл, который читает claude. Push
// и коммит в них обязаны быть: коммит делает система и только в свою ветку.
func TestDenyRulesForbidPushAndCommit(t *testing.T) {
	joined := strings.Join(denyRules, " ")
	for _, want := range []string{"git push", "git commit", "git checkout", "sudo"} {
		if !strings.Contains(joined, want) {
			t.Errorf("в запретах нет %q: %v", want, denyRules)
		}
	}
}

// Файл правил лежит рядом с копией, а не внутри: внутри он попал бы в коммит и
// уехал в ветку, которую человек потом откроет и не поймёт, откуда это.
func TestPermissionsFileStaysOutsideWorktree(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "wt")
	must(t, os.MkdirAll(wt, 0o755))
	path, err := writePermissions(wt)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(path, wt+string(filepath.Separator)) {
		t.Fatalf("правила легли внутрь рабочей копии: %s", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "git push") {
		t.Fatalf("в файле правил нет запрета push:\n%s", raw)
	}
}
