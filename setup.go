package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/term"
)

// Мастер установки.
//
// Развернуть steno должно быть можно и на ноутбуке одного человека, и на
// сервере команды с шестью параллельными созвонами. Разница между этими двумя
// установками — десяток решений, каждое из которых по отдельности неочевидно:
// сколько расшифровок держать одновременно, какой моделью распознавать, куда
// публиковать. Заставлять человека собирать это из документации значит не
// получить ни одной установки, кроме своей.
//
// Поэтому мастер спрашивает по одному и сразу проверяет ответ: ключ, который
// не работает, лучше узнать здесь, а не на первом созвоне.

type setupProfile struct {
	Key   string
	Name  string
	About string

	MaxConcurrentMeetings int
	MaxConcurrentWhisper  int
	Effort                string
}

var setupProfiles = []setupProfile{
	{
		Key: "personal", Name: "Для себя",
		About:                 "Один человек, свои созвоны. Записи и расшифровки на своей машине.",
		MaxConcurrentMeetings: 1, MaxConcurrentWhisper: 1, Effort: "low",
	},
	{
		Key: "team", Name: "Небольшая команда",
		About:                 "Несколько созвонов в неделю, редко больше одного разом.",
		MaxConcurrentMeetings: 2, MaxConcurrentWhisper: 1, Effort: "low",
	},
	{
		Key: "big", Name: "Большая команда",
		About:                 "Пять-шесть созвонов параллельно, отдельный сервер, GPU или Groq.",
		MaxConcurrentMeetings: 6, MaxConcurrentWhisper: 2, Effort: "low",
	},
}

type setupState struct {
	dir     string
	profile setupProfile
	cfg     *Config
	env     map[string]string
	in      *bufio.Reader
}

func cmdSetup(ctx context.Context, args []string) error {
	fs := newFlagSet("setup")
	out := fs.String("o", "steno.json", "куда записать конфиг")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}

	s := &setupState{
		cfg: defaultConfig(),
		env: map[string]string{},
		in:  bufio.NewReader(os.Stdin),
	}
	abs, err := filepath.Abs(*out)
	if err != nil {
		return err
	}
	s.dir = filepath.Dir(abs)

	title("steno — настройка")
	fmt.Println(dim("Спрошу по одному и сразу проверю. Пустой ответ берёт значение в скобках."))
	fmt.Println(dim("Прервать можно в любой момент — ничего не записывается до самого конца."))
	fmt.Println()

	if _, err := os.Stat(abs); err == nil {
		if !s.confirm(abs+" уже есть. Перезаписать?", false) {
			return fmt.Errorf("отменено")
		}
	}

	steps := []func(context.Context) error{
		s.askProfile,
		s.askData,
		s.askTranscribe,
		s.askClaude,
		s.askSources,
		s.askTargets,
		s.askPanel,
	}
	for _, step := range steps {
		if err := step(ctx); err != nil {
			return err
		}
	}
	return s.write(abs)
}

// --- шаги --------------------------------------------------------------------

func (s *setupState) askProfile(context.Context) error {
	section("Масштаб")
	var opts []string
	for _, p := range setupProfiles {
		opts = append(opts, p.Name+" — "+dim(p.About))
	}
	i := s.choose("Как будете пользоваться?", opts, 1)
	s.profile = setupProfiles[i]

	s.cfg.Calendar.MaxConcurrent = s.profile.MaxConcurrentMeetings
	s.cfg.Transcribe.MaxConcurrent = s.profile.MaxConcurrentWhisper
	s.cfg.Claude.Effort = s.profile.Effort
	return nil
}

func (s *setupState) askData(context.Context) error {
	section("Где хранить")
	def := filepath.Join(s.dir, "data")
	s.cfg.DataDir = s.ask("Каталог для записей и базы", def)
	if err := os.MkdirAll(s.cfg.DataDir, 0o755); err != nil {
		return fmt.Errorf("не создался каталог: %w", err)
	}
	fmt.Println(ok("каталог готов"))
	return nil
}

