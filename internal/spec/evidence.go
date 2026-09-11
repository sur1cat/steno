package spec

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/sur1cat/steno/internal/brain"
	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
)

// Что о задаче видно в самом репозитории.
//
// Справка о проекте (brain.BuildPrimer) отвечает на вопрос «что это за
// проект» — тридцать строк на весь репозиторий, и по ним нельзя написать ТЗ:
// в них нет ни одного файла. Поэтому здесь собирается второе, узкое: где
// именно в коде лежит то, о чём говорили на созвоне.
//
// Ищем по словам самой задачи, и главная трудность в том, что на созвоне
// говорят по-русски, а в коде всё латиницей. «Ануару нужно закончить Сапар» —
// в репозитории это каталог sapar/, сервис worker_sapar и очередь -Q sapar, и
// связать одно с другим можно только транслитерацией. Без неё ТЗ получается
// «по мотивам»: модель знает название проекта, но не знает ни одного файла и
// начинает их придумывать.

// Evidence — материал по конкретной задаче. Всё ограничено по объёму: это
// приложение к промпту, а не выгрузка репозитория.
type Evidence struct {
	Repo     string   // каталог репозитория
	Needles  []string // по каким словам искали — их видно в ТЗ и в логе
	Services []string // сервисы из docker-compose, попавшие под слова
	Paths    []string // файлы и каталоги, попавшие под слова
	Hits     []Hit    // строки кода, попавшие под слова
	Modules  []string // модули верхнего уровня — общая карта
	Verify   []string // чем в этом проекте принято проверять
}

type Hit struct {
	Path string
	Line string
}

const (
	maxPaths   = 40
	maxHits    = 30
	maxWalk    = 40000
	maxNeedles = 8
)

// noiseDirs — то, что никогда не является ответом на вопрос «где это в коде».
// Сюда же .claude/worktrees: там лежат рабочие копии прошлых запусков агента, и
// найденный в них файл — это наш собственный вчерашний след, выданный за код
// проекта.
var noiseDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "dist": true,
	"build": true, "target": true, ".venv": true, "venv": true,
	"__pycache__": true, ".next": true, "coverage": true, ".idea": true,
	".mypy_cache": true, ".pytest_cache": true, ".claude": true,
	"staticfiles": true, "media": true,
}

// Collect собирает материал по задаче. Ошибки поиска не фатальны: ТЗ без
// найденных файлов хуже, но честнее — оно просто назовёт это в своих дырах.
func Collect(ctx context.Context, repo string, it core.ProjectItem) Evidence {
	ev := Evidence{Repo: repo}
	ev.Needles = Needles(it.Text + " " + it.Quote)
	ev.Modules = brain.TopModuleNames(repo)
	ev.Verify = verifyWays(repo)

	for _, s := range brain.ComposeServices(repo) {
		if matchesAny(strings.ToLower(s), ev.Needles) {
			ev.Services = append(ev.Services, s)
		}
	}
	ev.Paths = matchPaths(repo, ev.Needles)
	ev.Hits = grepHits(ctx, repo, ev.Needles)
	return ev
}

// Render — материал так, как его читает модель.
func (e Evidence) Render() string {
	var b strings.Builder
	b.WriteString("\nЧто нашлось в репозитории по словам задачи.\n")
	fmt.Fprintf(&b, "Искали по: %s\n", strings.Join(e.Needles, ", "))
	if len(e.Needles) == 0 {
		b.WriteString("Из задачи не вышло ни одного слова для поиска — по коду не искали вовсе.\n")
	}
	if len(e.Services) > 0 {
		fmt.Fprintf(&b, "\nСервисы docker-compose под эти слова: %s\n", strings.Join(e.Services, ", "))
	}
	if len(e.Paths) > 0 {
		b.WriteString("\nФайлы и каталоги под эти слова (пути от корня репозитория):\n")
		for _, p := range e.Paths {
			fmt.Fprintf(&b, "  %s\n", p)
		}
	}
	if len(e.Hits) > 0 {
		b.WriteString("\nСтроки кода под эти слова:\n")
		for _, h := range e.Hits {
			fmt.Fprintf(&b, "  %s: %s\n", h.Path, h.Line)
		}
	}
	if len(e.Paths) == 0 && len(e.Hits) == 0 && len(e.Services) == 0 && len(e.Needles) > 0 {
		// Это важное сведение, а не пустой раздел: ТЗ, написанное поверх такого
		// поиска, не опирается на код вообще, и знать об этом должна и модель,
		// и человек.
		b.WriteString("\nПо этим словам в коде не нашлось НИЧЕГО. Значит, разговор шёл " +
			"словами, которых в этом репозитории нет, — или речь вообще о другом проекте.\n")
	}
	if len(e.Modules) > 0 {
		fmt.Fprintf(&b, "\nМодули верхнего уровня: %s\n", strings.Join(e.Modules, ", "))
	}
	if len(e.Verify) > 0 {
		b.WriteString("\nЧем в этом проекте проверяют:\n")
		for _, v := range e.Verify {
			fmt.Fprintf(&b, "  %s\n", v)
		}
	} else {
		b.WriteString("\nСледов автоматических проверок в проекте не нашлось.\n")
	}
	return b.String()
}

