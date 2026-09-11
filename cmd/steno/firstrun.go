package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/sur1cat/steno/internal/brain"
	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
	"golang.org/x/term"
)

// Первый запуск.
//
// Между `brew install steno` и первой заметкой стояло четыре препятствия, и
// каждое встречало человека молчанием или командой, которую надо набрать
// руками: нет настройки — `steno setup` на пять минут вопросов про бота и
// Telegram; нет модели whisper — адаптер печатает curl в stderr; нет доступа к
// модели для разбора — стоп. Здесь все четыре собраны в одно место и решаются
// до того, как человек начнёт говорить: наговорить пять минут и узнать, что
// расшифровывать нечем, — худший из возможных порядков.
//
// Правило одно на все четыре: то, что можно сделать молча, делается молча
// (настройка, модель VAD), то, что стоит гигабайта трафика или чужого ключа,
// спрашивается один раз и одним вопросом, а без терминала не спрашивается
// вовсе — говорится, что сделать, и на этом всё.

// ensureConfig — настройка для `steno note`: найти, а если нет — завести.
//
// Ищет там же, где все команды (ResolveConfigPath), и вдобавок в ~/steno: это
// каталог, который заводит мастер по умолчанию, и если указатель в ~/.config
// пропал, а файл там лежит, заводить второй рядом нельзя. Названный явно
// файл (-c, STENO_CONFIG) не подменяется и не заводится: опечатка в -c должна
// быть ошибкой, а не новой установкой.
func ensureConfig(flagPath string) (string, error) {
	if flagPath != core.DefaultConfigPath {
		return flagPath, nil
	}
	if p := core.ResolveConfigPath(flagPath); fileExists(p) {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if p := filepath.Join(home, "steno", core.DefaultConfigPath); fileExists(p) {
		core.RememberConfigPath(p)
		return p, nil
	}
	p, err := quietSetup()
	if err != nil {
		return "", fmt.Errorf(i18n.Tr("не завелась настройка: %w"), err)
	}
	fmt.Fprintln(os.Stderr, dim(i18n.Trf("завёл настройку %s — записи и база там же; бот, Telegram и календарь — steno setup", tildePath(p))))
	return p, nil
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// --- распознаватель ---------------------------------------------------------

const (
	// Какую модель предлагать. large-v3-q5_0, а не turbo, и это измерено, а
	// не выбрано из осторожности: на записи настоящего созвона turbo
	// превратила название продукта в похожее чужое и зациклилась (см.
	// README, «Какую модель брать»). Разница в скачивании — минута; разница в
	// расшифровке — выдуманное слово, которое follow-up перепишет уверенно.
	// И вторая причина: адаптер, мастер и doctor знают эту модель как лучшую,
	// а turbo — как ту, от которой надо предупреждать. Скачать по одному
	// Enter модель, о которой следующая же команда скажет «замени», — значит
	// две скачки вместо одной.
	whisperModelDefault = "ggml-large-v3-q5_0"
	whisperModelsURL    = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/"
	// VAD — 0.9 МБ. Без него whisper расшифровывает тишину и сочиняет на ней
	// текст; тянется молча, за компанию с моделью.
	whisperVADModel = "ggml-silero-v5.1.2"
	whisperVADURL   = "https://huggingface.co/ggml-org/whisper-vad/resolve/main/ggml-silero-v5.1.2.bin"
	// Пять байт в начале любого файла ggml. По ним отличаем модель от страницы
	// «превышен лимит», которую сервер отдаёт с кодом 200.
	ggmlMagic = "lmgg"
)

// Размеры известных моделей — чтобы назвать их в вопросе до того, как сделан
// запрос. Байты, как их отдаёт huggingface.
var whisperModelSizes = map[string]int64{
	"ggml-large-v3-q5_0":       1081140203,
	"ggml-large-v3-turbo-q5_0": 574041195,
	whisperVADModel:            885098,
}

// whisperState — что есть у распознавания через whisper.cpp и чего нет.
type whisperState struct {
	// Расшифровка идёт через whisper-cpp.sh. Иначе остальное не смотрим:
	// Groq и AssemblyAI ни бинарника, ни модели на диске не требуют.
	adapter bool
	missing []string // бинарники, которых нет в PATH
	dir     string   // каталог моделей
	model   string   // найденная модель, пусто — нет
	vad     bool
}

// inspectWhisper повторяет то, как адаптер ищет модель, — те же переменные, тот
// же каталог, — чтобы сказать «модели нет» до его запуска, а не по его stderr.
func inspectWhisper(cfg *core.Config) whisperState {
	var w whisperState
	if cfg.Transcribe.Source == "captions" || len(cfg.Transcribe.Cmd) == 0 ||
		filepath.Base(cfg.Transcribe.Cmd[0]) != "whisper-cpp.sh" {
		return w
	}
	w.adapter = true
	for _, bin := range []string{core.EnvOr("WHISPER_BIN", "whisper-cli"), "ffmpeg", "jq"} {
		if _, err := exec.LookPath(bin); err != nil {
			w.missing = append(w.missing, bin)
		}
	}
	home, _ := os.UserHomeDir()
	w.dir = core.EnvOr("WHISPER_MODEL_DIR", filepath.Join(home, ".cache", "whisper"))
	if p := os.Getenv("WHISPER_MODEL"); p != "" && fileExists(p) {
		w.model = p
	}
	if p := os.Getenv("WHISPER_VAD_MODEL"); p != "" && fileExists(p) {
		w.vad = true
	}
	entries, _ := os.ReadDir(w.dir)
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasPrefix(n, "ggml-") || !strings.HasSuffix(n, ".bin") {
			continue
		}
		if strings.HasPrefix(n, "ggml-silero-") {
			w.vad = true
			continue
		}
		if w.model == "" {
			w.model = filepath.Join(w.dir, n)
		}
	}
	return w
}

// noRecognizer — что сказать, когда whisper-cli не стоит. Отсутствие
// whisper — не поломка, а выбор: formula ставит его как recommended, и
// --without-whisper-cpp пишут нарочно. Поэтому две дороги и ни слова паники.
func noRecognizer(missing []string) error {
	local := i18n.Tr("brew install whisper-cpp jq") + "        "
	if runtime.GOOS != "darwin" {
		local = i18n.Tr("whisper-cli и jq в PATH (github.com/ggml-org/whisper.cpp)")
	}
	return fmt.Errorf(i18n.Tr("распознавателя нет (не хватает: %s). Годится любое:\n")+
		i18n.Tr("  → %s — на этой машине, бесплатно, без интернета\n")+
		i18n.Tr("  → steno setup                      — облако: Groq или AssemblyAI, нужен ключ"),
		strings.Join(missing, ", "), local)
}

// ensureTranscriber — распознаватель и модель до записи. Без терминала
// ничего не спрашивает: говорит, чего нет и чем это берётся, и выходит.
func ensureTranscriber(ctx context.Context, cfg *core.Config) error {
	w := inspectWhisper(cfg)
	if !w.adapter {
		return nil
	}
	if len(w.missing) > 0 {
		return noRecognizer(w.missing)
	}
	if w.model == "" {
		name := whisperModelName()
		if !stdinTTY() {
			return fmt.Errorf(i18n.Tr("речевой модели нет в %s. В терминале steno note скачает её сама, спросив; или руками:\n")+
				"  mkdir -p %s && curl -L -o %s/%s.bin \\\n    %s%s.bin",
				w.dir, w.dir, w.dir, name, whisperModelsURL, name)
		}
		if !askYN(i18n.Trf("Нужна речевая модель. Скачать %s (%s) в %s?", name,
			sizeText(whisperModelSizes[name]), tildePath(w.dir)), true) {
			return errors.New(i18n.Tr("без модели расшифровывать нечем. Положи свою: WHISPER_MODEL=/путь/до/ggml-*.bin"))
		}
		if err := fetchModel(ctx, whisperModelsURL+name+".bin", filepath.Join(w.dir, name+".bin")); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, ok(i18n.Tr("модель на месте: ")+tildePath(filepath.Join(w.dir, name+".bin"))))
	}
	if !w.vad && stdinTTY() {
		// Молча и без вопроса: 0.9 МБ, а без неё тишина идёт в расшифровку.
		// Не вышло — не беда, адаптер работает и без VAD и сам об этом скажет.
		if err := fetchModel(ctx, whisperVADURL, filepath.Join(w.dir, whisperVADModel+".bin")); err != nil {
			fmt.Fprintln(os.Stderr, warn(i18n.Tr("VAD не скачалась: ")+err.Error()))
		}
	}
	return nil
}

