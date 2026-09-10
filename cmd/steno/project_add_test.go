package main

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sur1cat/steno/internal/core"
)

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
	if strings.Join(p.OtherWords(), ",") != "Сапар,ЛК" {
		t.Errorf("словарь: %v", p.OtherWords())
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
	p := core.Project{Name: "Платежи"}
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

func savedProject(t *testing.T, cfgPath, name string) core.Project {
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

func askProjectFrom(t *testing.T, p *core.Project, input string, out io.Writer) {
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