// --- слова для поиска -------------------------------------------------------

// Needles — по каким словам искать в коде.
//
// Русские слова переводятся в латиницу и обрезаются до основы: на созвоне
// говорят «сапаром», «по сапару», а каталог называется sapar. Английские
// берутся как есть — их в речи тоже хватает.
func Needles(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	}) {
		if stopWords[w] {
			continue
		}
		n := needle(w)
		if len(n) < 4 || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
		if len(out) >= maxNeedles {
			break
		}
	}
	return out
}

func needle(w string) string {
	if !hasCyrillic(w) {
		return w
	}
	return translit(stem(w))
}

func hasCyrillic(s string) bool {
	for _, r := range s {
		if r >= 'а' && r <= 'я' || r == 'ё' {
			return true
		}
	}
	return false
}

// stem срезает окончание. Список короткий и нарочно грубый: нам не нужна
// морфология, нужна основа, по которой сработает поиск подстроки. Длинные
// окончания идут первыми, иначе «сапарами» потеряет только «и».
//
// Глагольных окончаний здесь нет намеренно. Ищем мы имена вещей — модулей,
// сервисов, полей, — а не действия; зато «-ли» и «-ла», срезанные у
// существительного, режут его до неузнаваемости: «водители» стало бы «водите».
var endings = []string{
	"ами", "ями", "ого", "его", "ому", "ему", "ыми", "ими", "ой", "ей",
	"ов", "ев", "ам", "ям", "ах", "ях", "ом", "ем", "ый", "ий", "ая", "яя",
	"ое", "ее", "ые", "ие", "у", "ю", "а", "я", "ы", "и", "е", "о", "ь",
}

func stem(w string) string {
	r := []rune(w)
	for _, e := range endings {
		er := []rune(e)
		if len(r)-len(er) >= 4 && string(r[len(r)-len(er):]) == e {
			return string(r[:len(r)-len(er)])
		}
	}
	return w
}

// translit — та самая таблица, ради которой всё. Вариант перевода выбран один,
// самый частый в именах файлов: «х» как h, «ц» как ts. Второго варианта не
// заводим — поиск по подстроке от лишних нитей только зашумится.
var lat = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e", 'ё': "e",
	'ж': "zh", 'з': "z", 'и': "i", 'й': "y", 'к': "k", 'л': "l", 'м': "m",
	'н': "n", 'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t", 'у': "u",
	'ф': "f", 'х': "h", 'ц': "ts", 'ч': "ch", 'ш': "sh", 'щ': "sch",
	'ъ': "", 'ы': "y", 'ь': "", 'э': "e", 'ю': "yu", 'я': "ya",
}