// whisperModelName — какую модель предлагать. STENO_WHISPER_MODEL — для тех,
// кому нужна другая (например, turbo на машине без GPU: вдвое меньше и в
// несколько раз быстрее, ценой того, что описано у whisperModelDefault).
func whisperModelName() string {
	n := strings.TrimSuffix(strings.TrimSpace(os.Getenv("STENO_WHISPER_MODEL")), ".bin")
	if n == "" {
		return whisperModelDefault
	}
	if !strings.HasPrefix(n, "ggml-") {
		n = "ggml-" + n
	}
	return n
}

func tildePath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

func sizeText(n int64) string {
	mb := float64(n) / (1 << 20)
	switch {
	case n <= 0:
		return "?"
	case mb >= 1000:
		return fmt.Sprintf(i18n.Tr("%.2f ГБ"), mb/1000)
	case mb < 10:
		return fmt.Sprintf(i18n.Tr("%.1f МБ"), mb)
	}
	return fmt.Sprintf(i18n.Tr("%.0f МБ"), mb)
}

// fetchModel скачивает файл модели и показывает, как идёт дело.
//
// Во временный файл рядом, переименование в конце: оборванная скачка не
// оставляет огрызка с именем модели, который следующий запуск принял бы за
// неё и упал бы внутри whisper с невнятным «failed to load». Обрывок
// убирается и при ошибке, и при Ctrl+C.
func fetchModel(ctx context.Context, url, dst string) error {
	live := term.IsTerminal(int(os.Stderr.Fd()))
	name := filepath.Base(dst)
	if !live {
		fmt.Fprintf(os.Stderr, i18n.Tr("качаю %s → %s\n"), name, dst)
	}
	pr := &progress{name: name, live: live}
	err := downloadFile(ctx, url, dst, pr.report)
	pr.finish()
	if err != nil {
		return fmt.Errorf(i18n.Tr("не скачалась %s: %w"), name, err)
	}
	return nil
}

