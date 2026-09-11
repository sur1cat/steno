package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sur1cat/steno/internal/audio"
	"github.com/sur1cat/steno/internal/bot"
	"github.com/sur1cat/steno/internal/brain"
	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
	"github.com/sur1cat/steno/internal/mcp"
	"github.com/sur1cat/steno/internal/note"
	"github.com/sur1cat/steno/internal/panel"
	"github.com/sur1cat/steno/internal/pipeline"
	"github.com/sur1cat/steno/internal/publish"
	"github.com/sur1cat/steno/internal/sources"
	"golang.org/x/term"
)

const usage = `steno — заметки и follow-up с созвонов.

  steno setup                настроить всё: спросит по одному и проверит
  steno ui                   всё в терминале: созвоны, задачи, проекты,
                             поиск и каналы. ? внутри — клавиши, q — выход;
                             serve для этого запускать не нужно
  steno start                запустить фоном: слушать источники и ходить
                             на созвоны. Останов — steno stop
  steno serve                то же, но не отпуская терминал
  steno stop                 остановить фоновый steno, дав дописать созвоны
  steno status               работает ли, с какого времени, где лог
  steno autostart on|off     запускать при входе в систему
  steno join <meet-url>      зайти в созвон, записать и разослать follow-up
                             --record-only  только запись
                             --no-followup  запись и расшифровка, без Claude
                             --captions     текст из субтитров Meet
  steno note                 наговорить заметку в микрофон: Enter — стоп,
                             дальше та же расшифровка и тот же разбор
                             --devices      список микрофонов
                             --device N     каким писать
                             --title "…"    назвать самому
                             --max 30m      потолок записи
                             steno note <id> — разобрать записанную заново
  steno process [id]         расшифровать и разослать записанный созвон
                             без id — последний
  steno publish [id]         разослать готовый follow-up ещё раз
  steno show [id]            показать follow-up
  steno transcript [id]      показать расшифровку
  steno list                 последние созвоны
  steno rm <id>              забыть созвон целиком: запись, расшифровку,
                             follow-up и то, что из него вышло. Спросит
                             --yes          не спрашивать, для скриптов
  steno prune                удалить старые записи по срокам из конфига
  steno doctor               проверить, чего не хватает для запуска
  steno demo                 панель на демо-данных: без настройки и созвона
  steno mcp                  MCP-сервер для Claude Code, Claude Desktop, Cursor
  steno version              версия и какой образ бота ей соответствует
  steno cost [дней]          сколько потрачено на follow-up
  steno projects [проект]    что открыто по проектам
  steno projects add <имя>   завести проект. Без флагов спросит сам:
                             кто участвует, какие слова звучат вслух
                             --repo <url>   репозиторий (можно несколько)
                             --path <dir>   каталог с кодом на этой машине
                             --url <адрес>  сайт или документ
                             --about "…"    одна строка, что это за проект
                             --alias a,b    как называют вслух
                             --people а,б   имена людей, как их зовут вслух
                             --word а,б     сервисы и сокращения проекта
  steno projects rm <имя>    убрать проект из реестра
  steno context [проект]     собрать справки о проектах по коду и сайтам
  steno spec                 ТЗ по задачам с созвонов: собрать, показать,
                             отдать агенту (steno spec help — подробно)
  steno google               вход по кнопке: что уже есть; steno google set —
                             вставить Client ID и секрет из консоли Google
  steno panel on|off         поднимать ли веб-панель вместе с сервисом
  steno agent on|off         разрешить агенту работать в репозитории;
                             steno agent auto on — собирать ТЗ самому
  steno bot --url <u>        сам бот; запускается внутри контейнера

Общие флаги:
  -c <path>                  путь к steno.json
`

func main() {
	log.SetFlags(log.Ltime)
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, i18n.Tr(usage))
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch cmd {
	case "setup":
		err = cmdSetup(ctx, args)
	case "start":
		// `steno start` — то же, что `serve -d`, но словом, которого от службы и
		// ждут. Пара start/stop очевидна, а «serve с флагом» надо вспоминать.
		args = append([]string{"-d"}, args...)
		fallthrough
	case "serve":
		// Фоновый режим и замок на pid-файле — до всего остального: см. daemon.go.
		if done, derr := daemonize(args); done || derr != nil {
			err = derr
			break
		}
		err = cmdServe(ctx, args)
	case "stop":
		err = cmdStop(args)
	case "status":
		err = cmdStatus(args)
	case "autostart":
		err = cmdAutostart(args)
	case "join":
		err = cmdJoin(ctx, args)
	case "process":
		err = cmdProcess(ctx, args)
	case "note":
		err = cmdNote(ctx, args)
	case "publish":
		err = cmdPublish(ctx, args)
	case "show":
		err = cmdShow(args)
	case "transcript":
		err = cmdTranscript(args)
	case "list":
		err = cmdList(args)
	case "rm":
		err = cmdMeetingRm(args)
	case "doctor":
		err = cmdDoctor(ctx, args)
	case "demo":
		err = cmdDemo(ctx, args)
	case "mcp":
		err = cmdMCP(ctx, args)
	case "version", "--version", "-v":
		err = cmdVersion()
	case "cost":
		err = cmdCost(args)
	case "projects":
		err = cmdProjects(args)
	case "spec":
		err = cmdSpec(ctx, args)
	case "google":
		err = cmdGoogle(ctx, args)
	case "agent":
		err = cmdAgent(args)
	case "panel":
		err = cmdPanel(args)
	case "context":
		err = cmdContext(ctx, args)
	case "prune":
		err = cmdPrune(args)
	case "ui":
		err = cmdUI(ctx, args)
	case "bot":
		err = cmdBot(ctx, args)
	case "-h", "--help", "help":
		fmt.Print(i18n.Tr(usage))
		return
	default:
		fmt.Fprintf(os.Stderr, i18n.Tr("неизвестная команда %q\n\n%s"), cmd, i18n.Tr(usage))
		os.Exit(2)
	}
	if err != nil {
		log.Fatalf(i18n.Tr("ошибка: %v"), err)
	}
}

