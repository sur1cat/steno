package core

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Словарь проекта — имена людей и слова, которыми команда о проекте говорит.
// Проверяется он целиком, от вопроса в терминале до строки в промпте: сломанное
// звено здесь ничего не роняет, а тихо возвращает follow-up, в котором задача
// приписана не тому человеку.
//
// Проверка раскладывается по пакетам вслед за самим словарём: хранилище — тут,
// промпт — в brain, ручка — в panel, вопрос в терминале — в cmd, форма — в tui.

// --- хранилище ---------------------------------------------------------------

// Главное обязательство: имена людей лежат и в общем словаре тоже. Его читает
// whisper одним плоским списком, и имя, оставшееся только в People, до
// распознавания не доедет — то есть пропадёт из расшифровки насовсем.
func TestSaveProjectKeepsPeopleInVocabulary(t *testing.T) {
	st := testStore(t)

	if err := st.SaveProject(Project{
		Name:       "Платежи",
		People:     []string{"Орынгали", "Рустем"},
		Vocabulary: []string{"Сапар", "эквайринг"},
	}); err != nil {
		t.Fatal(err)
	}

	p, err := st.Project("Платежи")
	if err != nil {
		t.Fatal(err)
	}
	for _, who := range []string{"Орынгали", "Рустем"} {
		if !hasWord(p.Vocabulary, who) {
			t.Errorf("имя %q не попало в словарь: %v — whisper его не увидит", who, p.Vocabulary)
		}
	}
	for _, w := range []string{"Сапар", "эквайринг"} {
		if !hasWord(p.Vocabulary, w) {
			t.Errorf("слово %q пропало из словаря: %v", w, p.Vocabulary)
		}
	}
	if got := strings.Join(p.People, ","); got != "Орынгали,Рустем" {
		t.Errorf("люди прочитались как %q", got)
	}
	// А в формах словарь показывается без имён: два поля с одним и тем же
	// содержимым правятся вразнобой, и стёртое в одном остаётся в другом.
	if other := p.OtherWords(); strings.Join(other, ",") != "Сапар,эквайринг" {
		t.Errorf("otherWords вернул %v — ожидали словарь без людей", other)
	}
}

// Правка через форму: прочитали, показали без людей, сохранили обратно. Список
// не должен расти и не должен терять имена.
func TestSaveProjectRoundTripIsStable(t *testing.T) {
	st := testStore(t)

	first := Project{Name: "Платежи", People: []string{"Орынгали"}, Vocabulary: []string{"Сапар"}}
	if err := st.SaveProject(first); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		p, err := st.Project("Платежи")
		if err != nil {
			t.Fatal(err)
		}
		// Ровно то, что делают все три формы: словарь без людей — в поле,
		// люди — в своё поле, и обратно в SaveProject.
		if err := st.SaveProject(Project{
			Name: p.Name, People: p.People, Vocabulary: p.OtherWords(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	p, err := st.Project("Платежи")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(p.Vocabulary, ",") != "Сапар,Орынгали" {
		t.Errorf("после трёх правок словарь стал %v", p.Vocabulary)
	}
	if strings.Join(p.People, ",") != "Орынгали" {
		t.Errorf("после трёх правок люди стали %v", p.People)
	}
}

func TestTrimAndMergeWords(t *testing.T) {
	if got := trimWords([]string{" Сапар ", "", "сапар", "ЛК", "  "}); strings.Join(got, ",") != "Сапар,ЛК" {
		t.Errorf("trimWords = %v: пустые и повторы (без учёта регистра) должны уходить", got)
	}
	if got := MergeWords([]string{"Сапар"}, []string{"Орынгали", "сапар"}); strings.Join(got, ",") != "Сапар,Орынгали" {
		t.Errorf("mergeWords = %v", got)
	}
	if got := withoutWords([]string{"Сапар", "Орынгали"}, []string{"орынгали"}); strings.Join(got, ",") != "Сапар" {
		t.Errorf("withoutWords = %v", got)
	}
	// Список из одних пустых строк — это пустой список, а не список с пустым
	// словом: пустое слово в подсказке whisper стоило бы одного лишнего токена
	// в каждом запросе и ничего бы не значило.
	if got := trimWords([]string{"", "   "}); len(got) != 0 {
		t.Errorf("trimWords из пустых строк дал %v", got)
	}
}

// База, заведённая до словаря, должна открыться и дочитаться. Без миграции
// первый же SELECT по проектам падал бы на «no such column», то есть steno
// переставал бы запускаться у всех, кто им уже пользуется.
func TestOldDatabaseMigratesToVocabulary(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "recordings"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Таблица проектов в том виде, в каком её создавали прошлые версии.
	db, err := sql.Open("sqlite", filepath.Join(dir, "steno.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE projects (
		name       TEXT PRIMARY KEY,
		aliases    TEXT NOT NULL DEFAULT '[]',
		about      TEXT NOT NULL DEFAULT '',
		sources    TEXT NOT NULL DEFAULT '[]',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO projects (name,aliases,about,sources,created_at,updated_at)
		VALUES ('Платежи','["биллинг"]','приём денег','[]',1,1)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("старая база не открылась: %v", err)
	}
	defer st.Close()

	p, err := st.Project("Платежи")
	if err != nil {
		t.Fatalf("проект из старой базы не читается: %v", err)
	}
	if p.About != "приём денег" || len(p.Aliases) != 1 {
		t.Errorf("миграция потеряла содержимое: %+v", p)
	}
	if len(p.People) != 0 || len(p.Vocabulary) != 0 {
		t.Errorf("у старого проекта словарь взялся из ниоткуда: %+v", p)
	}
	// И новое поле в догнанной базе должно писаться, а не молча теряться.
	p.People = []string{"Орынгали"}
	if err := st.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	again, err := st.Project("Платежи")
	if err != nil {
		t.Fatal(err)
	}
	if !hasWord(again.People, "Орынгали") || !hasWord(again.Vocabulary, "Орынгали") {
		t.Errorf("после миграции словарь не сохраняется: %+v", again)
	}
}

func testStore(t *testing.T) *Store {
	t.Helper()
	st, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func hasWord(words []string, want string) bool {
	for _, w := range words {
		if strings.EqualFold(w, want) {
			return true
		}
	}
	return false
}