// downloadFile — сама скачка, без слов: url в dst через dst+".part".
func downloadFile(ctx context.Context, url, dst string, report func(done, total int64)) (err error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	part := dst + ".part"
	// Остаток прошлой скачки, если её убили так, что убрать не успели.
	_ = os.Remove(part)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "steno/"+core.StenoVersion())
	resp, err := modelHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	if ct := resp.Header.Get("Content-Type"); strings.HasPrefix(ct, "text/html") {
		return errors.New(i18n.Tr("сервер прислал страницу вместо файла"))
	}
	total := resp.ContentLength

	f, err := os.OpenFile(part, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			f.Close()
			_ = os.Remove(part)
		}
	}()

	// Молчание сети дольше минуты — обрыв, а не «долго качается»: без этого
	// повисшее соединение висело бы вечно, а человек — вместе с ним.
	body := &stallGuard{r: resp.Body, limit: time.Minute, ctx: ctx}
	body.arm()
	defer body.stop()
	var done int64
	buf := make([]byte, 256<<10)
	for {
		n, rerr := body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return werr
			}
			done += int64(n)
			report(done, total)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if body.stalled() {
				return errors.New(i18n.Tr("сеть молчит дольше минуты"))
			}
			return rerr
		}
	}
	if total > 0 && done != total {
		return fmt.Errorf(i18n.Tr("пришло %d байт из %d"), done, total)
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := checkGGML(part); err != nil {
		_ = os.Remove(part)
		return err
	}
	return os.Rename(part, dst)
}

var modelHTTP = &http.Client{Transport: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	ResponseHeaderTimeout: 30 * time.Second,
}}