func (s *setupState) askTranscribe(ctx context.Context) error {
	section("Чем распознавать речь")
	fmt.Println(dim("От этого зависит и качество, и во что обойдётся железо."))
	fmt.Println()

	i := s.choose("Выбери", []string{
		"Groq — та же whisper-large-v3, но на их железе. " +
			dim("$0.04 за час звука, ничего ставить не надо, аудио уходит наружу"),
		"whisper на своей машине. " +
			dim("ничего не уходит наружу; нужна модель на 0.5–3 ГБ, а без GPU медленно"),
		"Субтитры Google Meet. " +
			dim("бесплатно и мгновенно, качество ниже, смешанную речь не тянет"),
	}, 0)

	switch i {
	case 0:
		s.cfg.Transcribe.Source = "command"
		s.cfg.Transcribe.Cmd = []string{"./adapters/groq.sh", "{{audio}}", "{{language}}"}
		key := s.askSecret("Ключ Groq", "console.groq.com/keys")
		if key != "" {
			s.env["GROQ_API_KEY"] = key
			fmt.Println(ok("ключ записан"))
		}
	case 1:
		s.cfg.Transcribe.Source = "command"
		s.cfg.Transcribe.Cmd = []string{"./adapters/whisper-cpp.sh", "{{audio}}", "{{language}}"}
		s.checkWhisper()
	case 2:
		s.cfg.Transcribe.Source = "captions"
		fmt.Println(dim("  Текст возьмётся из субтитров Meet. Имена говорящих в нём уже есть."))
	}
	return nil
}

// checkWhisper смотрит, чего не хватает, и говорит, чем это ставится. Узнать об
// отсутствующей модели на первом созвоне — худший момент из возможных.
func (s *setupState) checkWhisper() {
	missing := []string{}
	for _, bin := range []string{"whisper-cli", "ffmpeg", "jq"} {
		if _, err := exec.LookPath(bin); err != nil {
			missing = append(missing, bin)
		}
	}
	if len(missing) > 0 {
		fmt.Println(warn("не хватает: " + strings.Join(missing, ", ")))
		fmt.Println(dim("  brew install whisper-cpp ffmpeg jq"))
	} else {
		fmt.Println(ok("whisper-cli, ffmpeg и jq на месте"))
	}

	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".cache", "whisper")
	found := ""
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "ggml-") && strings.HasSuffix(e.Name(), ".bin") &&
				!strings.Contains(e.Name(), "silero") {
				found = e.Name()
				break
			}
		}
	}
	if found == "" {
		fmt.Println(warn("модели нет — без неё расшифровка не заработает"))
		fmt.Println(dim("  mkdir -p " + dir))
		fmt.Println(dim("  curl -L -o " + dir + "/ggml-large-v3-q5_0.bin \\"))
		fmt.Println(dim("    https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-large-v3-q5_0.bin"))
		fmt.Println(dim("  и отдельно VAD — 868 КБ, но ускоряет в восемь раз:"))
		fmt.Println(dim("  curl -L -o " + dir + "/ggml-silero-v5.1.2.bin \\"))
		fmt.Println(dim("    https://huggingface.co/ggml-org/whisper-vad/resolve/main/ggml-silero-v5.1.2.bin"))
	} else if strings.Contains(found, "turbo") {
		// turbo не просто хуже — она подменяет незнакомые слова похожими
		// знакомыми и зацикливается. Ошибка получается связной и правдоподобной,
		// в follow-up её уже не отличить от сказанного.
		fmt.Println(warn("модель " + found + " — она путает названия и зацикливается"))
		fmt.Println(dim("  на записи созвона turbo превратила Plaud в Cloud AI и повторила"))
		fmt.Println(dim("  его семнадцать раз подряд. Возьми large-v3-q5_0 — тот же размер:"))
		fmt.Println(dim("  curl -L -o " + dir + "/ggml-large-v3-q5_0.bin \\"))
		fmt.Println(dim("    https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-large-v3-q5_0.bin"))
	} else {
		fmt.Println(ok("модель " + found))
	}
}