func cmdVersion() error {
	v := core.StenoVersion()
	fmt.Printf("steno %s\n", v)
	fmt.Printf(i18n.Tr("образ бота: %s\n"), core.DefaultBotImage())
	return nil
}

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
	return fs.String("c", core.EnvOr("STENO_CONFIG", core.DefaultConfigPath), i18n.Tr("путь к конфигу"))
}

func open(configPath string) (*core.Config, *core.Store, error) {
	// Секреты лежат в .env рядом с конфигом. Уже заданное окружение главнее:
	// в проде переменные приходят от systemd или docker.
	if abs, err := filepath.Abs(configPath); err == nil {
		_ = loadDotEnv(filepath.Join(filepath.Dir(abs), ".env"))
	}

	if p := core.ResolveConfigPath(configPath); p != configPath {
		configPath = p
		log.Printf(i18n.Tr("настройка: %s"), configPath)
		_ = loadDotEnv(filepath.Join(filepath.Dir(p), ".env"))
	}
	if _, err := os.Stat(configPath); err != nil {
		// Умолчания подставляем, только если человек не называл файл сам.
		// Иначе опечатка в -c тихо запускала бы сервис без единого адресата:
		// созвон записан, токены Claude потрачены, follow-up никуда не ушёл.
		if configPath != core.DefaultConfigPath {
			return nil, nil, fmt.Errorf(i18n.Tr("конфиг %s: %w"), configPath, err)
		}
		configPath = ""
	}
	cfg, err := core.LoadConfig(configPath)
	if err == nil {
		// Язык из конфига — сразу после чтения и до всего остального:
		// дальше начинается вывод, и переключать язык посреди него поздно.
		i18n.SetLang(cfg.Lang)
	}
	if err != nil {
		return nil, nil, err
	}
	st, err := core.OpenStore(cfg.DataDir)
	if err != nil {
		return nil, nil, err
	}
	audio.ResizeTranscribeQueue(cfg.Transcribe.MaxConcurrent)
	// Адреса своих серверов Jitsi живут в selectors.json — там же, где
	// остальная вёрстка, и оттуда же едут внутрь контейнера бота.
	bot.ApplyPlatformConfig(cfg, log.Default())
	if n, err := core.ImportProjects(st, cfg); err != nil {
		st.Close()
		return nil, nil, fmt.Errorf(i18n.Tr("перенос проектов из конфига: %w"), err)
	} else if n > 0 {
		log.Printf(i18n.Tr("перенёс %d проектов из конфига в базу — дальше правь их в панели"), n)
	}
	if n, err := core.ImportChannels(st, cfg); err != nil {
		st.Close()
		return nil, nil, fmt.Errorf(i18n.Tr("перенос каналов из конфига: %w"), err)
	} else if n > 0 && configPath != "" {
		// Без конфига переносить нечего: в базу уезжают умолчания, и сообщать
		// человеку о «переносе из конфига», которого у него нет, — вводить в
		// заблуждение на первом же запуске.
		log.Printf(i18n.Tr("перенёс %d каналов из конфига в базу — дальше правь их в панели"), n)
	}
	// База главнее конфига: канал, выключенный в панели, должен остаться
	// выключенным и после перезапуска.
	//
	// У раздела «Разбор» это же правило больно кусается, и потому о нём надо
	// говорить вслух. Каналы человек и так правит в панели, а провайдера
	// описывают в steno.json — так написано и в примере конфига, и в README.
	// Правка файла после того, как выбор сделан кнопкой, не делает ничего, и
	// молчать об этом значит отправить человека искать, почему steno ходит не
	// туда, куда написано.
	fromFile := cfg.BrainSummary()
	if err := core.ApplyChannels(st, cfg); err != nil {
		st.Close()
		return nil, nil, fmt.Errorf(i18n.Tr("настройки каналов: %w"), err)
	}
	if inDB := cfg.BrainSummary(); inDB != fromFile && configPath != "" {
		log.Printf(i18n.Tr("разбор: взял из базы «%s», а не «%s» из конфига — выбор делается в панели"),
			inDB, fromFile)
	}
	// Провайдера никто не выбирал, Claude нет, а ключ Groq или OpenAI в
	// окружении есть — берём его, и прямо в конфиг: имя модели отсюда уходит в
	// базу рядом с follow-up и в `steno cost`. Молча нельзя — иначе человек с
	// ключом Groq ради расшифровки удивится, кто разобрал его созвон.
	if note, ok := brain.ApplyAutoOpenAI(cfg); ok {
		log.Printf(i18n.Tr("разбор: %s"), note)
	}
	return cfg, st, nil
}

// --- join ------------------------------------------------------------------

