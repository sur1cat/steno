package brain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
)

// Справка о проекте.
//
// Названия и псевдонимов мало. На созвоне говорят «поправим вебхуки в
// биллинге», «миграция схемы не успевает», «онбординг режем до трёх экранов» —
// и разложить это по проектам можно, только зная, из чего проект состоит и
// какими словами команда о нём говорит. Эти слова лежат в его репозитории и на
// его сайте.
//
// Читать репозиторий целиком на каждом созвоне нельзя: это десятки тысяч
// токенов на каждый проект. Поэтому материал собирается один раз в справку
// строк на тридцать, и уже она уходит в промпт.

// GatherSources собирает сырой материал. Всё ограничено по объёму: справка
// делается из README и структуры, а не из всего кода.
func GatherSources(ctx context.Context, dataDir string, p core.Project) (string, string, error) {
	var b strings.Builder
	var fp strings.Builder

	for _, src := range p.Sources {
		switch src.Kind {
		case "text":
			fmt.Fprintf(&b, i18n.Tr("\n## Описание\n\n%s\n"), src.Value)
			fp.WriteString(src.Value)

		case "path", "repo":
			dir := src.Value
			if src.Kind == "repo" {
				var err error
				if dir, err = ShallowClone(ctx, dataDir, p.Name, src.Value); err != nil {
					return "", "", fmt.Errorf("%s: %w", src.Value, err)
				}
			}
			dir = ExpandHome(dir)
			part, mark, err := describeRepo(ctx, dir)
			if err != nil {
				return "", "", fmt.Errorf("%s: %w", dir, err)
			}
			b.WriteString(part)
			fp.WriteString(mark)

		case "url":
			text, err := fetchText(ctx, src.Value)
			if err != nil {
				return "", "", fmt.Errorf("%s: %w", src.Value, err)
			}
			fmt.Fprintf(&b, i18n.Tr("\n## Сайт %s\n\n%s\n"), src.Value, text)
			fp.WriteString(src.Value)

		default:
			return "", "", fmt.Errorf(i18n.Tr("непонятный источник %q"), src.Kind)
		}
	}
	// Отпечаток — по самому материалу, а не по хешу коммита и адресам источников.
	// Раньше он их и считал, и когда steno научился читать из репозитория больше
	// (имена сервисов, авторов коммитов, модули), справки у всех остались
	// прежними: коммит тот же, отпечаток тот же, пересборки нет. По материалу
	// такого не бывает — изменилось то, что мы читаем, изменился и отпечаток.
	sum := sha256.Sum256([]byte(fp.String() + "\x00" + b.String()))
	return b.String(), hex.EncodeToString(sum[:8]), nil
}

func ExpandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// ShallowClone тянет репозиторий на один коммит: история для справки не нужна,
// а на большом репозитории полный клон — это минуты и гигабайты.
func ShallowClone(ctx context.Context, dataDir, project, url string) (string, error) {
	dir := filepath.Join(dataDir, "repos", SafeName(project))
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		cmd := exec.CommandContext(ctx, "git", "-C", dir, "fetch", "--depth", "1", "origin")
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("git fetch: %s", i18n.Tail(string(out), 300))
		}
		_ = exec.CommandContext(ctx, "git", "-C", dir, "reset", "--hard", "origin/HEAD").Run()
		return dir, nil
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", url, dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git clone: %s", i18n.Tail(string(out), 300))
	}
	return dir, nil
}

var unsafeName = regexp.MustCompile(`[^\p{L}\p{N}_-]+`)

func SafeName(s string) string {
	return strings.Trim(unsafeName.ReplaceAllString(s, "-"), "-")
}

var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "dist": true,
	"build": true, "target": true, ".venv": true, "__pycache__": true,
	".next": true, "coverage": true, ".idea": true,
}