func (s *setupState) askClaude(ctx context.Context) error {
	section("Claude — он собирает follow-up")

	// Два пути, и выбор между ними не про цену, а про то, кто запускает.
	// Подписка идёт через `claude -p` — штатный неинтерактивный режим Claude
	// Code. Отдельно платить не надо, но нужен выполненный вход, который делает
	// человек: на сервере, куда никто не заходит, это не работает. Поэтому
	// подписку предлагаем первой только если CLI уже готов.
	cliOK, cliNote := claudeCLIAvailable()
	opts := []string{
		"Подписка Claude " + dim("через claude -p, отдельный ключ не нужен"),
		"Ключ API " + dim("нужен для сервера: работает без входа человеком"),
	}
	if cliOK {
		opts[0] += "\n     " + ok(cliNote)
	} else {
		opts[0] += "\n     " + warn(cliNote)
	}
	def := 1
	if cliOK {
		def = 0
	}
	if s.choose("Чем платить за follow-up", opts, def) == 0 {
		s.cfg.Claude.Via = "cli"
		if !cliOK {
			fmt.Println(warn("CLI пока не готов — steno скажет об этом при первом созвоне"))
			fmt.Println(dim("  поставь Claude Code и войди: claude auth login"))
		}
		// Потолок на запрос: у подписки нет счёта, который придёт в конце
		// месяца, но есть лимит, который можно выбрать одним циклом.
		if s.cfg.Claude.MaxUSDPerCall == 0 {
			s.cfg.Claude.MaxUSDPerCall = 2
		}
		fmt.Println(dim("  Потолок на один follow-up: $" +
			strconv.FormatFloat(s.cfg.Claude.MaxUSDPerCall, 'f', -1, 64) +
			" — меняется в claude.max_usd_per_call"))
	} else {
		s.cfg.Claude.Via = "api"
		key := s.askSecret("Ключ Anthropic", "console.anthropic.com → API keys")
		if key != "" {
			s.env["ANTHROPIC_API_KEY"] = key
		}
	}

	i := s.choose("Модель", []string{
		"claude-opus-5 " + dim("умнее, около $0.40 за часовой созвон"),
		"claude-sonnet-5 " + dim("дешевле в два с половиной раза, для планёрок обычно хватает"),
	}, 0)
	s.cfg.Claude.Model = []string{"claude-opus-5", "claude-sonnet-5"}[i]
	fmt.Println(dim("  Усилие: " + s.cfg.Claude.Effort + ". Выше поднимать смысла нет: на замерах"))
	fmt.Println(dim("  усилие не добавило ни одной задачи, только время и расход."))
	return nil
}

func (s *setupState) askSources(ctx context.Context) error {
	section("Как бот попадает в звонок")
	fmt.Println(dim("Можно включить несколько. Календарь закрывает запланированное,"))
	fmt.Println(dim("остальные — внезапное."))
	fmt.Println()

	if s.confirm("Ходить по календарям команды?", false) {
		s.cfg.Calendar.Enabled = true
		s.cfg.Calendar.Calendars = commaList(s.ask("Чьи календари, через запятую", ""))
		s.askGoogleAccess()
	}
	if s.confirm("Приходить, когда бота добавляют в звонок по почте?", false) {
		s.cfg.Gmail.Enabled = true
		s.cfg.Gmail.Account = s.ask("Почта аккаунта бота", "")
		s.askGoogleAccess()
	}
	if s.confirm("Принимать ссылки в Telegram?", true) {
		s.cfg.Telegram.Listen = true
		if tok := s.askSecret("Токен бота Telegram", "@BotFather"); tok != "" {
			s.env["TELEGRAM_BOT_TOKEN"] = tok
		}
		s.cfg.Telegram.ChatID = s.ask("Из какого чата принимать (chat_id)", "")
		if s.cfg.Telegram.ChatID != "" {
			s.cfg.Telegram.AllowedChats = []string{s.cfg.Telegram.ChatID}
		} else {
			fmt.Println(warn("без chat_id сервис не запустит приём: принимать ссылки от кого угодно нельзя"))
		}
	}
	return nil
}

