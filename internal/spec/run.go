package spec

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/sur1cat/steno/internal/brain"
	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
)

// Исполнение ТЗ.
//
// Пять требований, из которых состоит этот файл, и все пять — требования, а не
// пожелания. steno ставят из Homebrew себе на рабочую машину, и эта часть даёт
// панели право запускать программу, которая пишет файлы и выполняет команды.
//
//  1. Выключено по умолчанию — agent.enabled в steno.json (см. settings.go).
//  2. Запуск только по действию человека. Никогда по расписанию и никогда по
//     тому, что пришло из телеграма, почты или HTTP-входа. Созвон — это чужой
//     ввод: на звонке кто угодно может сказать «снеси репозиторий», и оно
//     доедет сюда задачей. ТЗ делается само, исполнение — по кнопке. За этим
//     следит обязательный аргумент by и тест TestRunUnreachableFromInbound.
//  3. Рабочая копия (git worktree), а не каталог, в котором человек работает
//     прямо сейчас.
//  4. Своя ветка. Никаких коммитов в основную и никаких push (см. denyRules).
//  5. Ни одной команды и ни одного пути, пришедших из настроек или из панели.
//     Имя агента — из списка в agent.go, правила доступа — из констант.

// Sink — куда течёт ход работы. Возвращать её обязан вызывающий: журнал,
// панель, телеграм — решает он, но в никуда поток не идёт. nil означает «только
// в файл рядом с рабочей копией», и это тоже куда-то.
//
// kind: say — реплика агента, do — что он делает, warn — что пошло не так,
// note — наше собственное сообщение о ходе дела.
type Sink func(kind, text string)

// Run отдаёт ТЗ агенту.
//
// by — имя того, кто нажал. Аргумент обязателен и пустым не бывает: он и есть
// разница между «человек попросил» и «пришло из чата». Всё, что приходит
// снаружи — телеграм, почта, HTTP-вход, — не имеет права его подставить, и
// подставить его им неоткуда: у входящего сообщения есть отправитель, но нет
// человека, сидящего за этой машиной.
func Run(ctx context.Context, d Deps, set Settings, specID, by string, sink Sink) (*Spec, error) {
	if !set.Enabled {
		return nil, errors.New(i18n.Tr("исполнение выключено: поставь \"agent\": {\"enabled\": true} в steno.json"))
	}
	if strings.TrimSpace(by) == "" {
		return nil, errors.New(i18n.Tr("исполнение запускается только человеком — некому приписать запуск"))
	}
	sp, err := d.Sp.Get(specID)
	if err != nil {
		return nil, fmt.Errorf(i18n.Tr("нет ТЗ %s: %w"), specID, err)
	}
	if sp.Status == StatusRunning {
		return nil, errors.New(i18n.Tr("по этому ТЗ уже идёт работа"))
	}
	if sp.Status == StatusRejected {
		return nil, fmt.Errorf(i18n.Tr("это не ТЗ, а отказ: %s"), sp.Reject)
	}
	// Главная проверка. ТЗ, которое не назвало своих дыр или не нашло ни одного
	// файла, — это не задание, а сочинение, и запускать по нему агента значит
	// платить за мусор в репозитории.
	if reasons := sp.Gate(); len(reasons) > 0 {
		return nil, fmt.Errorf(i18n.Tr("по этому ТЗ нельзя работать:\n  · %s"),
			strings.Join(reasons, "\n  · "))
	}
	ag, err := resolveAgent(d.Cfg, set)
	if err != nil {
		return nil, err
	}
	if sp.Repo == "" {
		return nil, errors.New(i18n.Tr("у ТЗ не записан репозиторий"))
	}

	branch := prefixOf(set) + slug(firstNonEmpty(sp.Title, sp.ItemID)) + "-" + stamp(time.Now())
	wt := filepath.Join(worktreeDirOf(set, d.Cfg.DataDir), brain.SafeName(sp.Project), sp.ID)

	log := newLog(wt + ".log")
	defer log.Close()
	// Хвост журнала уезжает в базу по ходу дела, а не только в конце: панель,
	// строка меню и `steno ui` читают базу, и «идёт работа» без единой строки
	// о том, какая именно, — это полчаса неизвестности. Раз в несколько
	// секунд, а не на каждую строку: агент пишет по десятку событий в секунду.
	var flushed time.Time
	say := func(kind, text string) {
		log.write(kind, text)
		if sink != nil {
			sink(kind, text)
		}
		if sp.Status == StatusRunning && time.Since(flushed) > 3*time.Second {
			sp.RunLog = log.tail()
			_ = d.Sp.Save(sp)
			flushed = time.Now()
		}
	}

	base, err := baseCommit(sp.Repo, set.BaseBranch)
	if err != nil {
		return nil, err
	}
	say("note", i18n.Trf("рабочая копия: %s", wt))
	say("note", i18n.Trf("ветка %s от %s", branch, base))
	if err := makeWorktree(sp.Repo, branch, wt, base); err != nil {
		return nil, err
	}

	sp.Status, sp.Branch, sp.Worktree = StatusRunning, branch, wt
	sp.RunBy, sp.StartedAt, sp.RunError = by, time.Now(), ""
	if err := d.Sp.Save(sp); err != nil {
		return nil, err
	}

	say("note", i18n.Trf("исполняет %s", ag.Title))
	runCtx, cancel := context.WithTimeout(ctx, timeoutOf(set))
	defer cancel()

	runErr := runAgent(runCtx, ag, set, d.Cfg, wt, agentPrompt(sp, branch), say)

	// Коммитим в любом случае, даже если агент упал на середине: сделанное до
	// падения — это работа, и терять её, оставляя каталог в подвешенном виде,
	// незачем. Разбираться человек будет по ветке, а не по нашему пересказу.
	files, err := commit(wt, sp)
	switch {
	case err != nil:
		say("warn", i18n.Trf("не удалось сохранить изменения: %v", err))
	case files == 0:
		say("note", i18n.Tr("агент не изменил ни одного файла"))
	default:
		say("note", i18n.Trf("сохранено в ветку %s: файлов — %d", branch, files))
	}

	sp.RunLog = log.tail()
	if runErr != nil {
		sp.Status, sp.RunError = StatusFailed, runErr.Error()
	} else {
		sp.Status = StatusDone
	}
	if err := d.Sp.Save(sp); err != nil {
		return nil, err
	}
	return sp, runErr
}

