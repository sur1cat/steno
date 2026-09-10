package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

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

// См. uiTabTitles: таблица собирается лениво, уже на известном языке.
var setupProfiles = sync.OnceValue(func() []setupProfile {
	return []setupProfile{
		{
			Key: "personal", Name: tr("Для себя"),
			About:                 tr("Один человек, свои созвоны. Записи и расшифровки на своей машине."),
			MaxConcurrentMeetings: 1, MaxConcurrentWhisper: 1, Effort: "low",
		},
		{
			Key: "team", Name: tr("Небольшая команда"),
			About:                 tr("Несколько созвонов в неделю, редко больше одного разом."),
			MaxConcurrentMeetings: 2, MaxConcurrentWhisper: 1, Effort: "low",
		},
		{
			Key: "big", Name: tr("Большая команда"),
			About:                 tr("Пять-шесть созвонов параллельно, отдельный сервер, GPU или Groq."),
			MaxConcurrentMeetings: 6, MaxConcurrentWhisper: 2, Effort: "low",
		},
	}
})

type setupState struct {
	dir string
	// -o назван человеком: тогда каталог не переспрашиваем и не уводим в ~/steno.
	dirChosen bool
	profile   setupProfile
	cfg       *Config
	env       map[string]string
	in        *bufio.Reader
}

func cmdSetup(ctx context.Context, args []string) error {
	fs := newFlagSet("setup")
	out := fs.String("o", "", tr("куда записать конфиг"))
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	chosen := *out != ""
	if !chosen {
		*out = "steno.json"
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
	s.dirChosen = chosen

	// Ctrl+C обязан прерывать. Сам по себе он этого не делал: main перехватывает
	// SIGINT ради мягкой остановки сервиса, а мастер висит на чтении stdin и про
	// отмену контекста не знает — сигнал ловился и пропадал. Хуже того, в строке
	// ниже было написано, что прервать можно в любой момент.
	//
	// Состояние терминала снимаем заранее: прерывание на вводе пароля приходится
	// на сырой режим, и без восстановления человек остаётся с неработающей
	// оболочкой.
	fd := int(os.Stdin.Fd())
	tty, _ := term.GetState(fd)
	// done закрывается при выходе: без него обработчик срабатывал и на обычном
	// завершении — main отменяет контекст, когда команда вернулась, и мастер
	// дописывал «прервано» под успешно записанным конфигом.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-done:
			return
		case <-ctx.Done():
		}
		if tty != nil {
			_ = term.Restore(fd, tty)
		}
		fmt.Println()
		fmt.Println(dim(tr("прервано — ничего не записано")))
		os.Exit(130)
	}()

	title(tr("steno — настройка"))
	fmt.Println(dim(tr("Спрошу по одному и сразу проверю. Пустой ответ берёт значение в скобках.")))
	fmt.Println(dim(tr("Ctrl+C прерывает в любой момент — до самого конца ничего не записывается.")))
	fmt.Println()

	if _, err := os.Stat(abs); err == nil {
		if !s.confirm(abs+tr(" уже есть. Перезаписать?"), false) {
			return errors.New(tr("отменено"))
		}
	}

	steps := []func(context.Context) error{
		s.askLang,
		s.askProfile,
		s.askData,
		s.askTranscribe,
		s.askClaude,
		s.askSources,
		s.askTargets,
		s.askPanel,
	}
	// +1 — «Готово» тоже раздел и тоже печатает заголовок. Без этого последним
	// показывалось «шаг 8 из 7».
	stepNo, stepTotal = 0, len(steps)+1
	for _, step := range steps {
		if err := step(ctx); err != nil {
			return err
		}
	}
	return s.write(abs)
}

// --- шаги --------------------------------------------------------------------