// describeRepo вытаскивает то, из чего видно суть проекта и его словарь:
// README, состав верхнего уровня, манифесты и темы последних коммитов.
func describeRepo(ctx context.Context, dir string) (string, string, error) {
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return "", "", errors.New(i18n.Tr("не каталог"))
	}
	var b strings.Builder
	fmt.Fprintf(&b, i18n.Tr("\n## Репозиторий %s\n"), filepath.Base(dir))

	for _, name := range []string{"README.md", "README", "readme.md", "README.rst"} {
		if raw, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			fmt.Fprintf(&b, "\n### README\n\n%s\n", i18n.Cut(string(raw), 8000))
			break
		}
	}
	for _, name := range []string{"package.json", "go.mod", "pyproject.toml", "Cargo.toml", "composer.json"} {
		if raw, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			fmt.Fprintf(&b, "\n### %s\n\n%s\n", name, i18n.Cut(string(raw), 1500))
		}
	}

	// Имена сервисов из docker-compose. Это самый прямой словарь того, из чего
	// проект состоит и как эти части зовут вслух. На живом созвоне человек
	// сказал «Сапар», и это оказался worker_sapar — но справка его не знала, и
	// модель гадала, человек это или название.
	if names := ComposeServices(dir); len(names) > 0 {
		fmt.Fprintf(&b, i18n.Tr("\n### Сервисы\n\n%s\n"), strings.Join(names, ", "))
	}

	// Кто работает над проектом — по авторам коммитов. Имён людей в коде нет
	// нигде больше, а на созвоне звучат именно они: «Орынгали нужно закончить».
	// Без этого списка модель не может отличить имя от похожего слова.
	if who := repoAuthors(ctx, dir); who != "" {
		fmt.Fprintf(&b, i18n.Tr("\n### Кто работает над проектом\n\n%s\n"), who)
	}

	// Модули отдельным списком, а не только деревом. В дереве под каждым из них
	// повторяются api/, migrations/, services/ — двести строк, в которых сами
	// имена модулей тонут, а на большом репозитории обход ещё и обрывается по
	// потолку, отрезая последние по алфавиту. Между тем именно эти имена и
	// звучат на созвоне: «посмотри в биометрии», «алерты не приходят».
	if mods := topModules(dir); mods != "" {
		fmt.Fprintf(&b, i18n.Tr("\n### Модули\n\n%s\n"), mods)
	}

	if tree := listTree(dir, 2); tree != "" {
		fmt.Fprintf(&b, i18n.Tr("\n### Состав\n\n%s\n"), i18n.Cut(tree, 2500))
	}

	out, err := exec.CommandContext(ctx, "git", "-C", dir, "log",
		"--pretty=format:%s", "-n", "60").Output()
	head := ""
	if err == nil && len(out) > 0 {
		fmt.Fprintf(&b, i18n.Tr("\n### Последние коммиты\n\n%s\n"), i18n.Cut(string(out), 3000))
		if h, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "HEAD").Output(); err == nil {
			head = strings.TrimSpace(string(h))
		}
	}
	return b.String(), dir + "@" + head, nil
}

func listTree(root string, depth int) string {
	var lines []string
	var walk func(dir, prefix string, level int)
	walk = func(dir, prefix string, level int) {
		if level > depth || len(lines) > 200 {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, ".") && name != ".github" {
				continue
			}
			if e.IsDir() {
				if skipDirs[name] {
					continue
				}
				lines = append(lines, prefix+name+"/")
				walk(filepath.Join(dir, name), prefix+"  ", level+1)
			} else if level == 1 {
				lines = append(lines, prefix+name)
			}
		}
	}
	walk(root, "", 1)
	return strings.Join(lines, "\n")
}

var tagRe = regexp.MustCompile(`(?s)<(script|style)[^>]*>.*?</(script|style)>`)
var anyTag = regexp.MustCompile(`<[^>]+>`)
var manySpace = regexp.MustCompile(`[ \t]*\n\s*\n\s*`)

