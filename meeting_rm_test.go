package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Удаление созвона трогает семь таблиц и каталог на диске. Проверяем не «ушла
// строка из meetings», а что не осталось ни одной ссылки ни в одной таблице:
// половинчатое удаление выглядит успешным ровно до того дня, когда поиск
// находит созвон, которого нет, или в списке задач висит дело без созвона.

// rmSeed кладёт два созвона: тот, который удаляем, и соседний, который трогать
// нельзя. Возвращает путь к каталогу записи удаляемого.
func rmSeed(t *testing.T, st *Store) string {
	t.Helper()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	started := time.Date(2026, 9, 8, 11, 0, 0, 0, time.Local)

	for _, id := range []string{"уходит", "остаётся"} {
		must(st.CreateMeeting(&Meeting{ID: id, Title: "Планёрка " + id,
			MeetURL: "https://meet.example/" + id, StartedAt: started, Status: "published"}))
		must(st.SaveSegments(id, []Segment{
			{Start: 1, End: 5, Speaker: "А", Text: "миграция схемы в созвоне " + id},
			{Start: 6, End: 9, Speaker: "Б", Text: "закончу к четвергу"},
		}))
		must(st.SaveFollowup(id, "claude", &Followup{
			Title: "Релиз " + id,
			TLDR:  []string{"перенесли на пятницу"},
			ActionItems: []ActionItem{
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
	must(st.AddItem(ProjectItem{ID: "T-родился", Project: "Платежи", Kind: KindTask,
		Text: "закончить миграцию", Owner: "Б", OpenedIn: "уходит"}))
	must(st.AddItem(ProjectItem{ID: "D-родился", Project: "Платежи", Kind: KindDecision,
		Text: "релиз в пятницу", OpenedIn: "уходит"}))
	// Родился на удаляемом и там же закрыт: уходит, а не «открывается заново»
	// сиротой без созвона-родителя.
	must(st.AddItem(ProjectItem{ID: "T-родился-и-закрыт", Project: "Платежи", Kind: KindTask,
		Text: "проверить откат", OpenedIn: "уходит"}))
	must(st.CloseItem("T-родился-и-закрыт", "done", "сделали на месте", "уходит"))

	// Родился раньше, закрыт на удаляемом созвоне: остаётся и открывается заново.
	must(st.AddItem(ProjectItem{ID: "Q-чужой-закрыт", Project: "Платежи", Kind: KindQuestion,
		Text: "кто дежурит в выходные", Owner: "А", OpenedIn: "остаётся"}))
	must(st.CloseItem("Q-чужой-закрыт", "done", "решили на планёрке", "уходит"))

	// Ни при чём: другой созвон, там же и закрыт.
	must(st.AddItem(ProjectItem{ID: "T-посторонний", Project: "Платежи", Kind: KindTask,
		Text: "посторонняя задача", OpenedIn: "остаётся"}))
	must(st.CloseItem("T-посторонний", "dropped", "передумали", "остаётся"))

	return st.RecordingDir("уходит")
}

func rmStore(t *testing.T) *Store {
	t.Helper()
	st, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// countFor — сколько строк таблицы держится за созвон.
func countFor(t *testing.T, st *Store, table, id string) int {
	t.Helper()
	var n int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE meeting_id=?`, id).Scan(&n); err != nil {
		t.Fatalf("%s: %v", table, err)
	}
	return n
}

func item(t *testing.T, st *Store, id string) (ProjectItem, bool) {
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
	return ProjectItem{}, false
}

func TestDeleteMeetingLeavesNoTrace(t *testing.T) {
	st := rmStore(t)
	dir := rmSeed(t, st)

	// До удаления в каждой таблице что-то есть — иначе проверка «стало ноль»
	// проходила бы и на сломанном коде.
	for _, table := range meetingTables {
		if n := countFor(t, st, table, "уходит"); n == 0 {
			t.Fatalf("%s: перед удалением пусто, проверять нечего", table)
		}
	}

	toll, err := st.DeleteMeeting("уходит")
	if err != nil {
		t.Fatal(err)
	}

	for _, table := range meetingTables {
		if n := countFor(t, st, table, "уходит"); n != 0 {
			t.Errorf("%s: осталось %d строк удалённого созвона", table, n)
		}
		if n := countFor(t, st, table, "остаётся"); n == 0 {
			t.Errorf("%s: заодно вычистили соседний созвон", table)
		}
	}
	if _, err := st.Meeting("уходит"); err == nil {
		t.Error("строка созвона осталась в meetings")
	}
	if _, err := st.Meeting("остаётся"); err != nil {
		t.Errorf("соседний созвон пропал: %v", err)
	}

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("каталог записи %s остался на диске (%v)", dir, err)
	}
	if _, err := os.Stat(st.RecordingDir("остаётся")); err != nil {
		t.Errorf("каталог соседнего созвона снесли: %v", err)
	}

	// Поиск не должен находить удалённое — и должен находить оставшееся.
	hits, err := st.Search("миграция", 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.MeetingID == "уходит" {
			t.Errorf("поиск нашёл удалённый созвон: %+v", h)
		}
	}
	if len(hits) == 0 {
		t.Error("поиск перестал находить вообще всё — вычистили лишнее")
	}

	// Пункты проектов.
	for _, id := range []string{"T-родился", "D-родился", "T-родился-и-закрыт"} {
		if it, ok := item(t, st, id); ok {
			t.Errorf("%s пережил свой созвон: %+v", id, it)
		}
	}
	q, ok := item(t, st, "Q-чужой-закрыт")
	if !ok {
		t.Fatal("чужой пункт удалили вместе с созвоном, где его закрыли")
	}
	if q.Status != "open" {
		t.Errorf("Q-чужой-закрыт остался в статусе %q — основание закрытия стёрто, а он закрыт", q.Status)
	}
	if q.ClosedIn != "" {
		t.Errorf("Q-чужой-закрыт ссылается на удалённый созвон: closed_in=%q", q.ClosedIn)
	}
	other, ok := item(t, st, "T-посторонний")
	if !ok {
		t.Fatal("посторонний пункт удалили")
	}
	if other.Status != "dropped" || other.ClosedIn != "остаётся" {
		t.Errorf("посторонний пункт тронули: статус %q, closed_in %q", other.Status, other.ClosedIn)
	}

	// Ключ встречи освободился: иначе на ту же ссылку бот отвечал бы «уже иду».
	seen, err := st.EventSeen("ключ-уходит")
	if err != nil {
		t.Fatal(err)
	}
	if seen {
		t.Error("отметка о встрече пережила созвон — второй раз бота уже не позвать")
	}

	if toll.Reopen != 1 {
		t.Errorf("вернулось в работу %d пунктов, ждали 1", toll.Reopen)
	}
}

// Половинчатое удаление хуже, чем никакое: созвона нет в списке, а поиск по
// нему находит и задачи висят. Поэтому всё, что в базе, — одной транзакцией.
// Триггер валит последний шаг, когда предыдущие уже отработали.
func TestDeleteMeetingRollsBackWhole(t *testing.T) {
	st := rmStore(t)
	dir := rmSeed(t, st)

	if _, err := st.db.Exec(`CREATE TRIGGER стоп BEFORE DELETE ON meetings
		BEGIN SELECT RAISE(ABORT, 'не сегодня'); END`); err != nil {
		t.Fatal(err)
	}

	if _, err := st.DeleteMeeting("уходит"); err == nil {
		t.Fatal("удаление прошло, хотя последний шаг падает")
	}

	for _, table := range meetingTables {
		if n := countFor(t, st, table, "уходит"); n == 0 {
			t.Errorf("%s: строки удалились, хотя транзакция сорвалась", table)
		}
	}
	if _, err := st.Meeting("уходит"); err != nil {
		t.Errorf("созвон пропал при сорвавшейся транзакции: %v", err)
	}
	if _, ok := item(t, st, "T-родился"); !ok {
		t.Error("пункт удалился, хотя транзакция сорвалась")
	}
	if q, ok := item(t, st, "Q-чужой-закрыт"); !ok || q.Status != "done" {
		t.Errorf("чужой пункт открылся заново при сорвавшейся транзакции: %+v", q)
	}
	// Каталог сносится только после успешной фиксации: иначе строка осталась
	// бы без записи — молча потерянный звук у созвона, который человек видит
	// целым.
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("каталог записи снесли, хотя удаление сорвалось: %v", err)
	}
}

func TestMeetingTollCountsThePrice(t *testing.T) {
	st := rmStore(t)
	rmSeed(t, st)

	toll, err := st.MeetingToll("уходит")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		what string
		got  int
		want int
	}{
		{"реплик", toll.Segments, 2},
		{"задач follow-up", toll.Tasks, 1},
		{"публикаций", toll.Publications, 1},
		{"отметок о встрече", toll.Events, 1},
		{"задач проекта", toll.OpenedTasks, 2},
		{"решений", toll.OpenedDecisions, 1},
		{"вопросов", toll.OpenedQuestions, 0},
		{"откроется заново", toll.Reopen, 1},
	} {
		if c.got != c.want {
			t.Errorf("%s: %d, ждали %d", c.what, c.got, c.want)
		}
	}
	if !toll.Followup {
		t.Error("follow-up не посчитан")
	}
	if toll.Indexed == 0 {
		t.Error("куски поиска не посчитаны")
	}
	if toll.Bytes == 0 {
		t.Error("каталог записи не посчитан")
	}

	// Фраза — то единственное, что человек прочтёт перед «да». Цифры в ней
	// обязаны быть, и обязаны быть согласованы: «2 задачи», а не «2 задача».
	for _, want := range []string{"2 задачи", "1 решение", "1 пункт откроется заново", "МБ", "реплики"} {
		if !strings.Contains(toll.Text, want) {
			t.Errorf("в цене удаления нет %q:\n  %s", want, toll.Text)
		}
	}

	// Пустой созвон: обещать нечего, но и молчать нельзя.
	if err := st.CreateMeeting(&Meeting{ID: "пустой", MeetURL: "u",
		StartedAt: time.Now(), Status: "failed"}); err != nil {
		t.Fatal(err)
	}
	empty, err := st.MeetingToll("пустой")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(empty.Text, "только строка") {
		t.Errorf("цена удаления пустого созвона: %q", empty.Text)
	}
}

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
func rmCLI(t *testing.T) (cfgPath string, reopen func() *Store) {
	t.Helper()
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	cfgPath = filepath.Join(dir, "steno.json")
	if err := os.WriteFile(cfgPath, []byte(`{"data_dir":`+quote(data)+`}`), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := openStore(data)
	if err != nil {
		t.Fatal(err)
	}
	rmSeed(t, st)
	// Закрываем: у хранилища один коннект, и команда откроет своё.
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	return cfgPath, func() *Store {
		t.Helper()
		st, err := openStore(data)
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
