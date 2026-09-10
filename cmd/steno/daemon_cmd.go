package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
)

// Команды фонового режима: `serve -d`, `stop`, `status`.

const (
	// daemonEnv взводит родитель для дочернего процесса. По нему тот понимает,
	// что пишет в файл, а не в терминал, — и включает присмотр за размером лога.
	daemonEnv = "STENO_DAEMON"

	// Сколько ждать, пока фоновый steno отметится в pid-файле. Он успевает
	// открыть базу и подняться раньше; если не успел — человеку показывают
	// хвост лога, а не «запущено», за которым ничего нет.
	daemonStartWait = 10 * time.Second
)

var (
	// daemonOut — куда печатают stop, status и запуск в фоне. Переменная ради
	// тестов: проверять надо ровно то, что увидит человек.
	daemonOut io.Writer = os.Stdout

	// Обе переменные подменяются в тестах: настоящий дочерний процесс — это
	// docker, Chrome и созвоны, а проверить надо отвязку и pid-файл.
	daemonExecutable = os.Executable
	daemonChild      = func(exe string, args []string) *exec.Cmd {
		return exec.Command(exe, append([]string{"serve"}, args...)...)
	}

	stopPollInterval = 100 * time.Millisecond
)

// daemonize — хук в начале `steno serve`. Возвращает done=true, когда serve
// поднимать не надо: работу сделал родительский процесс, отправив в фон копию
// себя.
//
// Замок берётся в обоих случаях, и это главное. `steno serve` в терминале и
// `steno serve -d` в фоне — это два процесса на одной базе, и без замка человек
// узнал бы о них только по двум ботам на своём созвоне.
func daemonize(args []string) (bool, error) {
	// `steno serve --help` — это вопрос, а не запуск. Замок на него брать
	// нельзя: при работающем сервисе человек вместо справки получил бы «steno
	// уже работает».
	for _, a := range args {
		if a == "-h" || a == "--help" || a == "-help" {
			return false, nil
		}
	}
	rest, bg := cutDaemonFlag(args)
	p := daemonPathsFor(configArg(args))
	if !bg {
		return false, holdDaemonLock(p)
	}
	if !daemonSupported() {
		return true, errors.New(i18n.Tr("фоновый режим на этой системе не сделан; запусти `steno serve` в отдельном окне"))
	}
	return true, startBackground(p, rest)
}

// holdDaemonLock берёт замок на pid-файле и держит его до конца процесса.
func holdDaemonLock(p daemonPaths) error {
	exe, _ := daemonExecutable()
	bg := os.Getenv(daemonEnv) == "1"
	info := daemonInfo{
		PID: os.Getpid(), Started: time.Now(), Exe: exe,
		Config: p.Config, Background: bg, Version: core.StenoVersion(),
	}
	if bg {
		info.Log = p.Log
	}
	lock, err := acquireDaemonLock(p, info)
	if err != nil {
		if errors.Is(err, errAlreadyRunning) {
			return fmt.Errorf(i18n.Tr("%w\n  посмотреть, что с ним:  steno status\n")+
				i18n.Tr("  остановить:             steno stop"), err)
		}
		return err
	}
	heldLock = lock
	rememberDaemon(p)
	if bg {
		// Шапка отделяет запуски друг от друга: без неё в логе не видно, где
		// кончился прошлый и начался этот.
		fmt.Fprintf(os.Stderr, i18n.Tr("─── steno %s запущен %s, pid %d ───\n"),
			core.StenoVersion(), time.Now().Format("2006-01-02 15:04:05"), os.Getpid())
		go watchLogSize(make(chan struct{}), p.Log, logSizeLimit, logRotateEvery)
	}
	return nil
}

