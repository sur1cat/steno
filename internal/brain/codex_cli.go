package brain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
)

// Follow-up через `codex exec` — то же, чем для Claude служит `claude -p`.
//
// Смысл тот же: у человека уже есть подписка ChatGPT, и заводить ради своих
// созвонов отдельный биллинг незачем. Разница в одном месте, и она в нашу
// пользу: у codex схема задаётся файлом (--output-schema) и соблюдается, а у
// `claude -p` параметра формата нет вовсе и схему приходится просить словами.
// Той единственной платы, которая есть у пути через claude, здесь нет.
//
// Чего здесь нет — учёта расхода. Токены codex отдаёт только потоком событий
// (--json), а нам нужен последний ответ; выдумывать цену по длине текста мы не
// станем, поэтому в `steno cost` такой запрос честно значится «неизвестен».
// Соврать нулём было бы хуже: ноль читается как «бесплатно», а подписка стоит
// денег.

const codexDefaultTimeout = 15 * time.Minute

// CodexCLIAvailable — есть ли codex и выполнен ли вход.
//
// Формат вывода `codex login status` живьём не проверялся — на машине, где это
// писалось, codex не установлен. Поэтому опираемся на код возврата, а не на
// слова: по документации незалогиненный выходит с единицей. Ключ в переменной
// проверяется отдельно и раньше, потому что с ним вход не нужен вовсе.
func CodexCLIAvailable() (bool, string) {
	if _, err := exec.LookPath("codex"); err != nil {
		return false, i18n.Tr("команда codex не найдена — ставится `npm i -g @openai/codex`")
	}
	if env := codexKeyEnv(); env != "" {
		return true, i18n.Tr("ключ ") + env
	}
	cmd := exec.Command("codex", "login", "status")
	out, err := cmd.CombinedOutput()
	line := i18n.Tail(strings.TrimSpace(firstLine(string(out))), 120)
	if err != nil {
		if line == "" {
			line = err.Error()
		}
		return false, i18n.Tr("вход в codex не выполнен: ") + line
	}
	return true, core.FirstNonEmpty(line, i18n.Tr("вход выполнен"))
}

// codexKeyEnv — какой переменной codex воспользуется без входа. Порядок как в
// его документации: свой ключ важнее общего.
func codexKeyEnv() string {
	for _, env := range []string{"CODEX_API_KEY", "OPENAI_API_KEY"} {
		if v, err := core.Secret(env, ""); err == nil && v != "" {
			return env
		}
	}
	return ""
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// askViaCodex отправляет запрос и возвращает текст ответа.
func askViaCodex(ctx context.Context, cfg *core.Config, system, user string,
	schema map[string]any) (string, core.Spend, error) {

	spend := core.Spend{Model: cfg.BrainModel()}

	// Рабочий каталог — временный и пустой, а не тот, где запущен steno.
	// Песочница codex по умолчанию только на чтение, но читать ему тут нечего:
	// весь материал уже в промпте. Каталог с чужим кодом под рукой — это
	// лишний соблазн для модели и лишние секунды на его осмотр.
	dir, err := os.MkdirTemp("", "steno-codex")
	if err != nil {
		return "", spend, err
	}
	defer os.RemoveAll(dir)

	answer := filepath.Join(dir, "answer.txt")
	args := []string{"exec",
		"--skip-git-repo-check", // временный каталог не репозиторий, а codex это проверяет
		"--cd", dir,
		"-o", answer,
	}
	if schema != nil {
		// Вот ради чего этот путь и заведён: схема задаётся файлом и
		// соблюдается, а не просится словами.
		raw, err := json.MarshalIndent(schema, "", "  ")
		if err != nil {
			return "", spend, err
		}
		path := filepath.Join(dir, "schema.json")
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			return "", spend, err
		}
		args = append(args, "--output-schema", path)
	}
	if m := strings.TrimSpace(cfg.Brain.Codex.Model); m != "" {
		args = append(args, "--model", m)
	}
	// Системную часть кладём в тот же запрос: отдельного системного канала у
	// `codex exec` нет, а разделять их чертой модель понимает — ровно как у
	// `claude -p`.
	args = append(args, system+"\n\n---\n\n"+user)

	timeout := cfg.Brain.Codex.Timeout.D()
	if timeout <= 0 {
		timeout = codexDefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "codex", args...)
	cmd.Dir = dir
	if devnull, err := os.Open(os.DevNull); err == nil {
		cmd.Stdin = devnull
		defer devnull.Close()
	}
	core.SetProcessGroup(cmd)
	cmd.Cancel = func() error { return core.TerminateGroup(cmd) }

	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return "", spend, fmt.Errorf(
				i18n.Tr("codex exec не уложился в отведённое время (brain.codex.timeout): %w"), ctx.Err())
		}
		return "", spend, fmt.Errorf("codex exec: %w\n%s", err, i18n.Tail(strings.TrimSpace(string(out)), 400))
	}

	// Ответ берём из файла, а не из stdout: codex печатает туда и ход работы
	// тоже, и вырезать из этого последний ответ — гадание. Файл содержит ровно
	// его, и это его прямое назначение.
	raw, err := os.ReadFile(answer)
	if err != nil {
		return "", spend, fmt.Errorf(
			i18n.Tr("codex не записал ответ в файл (-o): %w\n%s"), err, i18n.Tail(strings.TrimSpace(string(out)), 300))
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return "", spend, errors.New(i18n.Tr("codex вернул пустой ответ"))
	}
	if schema != nil {
		text = extractJSON(text)
	} else {
		text = stripPreamble(text)
	}
	// PriceKnown остаётся false намеренно: расход по подписке нам никто не
	// сказал, и ноль здесь был бы неправдой. См. шапку файла.
	return text, spend, nil
}