func (s *setupState) askTargets(ctx context.Context) error {
	section("Куда складывать итоги")
	fmt.Println(dim("Панель есть всегда — там архив, поиск и проекты. Остальное по желанию."))
	fmt.Println()

	// Спрашиваем всегда, даже если приём в Telegram уже включён: включить
	// отправку молча, по одному лишь факту приёма, — значит не сказать
	// человеку, куда пойдут итоги его созвонов.
	if s.cfg.Telegram.ChatID != "" {
		if s.confirm("Присылать follow-up в Telegram, в тот же чат?", true) {
			s.cfg.Telegram.Enabled = true
		}
	} else if s.confirm("Присылать follow-up в Telegram?", false) {
		if tok := s.askSecret("Токен бота Telegram", "@BotFather"); tok != "" {
			s.env["TELEGRAM_BOT_TOKEN"] = tok
		}
		s.cfg.Telegram.ChatID = s.ask("В какой чат (chat_id)", "")
		s.cfg.Telegram.Enabled = s.cfg.Telegram.ChatID != ""
	}
	if s.confirm("Публиковать в Google Docs?", false) {
		s.cfg.GoogleDocs.Enabled = true
		s.askGoogleAccess()
		// От чужого имени умеет только ключ организации. По кнопке документы
		// создаются от того, кто её нажал, и спрашивать тут нечего.
		if s.cfg.GoogleDocs.CredentialsFile != "" {
			s.cfg.GoogleDocs.Subject = s.ask("От чьего имени создавать документы", "")
		}
		s.cfg.GoogleDocs.FolderID = s.ask("Папка на Drive: хвост адреса после /folders/ (пусто — корень)", "")
		s.cfg.GoogleDocs.ProjectDocs = s.confirm("Вести отдельный документ на каждый проект?", true)
	}
	if s.confirm("Публиковать в Slack?", false) {
		s.cfg.Slack.Enabled = true
		if tok := s.askSecret("Токен бота Slack (xoxb-…)", "api.slack.com/apps"); tok != "" {
			s.env["SLACK_BOT_TOKEN"] = tok
		}
		s.cfg.Slack.Channel = s.ask("Канал", "#созвоны")
		s.cfg.Slack.DMOwners = s.confirm("Писать в личку тем, на ком задача?", true)
	}
	return nil
}

func (s *setupState) askPanel(ctx context.Context) error {
	section("Панель")
	s.cfg.Panel.Enabled = true
	s.cfg.Panel.Addr = s.ask("Адрес", "127.0.0.1:8080")
	pass := s.askSecret("Пароль (общий на команду)", "")
	if pass == "" {
		pass = randomPassword()
		fmt.Println(ok("сгенерировал: " + pass))
	}
	s.env["STENO_PANEL_PASSWORD"] = pass
	if !strings.HasPrefix(s.cfg.Panel.Addr, "127.0.0.1") &&
		!strings.HasPrefix(s.cfg.Panel.Addr, "localhost") {
		s.cfg.Panel.Secure = s.confirm("Панель за HTTPS?", true)
		if !s.cfg.Panel.Secure {
			fmt.Println(warn("панель смотрит наружу без TLS — пароль и cookie пойдут открытым текстом"))
		}
	}
	return nil
}

// --- запись ------------------------------------------------------------------