// checkGGML — это модель, а не что-то другое с тем же именем.
func checkGGML(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	head := make([]byte, len(ggmlMagic))
	if _, err := io.ReadFull(f, head); err != nil || string(head) != ggmlMagic {
		return errors.New(i18n.Tr("это не файл ggml — скачалось что-то другое"))
	}
	return nil
}

// stallGuard роняет чтение, если данных нет дольше limit.
type stallGuard struct {
	r     io.Reader
	limit time.Duration
	ctx   context.Context
	timer *time.Timer
	hit   chan struct{}
}

func (g *stallGuard) arm() {
	g.hit = make(chan struct{})
	g.timer = time.AfterFunc(g.limit, func() { close(g.hit) })
}

func (g *stallGuard) stop() { g.timer.Stop() }

func (g *stallGuard) stalled() bool {
	select {
	case <-g.hit:
		return true
	default:
		return false
	}
}

func (g *stallGuard) Read(p []byte) (int, error) {
	type res struct {
		n   int
		err error
	}
	ch := make(chan res, 1)
	go func() {
		n, err := g.r.Read(p)
		ch <- res{n, err}
	}()
	select {
	case r := <-ch:
		g.timer.Reset(g.limit)
		return r.n, r.err
	case <-g.hit:
		return 0, errors.New("stalled")
	case <-g.ctx.Done():
		return 0, g.ctx.Err()
	}
}

// progress — одна строка на stderr, перерисовывается на месте.
type progress struct {
	name    string
	live    bool
	started time.Time
	last    time.Time
	drawn   bool
}

// progressMin — файлы меньше этого качаются без полосы: VAD на 0.9 МБ
// приходит за секунду, и полоса для него — мигание, а не ход дела.
const progressMin = 16 << 20

func (p *progress) report(done, total int64) {
	if !p.live || (total > 0 && total < progressMin) {
		return
	}
	now := time.Now()
	if p.started.IsZero() {
		p.started = now
	}
	if now.Sub(p.last) < 200*time.Millisecond && done != total {
		return
	}
	p.last = now
	speed := ""
	if el := now.Sub(p.started).Seconds(); el > 0.5 {
		speed = fmt.Sprintf(i18n.Tr("  %.0f МБ/с"), float64(done)/(1<<20)/el)
	}
	line := fmt.Sprintf("  %s  %s", p.name, sizeText(done))
	if total > 0 {
		line = fmt.Sprintf("  %s  %3d%%  %s / %s%s", p.name, done*100/total,
			sizeText(done), sizeText(total), speed)
	}
	fmt.Fprintf(os.Stderr, "\r%-72s", line)
	p.drawn = true
}

func (p *progress) finish() {
	if p.drawn {
		fmt.Fprint(os.Stderr, "\r"+strings.Repeat(" ", 72)+"\r")
	}
}

// --- разбор -----------------------------------------------------------------

// keyEnvByPrefix — чей это ключ, по его началу. Формы ключей у провайдеров
// разные и устоявшиеся; человек, вставивший ключ, не должен ещё и выбирать из
// списка, кому он принадлежит.
func keyEnvByPrefix(key string) string {
	switch {
	case strings.HasPrefix(key, "sk-ant-"):
		return "ANTHROPIC_API_KEY"
	case strings.HasPrefix(key, "gsk_"):
		return "GROQ_API_KEY"
	case strings.HasPrefix(key, "sk-or-"):
		return "OPENROUTER_API_KEY"
	case strings.HasPrefix(key, "sk-"):
		return "OPENAI_API_KEY"
	}
	return ""
}