// --- рабочая копия ----------------------------------------------------------

// baseCommit — от чего ответвляться.
//
// По умолчанию — коммит, на котором репозиторий стоит сейчас, а не имя ветки. У
// имени есть неприятное свойство: пока агент работает, человек в основном
// каталоге успевает закоммитить, и «та же ветка» через полчаса означает уже
// другое состояние. Коммит — это снимок, и он не меняется.
func baseCommit(repo, want string) (string, error) {
	if b := strings.TrimSpace(want); b != "" {
		if out, err := git(repo, "rev-parse", "--verify", b+"^{commit}"); err == nil {
			return strings.TrimSpace(out), nil
		}
		return "", fmt.Errorf(i18n.Tr("agent.base_branch=%q — такой ветки в репозитории нет"), b)
	}
	out, err := git(repo, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf(i18n.Tr("%s не похож на репозиторий git: %s"), repo, strings.TrimSpace(out))
	}
	return strings.TrimSpace(out), nil
}

// makeWorktree заводит отдельную копию под задачу.
//
// Отличие от radio, и оно намеренное: там при столкновении с чужой рабочей
// копией её сносят, чтобы освободить ветку. Здесь — отказ. radio хозяин своим
// веткам: их создаёт он же, по заявкам. Здесь репозиторий чужой, человек в нём
// работает прямо сейчас, и снести его рабочую копию ради своей — это ровно то,
// от чего мы защищаемся, только сделанное своими руками.
func makeWorktree(repo, branch, wt, base string) error {
	if err := os.MkdirAll(filepath.Dir(wt), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(wt); err == nil {
		return fmt.Errorf(i18n.Tr("каталог %s уже занят — убери его и повтори"), wt)
	}
	if _, err := git(repo, "rev-parse", "--verify", branch); err == nil {
		return fmt.Errorf(i18n.Tr("ветка %s уже есть — повтори запуск, имя берётся со временем"), branch)
	}
	git(repo, "worktree", "prune")
	if out, err := git(repo, "worktree", "add", "-b", branch, wt, base); err != nil {
		return fmt.Errorf(i18n.Tr("не удалось завести рабочую копию: %s"), strings.TrimSpace(out))
	}
	return nil
}

// commit сохраняет сделанное. Только в свою ветку и только локально: push не
// делается нигде и никогда — ни здесь, ни агентом (см. denyRules).
func commit(wt string, sp *Spec) (int, error) {
	if _, err := git(wt, "add", "-A"); err != nil {
		return 0, err
	}
	stat, _ := git(wt, "diff", "--cached", "--numstat")
	files := 0
	for _, line := range strings.Split(strings.TrimSpace(stat), "\n") {
		if len(strings.Fields(line)) >= 3 {
			files++
		}
	}
	if files == 0 {
		return 0, nil
	}
	msg := i18n.Cut(firstNonEmpty(sp.Title, sp.ItemID), 70)
	body := fmt.Sprintf("%s\n\nТЗ: %s\nЗадача: %s", msg, sp.ID, sp.ItemID)
	out, err := git(wt, "-c", "user.name=steno", "-c", "user.email=steno@local",
		"commit", "-m", msg, "-m", body)
	if err != nil {
		return files, fmt.Errorf("%v: %s", err, strings.TrimSpace(out))
	}
	return files, nil
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	b, err := cmd.CombinedOutput()
	return string(b), err
}

// --- запуск агента ----------------------------------------------------------

func runAgent(ctx context.Context, ag agent, set Settings, cfg *core.Config,
	wt, prompt string, say Sink) error {

	var args []string
	switch ag.Bin {
	case "claude":
		args = []string{"-p", prompt,
			"--output-format", "stream-json", "--verbose",
			"--permission-mode", "acceptEdits",
		}
		// Без файла правил claude пойдёт вообще без списка запретов, а
		// выглядеть это будет как обычный успешный запуск. Молчать нельзя.
		perms, err := writePermissions(wt)
		if err != nil {
			return fmt.Errorf(i18n.Tr("не удалось записать правила доступа, запуск отменён: %w"), err)
		}
		args = append(args, "--settings", perms)
		if m := strings.TrimSpace(cfg.Claude.Model); m != "" {
			args = append(args, "--model", m)
		}
		if set.MaxUSD > 0 {
			args = append(args, "--max-budget-usd", strconv.FormatFloat(set.MaxUSD, 'f', -1, 64))
		}
	case "codex":
		args = []string{"exec", "--cd", wt, "--full-auto"}
		if m := strings.TrimSpace(cfg.Brain.Codex.Model); m != "" {
			args = append(args, "--model", m)
		}
		args = append(args, prompt)
	default:
		return fmt.Errorf(i18n.Tr("неизвестный исполнитель %q"), ag.Bin)
	}

	cmd := exec.CommandContext(ctx, ag.Bin, args...)
	cmd.Dir = wt
	cmd.Env = scrubEnv(os.Environ())
	if devnull, err := os.Open(os.DevNull); err == nil {
		cmd.Stdin = devnull
		defer devnull.Close()
	}
	core.SetProcessGroup(cmd)
	cmd.Cancel = func() error { return core.TerminateGroup(cmd) }

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	if err := cmd.Start(); err != nil {
		return fmt.Errorf(i18n.Tr("не удалось запустить %s: %w"), ag.Bin, err)
	}

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 1<<20), 32<<20) // ответы инструментов большие
	for sc.Scan() {
		line := sc.Text()
		if ag.Bin == "claude" {
			// Поток claude — по событию на строку. Формат разбирается в
			// handleEvent; всё, что не разобралось, молча пропускаем: это
			// служебные события, а не потеря.
			if kind, text := handleEvent(line); text != "" {
				say(kind, text)
			}
			continue
		}
		// У codex формата событий, на который можно опереться, нет — во всяком
		// случае проверенного живьём. Поэтому его вывод идёт как есть: пусть
		// он менее опрятен, зато он правда его вывод, а не наш пересказ.
		if t := strings.TrimSpace(line); t != "" {
			say("do", i18n.Cut(t, 400))
		}
	}
	// Сканер останавливается не только на конце потока: слишком длинная строка
	// тоже его прерывает. Тогда агент продолжает писать в канал, который никто
	// не читает, и Wait() ждёт вечно — запуск застревает навсегда.
	if err := sc.Err(); err != nil {
		core.TerminateGroup(cmd)
		cmd.Wait()
		return fmt.Errorf(i18n.Tr("поток агента не удалось прочитать (%v) — запуск остановлен"), err)
	}
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return errors.New(i18n.Tr("агент не уложился в отведённое время (agent.timeout)"))
		}
		if s := strings.TrimSpace(errBuf.String()); s != "" {
			say("warn", i18n.Tail(s, 400))
		}
		return fmt.Errorf("%s: %w", ag.Bin, err)
	}
	return nil
}

