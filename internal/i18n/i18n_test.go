package i18n

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Каталог переводов проверяется по всему репозиторию, а не по своему каталогу:
// после разбора по пакетам вызовы Tr() остались во всех internal/* и cmd/, а
// сам каталог лежит здесь. Обход начинается от go.mod и берёт каждый .go.
func sourceFiles(t *testing.T) []string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("не нашли корень репозитория: %v", err)
	}
	var out []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "web", "assets", "adapters", "bar", "docker":
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == "i18n_en.go" {
			return nil
		}
		out = append(out, path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// trCall распознаёт и Tr("…") внутри этого пакета, и i18n.Tr("…") во всех
// остальных: после разбора по пакетам вызов стал квалифицированным, и обход,
// который умеет только *ast.Ident, нашёл бы ноль строк и сторожил бы пустоту.
func trCall(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		if fn.Name == "Tr" || fn.Name == "Trf" {
			return fn.Name
		}
	case *ast.SelectorExpr:
		x, ok := fn.X.(*ast.Ident)
		if ok && x.Name == "i18n" && (fn.Sel.Name == "Tr" || fn.Sel.Name == "Trf") {
			return fn.Sel.Name
		}
	}
	return ""
}

// Каждая строка, обёрнутая в Tr(), обязана иметь перевод. Пропуск не роняет
// сборку и не виден в тестах — он виден только человеку, у которого посреди
// английского экрана вдруг русское слово. Поэтому за этим следит тест.
//
// Исключения перечислены поимённо: это не текст для человека, а регулярные
// выражения и маркеры, перевод которых сломал бы сопоставление.
func TestTranslationsCoverEveryTrCall(t *testing.T) {
	allowed := map[string]bool{
		`модель\s+(\S+)`: true,
		"#беззаписи":     true,
	}
	files := sourceFiles(t)
	fset := token.NewFileSet()
	var missing []string
	seen := map[string]bool{}
	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			if trCall(call) == "" {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true // Tr(const) — константу проверяем ниже, по каталогу
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil || seen[s] {
				return true
			}
			seen[s] = true
			if TrEN[s] == "" && !allowed[s] {
				missing = append(missing, path+": "+strconv.Quote(Cut(s, 60)))
			}
			return true
		})
	}
	if len(missing) > 0 {
		t.Errorf("нет английского перевода для %d строк:\n%s", len(missing), strings.Join(missing, "\n"))
	}
	if len(seen) < 1000 {
		t.Errorf("найдено всего %d вызовов Tr() — похоже, обход сломался", len(seen))
	}
}

// Tr(const) проверка выше пропускает: в вызове стоит имя, а не строка. Обещание
// «константу проверяем ниже, по каталогу» до сих пор ничем не подкреплялось —
// и самая крупная из них, справка usage, оставалась без присмотра. Правка одной
// строки в usage меняет ключ целиком, английского значения с таким ключом в
// каталоге нет, и Tr() честно возвращает оригинал: вся справка `steno -h`
// молча выходит по-русски. Ровно это и ловится здесь.
func TestTranslationsCoverStringConstants(t *testing.T) {
	files := sourceFiles(t)
	fset := token.NewFileSet()
	consts := map[string]string{} // имя константы → её значение
	used := map[string][]string{} // имя константы → где её обернули в Tr()
	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if spec, ok := n.(*ast.ValueSpec); ok && len(spec.Names) == len(spec.Values) {
				for i, name := range spec.Names {
					lit, ok := spec.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					if s, err := strconv.Unquote(lit.Value); err == nil {
						consts[name.Name] = s
					}
				}
				return true
			}
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			if trCall(call) == "" {
				return true
			}
			if arg, ok := call.Args[0].(*ast.Ident); ok {
				used[arg.Name] = append(used[arg.Name], path)
			}
			return true
		})
	}

	// Справка обязана быть среди найденного: если обход её потерял, тест
	// проходит на пустом месте и стережёт пустоту.
	if len(used["usage"]) == 0 {
		t.Fatal("не нашли Tr(usage) — обход по константам сломался")
	}
	for name, where := range used {
		value, ok := consts[name]
		if !ok {
			continue // константа не здешняя или собрана из кусков
		}
		if TrEN[value] == "" {
			t.Errorf("нет английского перевода для константы %s (%s): ключ начинается с %q",
				name, strings.Join(where, ", "), Cut(value, 60))
		}
	}
}

// Форматная строка должна пережить перевод: %s, %d и %w обязаны остаться на
// местах и в том же порядке. Перепутанный глагол формата — это паника в
// рантайме на языке, которым разработчик не пользуется.
func TestTranslationsKeepFormatVerbs(t *testing.T) {
	verbs := func(s string) []string {
		var out []string
		for i := 0; i < len(s); i++ {
			if s[i] != '%' || i+1 >= len(s) {
				continue
			}
			j := i + 1
			for j < len(s) && strings.ContainsRune("#+- 0123456789.", rune(s[j])) {
				j++
			}
			if j < len(s) {
				out = append(out, s[i:j+1])
			}
			i = j
		}
		return out
	}
	for ru, en := range TrEN {
		a, b := verbs(ru), verbs(en)
		if strings.Join(a, "") != strings.Join(b, "") {
			t.Errorf("глаголы формата разошлись:\n  ru %q → %v\n  en %q → %v", Cut(ru, 60), a, Cut(en, 60), b)
		}
	}
}
