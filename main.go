package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const usage = `steno — заметки и follow-up с созвонов.

  steno setup                настроить всё: спросит по одному и проверит
  steno serve                слушать все источники и ходить на созвоны
  steno join <meet-url>      зайти в созвон, записать и разослать follow-up
                             --record-only  только запись
                             --no-followup  запись и расшифровка, без Claude
                             --captions     текст из субтитров Meet
  steno process <id>         расшифровать и разослать уже записанный созвон
  steno publish <id>         разослать готовый follow-up ещё раз
  steno show <id>            показать follow-up
  steno transcript <id>      показать расшифровку
  steno list                 последние созвоны
  steno prune                удалить старые записи по срокам из конфига
  steno doctor               проверить, чего не хватает для запуска
  steno cost [дней]          сколько потрачено на follow-up
  steno projects [проект]    что открыто по проектам
  steno projects add <имя>   завести проект
                             --repo <url>   репозиторий (можно несколько)
                             --path <dir>   каталог с кодом на этой машине
                             --url <адрес>  сайт или документ
                             --about "…"    одна строка, что это за проект
                             --alias a,b    как называют вслух
  steno projects rm <имя>    убрать проект из реестра
  steno context [проект]     собрать справки о проектах по коду и сайтам
  steno bot --url <u>        сам бот; запускается внутри контейнера

Общие флаги:
  -c <path>                  путь к steno.json
`

func main() {
	log.SetFlags(log.Ltime)
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch cmd {
	case "setup":
		err = cmdSetup(ctx, args)
	case "serve":
		err = cmdServe(ctx, args)
	case "join":
		err = cmdJoin(ctx, args)
	case "process":
		err = cmdProcess(ctx, args)
	case "publish":
		err = cmdPublish(ctx, args)
	case "show":
		err = cmdShow(args)
	case "transcript":
		err = cmdTranscript(args)
	case "list":
		err = cmdList(args)
	case "doctor":
		err = cmdDoctor(args)
	case "cost":
		err = cmdCost(args)
	case "projects":
		err = cmdProjects(args)
	case "context":
		err = cmdContext(ctx, args)
	case "prune":
		err = cmdPrune(args)
	case "bot":
		err = cmdBot(ctx, args)
	case "-h", "--help", "help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "неизвестная команда %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		log.Fatalf("ошибка: %v", err)
	}
}

const defaultConfigPath = "steno.json"

func newFlagSet(name string) *flag.FlagSet {
	return flag.NewFlagSet(name, flag.ExitOnError)
}

// parseArgs разбирает флаги, где бы они ни стояли. Стандартный flag
// останавливается на первом не-флаге, поэтому `steno process <id> --no-followup`
// молча брал конфиг по умолчанию и падал с невнятным «no rows in result set».
// Порядок аргументов — не то, на чём человек должен спотыкаться.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return positional, nil
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}

