package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Follow-up через CLI Claude Code.
//
// Человеку, который поставил steno для своих созвонов, заводить отдельный
// биллинг незачем: подписка на Claude Code у него уже есть, и `claude -p` —
// её штатный неинтерактивный режим. Это не обход: тот же продукт, тот же
// аккаунт, просто запрос отправляет не человек руками, а инструмент.
//
// Для сервера это не годится: CLI требует входа, который делает человек, и на
// машине без него работать не будет. Поэтому путь через ключ никуда не
// девается — просто перестаёт быть единственным.

type cliResult struct {
	Text  string
	USD   float64
	Usage cliUsage
}

// Токены CLI отдаёт свои, и они заметно больше наших: Claude Code добавляет к
// запросу собственный системный промпт и описания инструментов, а это пара
// тысяч токенов кеша сверх того, что послал steno. Поэтому в учёт идут именно
// они, а не наша оценка по длине текста — расход по подписке больше, чем был бы
// через API, и делать вид, что это не так, незачем.
type cliUsage struct {
	Input      int64 `json:"input_tokens"`
	Output     int64 `json:"output_tokens"`
	CacheRead  int64 `json:"cache_read_input_tokens"`
	CacheWrite int64 `json:"cache_creation_input_tokens"`
}

// claudeCLIAvailable — есть ли CLI и выполнен ли вход. Без второго CLI
// запустится и попросит логин, а мы увидим невнятную ошибку в середине созвона.
func claudeCLIAvailable() (bool, string) {
	if _, err := exec.LookPath("claude"); err != nil {
		return false, tr("команда claude не найдена")
	}
	cmd := exec.Command("claude", "auth", "status")
	cmd.Env = scrubClaudeEnv(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, "claude auth status: " + tail(strings.TrimSpace(string(out)), 120)
	}
	// Вывод — JSON. Разбираем: «вошёл» и «чем» — это ровно то, что человек
	// хочет увидеть в doctor.
	var st struct {
		LoggedIn   bool   `json:"loggedIn"`
		AuthMethod string `json:"authMethod"`
		Account    string `json:"account"`
	}
	if err := json.Unmarshal(out, &st); err != nil {
		// Формат мог поменяться — на это падать незачем, вход всё равно есть.
		return true, tr("вход выполнен")
	}
	if !st.LoggedIn {
		return false, tr("вход в Claude Code не выполнен — запусти `claude` и войди")
	}
	return true, firstNonEmpty(st.AuthMethod, tr("вход выполнен"))
}

// runClaudeCLI отправляет запрос и возвращает текст ответа.
func runClaudeCLI(ctx context.Context, cfg *Config, system, user string, maxUSD float64) (cliResult, error) {
	var res cliResult

	// Системную часть кладём в тот же запрос: у `claude -p` отдельного
	// системного канала нет, а разделять их заголовком модель понимает.
	prompt := system + "\n\n---\n\n" + user

	args := []string{"-p", prompt, "--output-format", "json"}
	if m := cfg.Claude.Model; m != "" {
		args = append(args, "--model", m)
	}
	if e := cfg.Claude.Effort; e != "" {
		args = append(args, "--effort", e)
	}
	if maxUSD > 0 {
		args = append(args, "--max-budget-usd", strconv.FormatFloat(maxUSD, 'f', -1, 64))
	}
	// Никаких инструментов: steno просит только текст, а разрешать файловые
	// операции ради этого — лишний риск на ровном месте.
	args = append(args, "--permission-mode", "plan")

	cmd := exec.CommandContext(ctx, "claude", args...)
	// Claude Code выставляет эти переменные, когда работает сам; оставить их
	// значит заставить дочерний процесс думать, что он вложен в сессию.
	cmd.Env = scrubClaudeEnv(os.Environ())
	if devnull, err := os.Open(os.DevNull); err == nil {
		cmd.Stdin = devnull
		defer devnull.Close()
	}
	setProcessGroup(cmd)
	cmd.Cancel = func() error { return terminateGroup(cmd) }

	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		detail := ""
		if errors.As(err, &ee) {
			detail = tail(strings.TrimSpace(string(ee.Stderr)), 400)
		}
		return res, fmt.Errorf("claude -p: %w\n%s", err, detail)
	}

	var env struct {
		Type    string   `json:"type"`
		Subtype string   `json:"subtype"`
		IsError bool     `json:"is_error"`
		Result  string   `json:"result"`
		Cost    float64  `json:"total_cost_usd"`
		Usage   cliUsage `json:"usage"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		return res, fmt.Errorf(tr("claude -p вернул не JSON: %w\n%s"), err, tail(string(out), 300))
	}
	if env.IsError {
		return res, fmt.Errorf("claude -p: %s", firstNonEmpty(env.Subtype, tail(env.Result, 300)))
	}
	if strings.TrimSpace(env.Result) == "" {
		return res, errors.New(tr("claude -p вернул пустой ответ"))
	}
	res.Text = env.Result
	res.USD = env.Cost
	res.Usage = env.Usage
	return res, nil
}

func scrubClaudeEnv(env []string) []string {
	drop := map[string]bool{
		"CLAUDECODE": true, "CLAUDE_CODE_ENTRYPOINT": true,
		"CLAUDE_CODE_SSE_PORT": true, "CLAUDE_CODE_SIMPLE": true,
		"CLAUDE_CODE_SAFE_MODE": true,
	}
	out := env[:0:0]
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if !drop[k] {
			out = append(out, kv)
		}
	}
	return out
}