// startBackground запускает копию себя без терминала и дожидается, пока она
// действительно поднимется.
//
// Ждать обязательно. Молча напечатать «работает в фоне» и вернуть управление —
// значит соврать в половине случаев: не тот конфиг, занятый порт панели, нет
// docker. Человек увидел бы это только вечером, когда созвон не записался.
func startBackground(p daemonPaths, rest []string) error {
	if info, state := inspectDaemon(p); state == daemonRunning {
		return fmt.Errorf(i18n.Tr("%w: pid %d, запущен %s\n  посмотреть: steno status"),
			errAlreadyRunning, info.PID, info.Started.Local().Format("02.01 15:04"))
	}
	exe, err := daemonExecutable()
	if err != nil {
		return fmt.Errorf(i18n.Tr("не нашёл собственный бинарник: %w"), err)
	}
	// Режем лог до запуска: раз в жизни процесса это единственный момент, когда
	// файл точно никем не открыт.
	if _, err := rotateLog(p.Log, logSizeLimit); err != nil {
		return fmt.Errorf(i18n.Tr("лог %s: %w"), p.Log, err)
	}
	lf, err := openLog(p.Log)
	if err != nil {
		return fmt.Errorf(i18n.Tr("лог %s: %w"), p.Log, err)
	}
	defer lf.Close()

	cmd := daemonChild(exe, rest)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = lf, lf, nil
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}
	cmd.Env = append(cmd.Env, daemonEnv+"=1")
	detachChild(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf(i18n.Tr("не запустился фоновый steno: %w"), err)
	}

	// Wait в горутине — чтобы отличить «ещё поднимается» от «уже упал». Без
	// этого падение на первой секунде выглядело бы как молчаливый успех.
	gone := make(chan error, 1)
	go func() { gone <- cmd.Wait() }()

	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	deadline := time.After(daemonStartWait)
	for {
		select {
		case werr := <-gone:
			return fmt.Errorf(i18n.Tr("фоновый steno сразу вышел%s\n%s"),
				exitNote(werr), indent(logTail(p.Log, 12)))
		case <-tick.C:
			info, state := inspectDaemon(p)
			if state == daemonRunning && info.PID == cmd.Process.Pid {
				announceStarted(p, info)
				return nil
			}
		case <-deadline:
			return fmt.Errorf(i18n.Tr("фоновый steno (pid %d) не отметился за %s; смотри лог %s"),
				cmd.Process.Pid, daemonStartWait, p.Log)
		}
	}
}

func announceStarted(p daemonPaths, info daemonInfo) {
	fmt.Fprintln(daemonOut, i18n.Tr("steno работает в фоне"))
	fmt.Fprintf(daemonOut, "  pid          %d\n", info.PID)
	// Какая настройка взята — первым делом. Молчание тут однажды стоило часа:
	// указатель на настройку протух, steno поднялся на умолчаниях (без панели и
	// без Telegram), и выглядело это как успешный запуск.
	if info.Config != "" {
		fmt.Fprintf(daemonOut, i18n.Tr("  настройка    %s\n"), info.Config)
	} else {
		fmt.Fprintln(daemonOut, i18n.Tr("  настройка    ")+configLine(""))
	}
	fmt.Fprintf(daemonOut, i18n.Tr("  лог          %s\n"), p.Log)
	fmt.Fprintf(daemonOut, i18n.Tr("  посмотреть   tail -f %s\n"), p.Log)
	fmt.Fprint(daemonOut, i18n.Tr("  остановить   steno stop\n"))
}

// --- stop --------------------------------------------------------------------

func cmdStop(args []string) error {
	fs := newFlagSet("stop")
	cfgPath := setupFlags(fs)
	timeout := fs.Duration("timeout", 0,
		i18n.Tr("сколько ждать выхода; 0 — взять из bot.shutdown_grace"))
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	return stopDaemon(*cfgPath, *timeout)
}

// stopDaemon просит сервис остановиться и ждёт, пока он допишет созвоны.
//
// SIGTERM, а не SIGKILL: serve по этому сигналу перестаёт брать новые созвоны и
// ждёт идущие записи (по умолчанию до 15 минут). Убить его на середине — значит
// потерять час разговора, ради которого всё и затевалось. Поэтому и добивать
// сами не будем: если не вышел, покажем pid и скажем, чем добить, — это решение
// человека, а не наше.
func stopDaemon(cfgPath string, timeout time.Duration) error {
	p, info, state := findDaemon(cfgPath)
	switch state {
	case daemonStopped:
		fmt.Fprintln(daemonOut, i18n.Tr("steno не запущен"))
		return nil
	case daemonStale:
		// Не «что-то не так», а «останавливать нечего»: pid-файл после выхода
		// остаётся нарочно, по нему status показывает, чем кончился прошлый
		// запуск.
		fmt.Fprintln(daemonOut, i18n.Tr("steno не запущен"))
		if info.PID > 0 {
			fmt.Fprintf(daemonOut, i18n.Tr("  прошлый запуск (pid %d%s) уже закончился\n"),
				info.PID, startedAtText(info))
		}
		return nil
	}
	if timeout <= 0 {
		timeout = stopTimeout(p, info)
	}
	if err := signalStop(info.PID); err != nil {
		return fmt.Errorf(i18n.Tr("не отправился сигнал процессу %d: %w"), info.PID, err)
	}
	fmt.Fprintf(daemonOut, i18n.Tr("останавливаю steno (pid %d); жду, пока допишутся идущие записи (до %s)\n"),
		info.PID, core.ChDurText(core.Duration(timeout)))

	deadline := time.Now().Add(timeout)
	for {
		if _, st := inspectDaemon(p); st != daemonRunning {
			fmt.Fprintln(daemonOut, i18n.Tr("steno остановлен"))
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf(i18n.Tr("steno (pid %d) не вышел за %s — возможно, идёт запись\n")+
				i18n.Tr("  посмотреть: tail -f %s\n")+
				i18n.Tr("  добить:     kill -9 %d  (запись оборвётся)"),
				info.PID, timeout, p.Log, info.PID)
		}
		time.Sleep(stopPollInterval)
	}
}

