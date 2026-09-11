package core

import (
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
	st, err := OpenStore(t.TempDir())
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
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE meeting_id=?`, id).Scan(&n); err != nil {
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

	if _, err := st.DB.Exec(`CREATE TRIGGER стоп BEFORE DELETE ON meetings
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

// Удаление созвона не снимает отметку с календарного события: она значит
// «бот сюда уже ходил», и после удаления мусорного захода нужна как раз для
// того, чтобы следующий опрос не отправил бота в ту же пустую комнату.
func TestDeleteMeetingKeepsEventMark(t *testing.T) {
	st, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := &Meeting{ID: "cal-x", MeetURL: "https://meet.google.com/abc-defg-hij", StartedAt: time.Now(), Status: "recording"}
	if err := st.CreateMeeting(m); err != nil {
		t.Fatal(err)
	}
	const key = "https://meet.google.com/abc-defg-hij@2026-09-11T12:07Z"
	if ok, err := st.MarkEventSeen(key, m.ID); err != nil || !ok {
		t.Fatalf("отметка: %v %v", ok, err)
	}
	if _, err := st.DeleteMeeting(m.ID); err != nil {
		t.Fatal(err)
	}
	if been, _ := st.EventAttempted(key); !been {
		t.Error("удаление созвона сняло отметку с события — календарь пойдёт туда снова")
	}
	// А для зова человеком дорога свободна: созвона нет, значит он не «идёт».
	if seen, _ := st.EventSeen(key); seen {
		t.Error("отвязанная отметка не должна выглядеть как идущий созвон")
	}
}