// askLang — первым вопросом и до всего остального: дальше мастер говорит на
// выбранном языке, и спрашивать об этом в конце было бы поздно. Заголовок и
// подписи здесь двуязычные — на этом шаге ещё неизвестно, чей это экран.
//
// Умолчание — тот язык, на котором steno запущен: STENO_LANG=ru steno setup
// не должен переспрашивать очевидное.
func (s *setupState) askLang(context.Context) error {
	section("Language · Язык")
	def := 0
	if uiLang == langRU {
		def = 1
	}
	i := s.choose("Interface language · Язык интерфейса", []string{
		"English " + dim("CLI, terminal interface, panel"),
		"Русский " + dim("CLI, терминальный интерфейс, панель"),
	}, def)
	s.cfg.Lang = []string{langEN, langRU}[i]
	setLang(s.cfg.Lang)
	// Часть умолчаний зависит от языка, а конфиг собран до этого вопроса:
	// имя бота, язык субтитров, язык follow-up и маркер календаря.
	applyLangDefaults(s.cfg)
	return nil
}

func (s *setupState) askProfile(context.Context) error {
	section(tr("Масштаб"))
	var opts []string
	for _, p := range setupProfiles() {
		opts = append(opts, p.Name+" — "+dim(p.About))
	}
	i := s.choose(tr("Как будете пользоваться?"), opts, 1)
	s.profile = setupProfiles()[i]

	s.cfg.Calendar.MaxConcurrent = s.profile.MaxConcurrentMeetings
	s.cfg.Transcribe.MaxConcurrent = s.profile.MaxConcurrentWhisper
	s.cfg.Claude.Effort = s.profile.Effort
	return nil
}

// askData выбирает каталог и заводит его сам. Раньше мастер молча писал в
// текущий, и человеку приходилось перед запуском делать mkdir и cd — шаг, о
// котором он узнавал только из инструкции.
func (s *setupState) askData(context.Context) error {
	section(tr("Где хранить"))
	fmt.Println(dim(tr("  Сюда лягут настройки, записи созвонов и база. Каталог заведу сам.")))
	fmt.Println()

	home, _ := os.UserHomeDir()
	def := filepath.Join(home, "steno")
	if s.dirChosen {
		def = s.dir // человек сам назвал файл через -o, не спорим
	}
	dir := expandHome(s.ask(tr("Каталог"), def))
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return fmt.Errorf(tr("не создался каталог: %w"), err)
	}
	s.dir = abs
	s.cfg.DataDir = filepath.Join(abs, "data")
	if err := os.MkdirAll(s.cfg.DataDir, 0o755); err != nil {
		return fmt.Errorf(tr("не создался каталог: %w"), err)
	}
	fmt.Println(ok(abs))
	return nil
}

func (s *setupState) askTranscribe(ctx context.Context) error {
	section(tr("Чем распознавать речь"))
	fmt.Println(dim(tr("  От этого зависит и качество, и во что обойдётся железо.")))
	fmt.Println()

	i := s.choose(tr("Выбери"), []string{
		tr("Groq — та же whisper-large-v3, но на их железе. ") +
			dim(tr("$0.04 за час звука, ничего ставить не надо, аудио уходит наружу")),
		tr("whisper на своей машине. ") +
			dim(tr("ничего не уходит наружу; нужна модель на 0.5–3 ГБ, а без GPU медленно")),
		tr("Субтитры Google Meet. ") +
			dim(tr("бесплатно и мгновенно, качество ниже, смешанную речь не тянет")),
	}, 0)

	switch i {
	case 0:
		s.cfg.Transcribe.Source = "command"
		s.cfg.Transcribe.Cmd = []string{findAdapter("groq.sh"), "{{audio}}", "{{language}}"}
		key := s.askSecret(tr("Ключ Groq"), "console.groq.com/keys")
		if key != "" {
			s.env["GROQ_API_KEY"] = key
			fmt.Println(ok(tr("ключ записан")))
		}
	case 1:
		s.cfg.Transcribe.Source = "command"
		s.cfg.Transcribe.Cmd = []string{findAdapter("whisper-cpp.sh"), "{{audio}}", "{{language}}"}
		s.checkWhisper()
	case 2:
		s.cfg.Transcribe.Source = "captions"
		fmt.Println(dim(tr("  Текст возьмётся из субтитров Meet. Имена говорящих в нём уже есть.")))
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
		fmt.Println(warn(tr("не хватает: ") + strings.Join(missing, ", ")))
		fmt.Println(dim("  brew install whisper-cpp ffmpeg jq"))
	} else {
		fmt.Println(ok(tr("whisper-cli, ffmpeg и jq на месте")))
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
		fmt.Println(warn(tr("модели нет — без неё расшифровка не заработает")))
		fmt.Println(dim("  mkdir -p " + dir))
		fmt.Println(dim("  curl -L -o " + dir + "/ggml-large-v3-q5_0.bin \\"))
		fmt.Println(dim("    https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-large-v3-q5_0.bin"))
		fmt.Println(dim(tr("  и отдельно VAD — 868 КБ, но ускоряет в восемь раз:")))
		fmt.Println(dim("  curl -L -o " + dir + "/ggml-silero-v5.1.2.bin \\"))
		fmt.Println(dim("    https://huggingface.co/ggml-org/whisper-vad/resolve/main/ggml-silero-v5.1.2.bin"))
	} else if strings.Contains(found, "turbo") {
		// turbo не просто хуже — она подменяет незнакомые слова похожими
		// знакомыми и зацикливается. Ошибка получается связной и правдоподобной,
		// в follow-up её уже не отличить от сказанного.
		fmt.Println(warn(tr("модель ") + found + tr(" — она путает названия и зацикливается")))
		fmt.Println(dim(tr("  на записи созвона turbo превратила Plaud в Cloud AI и повторила")))
		fmt.Println(dim(tr("  его семнадцать раз подряд. Возьми large-v3-q5_0 — тот же размер:")))
		fmt.Println(dim("  curl -L -o " + dir + "/ggml-large-v3-q5_0.bin \\"))
		fmt.Println(dim("    https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-large-v3-q5_0.bin"))
	} else {
		fmt.Println(ok(tr("модель ") + found))
	}
}