// writePermissions кладёт рядом с рабочей копией файл запретов для claude.
//
// Рядом, а не внутрь: внутри он попал бы в коммит и уехал бы в ветку, которую
// человек потом откроет и не поймёт, откуда в его репозитории наш файл.
func writePermissions(wt string) (string, error) {
	path := wt + ".permissions.json"
	raw, err := json.MarshalIndent(map[string]any{
		"permissions": map[string]any{"deny": denyRules},
	}, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// handleEvent превращает одну строку потока claude в одну строку для человека.
func handleEvent(line string) (kind, text string) {
	var ev map[string]any
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		return "", ""
	}
	if ev["type"] != "assistant" {
		return "", ""
	}
	msg, _ := ev["message"].(map[string]any)
	blocks, _ := msg["content"].([]any)
	for _, raw := range blocks {
		b, _ := raw.(map[string]any)
		switch b["type"] {
		case "text":
			if s, _ := b["text"].(string); strings.TrimSpace(s) != "" {
				return "say", i18n.Cut(strings.TrimSpace(s), 1200)
			}
		case "tool_use":
			name, _ := b["name"].(string)
			input, _ := b["input"].(map[string]any)
			if p := describeTool(name, input); p != "" {
				return "do", p
			}
		}
	}
	return "", ""
}

// describeTool — то же событие человеческими словами. Читать это будет не
// программист, поэтому «Read file_path=…» не годится.
func describeTool(name string, input map[string]any) string {
	str := func(k string) string { s, _ := input[k].(string); return s }
	switch name {
	case "Read":
		return i18n.Trf("читает %s", filepath.Base(str("file_path")))
	case "Edit", "MultiEdit", "NotebookEdit":
		return i18n.Trf("правит %s", filepath.Base(str("file_path")))
	case "Write":
		return i18n.Trf("пишет %s", filepath.Base(str("file_path")))
	case "Bash":
		return i18n.Trf("выполняет: %s", i18n.Cut(str("command"), 120))
	case "Grep", "Glob":
		return i18n.Trf("ищет %s", i18n.Cut(firstNonEmpty(str("pattern"), str("query")), 60))
	case "TodoWrite":
		return "" // это его собственный список дел, человеку он ничего не говорит
	}
	return i18n.Trf("работает: %s", name)
}

// scrubEnv убирает переменные, по которым Claude Code понимает, что запущен
// внутри своей же сессии. Оставить их значит заставить дочерний процесс думать,
// что он вложен в чужую.
func scrubEnv(env []string) []string {
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

// --- журнал -----------------------------------------------------------------

// runLog — ход работы на диске. Он нужен и после того, как панель закрыли:
// «покажи, что там делал агент» спрашивают на следующий день.
type runLog struct {
	f       *os.File
	tailBuf []string
}

func newLog(path string) *runLog {
	f, err := os.Create(path)
	if err != nil {
		return &runLog{}
	}
	return &runLog{f: f}
}

func (l *runLog) write(kind, text string) {
	line := time.Now().Format("15:04:05") + " " + kind + " " + text
	if l.f != nil {
		fmt.Fprintln(l.f, line)
	}
	// В базу кладём хвост, а не всё: полный журнал бывает в мегабайт, а на
	// экране всё равно видно последние строки.
	l.tailBuf = append(l.tailBuf, line)
	if len(l.tailBuf) > 200 {
		l.tailBuf = l.tailBuf[len(l.tailBuf)-200:]
	}
}

func (l *runLog) tail() string { return strings.Join(l.tailBuf, "\n") }

func (l *runLog) Close() {
	if l.f != nil {
		l.f.Close()
	}
}

// slug — имя ветки из названия задания. Латиница, потому что имя ветки набирают
// руками в терминале, а кириллица в нём — это разложенная по-разному «й» и
// команда, которая не находится по Tab.
func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if v, ok := lat[r]; ok {
			b.WriteString(v)
			dash = false
			continue
		}
		if unicode.IsLetter(r) && r < unicode.MaxASCII || unicode.IsDigit(r) && r < unicode.MaxASCII {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 40 {
		out = strings.Trim(out[:40], "-")
	}
	if out == "" {
		out = "task"
	}
	return out
}
