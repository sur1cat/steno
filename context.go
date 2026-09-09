package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

// gatherSources собирает сырой материал. Всё ограничено по объёму: справка
// делается из README и структуры, а не из всего кода.
func gatherSources(ctx context.Context, dataDir string, p Project) (string, string, error) {
	var b strings.Builder
	var fp strings.Builder

	for _, src := range p.Sources {
		switch src.Kind {
		case "text":
			fmt.Fprintf(&b, "\n## Описание\n\n%s\n", src.Value)
			fp.WriteString(src.Value)

		case "path", "repo":
			dir := src.Value
			if src.Kind == "repo" {
				var err error
				if dir, err = shallowClone(ctx, dataDir, p.Name, src.Value); err != nil {
					return "", "", fmt.Errorf("%s: %w", src.Value, err)
				}
			}
			dir = expandHome(dir)
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
			fmt.Fprintf(&b, "\n## Сайт %s\n\n%s\n", src.Value, text)
			fp.WriteString(src.Value)

		default:
			return "", "", fmt.Errorf("непонятный источник %q", src.Kind)
		}
	}
	sum := sha256.Sum256([]byte(fp.String()))
	return b.String(), hex.EncodeToString(sum[:8]), nil
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// shallowClone тянет репозиторий на один коммит: история для справки не нужна,
// а на большом репозитории полный клон — это минуты и гигабайты.
func shallowClone(ctx context.Context, dataDir, project, url string) (string, error) {
	dir := filepath.Join(dataDir, "repos", safeName(project))
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		cmd := exec.CommandContext(ctx, "git", "-C", dir, "fetch", "--depth", "1", "origin")
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("git fetch: %s", tail(string(out), 300))
		}
		_ = exec.CommandContext(ctx, "git", "-C", dir, "reset", "--hard", "origin/HEAD").Run()
		return dir, nil
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", url, dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git clone: %s", tail(string(out), 300))
	}
	return dir, nil
}

var unsafeName = regexp.MustCompile(`[^\p{L}\p{N}_-]+`)

func safeName(s string) string {
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
		return "", "", fmt.Errorf("не каталог")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n## Репозиторий %s\n", filepath.Base(dir))

	for _, name := range []string{"README.md", "README", "readme.md", "README.rst"} {
		if raw, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			fmt.Fprintf(&b, "\n### README\n\n%s\n", cut(string(raw), 8000))
			break
		}
	}
	for _, name := range []string{"package.json", "go.mod", "pyproject.toml", "Cargo.toml", "composer.json"} {
		if raw, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			fmt.Fprintf(&b, "\n### %s\n\n%s\n", name, cut(string(raw), 1500))
		}
	}

	if tree := listTree(dir, 2); tree != "" {
		fmt.Fprintf(&b, "\n### Состав\n\n%s\n", tree)
	}

	out, err := exec.CommandContext(ctx, "git", "-C", dir, "log",
		"--pretty=format:%s", "-n", "60").Output()
	head := ""
	if err == nil && len(out) > 0 {
		fmt.Fprintf(&b, "\n### Последние коммиты\n\n%s\n", cut(string(out), 3000))
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
		return "", fmt.Errorf("код %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	s := tagRe.ReplaceAllString(string(raw), " ")
	s = anyTag.ReplaceAllString(s, " ")
	s = manySpace.ReplaceAllString(strings.ReplaceAll(s, "\r", ""), "\n\n")
	return cut(strings.TrimSpace(s), 6000), nil
}

func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "\n…"
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
4. О чём здесь обычно идёт работа, судя по последним коммитам.

Жёстко:

- Только то, что видно в материале. Не додумывай ни назначение, ни архитектуру.
- Не пересказывай README целиком и не перечисляй файлы.
- Никаких вводных вроде «этот проект представляет собой» — сразу по делу.
- Не рассуждай о самом задании и не объявляй, что собираешься делать. Первая
  строка ответа — уже справка. Эта справка целиком уходит в другой промпт, и
  любая реплика о ходе работы поедет туда вместе с ней.
- Пиши по-русски, даже если материал английский: справку читает модель, которая
  разбирает русские созвоны.`

func buildPrimer(ctx context.Context, cfg *Config, p Project, material string) (string, Spend, error) {
	if strings.TrimSpace(material) == "" {
		return "", Spend{}, fmt.Errorf("нечего читать: не задан ни один источник")
	}
	head := fmt.Sprintf("Проект: %s\n", p.Name)
	if len(p.Aliases) > 0 {
		head += fmt.Sprintf("Как называют вслух: %s\n", strings.Join(p.Aliases, ", "))
	}
	if p.About != "" {
		head += fmt.Sprintf("Коротко: %s\n", p.About)
	}

	// Справка собирается редко и читается на каждом созвоне — экономить на ней
	// не стоит, но и рассуждать тут особо не о чем.
	text, spend, err := askLLM(ctx, cfg, primerSystem, head+"\nМатериал:\n"+material, nil, 4000)
	if err != nil {
		return "", Spend{}, err
	}
	if text = strings.TrimSpace(text); text == "" {
		return "", Spend{}, fmt.Errorf("вернулась пустая справка")
	}
	return text, spend, nil
}

// --- хранилище --------------------------------------------------------------

type ProjectContext struct {
	Project     string
	Primer      string
	Sources     string
	BuiltAt     time.Time
	Fingerprint string
}

func (s *Store) ProjectContext(project string) (ProjectContext, error) {
	var c ProjectContext
	var built int64
	err := s.db.QueryRow(`SELECT project,primer,sources,built_at,fingerprint
		FROM project_context WHERE project=?`, project).
		Scan(&c.Project, &c.Primer, &c.Sources, &built, &c.Fingerprint)
	c.BuiltAt = time.Unix(built, 0)
	return c, err
}

func (s *Store) SaveProjectContext(c ProjectContext) error {
	_, err := s.db.Exec(`INSERT INTO project_context (project,primer,sources,built_at,fingerprint)
		VALUES (?,?,?,?,?)
		ON CONFLICT(project) DO UPDATE SET primer=excluded.primer, sources=excluded.sources,
		built_at=excluded.built_at, fingerprint=excluded.fingerprint`,
		c.Project, c.Primer, c.Sources, time.Now().Unix(), c.Fingerprint)
	return err
}

// renderPrimers — то, что уходит в промпт созвона. Справки обрезаны по объёму:
// четыре проекта по тысяче слов съели бы больше, чем сама расшифровка.
func renderPrimers(st *Store, projects []Project) string {
	var b strings.Builder
	for _, p := range projects {
		c, err := st.ProjectContext(p.Name)
		if err != nil || strings.TrimSpace(c.Primer) == "" {
			continue
		}
		fmt.Fprintf(&b, "\n### %s\n\n%s\n", p.Name, cut(c.Primer, 2500))
	}
	if b.Len() == 0 {
		return ""
	}
	return "Справки о проектах — по ним видно, какими словами команда о них говорит:\n" + b.String()
}