// ensureBrain — есть ли чем разбирать, и если нет — один вопрос.
//
// Вопрос — не «какой путь выбираешь» (ответ на такой ничего не меняет, и
// человеку всё равно идти за ключом), а «вставь ключ»: с ключом в руках это
// одна вставка, и заметка разбирается сразу. Кому ближе подписка Claude Code,
// тот видит, что сделать, и возвращается после входа. Без терминала — те же
// дороги, без вопроса.
//
// Спрашиваем только там, где выбор не сделан: claude на auto или на ключе.
// Названный провайдер, который не готов, — это уже настройка, и её отказ
// объясняет ResolveVia словами того, кто её делал.
func ensureBrain(cfg *core.Config, cfgPath string) error {
	_, _, err := brain.ResolveVia(cfg)
	if err == nil {
		return nil
	}
	if cfg.BrainProvider() != core.ProviderClaude ||
		(cfg.Claude.Via != "auto" && cfg.Claude.Via != "api") {
		return err
	}
	envPath := filepath.Join(filepath.Dir(cfgPath), ".env")
	roads := i18n.Tr("Разбирать заметку нечем. Годится любое:\n") +
		i18n.Tr("  → Claude Code с подпиской: запусти `claude` и войди — steno возьмёт её через claude -p\n") +
		i18n.Tr("  → ключ Anthropic: console.anthropic.com → API keys\n") +
		i18n.Tr("  → ключ OpenAI, Groq или OpenRouter: platform.openai.com, console.groq.com, openrouter.ai")
	if !stdinTTY() {
		return errors.New(roads + i18n.Tr("\nКлюч — в окружение (export …) или строкой в ") + envPath)
	}
	fmt.Fprintln(os.Stderr, roads)
	key := readSecret(i18n.Trf("Вставь ключ — запишу в %s (пусто — выйти): ", tildePath(envPath)))
	if key == "" {
		return errors.New(i18n.Tr("ключа нет — вернись, когда будет, или войди в Claude Code"))
	}
	env := keyEnvByPrefix(key)
	if env == "" {
		return errors.New(i18n.Tr("не узнал ключ по виду: sk-ant-… это Anthropic, gsk_… Groq, sk-or-… OpenRouter, sk-… OpenAI. ") +
			i18n.Tr("Задай переменную сам: export OPENAI_API_KEY=… (или GROQ_API_KEY, OPENROUTER_API_KEY)"))
	}
	if cfg.Claude.Via == "api" && env != cfg.Claude.APIKeyEnv {
		return fmt.Errorf(i18n.Tr("в настройке выбран ключ Anthropic (claude.via=api), а это %s — либо ключ Anthropic, либо claude.via=auto"), env)
	}
	if err := writeEnvVar(envPath, env, key); err != nil {
		return err
	}
	_ = os.Setenv(env, key)
	if note, ok := brain.ApplyAutoOpenAI(cfg); ok {
		fmt.Fprintln(os.Stderr, dim("  "+note))
	}
	_, how, err := brain.ResolveVia(cfg)
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, ok(env+i18n.Tr(" записан в ")+tildePath(envPath)+" — "+how))
	return nil
}

// writeEnvVar дописывает или заменяет одну переменную в .env. Тот же файл, что
// пишет мастер, те же права 0600.
func writeEnvVar(path, k, v string) error {
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var lines []string
	if len(raw) > 0 {
		lines = strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	} else {
		lines = []string{
			strings.TrimRight(i18n.Tr("# Секреты steno. Файл читается при запуске.\n"), "\n"),
			strings.TrimRight(i18n.Tr("# Не клади его в репозиторий: тут ключи, а не настройки.\n\n"), "\n"),
		}
	}
	found := false
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), k+"=") {
			lines[i] = k + "=" + v
			found = true
		}
	}
	if !found {
		lines = append(lines, k+"="+v)
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

// --- терминал ---------------------------------------------------------------

func stdinTTY() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

// askYN — вопрос с умолчанием: Enter берёт то, что в скобках. Это не askYes —
// тот про необратимое и Enter согласием не считает; здесь же Enter и есть
// весь смысл: один вопрос, одно нажатие.
func askYN(question string, def bool) bool {
	hint := "y/N"
	if def {
		hint = "Y/n"
	}
	fmt.Fprintf(os.Stderr, "%s [%s] ", question, dim(hint))
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		fmt.Fprintln(os.Stderr)
		return def
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "":
		return def
	case "y", "yes", "д", "да":
		return true
	}
	return false
}

// readSecret читает ключ, не показывая его: ключи не должны оставаться в
// истории терминала и на плече у соседа.
func readSecret(prompt string) string {
	fmt.Fprint(os.Stderr, prompt)
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		return strings.TrimSpace(line)
	}
	raw, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}