func cmdJoin(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("join", flag.ExitOnError)
	cfgPath := setupFlags(fs)
	title := fs.String("title", "", i18n.Tr("название встречи"))
	invitees := fs.String("invitees", "", i18n.Tr("приглашённые через запятую"))
	local := fs.Bool("local", false, i18n.Tr("запустить бота прямо здесь, без docker (нужны Chromium, ffmpeg и pulseaudio)"))
	noPublish := fs.Bool("no-publish", false, i18n.Tr("только записать и расшифровать"))
	recordOnly := fs.Bool("record-only", false,
		i18n.Tr("только записать: ни расшифровки, ни follow-up, ни рассылки"))
	noFollowup := fs.Bool("no-followup", false,
		i18n.Tr("записать и расшифровать, показать расшифровку и остановиться (Claude не нужен)"))
	useCaptions := fs.Bool("captions", false,
		i18n.Tr("взять текст из субтитров Meet вместо whisper"))
	rest, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(rest) < 1 {
		return fmt.Errorf(i18n.Tr("нужна ссылка на созвон (%s): steno join https://meet.google.com/abc-defg-hij"),
			bot.SupportedPlatforms())
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
	meetURL := bot.FindMeetURL(rest[0])
	if meetURL == "" {
		return bot.MeetingLinkError(rest[0])
	}

	m := &core.Meeting{
		ID:        core.NewID(time.Now()),
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
		cfg.NoPublish = true
	}
	if *useCaptions {
		cfg.Transcribe.Source = "captions"
	}
	if *recordOnly {
		return pipeline.RecordOnce(ctx, cfg, st, m)
	}
	if *noFollowup {
		if err := pipeline.RecordMeeting(ctx, cfg, st, m); err != nil {
			return err
		}
		return pipeline.TranscribeOnly(ctx, cfg, st, m.ID)
	}
	return pipeline.RecordAndProcess(ctx, cfg, st, m)
}

// --- bot (внутри контейнера) ----------------------------------------------

func cmdBot(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("bot", flag.ExitOnError)
	meetURL := fs.String("url", "", i18n.Tr("ссылка на созвон"))
	outDir := fs.String("out", "/out", i18n.Tr("куда писать"))
	name := fs.String("name", i18n.Tr("Steno · идёт запись"), i18n.Tr("имя бота в списке участников"))
	selectors := fs.String("selectors", core.EnvOr("STENO_SELECTORS", ""), i18n.Tr("путь к selectors.json"))
	source := fs.String("source", core.EnvOr("STENO_AUDIO_SOURCE", "meet_out.monitor"), i18n.Tr("источник PulseAudio"))
	admission := fs.Duration("admission", 5*time.Minute, i18n.Tr("сколько ждать, пока впустят"))
	emptyFor := fs.Duration("empty-for", 2*time.Minute, i18n.Tr("уйти, если остался один дольше этого"))
	maxDur := fs.Duration("max", 4*time.Hour, i18n.Tr("потолок длительности"))
	headless := fs.Bool("headless", false, i18n.Tr("без Xvfb (Meet работает хуже)"))
	debugCaptions := fs.Bool("debug-captions", false,
		i18n.Tr("печатать, что на странице похоже на субтитры"))
	captionLang := fs.String("caption-language", "",
		i18n.Tr("язык субтитров Meet — он же язык распознавания"))
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	if *meetURL == "" {
		return errors.New(i18n.Tr("нужен --url"))
	}
	sel, err := bot.LoadSelectors(*selectors)
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

	res, err := bot.RunBot(ctx, bot.BotOptions{
		MeetURL:          *meetURL,
		DisplayName:      *name,
		OutDir:           *outDir,
		AudioSource:      *source,
		Selectors:        sel,
		AdmissionTimeout: *admission,
		EmptyFor:         *emptyFor,
		MaxDuration:      *maxDur,
		UserDataDir:      core.EnvOr("STENO_CHROME_PROFILE", ""),
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
	noPublish := fs.Bool("no-publish", false, i18n.Tr("не публиковать"))
	noFollowup := fs.Bool("no-followup", false,
		i18n.Tr("только расшифровать и показать — Claude не нужен"))
	useCaptions := fs.Bool("captions", false,
		i18n.Tr("взять текст из субтитров Meet вместо whisper"))
	rest, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	cfg, st, err := open(*cfgPath)
	if err != nil {
		return err
	}
	defer st.Close()
	id, err := core.MeetingArg(st, rest)
	if err != nil {
		return err
	}
	if *useCaptions {
		cfg.Transcribe.Source = "captions"
	}
	if *noFollowup {
		return pipeline.TranscribeOnly(ctx, cfg, st, id)
	}
	return pipeline.ProcessMeeting(ctx, cfg, st, id, *noPublish)
}

func cmdPublish(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("publish", flag.ExitOnError)
	cfgPath := setupFlags(fs)
	rest, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	cfg, st, err := open(*cfgPath)
	if err != nil {
		return err
	}
	defer st.Close()
	id, err := core.MeetingArg(st, rest)
	if err != nil {
		return err
	}
	m, err := st.Meeting(id)
	if err != nil {
		return err
	}
	f, err := st.Followup(id)
	if err != nil {
		return fmt.Errorf(i18n.Tr("follow-up ещё не сделан — сначала steno process %s"), id)
	}
	segs, err := st.Segments(id)
	if err != nil {
		// Молча опубликовать документ без расшифровки — хуже, чем не
		// опубликовать: снаружи он выглядит полным.
		return fmt.Errorf(i18n.Tr("расшифровка %s: %w"), id, err)
	}
	if errs := publish.PublishAll(ctx, cfg, st, m, f, segs, log.New(os.Stderr, "", log.Ltime)); len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func cmdShow(args []string) error {
	fs := flag.NewFlagSet("show", flag.ExitOnError)
	cfgPath := setupFlags(fs)
	asJSON := fs.Bool("json", false, i18n.Tr("выдать JSON"))
	rest, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	_, st, err := open(*cfgPath)
	if err != nil {
		return err
	}
	defer st.Close()
	id, err := core.MeetingArg(st, rest)
	if err != nil {
		return err
	}
	m, err := st.Meeting(id)
	if err != nil {
		return err
	}
	f, err := st.Followup(id)
	if err != nil {
		return fmt.Errorf(i18n.Tr("follow-up для %s ещё нет"), id)
	}
	if *asJSON {
		b, _ := json.MarshalIndent(f, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	fmt.Println(publish.RenderPlain(m, f))
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
	force := fs.Bool("force", false, i18n.Tr("пересобрать, даже если материал не менялся"))
	show := fs.Bool("show", false, i18n.Tr("показать готовые справки, не пересобирая"))
	rest, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	cfg, st, err := open(*cfgPath)
	if err != nil {
		return err
	}
	defer st.Close()
	projects := core.ActiveProjects(st, cfg)
	if len(projects) == 0 {
		return errors.New(i18n.Tr("нет ни одного проекта — заведи в панели или в конфиге"))
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
				fmt.Printf(i18n.Tr("\n%s — справки нет\n"), p.Name)
				continue
			}
			fmt.Printf(i18n.Tr("\n%s — собрана %s\n\n%s\n"), p.Name,
				c.BuiltAt.Format("02.01.2006 15:04"), c.Primer)
			continue
		}
		if len(p.Sources) == 0 {
			log.Printf(i18n.Tr("%s: источников нет — пропускаю"), p.Name)
			continue
		}

		log.Printf(i18n.Tr("%s: читаю источники"), p.Name)
		material, fp, err := brain.GatherSources(ctx, cfg.DataDir, p)
		if err != nil {
			log.Printf("%s: %v", p.Name, err)
			continue
		}
		// Материал не менялся — незачем платить за ту же справку снова.
		if !*force {
			if c, err := st.ProjectContext(p.Name); err == nil && c.Fingerprint == fp {
				log.Printf(i18n.Tr("%s: материал тот же, справка от %s"),
					p.Name, c.BuiltAt.Format("02.01.2006"))
				continue
			}
		}

		log.Printf(i18n.Tr("%s: собираю справку (%d символов материала)"), p.Name, len([]rune(material)))
		primer, spend, err := brain.BuildPrimer(ctx, cfg, p, material)
		if err != nil {
			log.Printf("%s: %v", p.Name, err)
			continue
		}
		total += spend.USD
		if err := st.SaveProjectContext(core.ProjectContext{
			Project: p.Name, Primer: primer, Fingerprint: fp,
			Sources: core.SourcesSummary(p),
		}); err != nil {
			return err
		}
		log.Printf(i18n.Tr("%s: готово, %s"), p.Name, spend)
	}
	if total > 0 {
		log.Printf(i18n.Tr("всего на справки: $%.3f"), total)
	}
	return nil
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
		fmt.Println(i18n.Tr("проектов нет. Завести:  steno projects add <название> --path ~/code/…"))
		return nil
	}
	kinds := []struct {
		k     core.ItemKind
		title string
	}{{core.KindTask, i18n.Tr("Задачи")}, {core.KindQuestion, i18n.Tr("Открытые вопросы")}, {core.KindDecision, i18n.Tr("Решения")}}

	for _, name := range names {
		items, err := st.OpenItems(name)
		if err != nil {
			return err
		}
		// Имя проекта переводится только на показ: в базе «не определён» —
		// метка, по которой ищут, и переводить её там значило бы её потерять.
		fmt.Printf(i18n.Tr("\n%s — открыто %d\n"), i18n.Tr(name), len(items))
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
					line += i18n.Tr(" (до ") + it.Due + ")"
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
			return errors.New(i18n.Tr("сколько дней? нужно число"))
		}
		days = n
	}
	cfg, st, err := open(*cfgPath)
	if err != nil {
		return err
	}
	defer st.Close()

	since := time.Now().AddDate(0, 0, -days)
	usd, in, out, n, unpriced, err := st.TotalSpend(since)
	if err != nil {
		return err
	}
	if n == 0 {
		fmt.Printf(i18n.Tr("за %d дней follow-up не делался\n"), days)
		return nil
	}
	fmt.Printf(i18n.Tr("за %d дней: %d follow-up, $%.2f\n"), days, n, usd)
	fmt.Printf(i18n.Tr("  токенов: вход %d, выход %d\n"), in, out)
	if priced := n - unpriced; priced > 0 {
		fmt.Printf(i18n.Tr("  в среднем: $%.3f за созвон\n"), usd/float64(priced))
	}
	// Разбор ценой не считается — и молчать об этом нельзя. Ноль в деньгах
	// читается как «бесплатно», а провайдер, цены которого steno не знает,
	// пришлёт счёт независимо от того, что тут напечатано.
	if unpriced > 0 {
		fmt.Printf(i18n.Tr("\nИз них %d без цены: steno не знает, сколько стоит эта модель, и в сумму\n"), unpriced)
		fmt.Printf(i18n.Tr("они не вошли. Цены задаются таблицей — %s.\n"), pricesKnob(cfg))
	}
	// Совет про усилие — только там, где усилие есть: у остальных провайдеров
	// ни effort, ни claude.model нет вовсе, и звать туда человека значит
	// послать его искать настройку, которой у него не заведено.
	if cfg.BrainProvider() != core.ProviderClaude {
		return nil
	}
	// Про effort говорим тот, что стоит на самом деле: совет «снизь high» на
	// установке с medium читается как «инструмент не смотрит на конфиг».
	eff := cfg.Claude.Effort
	if eff == "" {
		eff = "high"
	}
	fmt.Printf(i18n.Tr("\nВыход дороже входа в пять раз, и при effort=%s основная его часть —\n"), eff)
	if eff == "low" {
		fmt.Print(i18n.Tr("рассуждение модели, а не сам follow-up. Ниже уже не опустить —\n"))
		fmt.Print(i18n.Tr("дальше только модель подешевле в claude.model.\n"))
	} else {
		fmt.Print(i18n.Tr("рассуждение модели, а не сам follow-up. Дорого — сначала claude.effort.\n"))
	}
	return nil
}

// pricesKnob — где у выбранного провайдера лежит таблица цен. Называть все три
// сразу значит заставить человека выбирать из них наугад.
func pricesKnob(cfg *core.Config) string {
	switch cfg.BrainProvider() {
	case core.ProviderOpenAI:
		return "brain.openai.prices"
	case core.ProviderCommand:
		return i18n.Tr("llm.prices — или пусть скрипт считает сам, полем usd")
	case core.ProviderCodex:
		return i18n.Tr("расход по подписке codex не сообщает вовсе")
	}
	return "claude.prices"
}

func cmdTranscript(args []string) error {
	fs := newFlagSet("transcript")
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
	id, err := core.MeetingArg(st, rest)
	if err != nil {
		return err
	}
	segs, err := st.Segments(id)
	if err != nil {
		return err
	}
	if len(segs) == 0 {
		return fmt.Errorf(i18n.Tr("расшифровки для %s ещё нет"), id)
	}
	fmt.Print(brain.RenderTranscript(segs))
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
	rows, err := st.DB.Query(`SELECT id,title,started_at,status FROM meetings ORDER BY started_at DESC LIMIT 30`)
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
			time.Unix(started, 0).Format("02.01 15:04"), core.OrDash(title))
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if n == 0 {
		fmt.Println(i18n.Tr("созвонов пока нет"))
	}
	return nil
}

// cmdMCP — MCP-сервер по stdio: Claude Code, Claude Desktop и Cursor
// спрашивают про созвоны инструментами поверх той же базы, что у `steno show`
// и `steno projects`. Сервис не нужен — как и остальным командам, которые
// только читают; сами инструменты живут в internal/mcp.
//
// Клиент подключает его одной строкой: claude mcp add steno -- steno mcp.
// stdout занят протоколом, поэтому всё, что говорится человеку, — включая
// «настройка: …» из open(), — уходит в stderr через log, и печатать сюда
// fmt.Print нельзя.
func cmdMCP(ctx context.Context, args []string) error {
	fs := newFlagSet("mcp")
	cfgPath := setupFlags(fs)
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	_, st, err := open(*cfgPath)
	if err != nil {
		return err
	}
	defer st.Close()
	return mcp.Serve(ctx, st)
}

// cmdMeetingRm — `steno rm <id>`. Имя выбрано по соседям: `show`, `process`,
// `transcript`, `publish` — все верхнего уровня и все про созвон, а `projects`
// — своё пространство со своим `rm`. `steno rm <id>` читается там же, где
// человек взял id, — в `steno list`.
//
// id обязателен, хотя соседи умеют брать последний созвон без аргумента.
// Умолчание, стирающее данные, — ловушка: `steno rm` с промахом мимо клавиши
// унёс бы только что записанный созвон, а вместе с --yes сделал бы это молча.
func cmdMeetingRm(args []string) error {
	fs := newFlagSet("rm")
	cfgPath := setupFlags(fs)
	// Флаг для скриптов. Спрашивать в конвейере некого: вопрос уходит в лог,
	// ответа не будет никогда, и без флага такой вызов просто висел бы.
	yes := fs.Bool("yes", false, i18n.Tr("не спрашивать подтверждения (для скриптов)"))
	rest, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	id := strings.TrimSpace(strings.Join(rest, " "))
	if id == "" {
		return errors.New(i18n.Tr("какой созвон удалить? steno rm <id>, id — из steno list"))
	}
	_, st, err := open(*cfgPath)
	if err != nil {
		return err
	}
	defer st.Close()

	m, err := st.Meeting(id)
	if err != nil {
		return fmt.Errorf(i18n.Tr("созвона %s нет — посмотри steno list"), id)
	}
	toll, err := st.MeetingToll(id)
	if err != nil {
		return err
	}
	fmt.Printf("%s  %s  %s\n", m.ID, m.StartedAt.Format("02.01 15:04"), core.OrDash(m.Title))
	fmt.Printf("  %s\n", toll.Text)

	if !*yes && !askYes(i18n.Tr("Удалить созвон? Это навсегда")) {
		fmt.Println(i18n.Tr("отменил"))
		return nil
	}
	toll, err = st.DeleteMeeting(id)
	if err != nil {
		return err
	}
	fmt.Printf(i18n.Tr("созвон %s удалён\n"), id)
	if toll.Reopen > 0 {
		fmt.Printf(i18n.Tr("вернулось в работу: %s\n"), toll.ReopenWords())
	}
	return nil
}

// askYes — согласие на необратимое. Пустой ответ — отказ, и Enter согласием не
// считается: тот же уговор, что на экране подтверждения в `steno ui`. Enter по
// инерции после предыдущей команды слишком дёшев для действия, которое нечем
// отменить.
//
// Оборванный ввод (Ctrl+D, труба, скрипт без --yes) — тоже отказ: спросить
// некого, а молча удалить нельзя.
func askYes(question string) bool {
	fmt.Printf("%s [%s]: ", question, dim("y/N"))
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		// Ввод оборвался, перевода строки не будет — допечатываем сами, иначе
		// следующая строка вывода приклеивается к вопросу.
		fmt.Println()
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes", "д", "да":
		return true
	}
	return false
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
	d := sources.NewDispatcher(cfg, st, lg)

	// Источники созвонов независимы: можно включить любой набор. Календарь
	// закрывает запланированное, остальные три — неожиданное.
	var srcs []source
	if cfg.Calendar.Enabled {
		// Источник и сборщик расписания смотрят в одни и те же календари, но с
		// разным горизонтом. Клиент Google у них общий: иначе каждый заново
		// читал бы файл ключа и менял OAuth-токен на каждого сотрудника.
		cal := &sources.CalendarSource{Cfg: cfg, D: d, Log: lg}
		srcs = append(srcs, cal, &sources.SchedulePoller{Cfg: cfg, St: st, Log: lg, Src: cal})
		if cfg.Calendar.Remind {
			// Напоминание живёт отдельным тиком: раз в минуту, потому что
			// напоминание за десять минут, пришедшее за четыре, уже бесполезно.
			srcs = append(srcs, &sources.Reminder{Cfg: cfg, St: st, Log: lg})
		}
	}
	if cfg.Telegram.Listen {
		srcs = append(srcs, &sources.TelegramSource{Cfg: cfg, D: d, Log: lg})
	}
	if cfg.Gmail.Enabled {
		srcs = append(srcs, &sources.GmailSource{Cfg: cfg, D: d, Log: lg})
	}
	if cfg.HTTP.Enabled {
		srcs = append(srcs, &sources.HTTPSource{Cfg: cfg, D: d, Log: lg})
	}
	// Панель — не источник созвонов, но живёт по тем же правилам: своя
	// горутина, своя остановка по контексту.
	if cfg.Sync.Enabled {
		srcs = append(srcs, &sources.Syncer{Cfg: cfg, St: st, Log: lg})
	}
	if cfg.Panel.Enabled {
		p, err := panel.NewPanel(cfg, st, lg)
		if err != nil {
			return fmt.Errorf(i18n.Tr("панель: %w"), err)
		}
		// Из панели можно позвать бота на созвон — тем же путём, что из
		// Telegram и по HTTP. Диспетчер отдаётся здесь, а не в NewPanel:
		// у команд без сервиса его нет, а панель они всё равно не поднимают.
		p.D = d
		srcs = append(srcs, p)
	}
	if len(srcs) == 0 {
		return fmt.Errorf(i18n.Tr("нечего запускать: включи хотя бы один источник созвонов ")+
			i18n.Tr("(calendar, telegram.listen, gmail, http) или панель в %s"), *cfgPath)
	}

	go func() {
		t := time.NewTicker(24 * time.Hour)
		defer t.Stop()
		for {
			if res, err := core.Prune(st, cfg, lg); err != nil {
				lg.Printf(i18n.Tr("уборка: %v"), err)
			} else if res.Recordings > 0 || res.Events > 0 {
				lg.Printf(i18n.Tr("уборка: %s"), res)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()

	var wg sync.WaitGroup
	for _, src := range srcs {
		wg.Add(1)
		go func(src source) {
			defer wg.Done()
			if err := src.Run(ctx); err != nil && ctx.Err() == nil {
				lg.Printf(i18n.Tr("%s: остановился — %v"), src.Name(), err)
			}
		}(src)
	}
	wg.Wait()

	lg.Printf(i18n.Tr("останавливаюсь, жду текущие записи (до %s)"), cfg.Bot.ShutdownGrace.D())
	note.Notes.Park(st, lg)
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
	res, err := core.Prune(st, cfg, log.New(os.Stderr, "", log.Ltime))
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
//
// Названного одним именем проекта мало, и молчать в ответ на это неправильно.
// Из репозитория steno достаёт и авторов коммитов, и состав сервисов — но у
// половины проектов кода нет вовсе (продажи, поддержка, руководство), в
// коммитах человек подписан не тем именем, которым его зовут вслух, а часть
// сервисов живёт в чужих репозиториях. Поэтому `projects add <имя>` без
// подробностей переходит в разговор — по одному вопросу, каждый пропускается
// пустым ответом.
//
// С флагами — молчит. `projects add` зовут из скриптов, и вопрос, заданный
// такому вызову, — это подвисший навсегда конвейер.
func cmdProjectAdd(args []string) error {
	fs := newFlagSet("projects add")
	cfgPath := setupFlags(fs)
	var about, aliases, people, words string
	var repos, paths, urls stringList
	fs.StringVar(&about, "about", "", i18n.Tr("одна строка о том, что это за проект"))
	fs.StringVar(&aliases, "alias", "", i18n.Tr("как называют вслух, через запятую"))
	fs.StringVar(&people, "people", "", i18n.Tr("кто участвует: имена, которыми зовут вслух, через запятую"))
	fs.StringVar(&words, "word", "", i18n.Tr("сервисы, системы и сокращения, звучащие вслух, через запятую"))
	fs.Var(&repos, "repo", i18n.Tr("ссылка на репозиторий (можно несколько раз)"))
	fs.Var(&paths, "path", i18n.Tr("каталог с кодом на этой машине (можно несколько раз)"))
	fs.Var(&urls, "url", i18n.Tr("адрес сайта или документа (можно несколько раз)"))
	rest, err := parseArgs(fs, args)
	if err != nil {
		return err
	}

	p := core.Project{
		Name:       strings.TrimSpace(strings.Join(rest, " ")),
		About:      strings.TrimSpace(about),
		Aliases:    core.CommaList(aliases),
		People:     core.CommaList(people),
		Vocabulary: core.CommaList(words),
	}
	for _, v := range repos {
		p.Sources = append(p.Sources, core.Source{Kind: "repo", Value: v})
	}
	for _, v := range paths {
		p.Sources = append(p.Sources, core.Source{Kind: "path", Value: brain.ExpandHome(v)})
	}
	for _, v := range urls {
		p.Sources = append(p.Sources, core.Source{Kind: "url", Value: v})
	}

	// База открывается до разговора, а не после: спросить пять раз и упасть на
	// ненайденном конфиге — худший из возможных порядков.
	_, st, err := open(*cfgPath)
	if err != nil {
		return err
	}
	defer st.Close()

	if projectIsBare(p) && askableStdin() {
		// Проект под этим именем мог быть заведён раньше: тогда разговор
		// дописывает, а не заводит заново, и сказать об этом надо до вопросов,
		// а не после — иначе пустой ответ читается как «сотри, что было».
		_, err := st.Project(p.Name)
		(&projectAsk{in: bufio.NewReader(os.Stdin), out: os.Stdout}).run(&p, err == nil)
	}
	if p.Name == "" {
		return errors.New(i18n.Tr("как назвать проект? steno projects add <название> [--repo …] [--about …]"))
	}

	// Проект с таким именем уже заведён — дописываем к нему, а не заменяем.
	// `steno projects add Платежи --path ~/code/pay` — обычный способ приложить
	// репозиторий к тому, что уже описано, и молча стереть этим описание и
	// список людей нельзя: пропажу имени из словаря видно не сразу, а через
	// месяц, в задаче, уехавшей не тому человеку.
	known := false
	if old, err := st.Project(p.Name); err == nil {
		p, known = mergeProject(old, p), true
	}
	if err := st.SaveProject(p); err != nil {
		return err
	}
	if known {
		fmt.Printf(i18n.Tr("проект «%s» дополнен\n"), p.Name)
	} else {
		fmt.Printf(i18n.Tr("проект «%s» заведён\n"), p.Name)
	}
	if len(p.People) > 0 {
		fmt.Printf(i18n.Tr("  люди: %s\n"), strings.Join(p.People, ", "))
	}
	if other := p.OtherWords(); len(other) > 0 {
		fmt.Printf(i18n.Tr("  слова проекта: %s\n"), strings.Join(other, ", "))
	}
	if len(p.Sources) == 0 {
		fmt.Println(i18n.Tr("источников нет — справку собрать не из чего."))
		fmt.Println("  steno projects add " + p.Name + i18n.Tr(" --repo git@github.com:…  или --path ~/code/…"))
		return nil
	}
	fmt.Println(i18n.Tr("собрать справку по коду:  steno context ") + p.Name)
	return nil
}

// mergeProject накладывает названное сейчас на уже записанное. Пустое поле не
// стирает: в командной строке «не сказал» и «сказал, что пусто» неразличимы, а
// цена ошибки несимметрична — стёртое описание человек увидит сразу, стёртый
// список людей не увидит вовсе.
func mergeProject(old, add core.Project) core.Project {
	out := old
	if add.About != "" {
		out.About = add.About
	}
	out.Aliases = core.MergeWords(old.Aliases, add.Aliases)
	out.People = core.MergeWords(old.People, add.People)
	out.Vocabulary = core.MergeWords(old.Vocabulary, add.Vocabulary)
	out.Sources = mergeSources(old.Sources, add.Sources)
	return out
}

// mergeSources складывает списки источников, не заводя второй такой же: тот же
// каталог, приложенный дважды, — это вдвое больше материала в справке и вдвое
// больший поход в Claude за тем же самым.
func mergeSources(old, add []core.Source) []core.Source {
	seen := map[string]bool{}
	out := make([]core.Source, 0, len(old)+len(add))
	for _, s := range append(append([]core.Source{}, old...), add...) {
		key := s.Kind + "\x00" + s.Value
		if strings.TrimSpace(s.Value) == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	return out
}

// projectIsBare — про проект не сказано ничего, кроме, может быть, названия.
// Только в этом случае есть смысл спрашивать: человек, назвавший хоть один
// флаг, уже сказал, что хотел, а остальное допишет там, где ему удобно.
func projectIsBare(p core.Project) bool {
	return p.About == "" && len(p.Aliases) == 0 && len(p.People) == 0 &&
		len(p.Vocabulary) == 0 && len(p.Sources) == 0
}

// askableStdin — есть ли на том конце человек, которому можно задать вопрос.
// Вопрос, заданный конвейеру, висит до конца времён, а `projects add` в скрипте
// развёртывания — обычное дело.
//
// Смотрим на оба конца, а не только на ввод. `steno projects add Платежи >
// log.txt` запущен из терминала, отвечать есть кому — но вопрос уходит в файл,
// человек видит пустой экран и убивает программу, решив, что она повисла.
//
// Переменной, а не вызовом на месте, чтобы тест мог прогнать разговор: у теста
// стдин всегда труба.
var askableStdin = func() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

// projectAsk — разговор при заведении проекта: по одному вопросу, пустой ответ
// пропускает. Приёмы те же, что в `steno setup`, но состояние своё: тащить
// сюда мастер установки целиком ради чтения пяти строк незачем.
type projectAsk struct {
	in  *bufio.Reader
	out io.Writer
	// Ввод кончился: Ctrl+D или закрытая труба. Дальше не спрашиваем вовсе —
	// иначе на экран вываливаются все оставшиеся вопросы разом, каждый с
	// пустым ответом.
	eof bool
}

func (a *projectAsk) line(question, hint string) string {
	if a.eof {
		return ""
	}
	if hint != "" {
		fmt.Fprintf(a.out, "  %s %s\n", question, dim("— "+hint))
		fmt.Fprint(a.out, "  ")
	} else {
		fmt.Fprintf(a.out, "  %s: ", question)
	}
	s, err := a.in.ReadString('\n')
	if err != nil {
		a.eof = true
		fmt.Fprintln(a.out)
	}
	return strings.TrimSpace(s)
}

// run задаёт вопросы. known — проект с таким именем уже заведён: тогда пустой
// ответ не стирает записанное, и сказать об этом надо до вопросов, а не после.
func (a *projectAsk) run(p *core.Project, known bool) {
	fmt.Fprintln(a.out)
	fmt.Fprintln(a.out, bold(i18n.Tr("Пара вопросов о проекте")))
	if known {
		fmt.Fprintln(a.out, dim(i18n.Tr("Такой проект уже заведён — допишем к нему. Пустой ответ ничего не сотрёт.")))
	} else {
		fmt.Fprintln(a.out, dim(i18n.Tr("Пустой ответ пропускает вопрос. Всё это правится потом: steno ui или панель.")))
	}
	fmt.Fprintln(a.out)

	if p.Name == "" {
		p.Name = a.line(i18n.Tr("Как называется проект"), "")
		if p.Name == "" {
			return // сохранять нечего, дальше спрашивать не о чем
		}
	}
	p.About = a.line(i18n.Tr("О чём он, одной строкой"),
		i18n.Tr("по ней модель отличает его от соседнего проекта"))
	p.Aliases = core.CommaList(a.line(i18n.Tr("Как его называют вслух"),
		i18n.Tr("через запятую: «биллинг», «платежи»")))
	// Главный вопрос из всех. Имена людей — то, чего нет ни в одном источнике
	// в пригодном виде: в git человек подписан логином, в календаре — тем, что
	// он однажды вписал в аккаунт, а на созвоне его зовут по имени.
	p.People = core.CommaList(a.line(i18n.Tr("Кто в нём участвует"),
		i18n.Tr("именами, которыми зовут на созвоне, а не подписью в git")))
	p.Vocabulary = core.CommaList(a.line(i18n.Tr("Какие сервисы и сокращения звучат вслух"),
		i18n.Tr("через запятую; чужие сервисы тоже — их в репозитории нет")))

	question, hint := i18n.Tr("Где лежит код или документы"),
		i18n.Tr("путь, ссылка на репозиторий или адрес сайта")
	for {
		v := a.line(question, hint)
		if v == "" {
			return
		}
		kind := guessSourceKind(v)
		if kind == "path" {
			v = brain.ExpandHome(v)
		}
		p.Sources = append(p.Sources, core.Source{Kind: kind, Value: v})
		question, hint = i18n.Tr("Ещё один источник"), i18n.Tr("пусто — хватит")
	}
}

// guessSourceKind различает репозиторий, адрес и каталог по самой строке.
// Отдельный вопрос «а это что?» человек читает как недоверие: он только что
// вставил ссылку на GitHub, и по ней всё видно.
func guessSourceKind(v string) string {
	switch {
	case strings.HasPrefix(v, "git@"), strings.HasPrefix(v, "ssh://"),
		strings.HasSuffix(v, ".git"):
		return "repo"
	case strings.HasPrefix(v, "http://"), strings.HasPrefix(v, "https://"):
		// Ссылка на сам репозиторий — это репозиторий: склонировать его
		// полезнее, чем прочитать одну его страницу. А вот ссылка вглубь
		// (issues, pull, wiki) — обычный адрес, клонировать по ней нечего.
		if u, err := url.Parse(v); err == nil && isGitHost(u.Host) &&
			len(strings.Split(strings.Trim(u.Path, "/"), "/")) == 2 {
			return "repo"
		}
		return "url"
	}
	return "path"
}

func isGitHost(host string) bool {
	switch strings.TrimPrefix(strings.ToLower(host), "www.") {
	case "github.com", "gitlab.com", "bitbucket.org", "codeberg.org":
		return true
	}
	return false
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
		return errors.New(i18n.Tr("какой проект удалить? steno projects rm <название>"))
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
	fmt.Printf(i18n.Tr("проект «%s» удалён; задачи и решения по нему остались\n"), name)
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