func (s *setupState) write(configPath string) error {
	section("Готово")

	// Секреты кладём отдельным файлом с правами 0600 и не пускаем в конфиг:
	// конфиг хочется держать в репозитории, а токены — нет.
	envPath := filepath.Join(s.dir, ".env")
	if len(s.env) > 0 {
		var b strings.Builder
		b.WriteString("# Секреты steno. Файл читается при запуске.\n")
		b.WriteString("# Не клади его в репозиторий: тут ключи, а не настройки.\n\n")
		for _, k := range sortedKeys(s.env) {
			fmt.Fprintf(&b, "%s=%s\n", k, s.env[k])
		}
		if err := os.WriteFile(envPath, []byte(b.String()), 0o600); err != nil {
			return fmt.Errorf("не записался %s: %w", envPath, err)
		}
		fmt.Println(ok(envPath + "  " + dim("права 0600, "+strconv.Itoa(len(s.env))+" секретов")))
	}

	raw, err := marshalConfig(s.cfg)
	if err != nil {
		return err
	}
	if err := os.WriteFile(configPath, raw, 0o644); err != nil {
		return err
	}
	fmt.Println(ok(configPath))

	// .gitignore рядом с секретами — чтобы они не уехали в первый же коммит.
	gi := filepath.Join(s.dir, ".gitignore")
	if _, err := os.Stat(gi); os.IsNotExist(err) && len(s.env) > 0 {
		_ = os.WriteFile(gi, []byte(".env\ndata/\n"), 0o644)
		fmt.Println(ok(gi + "  " + dim("чтобы .env не уехал в репозиторий")))
	}

	fmt.Println()
	fmt.Println(bold("Дальше:"))
	fmt.Printf("  steno doctor -c %s   %s\n", filepath.Base(configPath),
		dim("проверить, что всё на месте"))
	fmt.Printf("  steno serve  -c %s   %s\n", filepath.Base(configPath),
		dim("запустить"))
	if s.cfg.Panel.Enabled {
		fmt.Printf("  http://%s%s\n", s.cfg.Panel.Addr, dim("  — панель"))
	}
	fmt.Println()
	fmt.Println(dim("Проверить на живом созвоне, ничего больше не настраивая:"))
	fmt.Printf("  steno join --no-followup --captions %s\n",
		dim("https://meet.google.com/… или https://meet.jit.si/…"))
	return nil
}

// --- ввод --------------------------------------------------------------------

func (s *setupState) ask(question, def string) string {
	for {
		if def != "" {
			fmt.Printf("  %s [%s]: ", question, dim(def))
		} else {
			fmt.Printf("  %s: ", question)
		}
		line, err := s.in.ReadString('\n')
		if err != nil && strings.TrimSpace(line) == "" {
			return def
		}
		if v := strings.TrimSpace(line); v != "" {
			return v
		}
		return def
	}
}

// askSecret не показывает ввод: пароли и ключи не должны оставаться в истории
// терминала и на плече у соседа.
func (s *setupState) askSecret(question, where string) string {
	if where != "" {
		fmt.Printf("  %s %s\n", question, dim("— "+where))
		fmt.Print("  ")
	} else {
		fmt.Printf("  %s: ", question)
	}
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		line, _ := s.in.ReadString('\n')
		return strings.TrimSpace(line)
	}
	raw, err := term.ReadPassword(fd)
	fmt.Println()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func (s *setupState) confirm(question string, def bool) bool {
	hint := "y/N"
	if def {
		hint = "Y/n"
	}
	for {
		fmt.Printf("  %s [%s]: ", question, dim(hint))
		line, _ := s.in.ReadString('\n')
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "":
			return def
		case "y", "yes", "д", "да":
			return true
		case "n", "no", "н", "нет":
			return false
		}
	}
}

func (s *setupState) choose(question string, options []string, def int) int {
	fmt.Printf("  %s\n", question)
	for i, o := range options {
		fmt.Printf("    %d) %s\n", i+1, o)
	}
	for {
		fmt.Printf("  Номер [%s]: ", dim(strconv.Itoa(def+1)))
		line, _ := s.in.ReadString('\n')
		v := strings.TrimSpace(line)
		if v == "" {
			return def
		}
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= len(options) {
			return n - 1
		}
	}
}