func fetchText(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf(i18n.Tr("код %d"), resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	s := tagRe.ReplaceAllString(string(raw), " ")
	s = anyTag.ReplaceAllString(s, " ")
	s = manySpace.ReplaceAllString(strings.ReplaceAll(s, "\r", ""), "\n\n")
	return i18n.Cut(strings.TrimSpace(s), 6000), nil
}

// --- справка ----------------------------------------------------------------

const primerSystem = `Ты готовишь справку о проекте для помощника, который ведёт
заметки с рабочих созвонов.

Помощник слушает разговор и раскладывает решения, задачи и вопросы по проектам.
Разложить он может, только если знает, какими словами команда говорит об этом
проекте вслух. В коде эти слова уже есть — в названиях модулей, в темах
коммитов, в README.

Напиши справку на 200–400 слов, по такой канве:

1. Что это за проект одним абзацем: что он делает и для кого.
2. Словарь: как части этого проекта называют вслух. Именно вслух — «биллинг»,
   «вебхуки», «очередь», «миграции», — включая сокращения и англицизмы в
   русской речи. Это самая полезная часть справки.
3. Из чего состоит: компоненты, сервисы, внешние системы, с которыми связан.
   Имена сервисов выпиши списком как есть — «worker_sapar» на созвоне звучит
   как «Сапар», и без этого списка его не отличить от имени человека.
4. Кто работает над проектом: перечисли имена из раздела «Кто работает» как
   есть. На созвоне звучат именно они — «Орынгали нужно закончить», — и без
   списка распознавание превращает незнакомое имя в похожее обычное слово.
5. О чём здесь обычно идёт работа, судя по последним коммитам.

Жёстко:

- Только то, что видно в материале. Не додумывай ни назначение, ни архитектуру.
- Не пересказывай README целиком и не перечисляй файлы.
- Никаких вводных вроде «этот проект представляет собой» — сразу по делу.
- Не рассуждай о самом задании и не объявляй, что собираешься делать. Первая
  строка ответа — уже справка. Эта справка целиком уходит в другой промпт, и
  любая реплика о ходе работы поедет туда вместе с ней.
- Пиши по-русски, даже если материал английский: справку читает модель, которая
  разбирает русские созвоны.`

func BuildPrimer(ctx context.Context, cfg *core.Config, p core.Project, material string) (string, core.Spend, error) {
	if strings.TrimSpace(material) == "" {
		return "", core.Spend{}, errors.New(i18n.Tr("нечего читать: не задан ни один источник"))
	}
	head := fmt.Sprintf(i18n.Tr("Проект: %s\n"), p.Name)
	if len(p.Aliases) > 0 {
		head += fmt.Sprintf(i18n.Tr("Как называют вслух: %s\n"), strings.Join(p.Aliases, ", "))
	}
	if p.About != "" {
		head += fmt.Sprintf(i18n.Tr("Коротко: %s\n"), p.About)
	}

	// Справка собирается редко и читается на каждом созвоне — экономить на ней
	// не стоит, но и рассуждать тут особо не о чем.
	text, spend, err := AskLLM(ctx, cfg, primerSystem, head+i18n.Tr("\nМатериал:\n")+material, nil, 4000)
	if err != nil {
		return "", core.Spend{}, err
	}
	if text = strings.TrimSpace(text); text == "" {
		return "", core.Spend{}, errors.New(i18n.Tr("вернулась пустая справка"))
	}
	return text, spend, nil
}

// RenderPrimers — то, что уходит в промпт созвона. Справки обрезаны по объёму:
// четыре проекта по тысяче слов съели бы больше, чем сама расшифровка.
func RenderPrimers(st *core.Store, projects []core.Project) string {
	var b strings.Builder
	for _, p := range projects {
		c, err := st.ProjectContext(p.Name)
		if err != nil || strings.TrimSpace(c.Primer) == "" {
			continue
		}
		fmt.Fprintf(&b, "\n### %s\n\n%s\n", p.Name, i18n.Cut(c.Primer, 2500))
	}
	if b.Len() == 0 {
		return ""
	}
	return i18n.Tr("Справки о проектах — по ним видно, какими словами команда о них говорит:\n") + b.String()
}

// ComposeServices — имена сервисов из docker-compose. Разбираем построчно, а не
// разбором YAML: нужен только список ключей верхнего уровня под services, и
// тянуть ради этого зависимость незачем.
func ComposeServices(dir string) []string {
	var out []string
	for _, name := range []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		inServices := false
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(line, "services:") {
				inServices = true
				continue
			}
			// Любой ключ без отступа кончает раздел services.
			if inServices && len(line) > 0 && line[0] != ' ' && line[0] != '\t' && line[0] != '#' {
				inServices = false
			}
			if !inServices {
				continue
			}
			t := strings.TrimRight(line, " \t\r")
			if !strings.HasSuffix(t, ":") {
				continue
			}
			// Ровно два пробела отступа — это имя сервиса; глубже лежат его
			// собственные поля вроде build: и environment:.
			if strings.HasPrefix(t, "   ") || !strings.HasPrefix(t, "  ") {
				continue
			}
			if n := strings.TrimSpace(strings.TrimSuffix(t, ":")); n != "" {
				out = append(out, n)
			}
		}
		if len(out) > 0 {
			break
		}
	}
	if len(out) > 40 {
		out = out[:40]
	}
	return out
}

