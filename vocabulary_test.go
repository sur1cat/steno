package main

import (
	"bufio"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Словарь проекта — имена людей и слова, которыми команда о проекте говорит.
// Проверяется он целиком, от вопроса в терминале до строки в промпте: сломанное
// звено здесь ничего не роняет, а тихо возвращает follow-up, в котором задача
// приписана не тому человеку.

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
	if other := p.otherWords(); strings.Join(other, ",") != "Сапар,эквайринг" {
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
			Name: p.Name, People: p.People, Vocabulary: p.otherWords(),
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
	if got := mergeWords([]string{"Сапар"}, []string{"Орынгали", "сапар"}); strings.Join(got, ",") != "Сапар,Орынгали" {
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

	st, err := openStore(dir)
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

// --- промпт follow-up ---------------------------------------------------------

// Ради этого всё и затевалось: модель должна прочитать, кто человек, а кто
// сервис, а не гадать по звучанию.
func TestFollowupHeadCarriesVocabulary(t *testing.T) {
	cfg := defaultConfig()
	m := &Meeting{Title: "Планёрка", StartedAt: time.Now()}
	head := followupHead(cfg, m, []Project{{
		Name:       "Платежи",
		Aliases:    []string{"биллинг"},
		About:      "приём денег",
		People:     []string{"Орынгали", "Рустем"},
		Vocabulary: []string{"Сапар", "Орынгали"},
	}}, "", "")

	if !strings.Contains(head, "люди: Орынгали, Рустем") {
		t.Errorf("в промпт не попали люди проекта:\n%s", head)
	}
	if !strings.Contains(head, "Сапар") {
		t.Errorf("в промпт не попал словарь проекта:\n%s", head)
	}
	// Имя не должно оказаться в строке «не люди»: ровно из этой строки модель и
	// узнаёт, кому задачу назначать нельзя.
	for _, line := range strings.Split(head, "\n") {
		if strings.Contains(line, "не люди") && strings.Contains(line, "Орынгали") {
			t.Errorf("имя человека уехало в список «не люди»: %q", line)
		}
	}
	// Проект без словаря не должен приносить в промпт пустых строк: модель
	// читает «люди:» с пустым списком как «людей нет».
	bare := followupHead(cfg, m, []Project{{Name: "Онбординг"}}, "", "")
	if strings.Contains(bare, "люди:") || strings.Contains(bare, "не люди") {
		t.Errorf("у проекта без словаря появились пустые строки:\n%s", bare)
	}
}

// Списка мало — модели надо сказать, что с ним делать. И сказать теми же
// словами, какими подписаны сами списки: правило про «людей» рядом с данными,
// подписанными «участники», — это правило про раздел, которого в промпте нет.
// Разъезжаются они молча, и ловится это только так.
func TestFollowupSystemExplainsVocabulary(t *testing.T) {
	head := followupHead(defaultConfig(), &Meeting{StartedAt: time.Now()}, []Project{{
		Name: "Платежи", People: []string{"Орынгали"}, Vocabulary: []string{"Сапар"},
	}}, "", "")

	for _, label := range []string{"люди", "сервисы и сокращения"} {
		if !strings.Contains(head, label) {
			t.Errorf("подпись %q пропала из данных промпта:\n%s", label, head)
		}
		if !strings.Contains(followupSystem, label) {
			t.Errorf("правило в системном промпте не упоминает %q — оно про другой раздел", label)
		}
	}
	// И правило должно быть запретом, а не описанием: список, про который
	// сказано только «бывает и такое», исполнителем всё равно станет.
	//
	// Переносы строк схлопываем: промпт свёрстан под 80 колонок, и фраза
	// разорвана посередине — искать её как есть значит проверять вёрстку.
	rule := strings.Join(strings.Fields(followupSystem), " ")
	for _, want := range []string{"на кого нельзя никогда", "не становится исполнителем"} {
		if !strings.Contains(rule, want) {
			t.Errorf("в системном промпте нет запрета назначать задачу на сервис: %q", want)
		}
	}
}

// --- панель ------------------------------------------------------------------

func TestPanelSavesAndReturnsVocabulary(t *testing.T) {
	srv, st, _ := testPanel(t)
	a := login(t, srv, "тайна")

	code, body := a.do("POST", "/api/projects", map[string]any{
		"name":       "Платежи",
		"about":      "приём денег",
		"aliases":    []string{"биллинг"},
		"sources":    []Source{},
		"people":     []string{"Орынгали"},
		"vocabulary": []string{"Сапар"},
	})
	if code != 200 {
		t.Fatalf("сохранение проекта: код %d, тело %s", code, body)
	}

	p, err := st.Project("Платежи")
	if err != nil {
		t.Fatal(err)
	}
	if !hasWord(p.People, "Орынгали") || !hasWord(p.Vocabulary, "Сапар") {
		t.Fatalf("панель не донесла словарь до базы: %+v", p)
	}
	if !hasWord(p.Vocabulary, "Орынгали") {
		t.Errorf("имя из панели не попало в словарь whisper: %v", p.Vocabulary)
	}

	var got struct {
		Projects []struct {
			Name       string   `json:"name"`
			People     []string `json:"people"`
			Vocabulary []string `json:"vocabulary"`
		} `json:"projects"`
	}
	a.get("/api/settings", &got)
	if len(got.Projects) != 1 {
		t.Fatalf("настройки вернули %d проектов", len(got.Projects))
	}
	x := got.Projects[0]
	if !hasWord(x.People, "Орынгали") {
		t.Errorf("панель не показывает людей: %+v", x)
	}
	// Форме словарь отдаётся без имён: иначе в двух полях подряд одно и то же.
	if hasWord(x.Vocabulary, "Орынгали") {
		t.Errorf("имя человека продублировалось в поле словаря: %v", x.Vocabulary)
	}
	if !hasWord(x.Vocabulary, "Сапар") {
		t.Errorf("панель потеряла слова проекта: %v", x.Vocabulary)
	}
}

// --- терминал ----------------------------------------------------------------

// Разговор при заведении проекта. Спрашиваем ровно тогда, когда человек не
// сказал ничего, кроме названия.
func TestProjectAddAsksWhenNothingTold(t *testing.T) {
	cfgPath := testConfig(t)
	restore := pretendTerminal(t)
	defer restore()

	answers := strings.Join([]string{
		"приём денег",              // о чём проект
		"биллинг, платежи",         // как называют вслух
		"Орынгали, Рустем",         // кто участвует
		"Сапар, ЛК",                // сервисы и сокращения
		"https://github.com/o/pay", // источник
		"",                         // хватит
	}, "\n") + "\n"

	withStdin(t, answers, func() {
		if err := cmdProjectAdd([]string{"-c", cfgPath, "Платежи"}); err != nil {
			t.Fatalf("projects add: %v", err)
		}
	})

	p := savedProject(t, cfgPath, "Платежи")
	if p.About != "приём денег" {
		t.Errorf("описание: %q", p.About)
	}
	if strings.Join(p.Aliases, ",") != "биллинг,платежи" {
		t.Errorf("псевдонимы: %v", p.Aliases)
	}
	if strings.Join(p.People, ",") != "Орынгали,Рустем" {
		t.Errorf("люди: %v", p.People)
	}
	if strings.Join(p.otherWords(), ",") != "Сапар,ЛК" {
		t.Errorf("словарь: %v", p.otherWords())
	}
	if len(p.Sources) != 1 || p.Sources[0].Kind != "repo" {
		t.Errorf("источник разобран как %+v — ссылка на GitHub это репозиторий", p.Sources)
	}
}

// Названный флагами проект вопросов не получает: `projects add` зовут из
// скриптов, и вопрос там — это подвисший навсегда конвейер.
func TestProjectAddStaysSilentWithFlags(t *testing.T) {
	cfgPath := testConfig(t)
	restore := pretendTerminal(t)
	defer restore()

	// Труба, в которую никто никогда не напишет и которую никто не закроет.
	// Любое чтение из неё — вечная блокировка, и тест это увидит.
	blocked, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	defer blocked.Close()
	old := os.Stdin
	os.Stdin = blocked
	defer func() { os.Stdin = old }()

	done := make(chan error, 1)
	go func() {
		done <- cmdProjectAdd([]string{"-c", cfgPath, "Платежи", "--about", "приём денег"})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("projects add с флагами: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("projects add с флагами полез спрашивать и завис")
	}

	p := savedProject(t, cfgPath, "Платежи")
	if p.About != "приём денег" || len(p.People) != 0 {
		t.Errorf("сохранилось не то, что просили: %+v", p)
	}
}

// В конвейере вопросов нет вовсе, даже когда о проекте не сказано ничего:
// на том конце некому отвечать.
func TestProjectAddDoesNotAskWithoutTerminal(t *testing.T) {
	cfgPath := testConfig(t)

	blocked, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	defer blocked.Close()
	old := os.Stdin
	os.Stdin = blocked
	defer func() { os.Stdin = old }()

	done := make(chan error, 1)
	go func() { done <- cmdProjectAdd([]string{"-c", cfgPath, "Платежи"}) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("projects add в конвейере: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("projects add без терминала задал вопрос и завис")
	}
	if p := savedProject(t, cfgPath, "Платежи"); p.Name != "Платежи" {
		t.Errorf("проект не завёлся: %+v", p)
	}
}

// Ввод может кончиться посреди разговора: Ctrl+D, закрытая труба. Оставшиеся
// вопросы задавать некому — и уж точно нельзя вываливать их все разом.
func TestProjectAskSurvivesEndOfInput(t *testing.T) {
	var out strings.Builder
	p := Project{Name: "Платежи"}
	askProjectFrom(t, &p, "приём денег\n", &out)

	if p.About != "приём денег" {
		t.Errorf("первый ответ потерялся: %q", p.About)
	}
	if len(p.People) != 0 || len(p.Sources) != 0 {
		t.Errorf("после конца ввода что-то заполнилось: %+v", p)
	}
	// «Кто участвует» — четвёртый вопрос; после конца ввода его быть не должно.
	if strings.Contains(out.String(), "Кто в нём участвует") {
		t.Errorf("вопросы посыпались после конца ввода:\n%s", out.String())
	}
}

// Разговор не должен спрашивать вид источника отдельным вопросом: по строке
// видно, репозиторий это, адрес или каталог.
func TestGuessSourceKind(t *testing.T) {
	for value, want := range map[string]string{
		"git@github.com:org/pay.git":              "repo",
		"https://github.com/org/pay":              "repo",
		"https://gitlab.com/org/pay":              "repo",
		"https://github.com/org/pay/issues/12":    "url",
		"https://pay.example.com/docs":            "url",
		"~/work/payments":                         "path",
		"/Users/kto-to/work/pay":                  "path",
		"https://www.github.com/org/pay":          "repo",
		"ssh://git@git.example.com/org/pay":       "repo",
		"https://example.com/a/b/c/d":             "url",
		"https://docs.google.com/document/d/1/ed": "url",
	} {
		if got := guessSourceKind(value); got != want {
			t.Errorf("guessSourceKind(%q) = %q, ожидали %q", value, got, want)
		}
	}
}

// --- подпорки -----------------------------------------------------------------

func testStore(t *testing.T) *Store {
	t.Helper()
	st, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// testConfig — конфиг с базой в своём каталоге: команды CLI открывают базу
// сами и по нему её и находят.
func testConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "steno.json")
	if err := os.WriteFile(path,
		[]byte(`{"data_dir":`+quote(filepath.Join(dir, "data"))+`}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func savedProject(t *testing.T, cfgPath, name string) Project {
	t.Helper()
	_, st, err := open(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p, err := st.Project(name)
	if err != nil {
		t.Fatalf("проект %q не найден: %v", name, err)
	}
	return p
}

// pretendTerminal заставляет `projects add` считать, что на том конце человек:
// у теста стдин всегда труба, и настоящая проверка всегда отвечает «нет».
func pretendTerminal(t *testing.T) func() {
	t.Helper()
	old := askableStdin
	askableStdin = func() bool { return true }
	return func() { askableStdin = old }
}

func askProjectFrom(t *testing.T, p *Project, input string, out io.Writer) {
	t.Helper()
	(&projectAsk{in: bufio.NewReader(strings.NewReader(input)), out: out}).run(p, false)
}

func hasWord(words []string, want string) bool {
	for _, w := range words {
		if strings.EqualFold(w, want) {
			return true
		}
	}
	return false
}

// --- терминальный интерфейс ----------------------------------------------------

// Тот же словарь правится из `steno ui`. Поле, добавленное только в панель,
// существует только в панели: человек, живущий в терминале, о нём не узнает.
func TestUIProjectFormKeepsVocabulary(t *testing.T) {
	m := uiTestModel(t, nil)
	press(m, "3", "n")

	typeIn(m, "Платежи")
	press(m, "tab")
	typeIn(m, "приём денег")
	press(m, "tab")
	typeIn(m, "биллинг")
	press(m, "tab")
	typeIn(m, "Орынгали, Рустем")
	press(m, "tab")
	typeIn(m, "Сапар, ЛК")
	press(m, "ctrl+s")

	if m.screen() != scrList {
		t.Fatalf("после сохранения остались на %v: %s", m.screen(), screen(m))
	}
	p, err := m.st.Project("Платежи")
	if err != nil {
		t.Fatalf("проект не сохранился: %v", err)
	}
	if strings.Join(p.People, ",") != "Орынгали,Рустем" {
		t.Errorf("люди из формы: %v", p.People)
	}
	if strings.Join(p.otherWords(), ",") != "Сапар,ЛК" {
		t.Errorf("словарь из формы: %v", p.otherWords())
	}
	if !hasWord(p.Vocabulary, "Орынгали") {
		t.Errorf("имя из формы не попало в словарь whisper: %v", p.Vocabulary)
	}

	// Открыли на правку: имена в своём поле, и они же не продублированы в
	// поле словаря — иначе стёртое в одном остаётся в другом.
	m.selectProject("Платежи")
	press(m, "e")
	if m.screen() != scrForm {
		t.Fatalf("e не открыла форму: %v", m.screen())
	}
	if m.form.people.String() != "Орынгали, Рустем" {
		t.Errorf("поле людей при правке: %q", m.form.people.String())
	}
	if strings.Contains(m.form.words.String(), "Орынгали") {
		t.Errorf("имя человека продублировалось в поле словаря: %q", m.form.words.String())
	}
	wantContains(t, screen(m), "кто участвует", "подпись поля с людьми")

	// И в карточке проекта словарь виден: править его можно там, где он
	// показан, а не там, где о нём надо догадаться.
	press(m, "esc", "enter")
	if m.screen() != scrProject {
		t.Fatalf("enter не открыл карточку: %v", m.screen())
	}
	body := bodyText(m)
	wantContains(t, body, "Орынгали", "люди в карточке проекта")
	wantContains(t, body, "Сапар", "слова проекта в карточке")
}

// `steno projects add` по уже заведённому проекту — обычный способ приложить к
// нему репозиторий. Раньше это заменяло запись целиком: описание и псевдонимы
// стирались молча. Со словарём цена такой потери выросла — имя, пропавшее из
// подсказки распознавания, возвращается не правкой, а перезаписью созвона.
func TestProjectAddDoesNotWipeWhatIsAlreadyThere(t *testing.T) {
	cfgPath := testConfig(t)

	if err := cmdProjectAdd([]string{"-c", cfgPath, "Платежи",
		"--about", "приём денег", "--alias", "биллинг",
		"--people", "Орынгали", "--word", "Сапар",
		"--path", "/tmp/pay"}); err != nil {
		t.Fatal(err)
	}
	// Второй заход добавляет только источник — про остальное не сказано ничего.
	if err := cmdProjectAdd([]string{"-c", cfgPath, "Платежи",
		"--repo", "git@github.com:org/pay"}); err != nil {
		t.Fatal(err)
	}

	p := savedProject(t, cfgPath, "Платежи")
	if p.About != "приём денег" {
		t.Errorf("описание стёрлось: %q", p.About)
	}
	if !hasWord(p.Aliases, "биллинг") {
		t.Errorf("псевдонимы стёрлись: %v", p.Aliases)
	}
	if !hasWord(p.People, "Орынгали") {
		t.Errorf("люди стёрлись: %v", p.People)
	}
	if !hasWord(p.Vocabulary, "Сапар") || !hasWord(p.Vocabulary, "Орынгали") {
		t.Errorf("словарь стёрся: %v", p.Vocabulary)
	}
	if len(p.Sources) != 2 {
		t.Errorf("источники: %+v — ожидали и старый, и новый", p.Sources)
	}

	// А тот же источник, названный дважды, вторым экземпляром не становится:
	// это вдвое больше материала в справке ради того же самого.
	if err := cmdProjectAdd([]string{"-c", cfgPath, "Платежи",
		"--repo", "git@github.com:org/pay"}); err != nil {
		t.Fatal(err)
	}
	if again := savedProject(t, cfgPath, "Платежи"); len(again.Sources) != 2 {
		t.Errorf("источник продублировался: %+v", again.Sources)
	}
}

// Промпт не переводится, и это не недосмотр.
//
// Его читает Claude, а не человек, и он обязан совпадать с followupSystem —
// русской константой, на которой мерили качество разбора. Язык ответа задаётся
// в самом промпте, отдельной строкой и правилом 6, а не языком интерфейса.
//
// Тест сторожит возврат tr() на эти строки: правка выглядит безобидной
// («забыли перевести»), а ломает она правила, которые ссылаются на слова
// каркаса дословно.
func TestFollowupPromptStaysRussianInEnglishUI(t *testing.T) {
	old := uiLang
	uiLang = langEN
	defer func() { uiLang = old }()

	// Ловушка. Из каталога эти строки убраны, поэтому вернувшийся tr() сам по
	// себе ничего не изменит — и тест бы его не заметил, а заметил бы человек
	// на созвоне, когда кто-нибудь дозаполнит i18n_en.go. Подкладываем перевод
	// сами: теперь любая обёртка tr() видна сразу.
	//
	// Каталог общий на пакет, поэтому тест не параллельный и всё за собой
	// убирает.
	for _, k := range []string{
		"Название встречи: %s\n", "Дата: %s\n", "Участники: %s\n",
		"Приглашены в календаре: %s\n", "Язык follow-up: %s\n",
		"\nПроекты команды:\n", " (вслух: %s)", "    люди: %s\n",
		"    сервисы и сокращения (не люди): %s\n",
		"  %s — если непонятно, к чему относится\n", "\nРасшифровка:\n\n",
		"неизвестно",
	} {
		if _, busy := trEN[k]; busy {
			t.Fatalf("строка %q снова в каталоге переводов — промпт переводиться не должен", k)
		}
		trEN[k] = "!ПЕРЕВЕДЕНО!"
		defer delete(trEN, k)
	}

	cfg := defaultConfig()
	head := followupHead(cfg, &Meeting{
		Title: "Планёрка", StartedAt: time.Now(),
		Participants: []string{"TomXemmings"},
		Invitees:     []string{"kto-to@example.com"},
	}, []Project{{
		Name: "Платежи", Aliases: []string{"биллинг"}, About: "приём денег",
		People: []string{"Орынгали"}, Vocabulary: []string{"Сапар"},
	}}, "", "")

	for _, want := range []string{
		"Название встречи:", "Дата:", "Участники:", "Приглашены в календаре:",
		"Проекты команды:", "(вслух:", "люди:", "сервисы и сокращения (не люди):",
		"если непонятно, к чему относится", "Расшифровка:",
	} {
		if !strings.Contains(head, want) {
			t.Errorf("подпись %q перевелась — правила followupSystem ссылаются на русские слова:\n%s",
				want, head)
		}
	}

	// А язык ответа при этом английский, и сказано об этом внутри промпта.
	if cfg.Claude.OutputLanguage != "English" {
		t.Fatalf("язык follow-up при английском интерфейсе: %q", cfg.Claude.OutputLanguage)
	}
	if !strings.Contains(head, "Язык follow-up: English") {
		t.Errorf("в промпте нет указания отвечать по-английски:\n%s", head)
	}

	// «неизвестно» — не подпись, а слово, на которое ссылается правило 9:
	// «Имя "неизвестно" означает, что имя не удалось снять». Переведись оно —
	// правило перестало бы срабатывать, и реплика без имени молча уехала бы
	// соседнему говорящему.
	line := renderTranscript([]Segment{{Start: 1, Text: "возьму на себя"}})
	if !strings.Contains(line, "неизвестно") {
		t.Errorf("имя говорящего без подписи перевелось: %q", line)
	}
	if !strings.Contains(strings.Join(strings.Fields(followupSystem), " "), "Имя «неизвестно»") {
		t.Error("правило про «неизвестно» пропало из followupSystem — слово в расшифровке осталось без смысла")
	}
}