func (s *setupState) askClaude(ctx context.Context) error {
	section(tr("Claude — он собирает follow-up"))

	// Два пути, и выбор между ними не про цену, а про то, кто запускает.
	// Подписка идёт через `claude -p` — штатный неинтерактивный режим Claude
	// Code. Отдельно платить не надо, но нужен выполненный вход, который делает
	// человек: на сервере, куда никто не заходит, это не работает. Поэтому
	// подписку предлагаем первой только если CLI уже готов.
	cliOK, cliNote := claudeCLIAvailable()
	opts := []string{
		tr("Подписка Claude ") + dim(tr("через claude -p, отдельный ключ не нужен")),
		tr("Ключ API ") + dim(tr("нужен для сервера: работает без входа человеком")),
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
	if s.choose(tr("Чем платить за follow-up"), opts, def) == 0 {
		s.cfg.Claude.Via = "cli"
		if !cliOK {
			fmt.Println(warn(tr("CLI пока не готов — steno скажет об этом при первом созвоне")))
			fmt.Println(dim(tr("  поставь Claude Code и войди: claude auth login")))
		}
		// Потолок на запрос: у подписки нет счёта, который придёт в конце
		// месяца, но есть лимит, который можно выбрать одним циклом.
		if s.cfg.Claude.MaxUSDPerCall == 0 {
			s.cfg.Claude.MaxUSDPerCall = 2
		}
		fmt.Println(dim(tr("  Потолок на один follow-up: $") +
			strconv.FormatFloat(s.cfg.Claude.MaxUSDPerCall, 'f', -1, 64) +
			tr(" — меняется в claude.max_usd_per_call")))
	} else {
		s.cfg.Claude.Via = "api"
		key := s.askSecret(tr("Ключ Anthropic"), "console.anthropic.com → API keys")
		if key != "" {
			s.env["ANTHROPIC_API_KEY"] = key
		}
	}

	i := s.choose(tr("Модель"), []string{
		"claude-opus-5 " + dim(tr("умнее, около $0.40 за часовой созвон")),
		"claude-sonnet-5 " + dim(tr("дешевле в два с половиной раза, для планёрок обычно хватает")),
	}, 0)
	s.cfg.Claude.Model = []string{"claude-opus-5", "claude-sonnet-5"}[i]
	fmt.Println(dim(tr("  Усилие: ") + s.cfg.Claude.Effort + tr(". Выше поднимать смысла нет: на замерах")))
	fmt.Println(dim(tr("  усилие не добавило ни одной задачи, только время и расход.")))
	return nil
}

func (s *setupState) askSources(ctx context.Context) error {
	section(tr("Как бот попадает в звонок"))
	fmt.Println(dim(tr("  Можно включить несколько. Календарь закрывает запланированное,")))
	fmt.Println(dim(tr("  остальные — внезапное.")))
	fmt.Println()

	if s.confirm(tr("Ходить по календарям команды?"), false) {
		s.cfg.Calendar.Enabled = true
		fmt.Println(dim(tr("  Нужны почтовые адреса тех, чьи встречи бот должен видеть.")))
		fmt.Println(dim(tr("  Свой — чтобы ходить на собственные созвоны. Чужой сработает,")))
		fmt.Println(dim(tr("  только если этот человек открыл боту доступ к своему календарю.")))
		s.cfg.Calendar.Calendars = commaList(s.ask(tr("Чьи календари (почта, через запятую)"), ""))
		s.askGoogleAccess()
	}
	if s.confirm(tr("Приходить, когда бота добавляют в звонок по почте?"), false) {
		s.cfg.Gmail.Enabled = true
		s.cfg.Gmail.Account = s.ask(tr("Почта аккаунта бота"), "")
		s.askGoogleAccess()
	}
	if s.confirm(tr("Принимать ссылки в Telegram?"), true) {
		s.cfg.Telegram.Listen = true
		if tok := s.askSecret(tr("Токен бота Telegram"), "@BotFather"); tok != "" {
			s.env["TELEGRAM_BOT_TOKEN"] = tok
		}
		s.cfg.Telegram.ChatID = s.ask(tr("Из какого чата принимать (chat_id)"), "")
		if s.cfg.Telegram.ChatID != "" {
			s.cfg.Telegram.AllowedChats = []string{s.cfg.Telegram.ChatID}
		} else {
			fmt.Println(warn(tr("без chat_id сервис не запустит приём: принимать ссылки от кого угодно нельзя")))
		}
	}
	return nil
}

func (s *setupState) askTargets(ctx context.Context) error {
	section(tr("Куда складывать итоги"))
	fmt.Println(dim(tr("  Панель есть всегда — там архив, поиск и проекты. Остальное по желанию.")))
	fmt.Println()

	// Спрашиваем всегда, даже если приём в Telegram уже включён: включить
	// отправку молча, по одному лишь факту приёма, — значит не сказать
	// человеку, куда пойдут итоги его созвонов.
	if s.cfg.Telegram.ChatID != "" {
		if s.confirm(tr("Присылать follow-up в Telegram, в тот же чат?"), true) {
			s.cfg.Telegram.Enabled = true
		}
	} else if s.confirm(tr("Присылать follow-up в Telegram?"), false) {
		if tok := s.askSecret(tr("Токен бота Telegram"), "@BotFather"); tok != "" {
			s.env["TELEGRAM_BOT_TOKEN"] = tok
		}
		s.cfg.Telegram.ChatID = s.ask(tr("В какой чат (chat_id)"), "")
		s.cfg.Telegram.Enabled = s.cfg.Telegram.ChatID != ""
	}
	if s.confirm(tr("Публиковать в Google Docs?"), false) {
		s.cfg.GoogleDocs.Enabled = true
		s.askGoogleAccess()
		// От чужого имени умеет только ключ организации. По кнопке документы
		// создаются от того, кто её нажал, и спрашивать тут нечего.
		if s.cfg.GoogleDocs.CredentialsFile != "" {
			s.cfg.GoogleDocs.Subject = s.ask(tr("От чьего имени создавать документы"), "")
		}
		s.cfg.GoogleDocs.FolderID = s.ask(tr("Папка на Drive: хвост адреса после /folders/ (пусто — корень)"), "")
		s.cfg.GoogleDocs.ProjectDocs = s.confirm(tr("Вести отдельный документ на каждый проект?"), true)
	}
	if s.confirm(tr("Публиковать в Slack?"), false) {
		s.cfg.Slack.Enabled = true
		if tok := s.askSecret(tr("Токен бота Slack (xoxb-…)"), "api.slack.com/apps"); tok != "" {
			s.env["SLACK_BOT_TOKEN"] = tok
		}
		s.cfg.Slack.Channel = s.ask(tr("Канал"), tr("#созвоны"))
		s.cfg.Slack.DMOwners = s.confirm(tr("Писать в личку тем, на ком задача?"), true)
	}
	return nil
}

func (s *setupState) askPanel(ctx context.Context) error {
	section(tr("Панель"))
	s.cfg.Panel.Enabled = true
	s.cfg.Panel.Addr = s.ask(tr("Адрес"), "127.0.0.1:8422")
	pass := s.askSecret(tr("Пароль (общий на команду)"), "")
	if pass == "" {
		pass = randomPassword()
		fmt.Println(ok(tr("сгенерировал: ") + pass))
	}
	s.env["STENO_PANEL_PASSWORD"] = pass
	if !strings.HasPrefix(s.cfg.Panel.Addr, "127.0.0.1") &&
		!strings.HasPrefix(s.cfg.Panel.Addr, "localhost") {
		s.cfg.Panel.Secure = s.confirm(tr("Панель за HTTPS?"), true)
		if !s.cfg.Panel.Secure {
			fmt.Println(warn(tr("панель смотрит наружу без TLS — пароль и cookie пойдут открытым текстом")))
		}
	}
	return nil
}

// --- запись ------------------------------------------------------------------

func (s *setupState) write(configPath string) error {
	section(tr("Готово"))

	// Секреты кладём отдельным файлом с правами 0600 и не пускаем в конфиг:
	// конфиг хочется держать в репозитории, а токены — нет.
	envPath := filepath.Join(s.dir, ".env")
	if len(s.env) > 0 {
		var b strings.Builder
		b.WriteString(tr("# Секреты steno. Файл читается при запуске.\n"))
		b.WriteString(tr("# Не клади его в репозиторий: тут ключи, а не настройки.\n\n"))
		for _, k := range sortedKeys(s.env) {
			fmt.Fprintf(&b, "%s=%s\n", k, s.env[k])
		}
		if err := os.WriteFile(envPath, []byte(b.String()), 0o600); err != nil {
			return fmt.Errorf(tr("не записался %s: %w"), envPath, err)
		}
		fmt.Println(ok(envPath + "  " + dim(tr("права 0600, ")+strconv.Itoa(len(s.env))+tr(" секретов"))))
	}

	// Каталог мог поменяться на шаге «Где хранить»: конфиг кладём туда же, где
	// данные, а не туда, откуда запустили мастер.
	if !s.dirChosen {
		configPath = filepath.Join(s.dir, filepath.Base(configPath))
	}
	raw, err := marshalConfig(s.cfg)
	if err != nil {
		return err
	}
	if err := os.WriteFile(configPath, raw, 0o644); err != nil {
		return err
	}
	fmt.Println(ok(configPath))
	// Запоминаем, где настройка: иначе команды, набранные из другого каталога,
	// берут умолчания и ведут себя так, будто настройки не было.
	rememberConfigPath(configPath)

	// .gitignore рядом с секретами — чтобы они не уехали в первый же коммит.
	gi := filepath.Join(s.dir, ".gitignore")
	if _, err := os.Stat(gi); os.IsNotExist(err) && len(s.env) > 0 {
		_ = os.WriteFile(gi, []byte(".env\ndata/\n"), 0o644)
		fmt.Println(ok(gi + "  " + dim(tr("чтобы .env не уехал в репозиторий"))))
	}

	fmt.Println()
	fmt.Println(bold(tr("Дальше:")))
	// Путь целиком, а не имя файла: мастер мог завести каталог не там, откуда
	// его запустили, и «steno doctor -c steno.json» из другого места не сработает.
	fmt.Printf("  cd %s\n", s.dir)
	fmt.Printf("  steno doctor   %s\n", dim(tr("проверить, что всё на месте")))
	fmt.Printf("  steno serve    %s\n", dim(tr("запустить")))
	if s.cfg.Panel.Enabled {
		fmt.Printf("  http://%s%s\n", s.cfg.Panel.Addr, dim(tr("  — панель")))
	}
	fmt.Println()
	fmt.Println(dim(tr("Проверить на живом созвоне, ничего больше не настраивая:")))
	fmt.Printf("  steno join --no-followup --captions %s\n",
		dim(tr("https://meet.google.com/… или https://meet.jit.si/…")))
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
		fmt.Printf(tr("  Номер [%s]: "), dim(strconv.Itoa(def+1)))
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
	i := s.choose(tr("Как steno попадёт в Google?"), []string{
		tr("По кнопке в панели ") +
			dim(tr("человек соглашается один раз; steno видит ровно то, что видит он")),
		tr("Ключом организации ") +
			dim(tr("файл service-account: видит календари всех, нужен свой домен и админка")),
	}, 0)
	if i == 1 {
		s.cfg.GoogleDocs.CredentialsFile = s.askGoogleKey()
		return
	}
	// Адрес страницы, а не путь по меню: меню Google переставляет пункты
	// чаще, чем меняет адреса, и человек, который ищет «Credentials» глазами,
	// натыкается на переименованный раздел.
	fmt.Println(dim(tr("  Открой https://console.cloud.google.com/apis/credentials")))
	fmt.Println(dim(tr("  → Create credentials → OAuth client ID → тип Desktop app.")))
	fmt.Println(dim(tr("  Google покажет две строки — скопируй их сюда.")))
	s.cfg.Google.ClientID = s.ask("Client ID", "")
	if sec := s.askSecret("Client secret", ""); sec != "" {
		s.env["GOOGLE_CLIENT_SECRET"] = sec
	}
	if s.cfg.Google.ClientID == "" {
		fmt.Println(warn(tr("без этого кнопка в панели не появится")))
		return
	}
	fmt.Println(ok(tr("осталось нажать «Подключить Google» в настройках панели")))
}

func (s *setupState) askGoogleKey() string {
	fmt.Println(dim(tr("  Нужен ключ service-account с domain-wide delegation.")))
	fmt.Println(dim(tr("  Консоль Google → IAM → сервисные аккаунты → ключи → создать JSON.")))
	for {
		p := s.ask(tr("Путь к файлу ключа"), "")
		if p == "" {
			fmt.Println(warn(tr("без него календарь, почта и Google Docs не заработают")))
			return ""
		}
		p = expandHome(p)
		if c := checkGoogleKey(tr("ключ"), p); c.state == "ok" {
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

// Шаги нумеруются на ходу. Человек, отвечающий на седьмой вопрос подряд, не
// знает, седьмой он из восьми или из тридцати, и это единственное, что отличает
// «сейчас закончим» от «конца не видно».
var (
	stepNo    int
	stepTotal int
)

func section(s string) {
	stepNo++
	fmt.Println()
	if stepTotal > 0 {
		fmt.Printf("%s  %s\n", bold("· "+s),
			dim(fmt.Sprintf(tr("шаг %d из %d"), stepNo, stepTotal)))
		return
	}
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

// findAdapter ищет адаптер расшифровки и возвращает путь, который сработает из
// любого каталога.
//
// Раньше в конфиг писалось «./adapters/whisper-cpp.sh» — относительный путь,
// живущий только внутри клона репозитория. У поставившего через brew файла по
// этому пути нет вовсе, и распознавание молча оказывалось неработающим: doctor
// говорил «не запускается», а откуда взять — нет.
//
// Порядок понятный: рядом с текущим каталогом (разработка), рядом с самим
// бинарником, и в share пакета — туда их кладёт формула Homebrew.
func findAdapter(name string) string {
	var roots []string
	if wd, err := os.Getwd(); err == nil {
		roots = append(roots, filepath.Join(wd, "adapters"))
	}
	if exe, err := os.Executable(); err == nil {
		if exe, err = filepath.EvalSymlinks(exe); err == nil {
			dir := filepath.Dir(exe)
			roots = append(roots,
				filepath.Join(dir, "adapters"),
				// Cellar/steno/<версия>/bin/steno → .../share/steno/adapters
				filepath.Join(dir, "..", "share", "steno", "adapters"),
			)
		}
	}
	if p := os.Getenv("HOMEBREW_PREFIX"); p != "" {
		roots = append(roots, filepath.Join(p, "share", "steno", "adapters"))
	}
	roots = append(roots, "/opt/homebrew/share/steno/adapters", "/usr/local/share/steno/adapters")

	for _, r := range roots {
		p := filepath.Join(r, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			if abs, err := filepath.Abs(p); err == nil {
				return abs
			}
			return p
		}
	}
	// Не нашли — оставляем прежний вид, чтобы doctor сказал об этом словами, а
	// конфиг остался читаемым и правился руками.
	return filepath.Join("adapters", name)
}
