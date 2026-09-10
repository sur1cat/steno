package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sur1cat/steno/internal/core"
)

// --- командная строка ---------------------------------------------------------

// Ввод подменяет withStdin из setup_test.go: askYes читает os.Stdin напрямую,
// и проверить согласие иначе нечем.

// captureStdout ловит то, что команда печатает человеку: цена удаления
// показывается именно там, и без неё вопрос «удалить?» задан вслепую.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()
	fn()
	os.Stdout = old
	w.Close()
	out := <-done
	r.Close()
	return out
}

// rmCLI разворачивает конфиг и базу, как их видит команда: `steno rm` ходит
// через open(), а не через переданное хранилище.
func rmCLI(t *testing.T) (cfgPath string, reopen func() *core.Store) {
	t.Helper()
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	cfgPath = filepath.Join(dir, "steno.json")
	if err := os.WriteFile(cfgPath, []byte(`{"data_dir":`+quote(data)+`}`), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := core.OpenStore(data)
	if err != nil {
		t.Fatal(err)
	}
	rmSeed(t, st)
	// Закрываем: у хранилища один коннект, и команда откроет своё.
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	return cfgPath, func() *core.Store {
		t.Helper()
		st, err := core.OpenStore(data)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
		return st
	}
}

// Без явного согласия команда не удаляет ничего. Enter и «n» — отказ: отменить
// удаление нечем, и цена ошибки здесь несимметрична.
func TestMeetingRmNeedsConsent(t *testing.T) {
	for _, answer := range []string{"\n", "n\n", "нет\n", ""} {
		cfgPath, reopen := rmCLI(t)
		var err error
		out := captureStdout(t, func() {
			withStdin(t, answer, func() {
				err = cmdMeetingRm([]string{"-c", cfgPath, "уходит"})
			})
		})
		if err != nil {
			t.Fatalf("ответ %q: %v", answer, err)
		}
		st := reopen()
		if _, e := st.Meeting("уходит"); e != nil {
			t.Errorf("ответ %q: созвон удалён без согласия", answer)
		}
		if !strings.Contains(out, "отменил") {
			t.Errorf("ответ %q: команда не сказала, что отменила:\n%s", answer, out)
		}
	}
}

// Цену показываем до вопроса, а не после удаления: соглашаются, читая её.
func TestMeetingRmShowsPriceThenDeletes(t *testing.T) {
	cfgPath, reopen := rmCLI(t)
	var err error
	out := captureStdout(t, func() {
		withStdin(t, "y\n", func() {
			err = cmdMeetingRm([]string{"-c", cfgPath, "уходит"})
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Планёрка уходит", "2 задачи", "1 решение",
		"1 пункт откроется заново", "созвон уходит удалён", "вернулось в работу"} {
		if !strings.Contains(out, want) {
			t.Errorf("в выводе нет %q:\n%s", want, out)
		}
	}
	// Цена обязана стоять раньше вопроса — иначе её читают уже после «да».
	if i, j := strings.Index(out, "откроется заново"), strings.Index(out, "Удалить созвон?"); i < 0 || j < 0 || i > j {
		t.Errorf("цена показана не до вопроса (цена %d, вопрос %d):\n%s", i, j, out)
	}

	st := reopen()
	if _, e := st.Meeting("уходит"); e == nil {
		t.Error("созвон не удалился")
	}
	if q, ok := item(t, st, "Q-чужой-закрыт"); !ok || q.Status != "open" {
		t.Errorf("чужой пункт не вернулся в работу: %+v", q)
	}
}

// --yes нужен скриптам: вопрос, заданный конвейеру, висит до конца времён.
func TestMeetingRmYesSkipsTheQuestion(t *testing.T) {
	cfgPath, reopen := rmCLI(t)
	var err error
	out := captureStdout(t, func() {
		withStdin(t, "", func() { // ввода нет вовсе — спрашивать некого
			err = cmdMeetingRm([]string{"-c", cfgPath, "--yes", "уходит"})
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "Удалить созвон?") {
		t.Errorf("с --yes всё равно спросили:\n%s", out)
	}
	st := reopen()
	if _, e := st.Meeting("уходит"); e == nil {
		t.Error("с --yes созвон не удалился")
	}
}

// Опечатка в id не должна выглядеть как успешное удаление.
func TestMeetingRmComplainsAboutUnknownID(t *testing.T) {
	cfgPath, reopen := rmCLI(t)
	var err error
	captureStdout(t, func() {
		withStdin(t, "y\n", func() {
			err = cmdMeetingRm([]string{"-c", cfgPath, "--yes", "нет-такого"})
		})
	})
	if err == nil {
		t.Fatal("удаление несуществующего созвона прошло молча")
	}
	if !strings.Contains(err.Error(), "steno list") {
		t.Errorf("не подсказали, где взять id: %v", err)
	}
	st := reopen()
	if _, e := st.Meeting("уходит"); e != nil {
		t.Error("заодно снесли существующий созвон")
	}
}

// id обязателен. Соседи (`show`, `process`) без аргумента берут последний
// созвон — здесь такое умолчание стирало бы данные по промаху мимо клавиши.
func TestMeetingRmRefusesWithoutID(t *testing.T) {
	cfgPath, reopen := rmCLI(t)
	err := cmdMeetingRm([]string{"-c", cfgPath, "--yes"})
	if err == nil {
		t.Fatal("без id команда что-то сделала")
	}
	st := reopen()
	if _, e := st.Meeting("уходит"); e != nil {
		t.Error("без id удалили последний созвон")
	}
}

func rmSeed(t *testing.T, st *core.Store) string {
	t.Helper()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	started := time.Date(2026, 9, 8, 11, 0, 0, 0, time.Local)

	for _, id := range []string{"уходит", "остаётся"} {
		must(st.CreateMeeting(&core.Meeting{ID: id, Title: "Планёрка " + id,
			MeetURL: "https://meet.example/" + id, StartedAt: started, Status: "published"}))
		must(st.SaveSegments(id, []core.Segment{
			{Start: 1, End: 5, Speaker: "А", Text: "миграция схемы в созвоне " + id},
			{Start: 6, End: 9, Speaker: "Б", Text: "закончу к четвергу"},
		}))
		must(st.SaveFollowup(id, "claude", &core.Followup{
			Title: "Релиз " + id,
			TLDR:  []string{"перенесли на пятницу"},
			ActionItems: []core.ActionItem{
				{Owner: "Б", What: "закончить миграцию", Due: "2026-09-11", At: 6},
			},
		}))
		must(st.SavePublication(id, "slack", "https://slack/"+id, ""))
		if _, err := st.MarkEventSeen("ключ-"+id, id); err != nil {
			t.Fatal(err)
		}
		// Каталог записи: звук, субтитры, лог ffmpeg.
		dir := st.RecordingDir(id)
		must(os.MkdirAll(dir, 0o755))
		for _, name := range []string{"audio.ogg", "captions.txt", "ffmpeg.log"} {
			must(os.WriteFile(filepath.Join(dir, name), []byte("звук "+id), 0o644))
		}
	}

	// Родились на удаляемом созвоне — уйдут вместе с ним.
	must(st.AddItem(core.ProjectItem{ID: "T-родился", Project: "Платежи", Kind: core.KindTask,
		Text: "закончить миграцию", Owner: "Б", OpenedIn: "уходит"}))
	must(st.AddItem(core.ProjectItem{ID: "D-родился", Project: "Платежи", Kind: core.KindDecision,
		Text: "релиз в пятницу", OpenedIn: "уходит"}))
	// Родился на удаляемом и там же закрыт: уходит, а не «открывается заново»
	// сиротой без созвона-родителя.
	must(st.AddItem(core.ProjectItem{ID: "T-родился-и-закрыт", Project: "Платежи", Kind: core.KindTask,
		Text: "проверить откат", OpenedIn: "уходит"}))
	must(st.CloseItem("T-родился-и-закрыт", "done", "сделали на месте", "уходит"))

	// Родился раньше, закрыт на удаляемом созвоне: остаётся и открывается заново.
	must(st.AddItem(core.ProjectItem{ID: "Q-чужой-закрыт", Project: "Платежи", Kind: core.KindQuestion,
		Text: "кто дежурит в выходные", Owner: "А", OpenedIn: "остаётся"}))
	must(st.CloseItem("Q-чужой-закрыт", "done", "решили на планёрке", "уходит"))

	// Ни при чём: другой созвон, там же и закрыт.
	must(st.AddItem(core.ProjectItem{ID: "T-посторонний", Project: "Платежи", Kind: core.KindTask,
		Text: "посторонняя задача", OpenedIn: "остаётся"}))
	must(st.CloseItem("T-посторонний", "dropped", "передумали", "остаётся"))

	return st.RecordingDir("уходит")
}

func item(t *testing.T, st *core.Store, id string) (core.ProjectItem, bool) {
	t.Helper()
	items, err := st.ProjectItems("Платежи")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.ID == id {
			return it, true
		}
	}
	return core.ProjectItem{}, false
}