// stopTimeout — сколько ждать выхода. Ровно столько, сколько сервис отвёл себе
// на дописывание записей, плюс минута на закрытие базы.
func stopTimeout(p daemonPaths, info daemonInfo) time.Duration {
	grace := 15 * time.Minute
	if cfg, err := core.LoadConfig(daemonConfig(p, info)); err == nil && cfg.Bot.ShutdownGrace.D() > 0 {
		grace = cfg.Bot.ShutdownGrace.D()
	}
	return grace + time.Minute
}

// --- status ------------------------------------------------------------------

func cmdStatus(args []string) error {
	fs := newFlagSet("status")
	cfgPath := setupFlags(fs)
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	return printDaemonStatus(*cfgPath)
}

func printDaemonStatus(cfgPath string) error {
	p, info, state := findDaemon(cfgPath)
	if state != daemonRunning {
		fmt.Fprintln(daemonOut, i18n.Tr("steno не запущен"))
		if state == daemonStale && info.PID > 0 {
			fmt.Fprintf(daemonOut, i18n.Tr("  прошлый запуск (pid %d%s) закончился\n"),
				info.PID, startedAtText(info))
		}
		if n := fileSize(p.Log); n > 0 {
			fmt.Fprintf(daemonOut, i18n.Tr("  лог          %s  (%s)\n"), p.Log, humanSize(n))
		}
		fmt.Fprintln(daemonOut, i18n.Tr("  настройка    ")+configLine(cfgPath))
		fmt.Fprintf(daemonOut, i18n.Tr("  запустить    steno start  %s\n"), dim(i18n.Tr("в фоне")))
		fmt.Fprintln(daemonOut, i18n.Tr("  автозапуск   ")+autostartLine())
		return nil
	}

	fmt.Fprintln(daemonOut, i18n.Tr("steno работает"))
	fmt.Fprintf(daemonOut, "  pid          %d%s\n", info.PID, core.OrEmpty(modeText(info), "  "+modeText(info)))
	if !info.Started.IsZero() {
		fmt.Fprintf(daemonOut, i18n.Tr("  запущен      %s  (%s назад)\n"),
			info.Started.Local().Format("02.01 15:04"), sinceText(time.Since(info.Started)))
	}
	fmt.Fprintln(daemonOut, i18n.Tr("  созвонов     ")+meetingsLine(p, info))
	if cfgFile := daemonConfig(p, info); cfgFile != "" {
		fmt.Fprintf(daemonOut, i18n.Tr("  настройка    %s\n"), cfgFile)
	}
	if info.Log != "" {
		fmt.Fprintf(daemonOut, i18n.Tr("  лог          %s  (%s)\n"), info.Log, humanSize(fileSize(info.Log)))
	} else {
		fmt.Fprintln(daemonOut, i18n.Tr("  лог          пишет в терминал, из которого запущен"))
	}
	if cfg, err := core.LoadConfig(daemonConfig(p, info)); err == nil && cfg.Panel.Enabled {
		fmt.Fprintf(daemonOut, i18n.Tr("  панель       http://%s\n"), cfg.Panel.Addr)
	}
	fmt.Fprintln(daemonOut, i18n.Tr("  автозапуск   ")+autostartLine())
	fmt.Fprintln(daemonOut, i18n.Tr("  остановить   steno stop"))
	return nil
}