// askGoogleAccess спрашивает, каким путём steno попадёт в Google, и спрашивает
// один раз: календарь, почта и Google Docs ходят туда одним и тем же способом.
//
// Путей два, и выбор между ними не про удобство, а про то, чей это Google.
// Ключ service-account читает календари всех сотрудников, никого не спрашивая,
// — но за ним стоят проект в Google Cloud, три включённых API и админка домена.
// У человека, который ставит steno себе, ничего этого нет и быть не может, и
// упереться в это на первом же шаге значит не поставить steno вовсе.
func (s *setupState) askGoogleAccess() {
	if s.cfg.Google.ClientID != "" || s.cfg.GoogleDocs.CredentialsFile != "" {
		return // уже спрашивали
	}
	i := s.choose("Как steno попадёт в Google?", []string{
		"По кнопке в панели " +
			dim("человек соглашается один раз; steno видит ровно то, что видит он"),
		"Ключом организации " +
			dim("файл service-account: видит календари всех, нужен свой домен и админка"),
	}, 0)
	if i == 1 {
		s.cfg.GoogleDocs.CredentialsFile = s.askGoogleKey()
		return
	}
	// Адрес страницы, а не путь по меню: меню Google переставляет пункты
	// чаще, чем меняет адреса, и человек, который ищет «Credentials» глазами,
	// натыкается на переименованный раздел.
	fmt.Println(dim("  Открой https://console.cloud.google.com/apis/credentials"))
	fmt.Println(dim("  → Create credentials → OAuth client ID → тип Desktop app."))
	fmt.Println(dim("  Google покажет две строки — скопируй их сюда."))
	s.cfg.Google.ClientID = s.ask("Client ID", "")
	if sec := s.askSecret("Client secret", ""); sec != "" {
		s.env["GOOGLE_CLIENT_SECRET"] = sec
	}
	if s.cfg.Google.ClientID == "" {
		fmt.Println(warn("без этого кнопка в панели не появится"))
		return
	}
	fmt.Println(ok("осталось нажать «Подключить Google» в настройках панели"))
}

func (s *setupState) askGoogleKey() string {
	fmt.Println(dim("  Нужен ключ service-account с domain-wide delegation."))
	fmt.Println(dim("  Консоль Google → IAM → сервисные аккаунты → ключи → создать JSON."))
	for {
		p := s.ask("Путь к файлу ключа", "")
		if p == "" {
			fmt.Println(warn("без него календарь, почта и Google Docs не заработают"))
			return ""
		}
		p = expandHome(p)
		if c := checkGoogleKey("ключ", p); c.state == "ok" {
			fmt.Println(ok(c.note))
			return p
		} else {
			fmt.Println(warn(c.note))
			for _, f := range c.fix {
				fmt.Println(dim("  " + f))
			}
		}
	}
}

// --- оформление ---------------------------------------------------------------
//
// Цвета включаются, только если вывод идёт в терминал: в логе systemd или в
// пайпе escape-последовательности только мешают.

var useColor = term.IsTerminal(int(os.Stdout.Fd())) && os.Getenv("NO_COLOR") == ""

func paint(code, s string) string {
	if !useColor {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func bold(s string) string { return paint("1", s) }
func dim(s string) string  { return paint("2", s) }

func title(s string) {
	fmt.Println()
	fmt.Println(bold(s))
	fmt.Println(dim(strings.Repeat("─", len([]rune(s)))))
}

func section(s string) {
	fmt.Println()
	fmt.Println(bold("· " + s))
}

func ok(s string) string   { return "  " + paint("32", "✓") + " " + s }
func warn(s string) string { return "  " + paint("33", "!") + " " + s }

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func randomPassword() string {
	const alphabet = "abcdefghijkmnpqrstuvwxyz23456789"
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

// loadDotEnv подхватывает секреты из файла рядом с конфигом. Просить человека
// каждый раз экспортировать шесть переменных — верный способ получить сервис,
// запущенный без половины из них.
func loadDotEnv(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		v = strings.Trim(v, `"'`)
		// Уже заданное окружение главнее файла: в проде переменные приходят от
		// systemd или docker, и файл не должен их перебивать.
		if _, exists := os.LookupEnv(k); !exists {
			_ = os.Setenv(k, v)
		}
	}
	return nil
}

// marshalConfig печатает конфиг человекочитаемо: его будут править руками.
func marshalConfig(c *Config) ([]byte, error) {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// commaList — свой разбор списка через запятую: одноимённая функция живёт в
// файлах панели, которые сейчас переписываются, и завязываться на неё незачем.
func commaList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}