func setupFlags(fs *flag.FlagSet) *string {
	return fs.String("c", envOr("STENO_CONFIG", defaultConfigPath), "путь к конфигу")
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func open(configPath string) (*Config, *Store, error) {
	// Секреты лежат в .env рядом с конфигом. Уже заданное окружение главнее:
	// в проде переменные приходят от systemd или docker.
	if abs, err := filepath.Abs(configPath); err == nil {
		_ = loadDotEnv(filepath.Join(filepath.Dir(abs), ".env"))
	}

	if _, err := os.Stat(configPath); err != nil {
		// Умолчания подставляем, только если человек не называл файл сам.
		// Иначе опечатка в -c тихо запускала бы сервис без единого адресата:
		// созвон записан, токены Claude потрачены, follow-up никуда не ушёл.
		if configPath != defaultConfigPath {
			return nil, nil, fmt.Errorf("конфиг %s: %w", configPath, err)
		}
		configPath = ""
	}
	cfg, err := loadConfig(configPath)
	if err != nil {
		return nil, nil, err
	}
	st, err := openStore(cfg.DataDir)
	if err != nil {
		return nil, nil, err
	}
	resizeTranscribeQueue(cfg.Transcribe.MaxConcurrent)
	// Адреса своих серверов Jitsi живут в selectors.json — там же, где
	// остальная вёрстка, и оттуда же едут внутрь контейнера бота.
	applyPlatformConfig(cfg, log.Default())
	if n, err := importProjects(st, cfg); err != nil {
		st.Close()
		return nil, nil, fmt.Errorf("перенос проектов из конфига: %w", err)
	} else if n > 0 {
		log.Printf("перенёс %d проектов из конфига в базу — дальше правь их в панели", n)
	}
	if n, err := importChannels(st, cfg); err != nil {
		st.Close()
		return nil, nil, fmt.Errorf("перенос каналов из конфига: %w", err)
	} else if n > 0 {
		log.Printf("перенёс %d каналов из конфига в базу — дальше правь их в панели", n)
	}
	// База главнее конфига: канал, выключенный в панели, должен остаться
	// выключенным и после перезапуска.
	if err := applyChannels(st, cfg); err != nil {
		st.Close()
		return nil, nil, fmt.Errorf("настройки каналов: %w", err)
	}
	return cfg, st, nil
}

func newID(t time.Time) string {
	var b [2]byte
	_, _ = rand.Read(b[:])
	return t.Format("2006-01-02-1504") + "-" + hex.EncodeToString(b[:])
}

// --- join ------------------------------------------------------------------

func cmdJoin(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("join", flag.ExitOnError)
	cfgPath := setupFlags(fs)
	title := fs.String("title", "", "название встречи")
	invitees := fs.String("invitees", "", "приглашённые через запятую")
	local := fs.Bool("local", false, "запустить бота прямо здесь, без docker (нужны Chromium, ffmpeg и pulseaudio)")
	noPublish := fs.Bool("no-publish", false, "только записать и расшифровать")
	recordOnly := fs.Bool("record-only", false,
		"только записать: ни расшифровки, ни follow-up, ни рассылки")
	noFollowup := fs.Bool("no-followup", false,
		"записать и расшифровать, показать расшифровку и остановиться (Claude не нужен)")
	useCaptions := fs.Bool("captions", false,
		"взять текст из субтитров Meet вместо whisper")
	rest, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(rest) < 1 {
		return fmt.Errorf("нужна ссылка на созвон (%s): steno join https://meet.google.com/abc-defg-hij",
			supportedPlatforms())
	}
	cfg, st, err := open(*cfgPath)
	if err != nil {
		return err
	}
	defer st.Close()

	// Ссылку приводим к канонической здесь же — уже после open(), потому что
	// адреса своих серверов Jitsi приезжают из конфига. Без этого `steno join
	// meet.google.com/abc-defg-hij` уходил в браузер как есть, без схемы, а
	// ссылка чужой площадки доезжала до запуска Chromium и падала там — вместо
	// того чтобы сразу получить объяснение.
	meetURL := findMeetURL(rest[0])
	if meetURL == "" {
		return meetingLinkError(rest[0])
	}

	m := &Meeting{
		ID:        newID(time.Now()),
		Title:     *title,
		MeetURL:   meetURL,
		StartedAt: time.Now(),
		Status:    "recording",
	}
	if *invitees != "" {
		for _, s := range strings.Split(*invitees, ",") {
			if s = strings.TrimSpace(s); s != "" {
				m.Invitees = append(m.Invitees, s)
			}
		}
	}
	if *local {
		cfg.Bot.Local = true
	}
	if *noPublish || *recordOnly || *noFollowup {
		cfg.noPublish = true
	}
	if *useCaptions {
		cfg.Transcribe.Source = "captions"
	}
	if *recordOnly {
		return recordOnce(ctx, cfg, st, m)
	}
	if *noFollowup {
		if err := recordMeeting(ctx, cfg, st, m); err != nil {
			return err
		}
		return transcribeOnly(ctx, cfg, st, m.ID)
	}
	return recordAndProcess(ctx, cfg, st, m)
}

// recordOnce доводит созвон только до записи. Первая проверка на живом звонке
// упирается ровно в неё: зашёл ли бот, впустили ли его, есть ли звук и попали
// ли в субтитры имена. Расшифровка и follow-up к этому вопросу отношения не
// имеют, а требуют ключей и установленного whisper.
func recordOnce(ctx context.Context, cfg *Config, st *Store, m *Meeting) error {
	if err := recordMeeting(ctx, cfg, st, m); err != nil {
		return err
	}
	utts, err := readUtterances(m.CaptionsPath)
	if err != nil {
		log.Printf("субтитры не прочитались: %v", err)
	}
	named := map[string]int{}
	for _, u := range utts {
		named[u.Speaker]++
	}
	size := int64(0)
	if st, err := os.Stat(m.AudioPath); err == nil {
		size = st.Size()
	}
	log.Printf("готово: аудио %.1f МБ, реплик в субтитрах %d, говорящих %d",
		float64(size)/(1<<20), len(utts), len(named))
	for who, n := range named {
		log.Printf("  %s — %d реплик", orDash(who), n)
	}
	if len(utts) == 0 {
		// Что именно случилось, бот уже сказал строкой «субтитры: …» — она
		// различает «площадка их не отдаёт» и «должны были быть, но не
		// включились». Повторять здесь догадку про Meet нельзя: у Jitsi
		// пустые субтитры это норма, а не поломка вёрстки.
		log.Printf("субтитров нет — расшифровка пойдёт по звуку, без имён говорящих; " +
			"причину смотри выше, в строке бота «субтитры: …»")
	}
	return nil
}

// transcribeOnly доводит созвон до расшифровки и печатает её. Ступенька между
// «только записал» и «сделал follow-up»: проверить, что бот зашёл и текст
// получился, можно без ключа Claude и без единой копейки.
func transcribeOnly(ctx context.Context, cfg *Config, st *Store, id string) error {
	m, err := st.Meeting(id)
	if err != nil {
		return err
	}
	segs, err := transcribeMeeting(ctx, cfg, m)
	if err != nil {
		_ = st.SetStatus(id, "failed", err.Error())
		return err
	}
	if err := checkTranscript(segs); err != nil {
		// Платить Claude за расшифровку из одной тишины незачем, а главное —
		// молчаливый пустой follow-up выглядит как настоящий.
		_ = st.SetStatus(id, "failed", err.Error())
		return err
	}
	if err := st.SaveSegments(id, segs); err != nil {
		_ = st.SetStatus(id, "failed", err.Error())
		return err
	}
	_ = st.SetStatus(id, "transcribed", "")
	log.Printf("реплик: %d, из них с именем: %d", len(segs), namedCount(segs))
	fmt.Println()
	fmt.Print(renderTranscript(segs))
	fmt.Printf("\nfollow-up: steno process %s (нужен ключ Claude)\n", id)
	return nil
}

// recordAndProcess — весь путь одного созвона: завести бота, дождаться конца,
// расшифровать, собрать follow-up, разослать. Одна и та же дорога у `steno
// join` и у календарного watcher'а.
func recordAndProcess(ctx context.Context, cfg *Config, st *Store, m *Meeting) error {
	if err := recordMeeting(ctx, cfg, st, m); err != nil {
		return err
	}
	return processMeeting(ctx, cfg, st, m.ID, false)
}

// recordMeeting — только запись: завести бота, дождаться конца, сохранить.
func recordMeeting(ctx context.Context, cfg *Config, st *Store, m *Meeting) error {
	outDir := st.RecordingDir(m.ID)
	m.AudioPath = filepath.Join(outDir, "audio.ogg")
	m.CaptionsPath = filepath.Join(outDir, "captions.jsonl")
	if err := st.CreateMeeting(m); err != nil {
		return err
	}
	log.Printf("созвон %s", m.ID)

	var (
		res *BotResult
		err error
	)
	if cfg.Bot.Local {
		res, err = runBotHere(ctx, cfg, m.MeetURL, outDir)
	} else {
		res, err = runBotInDocker(ctx, cfg, m.ID, m.MeetURL, outDir)
	}
	if err != nil {
		_ = st.FinishMeeting(m.ID, time.Time{}, time.Now(), nil, "failed", err.Error(), "")
		return err
	}
	if err := st.FinishMeeting(m.ID, res.Started, res.Ended, res.Participants,
		"recorded", "", res.LeftReason); err != nil {
		return err
	}
	log.Printf("записано: %s (%s)", m.AudioPath, res.LeftReason)
	return nil
}

func runBotHere(ctx context.Context, cfg *Config, meetURL, outDir string) (*BotResult, error) {
	sel, err := loadSelectors(cfg.Bot.Selectors)
	if err != nil {
		return nil, err
	}
	return RunBot(ctx, BotOptions{
		MeetURL:          meetURL,
		DisplayName:      cfg.Bot.DisplayName,
		OutDir:           outDir,
		AudioSource:      envOr("STENO_AUDIO_SOURCE", "meet_out.monitor"),
		Selectors:        sel,
		AdmissionTimeout: cfg.Bot.AdmissionTimeout.D(),
		EmptyFor:         cfg.Bot.EmptyFor.D(),
		MaxDuration:      cfg.Bot.MaxDuration.D(),
		CaptionLanguage:  cfg.Bot.CaptionLanguage,
		UserDataDir:      envOr("STENO_CHROME_PROFILE", ""),
		Log:              log.New(os.Stderr, "bot: ", log.Ltime),
	})
}

// runBotInDocker поднимает контейнер с Chromium и PulseAudio, отдаёт ему папку
// созвона и ждёт. Внутри крутится тот же бинарник в роли `steno bot`.
func runBotInDocker(ctx context.Context, cfg *Config, meetingID, meetURL, outDir string) (*BotResult, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	// Внутри контейнера бот работает под uid 1000 (PulseAudio отказывается
	// стартовать под root). Если сервис на хосте — другой пользователь, папка
	// созвона окажется ему недоступна на запись, и первым же упадёт лог ffmpeg.
	if err := os.Chmod(outDir, 0o777); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(outDir)
	if err != nil {
		return nil, err
	}
	// Имя и метка нужны, чтобы контейнер можно было найти и погасить снаружи:
	//   docker kill $(docker ps -q -f label=steno)
	name := "steno-" + meetingID
	args := []string{
		"run", "--rm",
		"--name", name,
		"--label", "steno=1",
		"-v", abs + ":/out",
		"--shm-size=1g",
	}
	if p := os.Getenv("STENO_CHROME_PROFILE"); p != "" {
		args = append(args, "-v", p+":/profile", "-e", "STENO_CHROME_PROFILE=/profile")
	}
	// Свой selectors.json — обещанный способ починить бота без пересборки
	// образа. Без проброса внутрь контейнера правка файла ничего не меняла.
	botArgs := []string{
		"steno", "bot",
		"--url", meetURL,
		"--out", "/out",
		"--name", cfg.Bot.DisplayName,
		"--admission", cfg.Bot.AdmissionTimeout.D().String(),
		"--empty-for", cfg.Bot.EmptyFor.D().String(),
		"--max", cfg.Bot.MaxDuration.D().String(),
	}
	if cfg.Bot.CaptionLanguage != "" {
		botArgs = append(botArgs, "--caption-language", cfg.Bot.CaptionLanguage)
	}
	if os.Getenv("STENO_DEBUG_CAPTIONS") != "" {
		botArgs = append(botArgs, "--debug-captions")
	}
	if cfg.Bot.Selectors != "" {
		selAbs, err := filepath.Abs(cfg.Bot.Selectors)
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(selAbs); err != nil {
			return nil, fmt.Errorf("bot.selectors: %w", err)
		}
		args = append(args, "-v", selAbs+":/selectors.json:ro")
		botArgs = append(botArgs, "--selectors", "/selectors.json")
	}
	args = append(args, cfg.Bot.Image)
	args = append(args, botArgs...)

	cmd := exec.CommandContext(ctx, "docker", args...)
	// docker run — это клиент демона. Убить его недостаточно: контейнер
	// останется работать и будет держать процессор и гигабайт shm, пока его
	// не погасят руками.
	cmd.Cancel = func() error {
		_ = exec.Command("docker", "kill", name).Run()
		return cmd.Process.Kill()
	}
	cmd.WaitDelay = 20 * time.Second
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	log.Printf("запускаю бота в контейнере %s", name)
	if err := cmd.Run(); err != nil {
		// Контекст мог закончиться уже после того, как бот дописал результат.
		if _, statErr := os.Stat(filepath.Join(outDir, "result.json")); statErr != nil {
			_ = exec.Command("docker", "kill", name).Run()
			return nil, fmt.Errorf("контейнер бота: %w", err)
		}
	}
	return readBotResult(outDir)
}

func readBotResult(outDir string) (*BotResult, error) {
	b, err := os.ReadFile(filepath.Join(outDir, "result.json"))
	if err != nil {
		return nil, fmt.Errorf("бот не оставил result.json: %w", err)
	}
	var res BotResult
	if err := json.Unmarshal(b, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// --- bot (внутри контейнера) ----------------------------------------------

func cmdBot(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("bot", flag.ExitOnError)
	meetURL := fs.String("url", "", "ссылка на созвон")
	outDir := fs.String("out", "/out", "куда писать")
	name := fs.String("name", "Steno · идёт запись", "имя бота в списке участников")
	selectors := fs.String("selectors", envOr("STENO_SELECTORS", ""), "путь к selectors.json")
	source := fs.String("source", envOr("STENO_AUDIO_SOURCE", "meet_out.monitor"), "источник PulseAudio")
	admission := fs.Duration("admission", 5*time.Minute, "сколько ждать, пока впустят")
	emptyFor := fs.Duration("empty-for", 2*time.Minute, "уйти, если остался один дольше этого")
	maxDur := fs.Duration("max", 4*time.Hour, "потолок длительности")
	headless := fs.Bool("headless", false, "без Xvfb (Meet работает хуже)")
	debugCaptions := fs.Bool("debug-captions", false,
		"печатать, что на странице похоже на субтитры")
	captionLang := fs.String("caption-language", "",
		"язык субтитров Meet — он же язык распознавания")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	if *meetURL == "" {
		return fmt.Errorf("нужен --url")
	}
	sel, err := loadSelectors(*selectors)
	if err != nil {
		return err
	}

	// Потолок на весь запуск, а не только на цикл записи. Всё, что до входа в
	// звонок — навигация, комната ожидания, ожидание впуска, — раньше не было
	// ограничено ничем: зависший Chromium оставлял контейнер жить вечно, даже
	// когда --max стоял в миллисекунду.
	hard := *maxDur + *admission + 3*time.Minute
	ctx, cancelHard := context.WithTimeout(ctx, hard)
	defer cancelHard()

	res, err := RunBot(ctx, BotOptions{
		MeetURL:          *meetURL,
		DisplayName:      *name,
		OutDir:           *outDir,
		AudioSource:      *source,
		Selectors:        sel,
		AdmissionTimeout: *admission,
		EmptyFor:         *emptyFor,
		MaxDuration:      *maxDur,
		UserDataDir:      envOr("STENO_CHROME_PROFILE", ""),
		Headless:         *headless,
		DebugCaptions:    *debugCaptions,
		CaptionLanguage:  *captionLang,
		Log:              log.New(os.Stderr, "bot: ", log.Ltime),
	})
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(res, "", "  ")
	return os.WriteFile(filepath.Join(*outDir, "result.json"), b, 0o644)
}

// --- process / publish / show / list --------------------------------------

func cmdProcess(ctx context.Context, args []string) error {
	fs := newFlagSet("process")
	cfgPath := setupFlags(fs)
	noPublish := fs.Bool("no-publish", false, "не публиковать")
	noFollowup := fs.Bool("no-followup", false,
		"только расшифровать и показать — Claude не нужен")
	useCaptions := fs.Bool("captions", false,
		"взять текст из субтитров Meet вместо whisper")
	rest, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(rest) < 1 {
		return fmt.Errorf("нужен id созвона")
	}
	cfg, st, err := open(*cfgPath)
	if err != nil {
		return err
	}
	defer st.Close()
	if *useCaptions {
		cfg.Transcribe.Source = "captions"
	}
	if *noFollowup {
		return transcribeOnly(ctx, cfg, st, rest[0])
	}
	return processMeeting(ctx, cfg, st, rest[0], *noPublish)
}

func processMeeting(ctx context.Context, cfg *Config, st *Store, id string, noPublish bool) error {
	m, err := st.Meeting(id)
	if err != nil {
		return fmt.Errorf("созвон %s: %w", id, err)
	}

	if cfg.Transcribe.Source != "captions" && m.AudioPath == "" {
		// Запись удалена по сроку хранения. Расшифровка и follow-up при этом
		// целы, и портить им статус на «сорвался» незачем.
		return fmt.Errorf("запись созвона %s удалена по сроку хранения — расшифровывать нечего", id)
	}
	if cfg.Transcribe.Source != "captions" {
		if _, err := os.Stat(m.AudioPath); err != nil {
			return fmt.Errorf("запись %s недоступна: %w", m.AudioPath, err)
		}
	}
	segs, err := transcribeMeeting(ctx, cfg, m)
	if err != nil {
		_ = st.SetStatus(id, "failed", err.Error())
		return err
	}
	if err := st.SaveSegments(id, segs); err != nil {
		// Молча вернуться нельзя: созвон остался бы в статусе «записан», а это
		// спокойный статус, который никто не перепроверяет и никто не
		// перезапускает.
		_ = st.SetStatus(id, "failed", err.Error())
		return err
	}
	_ = st.SetStatus(id, "transcribed", "")
	log.Printf("реплик: %d, из них с именем: %d", len(segs), namedCount(segs))

	if _, how, err := claudeClient(cfg); err == nil {
		log.Printf("делаю follow-up (%s, доступ: %s)", cfg.Claude.Model, how)
	} else {
		log.Printf("делаю follow-up (%s)", cfg.Claude.Model)
	}
	// Модель должна видеть, что уже висит открытым: иначе каждый созвон
	// заводит копии тех же задач, и состояние проекта тонет в дублях.
	open, err := st.OpenItems("")
	if err != nil {
		log.Printf("не прочитал открытые пункты: %v", err)
	}
	projects := activeProjects(st, cfg)
	f, spend, err := makeFollowup(ctx, cfg, m, segs, projects,
		renderPrimers(st, projects), renderOpenItems(open))
	if err != nil {
		_ = st.SetStatus(id, "failed", err.Error())
		return err
	}
	log.Printf("расход: %s", spend)
	// Порядок важен: расход дописывается в строку follow-up, а создаёт её
	// SaveFollowup. Наоборот UPDATE не находил строки и молча терял расход —
	// на повторном запуске всё сходилось, на первом `steno cost` показывал ноль.
	if err := st.SaveFollowup(id, cfg.Claude.Model, f); err != nil {
		_ = st.SetStatus(id, "failed", err.Error())
		return err
	}
	if err := st.SaveSpend(id, spend); err != nil {
		log.Printf("не записал расход: %v", err)
	}
	_ = st.SetStatus(id, "summarized", "")
	log.Printf("задач: %d, решений: %d, открытых вопросов: %d",
		len(f.ActionItems), len(f.Decisions), len(f.OpenQuestions))
	if addedN, closedN, err := applyFollowup(st, projects, id, f); err != nil {
		log.Printf("состояние проектов: %v", err)
	} else if addedN > 0 || closedN > 0 {
		log.Printf("по проектам: добавлено %d, закрыто %d", addedN, closedN)
	}
	publishProjectDocs(ctx, cfg, st, f, id, log.New(os.Stderr, "", log.Ltime))

	if noPublish {
		return nil
	}
	errs := publishAll(ctx, cfg, st, m, f, segs, log.New(os.Stderr, "", log.Ltime))
	if len(errs) > 0 {
		msg := errors.Join(errs...).Error()
		_ = st.SetStatus(id, "publish_failed", msg)
		return fmt.Errorf("follow-up сделан, но не разослан: %s", msg)
	}
	return st.SetStatus(id, "published", "")
}

// transcribeMeeting превращает записанный созвон в реплики с именами — из
// whisper или прямо из субтитров Meet, смотря что настроено.
func transcribeMeeting(ctx context.Context, cfg *Config, m *Meeting) ([]Segment, error) {
	if cfg.Transcribe.Source == "captions" {
		log.Printf("беру текст из субтитров Meet (%s)", m.CaptionsPath)
		return segmentsFromCaptions(m.CaptionsPath)
	}
	log.Printf("расшифровываю %s", m.AudioPath)
	segs, notes, err := runTranscriber(ctx, cfg, m.AudioPath)
	if err != nil {
		return nil, err
	}
	for _, line := range lastLines(notes, 4) {
		log.Printf("  %s", line)
	}
	utts, uerr := readUtterances(m.CaptionsPath)
	if uerr != nil {
		log.Printf("субтитры не прочитались (%v) — расшифровка будет без имён", uerr)
	}
	return alignSpeakers(segs, utts), nil
}

func namedCount(segs []Segment) int {
	n := 0
	for _, s := range segs {
		if s.Speaker != "" {
			n++
		}
	}
	return n
}

func cmdPublish(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("publish", flag.ExitOnError)
	cfgPath := setupFlags(fs)
	rest, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(rest) < 1 {
		return fmt.Errorf("нужен id созвона")
	}
	cfg, st, err := open(*cfgPath)
	if err != nil {
		return err
	}
	defer st.Close()
	id := rest[0]
	m, err := st.Meeting(id)
	if err != nil {
		return err
	}
	f, err := st.Followup(id)
	if err != nil {
		return fmt.Errorf("follow-up ещё не сделан — сначала steno process %s", id)
	}
	segs, err := st.Segments(id)
	if err != nil {
		// Молча опубликовать документ без расшифровки — хуже, чем не
		// опубликовать: снаружи он выглядит полным.
		return fmt.Errorf("расшифровка %s: %w", id, err)
	}
	if errs := publishAll(ctx, cfg, st, m, f, segs, log.New(os.Stderr, "", log.Ltime)); len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func cmdShow(args []string) error {
	fs := flag.NewFlagSet("show", flag.ExitOnError)
	cfgPath := setupFlags(fs)
	asJSON := fs.Bool("json", false, "выдать JSON")
	rest, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(rest) < 1 {
		return fmt.Errorf("нужен id созвона")
	}
	_, st, err := open(*cfgPath)
	if err != nil {
		return err
	}
	defer st.Close()
	id := rest[0]
	m, err := st.Meeting(id)
	if err != nil {
		return err
	}
	f, err := st.Followup(id)
	if err != nil {
		return fmt.Errorf("follow-up для %s ещё нет", id)
	}
	if *asJSON {
		b, _ := json.MarshalIndent(f, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	fmt.Println(renderPlain(m, f))
	if links, _ := st.Publications(id); len(links) > 0 {
		fmt.Println()
		for target, url := range links {
			fmt.Printf("%-12s %s\n", target, url)
		}
	}
	return nil
}

func cmdContext(ctx context.Context, args []string) error {
	fs := newFlagSet("context")
	cfgPath := setupFlags(fs)
	force := fs.Bool("force", false, "пересобрать, даже если материал не менялся")
	show := fs.Bool("show", false, "показать готовые справки, не пересобирая")
	rest, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	cfg, st, err := open(*cfgPath)
	if err != nil {
		return err
	}
	defer st.Close()
	projects := activeProjects(st, cfg)
	if len(projects) == 0 {
		return fmt.Errorf("нет ни одного проекта — заведи в панели или в конфиге")
	}

	only := rest[0]
	var total float64
	for _, p := range projects {
		if only != "" && !strings.EqualFold(p.Name, only) {
			continue
		}
		if *show {
			c, err := st.ProjectContext(p.Name)
			if err != nil {
				fmt.Printf("\n%s — справки нет\n", p.Name)
				continue
			}
			fmt.Printf("\n%s — собрана %s\n\n%s\n", p.Name,
				c.BuiltAt.Format("02.01.2006 15:04"), c.Primer)
			continue
		}
		if len(p.Sources) == 0 {
			log.Printf("%s: источников нет — пропускаю", p.Name)
			continue
		}

		log.Printf("%s: читаю источники", p.Name)
		material, fp, err := gatherSources(ctx, cfg.DataDir, p)
		if err != nil {
			log.Printf("%s: %v", p.Name, err)
			continue
		}
		// Материал не менялся — незачем платить за ту же справку снова.
		if !*force {
			if c, err := st.ProjectContext(p.Name); err == nil && c.Fingerprint == fp {
				log.Printf("%s: материал тот же, справка от %s",
					p.Name, c.BuiltAt.Format("02.01.2006"))
				continue
			}
		}

		log.Printf("%s: собираю справку (%d символов материала)", p.Name, len([]rune(material)))
		primer, spend, err := buildPrimer(ctx, cfg, p, material)
		if err != nil {
			log.Printf("%s: %v", p.Name, err)
			continue
		}
		total += spend.USD
		if err := st.SaveProjectContext(ProjectContext{
			Project: p.Name, Primer: primer, Fingerprint: fp,
			Sources: sourcesSummary(p),
		}); err != nil {
			return err
		}
		log.Printf("%s: готово, %s", p.Name, spend)
	}
	if total > 0 {
		log.Printf("всего на справки: $%.3f", total)
	}
	return nil
}

func sourcesSummary(p Project) string {
	var out []string
	for _, s := range p.Sources {
		out = append(out, s.Kind+":"+s.Value)
	}
	return strings.Join(out, ", ")
}

func cmdProjects(args []string) error {
	// Завести проект можно было только панелью или правкой конфига, хотя
	// остальное в steno делается и тем и другим. Нашлось это прогоном с нуля:
	// `steno context` на свежей установке отправлял «заведи в панели», то есть
	// человек, ставивший всё из терминала, упирался в веб-интерфейс.
	if len(args) > 0 {
		switch args[0] {
		case "add":
			return cmdProjectAdd(args[1:])
		case "rm":
			return cmdProjectRm(args[1:])
		}
	}

	fs := newFlagSet("projects")
	cfgPath := setupFlags(fs)
	rest, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	_, st, err := open(*cfgPath)
	if err != nil {
		return err
	}
	defer st.Close()

	// Порядок важен: без проверки `rest[0]` вычислялся раньше неё, и `steno
	// projects` без аргументов — то есть ровно та форма, что напечатана в
	// справке, — падал паникой на любой свежей установке.
	names := rest[:min(len(rest), 1)]
	if len(names) == 0 {
		// Заведённые проекты и проекты, по которым что-то накопилось, — разные
		// списки: первый созвон случается позже, чем заводят проект. Раньше
		// показывался только второй, и человек, только что заведший два
		// проекта, читал «ничего не накопилось» как «проектов нет».
		if names, err = st.KnownProjects(); err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, n := range names {
			seen[n] = true
		}
		registered, err := st.Projects()
		if err != nil {
			return err
		}
		for _, pr := range registered {
			if !seen[pr.Name] {
				names = append(names, pr.Name)
			}
		}
	}
	if len(names) == 0 || names[0] == "" {
		fmt.Println("проектов нет. Завести:  steno projects add <название> --path ~/code/…")
		return nil
	}
	kinds := []struct {
		k     ItemKind
		title string
	}{{KindTask, "Задачи"}, {KindQuestion, "Открытые вопросы"}, {KindDecision, "Решения"}}

	for _, name := range names {
		items, err := st.OpenItems(name)
		if err != nil {
			return err
		}
		fmt.Printf("\n%s — открыто %d\n", name, len(items))
		for _, kd := range kinds {
			first := true
			for _, it := range items {
				if it.Kind != kd.k {
					continue
				}
				if first {
					fmt.Printf("  %s\n", kd.title)
					first = false
				}
				line := fmt.Sprintf("    %s  %s", it.ID, it.Text)
				if it.Owner != "" {
					line += " — " + it.Owner
				}
				if it.Due != "" {
					line += " (до " + it.Due + ")"
				}
				fmt.Println(line)
			}
		}
	}
	return nil
}

func cmdCost(args []string) error {
	fs := newFlagSet("cost")
	cfgPath := setupFlags(fs)
	rest, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	days := 30
	if len(rest) > 0 {
		n, err := strconv.Atoi(rest[0])
		if err != nil || n <= 0 {
			return fmt.Errorf("сколько дней? нужно число")
		}
		days = n
	}
	cfg, st, err := open(*cfgPath)
	if err != nil {
		return err
	}
	defer st.Close()

	since := time.Now().AddDate(0, 0, -days)
	usd, in, out, n, err := st.TotalSpend(since)
	if err != nil {
		return err
	}
	if n == 0 {
		fmt.Printf("за %d дней follow-up не делался\n", days)
		return nil
	}
	fmt.Printf("за %d дней: %d follow-up, $%.2f\n", days, n, usd)
	fmt.Printf("  токенов: вход %d, выход %d\n", in, out)
	fmt.Printf("  в среднем: $%.3f за созвон\n", usd/float64(n))
	// Про effort говорим тот, что стоит на самом деле: совет «снизь high» на
	// установке с medium читается как «инструмент не смотрит на конфиг».
	eff := cfg.Claude.Effort
	if eff == "" {
		eff = "high"
	}
	fmt.Printf("\nВыход дороже входа в пять раз, и при effort=%s основная его часть —\n", eff)
	if eff == "low" {
		fmt.Printf("рассуждение модели, а не сам follow-up. Ниже уже не опустить —\n")
		fmt.Printf("дальше только модель подешевле в claude.model.\n")
	} else {
		fmt.Printf("рассуждение модели, а не сам follow-up. Дорого — сначала claude.effort.\n")
	}
	return nil
}

func cmdTranscript(args []string) error {
	fs := newFlagSet("transcript")
	cfgPath := setupFlags(fs)
	rest, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(rest) < 1 {
		return fmt.Errorf("нужен id созвона")
	}
	_, st, err := open(*cfgPath)
	if err != nil {
		return err
	}
	defer st.Close()
	segs, err := st.Segments(rest[0])
	if err != nil {
		return err
	}
	if len(segs) == 0 {
		return fmt.Errorf("расшифровки для %s ещё нет", rest[0])
	}
	fmt.Print(renderTranscript(segs))
	return nil
}

func cmdList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	cfgPath := setupFlags(fs)
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	_, st, err := open(*cfgPath)
	if err != nil {
		return err
	}
	defer st.Close()
	rows, err := st.db.Query(`SELECT id,title,started_at,status FROM meetings ORDER BY started_at DESC LIMIT 30`)
	if err != nil {
		return err
	}
	defer rows.Close()
	// Пустой вывод человек читает как «команда не сработала», а не как «созвонов
	// нет»: соседние `projects` и `cost` на пустой установке говорят словами.
	n := 0
	for rows.Next() {
		n++
		var id, title, status string
		var started int64
		if err := rows.Scan(&id, &title, &started, &status); err != nil {
			return err
		}
		fmt.Printf("%-22s %-11s %s  %s\n", id, status,
			time.Unix(started, 0).Format("02.01 15:04"), orDash(title))
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if n == 0 {
		fmt.Println("созвонов пока нет")
	}
	return nil
}

// --- serve -----------------------------------------------------------------

func cmdServe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	cfgPath := setupFlags(fs)
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	cfg, st, err := open(*cfgPath)
	if err != nil {
		return err
	}
	defer st.Close()

	lg := log.New(os.Stderr, "", log.Ltime)
	d := newDispatcher(cfg, st, lg)

	// Источники созвонов независимы: можно включить любой набор. Календарь
	// закрывает запланированное, остальные три — неожиданное.
	var sources []source
	if cfg.Calendar.Enabled {
		// Источник и сборщик расписания смотрят в одни и те же календари, но с
		// разным горизонтом. Клиент Google у них общий: иначе каждый заново
		// читал бы файл ключа и менял OAuth-токен на каждого сотрудника.
		cal := &calendarSource{cfg: cfg, d: d, log: lg}
		sources = append(sources, cal, &schedulePoller{cfg: cfg, st: st, log: lg, src: cal})
		if cfg.Calendar.Remind {
			// Напоминание живёт отдельным тиком: раз в минуту, потому что
			// напоминание за десять минут, пришедшее за четыре, уже бесполезно.
			sources = append(sources, &reminder{cfg: cfg, st: st, log: lg})
		}
	}
	if cfg.Telegram.Listen {
		sources = append(sources, &telegramSource{cfg: cfg, d: d, log: lg})
	}
	if cfg.Gmail.Enabled {
		sources = append(sources, &gmailSource{cfg: cfg, d: d, log: lg})
	}
	if cfg.HTTP.Enabled {
		sources = append(sources, &httpSource{cfg: cfg, d: d, log: lg})
	}
	// Панель — не источник созвонов, но живёт по тем же правилам: своя
	// горутина, своя остановка по контексту.
	if cfg.Sync.Enabled {
		sources = append(sources, &syncer{cfg: cfg, st: st, log: lg})
	}
	if cfg.Panel.Enabled {
		p, err := newPanel(cfg, st, lg)
		if err != nil {
			return fmt.Errorf("панель: %w", err)
		}
		// Из панели можно позвать бота на созвон — тем же путём, что из
		// Telegram и по HTTP. Диспетчер отдаётся здесь, а не в newPanel:
		// у команд без сервиса его нет, а панель они всё равно не поднимают.
		p.d = d
		sources = append(sources, p)
	}
	if len(sources) == 0 {
		return fmt.Errorf("нечего запускать: включи хотя бы один источник созвонов "+
			"(calendar, telegram.listen, gmail, http) или панель в %s", *cfgPath)
	}

	go func() {
		t := time.NewTicker(24 * time.Hour)
		defer t.Stop()
		for {
			if res, err := prune(st, cfg, lg); err != nil {
				lg.Printf("уборка: %v", err)
			} else if res.Recordings > 0 || res.Events > 0 {
				lg.Printf("уборка: %s", res)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()

	var wg sync.WaitGroup
	for _, src := range sources {
		wg.Add(1)
		go func(src source) {
			defer wg.Done()
			if err := src.Run(ctx); err != nil && ctx.Err() == nil {
				lg.Printf("%s: остановился — %v", src.Name(), err)
			}
		}(src)
	}
	wg.Wait()

	lg.Printf("останавливаюсь, жду текущие записи (до %s)", cfg.Bot.ShutdownGrace.D())
	d.WaitIdle(cfg.Bot.ShutdownGrace.D())
	return nil
}

func cmdPrune(args []string) error {
	fs := flag.NewFlagSet("prune", flag.ExitOnError)
	cfgPath := setupFlags(fs)
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	cfg, st, err := open(*cfgPath)
	if err != nil {
		return err
	}
	defer st.Close()
	res, err := prune(st, cfg, log.New(os.Stderr, "", log.Ltime))
	if err != nil {
		return err
	}
	fmt.Println(res)
	return nil
}

// source — способ узнать, что идёт созвон. Все они кладут находку в один и тот
// же Dispatcher, который следит, чтобы на встречу не пришли два бота.
type source interface {
	Name() string
	Run(ctx context.Context) error
}

// cmdProjectAdd заводит проект из терминала. Источники повторяются: один
// проект — это обычно и репозиторий, и сайт, и пара слов о том, что это.
func cmdProjectAdd(args []string) error {
	fs := newFlagSet("projects add")
	cfgPath := setupFlags(fs)
	var about, aliases string
	var repos, paths, urls stringList
	fs.StringVar(&about, "about", "", "одна строка о том, что это за проект")
	fs.StringVar(&aliases, "alias", "", "как называют вслух, через запятую")
	fs.Var(&repos, "repo", "ссылка на репозиторий (можно несколько раз)")
	fs.Var(&paths, "path", "каталог с кодом на этой машине (можно несколько раз)")
	fs.Var(&urls, "url", "адрес сайта или документа (можно несколько раз)")
	rest, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	name := strings.TrimSpace(strings.Join(rest, " "))
	if name == "" {
		return fmt.Errorf("как назвать проект? steno projects add <название> [--repo …] [--about …]")
	}

	var sources []Source
	for _, v := range repos {
		sources = append(sources, Source{Kind: "repo", Value: v})
	}
	for _, v := range paths {
		sources = append(sources, Source{Kind: "path", Value: expandHome(v)})
	}
	for _, v := range urls {
		sources = append(sources, Source{Kind: "url", Value: v})
	}

	_, st, err := open(*cfgPath)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.SaveProject(Project{
		Name: name, About: strings.TrimSpace(about),
		Aliases: commaList(aliases), Sources: sources,
	}); err != nil {
		return err
	}
	fmt.Printf("проект «%s» заведён\n", name)
	if len(sources) == 0 {
		fmt.Println("источников нет — справку собрать не из чего.")
		fmt.Println("  steno projects add " + name + " --repo git@github.com:…  или --path ~/code/…")
		return nil
	}
	fmt.Println("собрать справку по коду:  steno context " + name)
	return nil
}

func cmdProjectRm(args []string) error {
	fs := newFlagSet("projects rm")
	cfgPath := setupFlags(fs)
	rest, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	name := strings.TrimSpace(strings.Join(rest, " "))
	if name == "" {
		return fmt.Errorf("какой проект удалить? steno projects rm <название>")
	}
	_, st, err := open(*cfgPath)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.DeleteProject(name); err != nil {
		return err
	}
	// Задачи и решения остаются: они принадлежат созвонам, а не проекту, и
	// молча уносить их вместе с записью в реестре — потеря без предупреждения.
	fmt.Printf("проект «%s» удалён; задачи и решения по нему остались\n", name)
	return nil
}

// stringList — флаг, который можно повторять: --repo A --repo B.
type stringList []string

func (l *stringList) String() string { return strings.Join(*l, ", ") }
func (l *stringList) Set(v string) error {
	if v = strings.TrimSpace(v); v != "" {
		*l = append(*l, v)
	}
	return nil
}