// startedAtText — «, 09.09 17:53» или пусто, если времени в файле нет. Месяц
// цифрами: Go пишет названия месяцев только по-английски, а «9 Sep» посреди
// русского вывода читается как чужая строка.
func startedAtText(info daemonInfo) string {
	if info.Started.IsZero() {
		return ""
	}
	return ", " + info.Started.Local().Format("02.01 15:04")
}

func modeText(info daemonInfo) string {
	if info.Background {
		return dim(i18n.Tr("в фоне"))
	}
	return dim(i18n.Tr("в терминале"))
}

// meetingsLine — сколько созвонов записано с этого запуска и всего.
//
// База открывается на чтение параллельно с работающим сервисом: у неё включён
// WAL, читатель писателю не мешает. Если всё-таки не открылась — молчим про
// счётчик, но остальное показываем: status не должен падать из-за украшения.
func meetingsLine(p daemonPaths, info daemonInfo) string {
	cfg, err := core.LoadConfig(daemonConfig(p, info))
	if err != nil {
		return dim(i18n.Tr("не знаю: не читается настройка"))
	}
	st, err := core.OpenStore(cfg.DataDir)
	if err != nil {
		return dim(i18n.Tr("не знаю: не открылась база"))
	}
	defer st.Close()
	total, err := st.CountMeetings()
	if err != nil {
		return dim(i18n.Tr("не знаю: не читается база"))
	}
	if info.Started.IsZero() {
		return fmt.Sprintf(i18n.Tr("%d всего"), total)
	}
	since, err := st.CountMeetingsSince(info.Started)
	if err != nil {
		return fmt.Sprintf(i18n.Tr("%d всего"), total)
	}
	return fmt.Sprintf(i18n.Tr("%d с этого запуска, %d всего"), since, total)
}

// --- общее -------------------------------------------------------------------

// findDaemon ищет запущенный экземпляр: сначала там, куда указывает настройка,
// потом там, где отметился последний запуск. Второе — не прихоть: человек мог
// завести steno.json руками, запустить serve из того каталога, а `steno stop`
// набрать из домашнего. Отвечать ему «не запущен» при живом сервисе нельзя.
func findDaemon(cfgPath string) (daemonPaths, daemonInfo, runState) {
	p := daemonPathsFor(cfgPath)
	info, state := inspectDaemon(p)
	if state == daemonRunning {
		return p, info, state
	}
	for _, alt := range rememberedDaemons() {
		if alt.PID == p.PID {
			continue
		}
		if altInfo, altState := inspectDaemon(alt); altState == daemonRunning {
			return alt, altInfo, altState
		}
	}
	return p, info, state
}

// daemonConfig — с какой настройкой работает найденный экземпляр. Своё знание
// о путях спрашиваем у него самого: он мог быть запущен с другим -c.
func daemonConfig(p daemonPaths, info daemonInfo) string {
	if info.Config != "" {
		if _, err := os.Stat(info.Config); err == nil {
			return info.Config
		}
	}
	if p.Config != "" {
		if _, err := os.Stat(p.Config); err == nil {
			return p.Config
		}
	}
	return ""
}

func exitNote(err error) string {
	if err == nil {
		return ""
	}
	return ": " + err.Error()
}

// logTail — последние строки лога. Их печатают, когда фоновый запуск не удался:
// причина почти всегда в первых же строках, а отправлять человека читать файл,
// который steno только что написал сам, — лишний шаг.
func logTail(path string, lines int) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return ""
	}
	const window = 8 << 10
	off := st.Size() - window
	if off < 0 {
		off = 0
	}
	buf := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return ""
	}
	all := strings.Split(strings.TrimRight(string(buf), "\n"), "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return strings.Join(all, "\n")
}

func indent(s string) string {
	if s == "" {
		return ""
	}
	return "  " + strings.ReplaceAll(s, "\n", "\n  ")
}

// configLine — какая настройка будет взята, и честно, если никакой.
//
// Умолчания вместо настройки — не мелочь: без неё выключены и панель, и все
// источники, то есть steno запускается и не делает ничего. Человек при этом
// видит «работает».
func configLine(cfgPath string) string {
	if cfgPath == "" {
		cfgPath = core.DefaultConfigPath
	}
	p := core.ResolveConfigPath(cfgPath)
	if _, err := os.Stat(p); err != nil {
		return dim(i18n.Tr("не найдена — работаю на умолчаниях, без панели и источников; настроить:  steno setup"))
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}