func translit(s string) string {
	var b strings.Builder
	for _, r := range s {
		if v, ok := lat[r]; ok {
			b.WriteString(v)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// stopWords — слова, по которым искать бессмысленно: они есть в любой задаче и
// найдутся в любом репозитории. Список ручной и короткий; всё, чего в нём нет,
// проходит через ограничение на длину основы.
var stopWords = map[string]bool{
	"надо": true, "нужно": true, "должен": true, "должна": true, "сделать": true,
	"проверить": true, "закончить": true, "посмотреть": true, "разобраться": true,
	"добавить": true, "поправить": true, "чтобы": true, "который": true,
	"когда": true, "если": true, "потом": true, "сегодня": true, "завтра": true,
	"после": true, "перед": true, "может": true, "можно": true, "будет": true,
	"было": true, "этот": true, "эта": true, "это": true, "там": true, "тут": true,
	"все": true, "всё": true, "весь": true, "себя": true, "него": true, "them": true,
	"нормальные": true, "нормально": true, "работает": true, "работать": true,
	"and": true, "the": true, "for": true, "with": true, "that": true,
	"need": true, "should": true, "make": true, "check": true, "fix": true,
}

func matchesAny(s string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

// --- поиск по репозиторию ---------------------------------------------------

// matchPaths — файлы и каталоги, в имени которых есть одно из слов.
//
// Порядок ответа — по глубине: sapar/ важнее, чем
// sapar/migrations/0007_sapardocument_period.py, и если резать список по
// потолку, то резать надо с конца, а не по алфавиту.
func matchPaths(repo string, needles []string) []string {
	if len(needles) == 0 {
		return nil
	}
	var out []string
	seen := 0
	_ = filepath.WalkDir(repo, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if seen++; seen > maxWalk {
			return fs.SkipAll
		}
		name := d.Name()
		if d.IsDir() {
			if path != repo && (noiseDirs[name] || strings.HasPrefix(name, ".")) {
				return fs.SkipDir
			}
		} else if strings.HasSuffix(name, ".pyc") || strings.HasSuffix(name, ".map") {
			return nil
		}
		if !matchesAny(strings.ToLower(name), needles) {
			return nil
		}
		rel, err := filepath.Rel(repo, path)
		if err != nil || rel == "." {
			return nil
		}
		if d.IsDir() {
			rel += "/"
		}
		out = append(out, rel)
		return nil
	})
	// Глубину считаем без завершающей косой черты у каталогов: иначе sapar/ и
	// apps/sapar_config.py оказываются на одном уровне, и наверх всплывает тот,
	// у кого имя раньше по алфавиту, а не тот, кто ближе к корню.
	depth := func(p string) int {
		return strings.Count(strings.TrimSuffix(p, "/"), string(filepath.Separator))
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := depth(out[i]), depth(out[j])
		if a != b {
			return a < b
		}
		return out[i] < out[j]
	})
	if len(out) > maxPaths {
		out = out[:maxPaths]
	}
	return out
}

// grepHits — строки, где эти слова встречаются в коде.
//
// Через `git grep`, а не своим обходом: он уже умеет не заходить в
// .gitignore — то есть мимо виртуальных окружений, сборок и кешей, которых в
// живом рабочем каталоге больше, чем самого кода. В каталоге без git ничего не
// ищем: имена файлов у нас уже есть, а обход всего подряд ради строк — это
// минуты на большом проекте.
func grepHits(ctx context.Context, repo string, needles []string) []Hit {
	if len(needles) == 0 {
		return nil
	}
	args := []string{"-C", repo, "grep", "--no-color", "-n", "-I", "-i", "-F"}
	for _, n := range needles {
		args = append(args, "-e", n)
	}
	out, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil && len(out) == 0 {
		return nil
	}
	var hits []Hit
	for _, line := range strings.Split(string(out), "\n") {
		path, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if skipPath(path) {
			continue
		}
		_, text, ok := strings.Cut(rest, ":")
		if !ok {
			continue
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		hits = append(hits, Hit{Path: path, Line: i18n.Cut(text, 200)})
		if len(hits) >= maxHits {
			break
		}
	}
	return hits
}

func skipPath(p string) bool {
	for _, part := range strings.Split(p, "/") {
		if noiseDirs[part] {
			return true
		}
	}
	// Миграции упоминают всё на свете и не говорят ничего о том, как это
	// работает сейчас. В путях они остаются — по ним видно, что модель есть, —
	// а вот строками они бы вытеснили настоящий код из потолка в тридцать строк.
	return strings.Contains(p, "/migrations/") || strings.HasSuffix(p, ".lock")
}

// verifyWays — чем в этом проекте принято проверять себя.
//
// Только факты, найденные на диске: цели Makefile, файлы настройки тестов,
// скрипты package.json. Команду не выдумываем — «наверное, pytest» в ТЗ
// выглядит как указание и стоит человеку получаса на выяснение, что такой
// команды в проекте нет.
func verifyWays(repo string) []string {
	var out []string
	if raw, err := os.ReadFile(filepath.Join(repo, "Makefile")); err == nil {
		var targets []string
		for _, line := range strings.Split(string(raw), "\n") {
			name, _, ok := strings.Cut(line, ":")
			if !ok || name == "" || strings.ContainsAny(name, " \t#.$/") {
				continue
			}
			targets = append(targets, name)
			if len(targets) >= 20 {
				break
			}
		}
		if len(targets) > 0 {
			out = append(out, "Makefile, цели: "+strings.Join(targets, ", "))
		}
	}
	for _, f := range []string{"pytest.ini", "tox.ini", "setup.cfg", "manage.py",
		"go.mod", "package.json", "Cargo.toml", ".golangci.yml", "conftest.py"} {
		if _, err := os.Stat(filepath.Join(repo, f)); err == nil {
			out = append(out, "есть "+f)
		}
	}
	if _, err := os.Stat(filepath.Join(repo, "docker-compose.yml")); err == nil {
		out = append(out, "есть docker-compose.yml")
	}
	return out
}

// RepoOf — каталог с кодом проекта.
//
// Берём первый источник, который лежит на диске: path — как есть, repo —
// поверхностный клон, который steno уже сделал для справки. Клонировать здесь
// заново не надо, а вот отсутствие каталога — повод отказаться от ТЗ, а не
// выдумывать его по одной справке.
func RepoOf(dataDir string, p core.Project) (string, error) {
	for _, s := range p.Sources {
		switch s.Kind {
		case "path":
			dir := brain.ExpandHome(s.Value)
			if st, err := os.Stat(dir); err == nil && st.IsDir() {
				return dir, nil
			}
		case "repo":
			dir := filepath.Join(dataDir, "repos", brain.SafeName(p.Name))
			if st, err := os.Stat(dir); err == nil && st.IsDir() {
				return dir, nil
			}
		}
	}
	return "", fmt.Errorf(i18n.Tr("у проекта %q нет каталога с кодом на этой машине"), p.Name)
}