// repoAuthors — кто коммитил, по убыванию числа коммитов. Почты не берём: для
// словаря нужны имена, которыми людей зовут, а не адреса.
func repoAuthors(ctx context.Context, dir string) string {
	return strings.Join(RepoAuthorNames(ctx, dir), ", ")
}

// RepoAuthorNames — то же самое списком. Список нужен отдельно от строки:
// кроме справки эти имена уходят подсказкой в whisper (vocab.go), а там их
// приходится считать и обрезать поштучно.
func RepoAuthorNames(ctx context.Context, dir string) []string {
	out, err := exec.CommandContext(ctx, "git", "-C", dir,
		"log", "--pretty=format:%an", "-n", "2000").Output()
	if err != nil {
		return nil
	}
	count := map[string]int{}
	var order []string
	for _, line := range strings.Split(string(out), "\n") {
		n := strings.TrimSpace(line)
		if n == "" || strings.Contains(n, "[bot]") {
			continue
		}
		if count[n] == 0 {
			order = append(order, n)
		}
		count[n]++
	}
	sort.SliceStable(order, func(i, j int) bool { return count[order[i]] > count[order[j]] })
	if len(order) > 25 {
		order = order[:25]
	}
	return order
}

// topModules — каталоги верхнего уровня и, если рядом лежит своя документация,
// первый заголовок из неё. Имя модуля плюс строка о том, что он делает, — это и
// есть словарь, которым на созвоне называют части системы.
func topModules(dir string) string {
	var out []string
	for _, name := range TopModuleNames(dir) {
		if title := DocTitle(filepath.Join(dir, name)); title != "" {
			out = append(out, name+" — "+title)
		} else {
			out = append(out, name)
		}
	}
	return strings.Join(out, "\n")
}

// TopModuleNames — только имена модулей, без описаний. Отдельно от topModules
// они нужны словарю для whisper (vocab.go): в подсказке место есть на слова, а
// не на строку с тире и пояснением.
func TopModuleNames(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || strings.HasPrefix(name, ".") || skipDirs[name] || boilerplateDirs[name] {
			continue
		}
		out = append(out, name)
		if len(out) >= 60 {
			break
		}
	}
	return out
}

// boilerplateDirs — каталоги, которые есть у всех и ничего не говорят о том,
// чем занят проект.
var boilerplateDirs = map[string]bool{
	"api": true, "migrations": true, "tests": true, "test": true,
	"static": true, "templates": true, "locale": true, "media": true,
	"scripts": true, "bin": true, "tmp": true, "logs": true,
}

// docTitle — первый заголовок из документации модуля. Берём именно заголовок, а
// не первый абзац: он короткий и написан затем, чтобы назвать суть.
func DocTitle(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(raw), "\n") {
			t := strings.TrimSpace(strings.TrimLeft(line, "#"))
			if t != "" && strings.HasPrefix(strings.TrimSpace(line), "#") {
				return i18n.Cut(t, 120)
			}
		}
	}
	return ""
}
