//go:build !windows

package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sur1cat/steno/internal/core"
	"golang.org/x/sys/unix"
)

// Фоновый режим проверяется настоящими процессами, а не подделками.
//
// Всё, что здесь важно, — про то, чего в одном процессе не увидеть: замок,
// который отпускает ядро; сигнал, который надо дождаться; pid, который система
// переиспользовала. Поэтому вторым экземпляром steno работает сам тестовый
// бинарник, запущенный отдельным процессом (TestDaemonHelperProcess), а не
// заглушка: заглушка не умеет ни падать, ни зависать, ни держать flock.

const helperEnv = "STENO_TEST_HELPER"

// TestDaemonHelperProcess — не тест, а дочерний процесс для остальных тестов.
// Ведёт себя как настоящий `steno serve`: берёт замок, пишет pid-файл, по
// SIGTERM ещё немного «дописывает запись» и выходит.
func TestDaemonHelperProcess(t *testing.T) {
	mode := os.Getenv(helperEnv)
	if mode == "" {
		t.Skip("вспомогательный процесс, отдельно не запускается")
	}
	p := daemonPaths{
		Dir: filepath.Dir(os.Getenv("STENO_TEST_PID")),
		PID: os.Getenv("STENO_TEST_PID"),
		Log: os.Getenv("STENO_TEST_LOG"),
	}
	// Сигнал перехватываем до того, как отметились в pid-файле, — как это
	// делает настоящий main. Иначе SIGTERM, пришедший в щель между «взял
	// замок» и «слушаю сигнал», убьёт процесс действием по умолчанию, и тесты
	// про ожидание записи станут мигающими. Один раз уже стали.
	ch := make(chan os.Signal, 1)
	if mode == "deaf" {
		// Делает вид, что идёт запись и уходить он не собирается.
		signal.Ignore(syscall.SIGTERM)
	} else {
		signal.Notify(ch, syscall.SIGTERM)
	}
	if err := holdDaemonLock(p); err != nil {
		fmt.Fprintln(os.Stderr, "дочерний: замок не взялся:", err)
		os.Exit(3)
	}
	fmt.Println("дочерний steno поднялся")
	if mode == "deaf" {
		time.Sleep(30 * time.Second)
		os.Exit(5)
	}
	select {
	case <-ch:
		if d, err := time.ParseDuration(os.Getenv("STENO_TEST_HOLD")); err == nil {
			time.Sleep(d) // «дописываем идущую запись»
		}
		fmt.Println("дочерний steno остановился")
		os.Exit(0)
	case <-time.After(30 * time.Second):
		os.Exit(4)
	}
}

// --- обвязка -----------------------------------------------------------------

// daemonTestEnv — изолированный каталог со своим HOME. Свой HOME обязателен:
// serve оставляет указатель в ~/.config/steno, и тест не должен трогать
// настоящий, иначе `steno stop` человека пойдёт искать демона в /tmp.
func daemonTestEnv(t *testing.T) (dir, cfgPath string, p daemonPaths) {
	t.Helper()
	dir = t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	cfgPath = filepath.Join(dir, "steno.json")
	cfg := `{"data_dir":` + quote(filepath.Join(dir, "data")) +
		`,"bot":{"shutdown_grace":"2s"}}`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	p = daemonPathsFor(cfgPath)
	if p.Dir != dir {
		t.Fatalf("служебные файлы легли не рядом с настройкой: %s", p.Dir)
	}
	t.Cleanup(releaseHeldLock)
	return dir, cfgPath, p
}

func releaseHeldLock() {
	heldLock.release()
	heldLock = nil
}

func capture(t *testing.T) *bytes.Buffer {
	t.Helper()
	var b bytes.Buffer
	prev := daemonOut
	daemonOut = &b
	t.Cleanup(func() { daemonOut = prev })
	return &b
}

func helperCommand(p daemonPaths, mode, hold string) *exec.Cmd {
	c := exec.Command(os.Args[0], "-test.run=TestDaemonHelperProcess")
	c.Env = append(os.Environ(),
		helperEnv+"="+mode,
		"STENO_TEST_PID="+p.PID,
		"STENO_TEST_LOG="+p.Log,
		"STENO_TEST_HOLD="+hold,
	)
	return c
}

// spawnHelper поднимает «уже запущенный steno» и ждёт, пока он отметится.
func spawnHelper(t *testing.T, p daemonPaths, mode, hold string) int {
	t.Helper()
	cmd := helperCommand(p, mode, hold)
	lf, err := openLog(p.Log)
	if err != nil {
		t.Fatal(err)
	}
	defer lf.Close()
	cmd.Stdout, cmd.Stderr = lf, lf
	cmd.Env = append(cmd.Env, daemonEnv+"=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Хороним ребёнка сами: незахороненный процесс остаётся зомби, а зомби
	// отвечает на kill(pid, 0) как живой — тесты про остановку зависли бы.
	go func() { _ = cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	pid := cmd.Process.Pid
	waitFor(t, "дочерний steno возьмёт замок", func() bool {
		info, state := inspectDaemon(p)
		return state == daemonRunning && info.PID == pid
	})
	return pid
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("не дождался: %s", what)
}

// deadPID — номер процесса, которого точно больше нет.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

func writePIDFile(t *testing.T, path string, info daemonInfo) {
	t.Helper()
	b, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// --- запуск ------------------------------------------------------------------

// Обычный `steno serve` тоже обязан брать замок. Иначе `serve` в терминале и
// `serve -d` в фоне сойдутся на одной базе, и на созвон придут два бота.
func TestServeTakesLock(t *testing.T) {
	_, cfgPath, p := daemonTestEnv(t)

	done, err := daemonize([]string{"-c", cfgPath})
	if err != nil {
		t.Fatalf("serve не запустился: %v", err)
	}
	if done {
		t.Fatal("serve без -d обязан работать в этом же процессе")
	}

	info, err := readDaemonInfo(p.PID)
	if err != nil {
		t.Fatalf("pid-файл не написан: %v", err)
	}
	if info.PID != os.Getpid() {
		t.Errorf("в pid-файле %d, а мы %d", info.PID, os.Getpid())
	}
	if info.Started.IsZero() {
		t.Error("не записано время запуска — status не покажет, с какого времени работает")
	}
	if info.Exe == "" {
		t.Error("не записан бинарник — по одному pid чужой процесс от своего не отличить")
	}
	if info.Background {
		t.Error("запуск в терминале помечен как фоновый")
	}
	if _, state := inspectDaemon(p); state != daemonRunning {
		t.Errorf("собственный запуск не опознан как живой: %v", state)
	}
	// Указатель — чтобы `steno stop` из другого каталога нашёл этот запуск.
	if got := rememberedDaemons(); len(got) == 0 || got[0].PID != p.PID {
		t.Errorf("запуск не отметился в указателе: %+v", got)
	}
}

// `steno status` из домашнего каталога должен находить работающий сервис, даже
// если после него запускали и останавливали другую установку.
func TestFindsRunningInstanceStartedElsewhere(t *testing.T) {
	dir, _, first := daemonTestEnv(t)

	second := filepath.Join(dir, "вторая-установка")
	if err := os.MkdirAll(second, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgB := filepath.Join(second, "steno.json")
	if err := os.WriteFile(cfgB,
		[]byte(`{"data_dir":`+quote(filepath.Join(second, "data"))+`}`), 0o644); err != nil {
		t.Fatal(err)
	}
	pB := daemonPathsFor(cfgB)

	pidA := spawnHelper(t, first, "graceful", "0s")
	pidB := spawnHelper(t, pB, "graceful", "0s")
	// Вторую останавливаем: она легла в указатель последней и заслоняет первую.
	if err := syscall.Kill(pidB, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "вторая установка остановится", func() bool {
		_, state := inspectDaemon(pB)
		return state != daemonRunning
	})

	// Ищем оттуда, где никакой настройки нет вовсе.
	_, info, state := findDaemon(filepath.Join(dir, "чужой-каталог", "steno.json"))
	if state != daemonRunning || info.PID != pidA {
		t.Fatalf("работающий steno (pid %d) не найден: %v, нашли pid %d", pidA, state, info.PID)
	}
}

// Второй запуск при живом первом. Ровно тот случай, ради которого всё это и
// делалось: два процесса на одной базе — испорченные данные.
func TestSecondServeRefusedWhileFirstAlive(t *testing.T) {
	_, cfgPath, p := daemonTestEnv(t)
	pid := spawnHelper(t, p, "graceful", "0s")

	err := holdDaemonLock(p)
	if err == nil {
		t.Fatal("второй steno запустился поверх живого — на созвон придут два бота")
	}
	if !errors.Is(err, errAlreadyRunning) {
		t.Fatalf("отказ не из-за запущенного экземпляра: %v", err)
	}
	if !strings.Contains(err.Error(), strconv.Itoa(pid)) {
		t.Errorf("в ошибке нет pid живого процесса, человеку нечего останавливать: %v", err)
	}
	if info, _ := readDaemonInfo(p.PID); info.PID != pid {
		t.Errorf("pid-файл перебит: %d вместо %d", info.PID, pid)
	}

	// И то же самое через настоящую точку входа `serve -d`: до запуска
	// дочернего процесса дело дойти не должно.
	spawned := false
	restoreChild := daemonChild
	daemonChild = func(exe string, args []string) *exec.Cmd {
		spawned = true
		return helperCommand(p, "graceful", "0s")
	}
	t.Cleanup(func() { daemonChild = restoreChild })

	done, err := daemonize([]string{"-c", cfgPath, "-d"})
	if !done {
		t.Error("serve -d вернул управление в serve")
	}
	if !errors.Is(err, errAlreadyRunning) {
		t.Errorf("serve -d при живом экземпляре: %v", err)
	}
	if spawned {
		t.Error("второй steno всё-таки запущен")
	}
}

// Несвежий pid-файл после падения не должен запирать запуск навсегда.
func TestStalePIDFileDoesNotBlockStart(t *testing.T) {
	_, cfgPath, p := daemonTestEnv(t)
	dead := deadPID(t)
	writePIDFile(t, p.PID, daemonInfo{
		PID: dead, Started: time.Now().Add(-time.Hour),
		Exe: "/usr/local/bin/steno", Log: p.Log, Background: true,
	})

	if info, state := inspectDaemon(p); state != daemonStale {
		t.Fatalf("файл от мёртвого процесса %d принят за живой: %v %+v", dead, state, info)
	}
	// То же самое там, где flock не работает (сетевые файловые системы): решать
	// должно то, что процесса с таким номером просто нет.
	func() {
		restore := lockHeld
		lockHeld = func(string) (bool, bool) { return false, false }
		defer func() { lockHeld = restore }()
		if _, state := inspectDaemon(p); state != daemonStale {
			t.Fatalf("без замка мёртвый процесс %d считается живым: %v", dead, state)
		}
	}()
	if _, err := daemonize([]string{"-c", cfgPath}); err != nil {
		t.Fatalf("несвежий pid-файл запер запуск навсегда: %v", err)
	}
	if info, _ := readDaemonInfo(p.PID); info.PID != os.Getpid() {
		t.Errorf("pid-файл не перехвачен: в нём %d", info.PID)
	}

	// И stop на таком файле должен сказать правду, а не сделать вид, что
	// остановил.
	releaseHeldLock()
	writePIDFile(t, p.PID, daemonInfo{PID: dead, Exe: "/usr/local/bin/steno"})
	out := capture(t)
	if err := stopDaemon(cfgPath, time.Second); err != nil {
		t.Fatalf("stop на несвежем файле: %v", err)
	}
	if !strings.Contains(out.String(), "не запущен") {
		t.Errorf("stop не сказал, что останавливать нечего: %q", out.String())
	}
}

// Номер процесса система переиспользует. Через сутки после падения steno тот же
// номер носит чужая программа — и `steno stop` не имеет права слать ей SIGTERM.
//
// Замок здесь не спасает: делаем вид, что файловая система его не поддерживает
// (так бывает на сетевых), и смотрим, что решает опознание процесса.
func TestForeignProcessWithReusedPIDIsNotOurs(t *testing.T) {
	_, cfgPath, p := daemonTestEnv(t)

	sleeper := exec.Command("sleep", "30")
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = sleeper.Wait() }()
	t.Cleanup(func() { _ = sleeper.Process.Kill() })
	foreign := sleeper.Process.Pid

	writePIDFile(t, p.PID, daemonInfo{
		PID: foreign, Started: time.Now().Add(-time.Hour),
		Exe: "/usr/local/bin/steno", Log: p.Log, Background: true,
	})
	restore := lockHeld
	lockHeld = func(string) (bool, bool) { return false, false } // «flock тут не работает»
	t.Cleanup(func() { lockHeld = restore })

	if !processAlive(foreign) {
		t.Fatal("подопытный процесс не запустился")
	}
	if info, state := inspectDaemon(p); state != daemonStale {
		t.Fatalf("чужой процесс %d принят за steno: %v %+v", foreign, state, info)
	}

	out := capture(t)
	if err := stopDaemon(cfgPath, time.Second); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if !processAlive(foreign) {
		t.Fatal("steno stop убил чужой процесс, которому просто достался наш номер")
	}
	if !strings.Contains(out.String(), "не запущен") {
		t.Errorf("stop не объяснил, что происходит: %q", out.String())
	}
}

func TestProcessLooksLike(t *testing.T) {
	sleeper := exec.Command("sleep", "30")
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = sleeper.Wait() }()
	t.Cleanup(func() { _ = sleeper.Process.Kill() })

	if processLooksLike(sleeper.Process.Pid, "/usr/local/bin/steno") {
		t.Error("sleep опознан как steno")
	}
	if !processLooksLike(os.Getpid(), os.Args[0]) {
		t.Errorf("собственный процесс не опознан: %s vs %s",
			processName(os.Getpid()), os.Args[0])
	}
	// Старый pid-файл без имени бинарника: судить не по чему, решает замок.
	if !processLooksLike(sleeper.Process.Pid, "") {
		t.Error("пустое имя должно означать «не знаю», а не «чужой»")
	}
	for _, c := range []struct {
		got, want string
		ok        bool
	}{
		{"/opt/homebrew/bin/steno", "/usr/local/Cellar/steno/0.1.2/bin/steno", true},
		{"steno", "steno", true},
		{"sleep", "steno", false},
		{"steno.test", "steno", false},
		// Linux обрезает /proc/<pid>/comm до 15 символов.
		{"steno-very-long", "steno-very-long-name", true},
	} {
		if got := sameProcessName(c.got, c.want); got != c.ok {
			t.Errorf("sameProcessName(%q, %q) = %v", c.got, c.want, got)
		}
	}
}

// --- остановка ---------------------------------------------------------------

// stop обязан дождаться, пока сервис допишет идущие записи. Вернуть управление
// раньше — значит соврать: человек выключит ноутбук на середине созвона.
func TestStopWaitsForRecordings(t *testing.T) {
	_, cfgPath, p := daemonTestEnv(t)
	prevPoll := stopPollInterval
	stopPollInterval = 20 * time.Millisecond
	t.Cleanup(func() { stopPollInterval = prevPoll })

	pid := spawnHelper(t, p, "graceful", "400ms")
	out := capture(t)

	start := time.Now()
	if err := stopDaemon(cfgPath, 10*time.Second); err != nil {
		t.Fatalf("stop: %v", err)
	}
	took := time.Since(start)
	if took < 350*time.Millisecond {
		t.Fatalf("stop вернулся через %s — он не стал ждать, пока допишется запись", took)
	}
	if !strings.Contains(out.String(), "steno остановлен") {
		t.Errorf("stop не сказал, чем кончилось: %q", out.String())
	}
	waitFor(t, "процесс исчезнет", func() bool { return !processAlive(pid) })
	if _, state := inspectDaemon(p); state == daemonRunning {
		t.Error("после stop экземпляр всё ещё считается живым")
	}
}

// Если сервис не вышел за отведённое время, добивать его сами мы не вправе:
// SIGKILL посреди созвона — это потерянный час разговора.
func TestStopDoesNotKillOnTimeout(t *testing.T) {
	_, cfgPath, p := daemonTestEnv(t)
	prevPoll := stopPollInterval
	stopPollInterval = 20 * time.Millisecond
	t.Cleanup(func() { stopPollInterval = prevPoll })

	pid := spawnHelper(t, p, "deaf", "")
	capture(t)

	err := stopDaemon(cfgPath, 300*time.Millisecond)
	if err == nil {
		t.Fatal("stop отчитался об остановке, хотя процесс не вышел")
	}
	if !strings.Contains(err.Error(), "kill -9") {
		t.Errorf("человеку не сказали, чем добить: %v", err)
	}
	// Смотрим на замок, а не только на «процесс есть»: убитый процесс ядро
	// сначала оставляет зомби, и kill(pid, 0) отвечает на него как на живой —
	// проверка «жив ли» была бы мигающей. Замок ядро отпускает сразу.
	if _, state := inspectDaemon(p); state != daemonRunning {
		t.Fatalf("stop добил процесс сам — идущая запись оборвалась бы (%v)", state)
	}
	if !processAlive(pid) {
		t.Fatal("stop добил процесс сам — идущая запись оборвалась бы")
	}
}

// --- status ------------------------------------------------------------------

func TestStatusRunning(t *testing.T) {
	_, cfgPath, p := daemonTestEnv(t)
	pid := spawnHelper(t, p, "graceful", "0s")
	info, err := readDaemonInfo(p.PID)
	if err != nil {
		t.Fatal(err)
	}

	// Один созвон до запуска, один после: status должен различать их.
	cfg, err := core.LoadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	st, err := core.OpenStore(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []*core.Meeting{
		{ID: "old", Title: "прошлый", StartedAt: info.Started.Add(-2 * time.Hour), Status: "recorded"},
		{ID: "new", Title: "этот", StartedAt: info.Started.Add(time.Minute), Status: "recorded"},
	} {
		if err := st.CreateMeeting(m); err != nil {
			t.Fatal(err)
		}
	}
	st.Close()

	out := capture(t)
	if err := printDaemonStatus(cfgPath); err != nil {
		t.Fatalf("status: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"steno работает",
		strconv.Itoa(pid),
		"1 с этого запуска, 2 всего",
		p.Log,
		cfgPath,
		"steno stop",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("в status нет %q:\n%s", want, got)
		}
	}
}

func TestStatusStopped(t *testing.T) {
	_, cfgPath, p := daemonTestEnv(t)
	out := capture(t)
	if err := printDaemonStatus(cfgPath); err != nil {
		t.Fatalf("status: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "не запущен") {
		t.Errorf("status на незапущенном steno: %q", got)
	}
	if !strings.Contains(got, "steno start") {
		t.Errorf("status не подсказал, как запустить: %q", got)
	}
	if strings.Contains(got, p.PID) {
		t.Errorf("status показал pid-файл, которого нет: %q", got)
	}
}

// --- serve -d ----------------------------------------------------------------

// Полный путь `steno serve -d`: отвязаться от терминала, написать лог, сказать
// pid и где лог, вернуть управление.
func TestBackgroundStartDetachesAndReports(t *testing.T) {
	_, cfgPath, p := daemonTestEnv(t)

	var childArgs []string
	prevExe, prevChild := daemonExecutable, daemonChild
	daemonExecutable = func() (string, error) { return os.Args[0], nil }
	daemonChild = func(exe string, args []string) *exec.Cmd {
		childArgs = args
		return helperCommand(p, "graceful", "0s")
	}
	t.Cleanup(func() { daemonExecutable, daemonChild = prevExe, prevChild })

	out := capture(t)
	done, err := daemonize([]string{"-c", cfgPath, "-d"})
	if err != nil {
		t.Fatalf("serve -d: %v", err)
	}
	if !done {
		t.Fatal("после ухода в фон родитель обязан вернуть управление, а не поднимать serve")
	}

	// Флаг фона до ребёнка доезжать не должен: иначе он уйдёт в фон ещё раз, и
	// так до бесконечности.
	if want := []string{"-c", cfgPath}; strings.Join(childArgs, " ") != strings.Join(want, " ") {
		t.Errorf("дочернему процессу передали %q, ждали %q", childArgs, want)
	}

	info, err := readDaemonInfo(p.PID)
	if err != nil {
		t.Fatalf("фоновый экземпляр не отметился: %v", err)
	}
	t.Cleanup(func() { _ = syscall.Kill(info.PID, syscall.SIGKILL) })

	got := out.String()
	for _, want := range []string{"в фоне", strconv.Itoa(info.PID), p.Log, "steno stop"} {
		if !strings.Contains(got, want) {
			t.Errorf("при запуске не сказали %q:\n%s", want, got)
		}
	}
	if !info.Background {
		t.Error("фоновый запуск не помечен фоновым — status покажет «в терминале»")
	}
	if info.Log != p.Log {
		t.Errorf("в pid-файле лог %q, а пишем в %q", info.Log, p.Log)
	}

	// Отвязка: свой сеанс, иначе Ctrl+C в терминале, из которого запустили,
	// убьёт фоновый steno вместе с окном.
	mine, err := unix.Getsid(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	his, err := unix.Getsid(info.PID)
	if err != nil {
		t.Fatalf("getsid(%d): %v", info.PID, err)
	}
	if mine == his {
		t.Errorf("фоновый процесс остался в сеансе терминала (sid %d)", his)
	}

	waitFor(t, "вывод дочернего процесса попадёт в лог", func() bool {
		b, _ := os.ReadFile(p.Log)
		return strings.Contains(string(b), "дочерний steno поднялся")
	})
}

// Если фоновый процесс сразу упал, «работает в фоне» печатать нельзя: человек
// узнает правду вечером, когда созвон не запишется.
func TestBackgroundStartReportsChildFailure(t *testing.T) {
	_, cfgPath, p := daemonTestEnv(t)
	prevExe, prevChild := daemonExecutable, daemonChild
	daemonExecutable = func() (string, error) { return os.Args[0], nil }
	daemonChild = func(exe string, args []string) *exec.Cmd {
		return exec.Command("/bin/sh", "-c", "echo 'нет настройки: steno.json' >&2; exit 1")
	}
	t.Cleanup(func() { daemonExecutable, daemonChild = prevExe, prevChild })

	out := capture(t)
	done, err := daemonize([]string{"-c", cfgPath, "-d"})
	if !done {
		t.Fatal("serve -d вернул управление в serve")
	}
	if err == nil {
		t.Fatal("падение фонового процесса выдали за успешный запуск")
	}
	if !strings.Contains(err.Error(), "нет настройки") {
		t.Errorf("в ошибке нет хвоста лога, по которому видно причину: %v", err)
	}
	if strings.Contains(out.String(), "работает в фоне") {
		t.Errorf("напечатали успех: %q", out.String())
	}
	if _, state := inspectDaemon(p); state == daemonRunning {
		t.Error("после падения экземпляр считается живым")
	}
}

// --- лог ---------------------------------------------------------------------

func TestLogRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "steno.log")
	f, err := openLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	first := strings.Repeat("созвон\n", 200) // заведомо больше предела ниже
	if _, err := f.WriteString(first); err != nil {
		t.Fatal(err)
	}
	const limit = 500

	cut, err := rotateLog(path, limit)
	if err != nil {
		t.Fatal(err)
	}
	if !cut {
		t.Fatalf("лог в %d байт не порезан при пределе %d", len(first), limit)
	}
	if got := mustRead(t, path+".1"); got != first {
		t.Errorf("старый лог потерян: %d байт вместо %d", len(got), len(first))
	}
	if n := fileSize(path); n != 0 {
		t.Errorf("после ротации в логе %d байт", n)
	}

	// Главное: уже открытый файл продолжает писаться с начала. Без O_APPEND
	// здесь была бы дыра из нулей размером со старый лог.
	const after = "после ротации\n"
	if _, err := f.WriteString(after); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, path); got != after {
		t.Errorf("в логе %d байт вместо %d — запись пошла со старого смещения",
			len(got), len(after))
	}

	if cut, err := rotateLog(path, 1<<20); err != nil || cut {
		t.Errorf("маленький лог порезан: %v %v", cut, err)
	}
	if cut, err := rotateLog(filepath.Join(dir, "нет-такого.log"), 10); err != nil || cut {
		t.Errorf("несуществующий лог: %v %v", cut, err)
	}
}

// --- разбор аргументов -------------------------------------------------------

func TestCutDaemonFlag(t *testing.T) {
	for _, c := range []struct {
		in   []string
		out  []string
		want bool
	}{
		{[]string{"-d"}, []string{}, true},
		{[]string{"--daemon", "-c", "a.json"}, []string{"-c", "a.json"}, true},
		{[]string{"-c", "a.json", "-d"}, []string{"-c", "a.json"}, true},
		{[]string{"-c", "a.json"}, []string{"-c", "a.json"}, false},
		{nil, []string{}, false},
	} {
		got, bg := cutDaemonFlag(c.in)
		if bg != c.want || strings.Join(got, " ") != strings.Join(c.out, " ") {
			t.Errorf("cutDaemonFlag(%q) = %q, %v; ждали %q, %v", c.in, got, bg, c.out, c.want)
		}
	}
}

func TestConfigArg(t *testing.T) {
	t.Setenv("STENO_CONFIG", "")
	for _, c := range []struct {
		in   []string
		want string
	}{
		{[]string{"-c", "a.json"}, "a.json"},
		{[]string{"-c=a.json"}, "a.json"},
		{[]string{"--config", "b.json"}, "b.json"},
		{[]string{"-d"}, core.DefaultConfigPath},
		{nil, core.DefaultConfigPath},
	} {
		if got := configArg(c.in); got != c.want {
			t.Errorf("configArg(%q) = %q, ждали %q", c.in, got, c.want)
		}
	}
	t.Setenv("STENO_CONFIG", "/etc/steno/steno.json")
	if got := configArg(nil); got != "/etc/steno/steno.json" {
		t.Errorf("STENO_CONFIG не учтён: %q", got)
	}
}

// --- автозапуск --------------------------------------------------------------

func testAutostartSpec(dir string) autostartSpec {
	return autostartSpec{
		Label: autostartLabel,
		Exe:   "/opt/homebrew/bin/steno",
		// Каталог с & — чтобы поймать неэкранированный XML.
		Config: filepath.Join(dir, "R&D", "steno.json"),
		Dir:    filepath.Join(dir, "R&D"),
		Log:    filepath.Join(dir, "R&D", "steno.log"),
		Path:   "/opt/homebrew/bin:/usr/bin:/bin",
		Grace:  15 * time.Minute,
	}
}

func TestLaunchdPlist(t *testing.T) {
	s := testAutostartSpec(t.TempDir())
	got := launchdPlist(s)

	if err := xml.Unmarshal([]byte(got), new(struct {
		XMLName xml.Name `xml:"plist"`
	})); err != nil {
		t.Fatalf("plist невалидный, launchd его не примет: %v\n%s", err, got)
	}
	for _, want := range []string{
		"<string>" + s.Exe + "</string>",
		"<string>serve</string>",
		"<string>-c</string>",
		xmlEsc(s.Config),
		"<key>RunAtLoad</key>",
		"<key>SuccessfulExit</key>",
		"<key>PATH</key>",
		"/opt/homebrew/bin",
		daemonEnv,
		xmlEsc(s.Log),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("в plist нет %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "R&D") {
		t.Errorf("амперсанд не экранирован — launchd не прочитает файл:\n%s", got)
	}
	// launchd по умолчанию добивает процесс через 20 секунд после SIGTERM.
	// Идущая запись созвона на этом обрывается.
	sec := plistInt(t, got, "ExitTimeOut")
	if sec < int(s.Grace.Seconds()) {
		t.Errorf("ExitTimeOut %d сек меньше, чем сервис ждёт записи (%s)", sec, s.Grace)
	}
}

func plistInt(t *testing.T, plist, key string) int {
	t.Helper()
	_, rest, ok := strings.Cut(plist, "<key>"+key+"</key>")
	if !ok {
		t.Fatalf("в plist нет ключа %s:\n%s", key, plist)
	}
	_, rest, _ = strings.Cut(rest, "<integer>")
	v, _, _ := strings.Cut(rest, "</integer>")
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		t.Fatalf("%s: %v", key, err)
	}
	return n
}

func TestSystemdUnit(t *testing.T) {
	s := testAutostartSpec(t.TempDir())
	got := systemdUnit(s)
	for _, want := range []string{
		"ExecStart=" + s.Exe + " serve -c " + s.Config,
		"WorkingDirectory=" + s.Dir,
		"Environment=PATH=" + s.Path,
		"StandardOutput=append:" + s.Log,
		"KillSignal=SIGTERM",
		"WantedBy=default.target",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("в unit нет %q:\n%s", want, got)
		}
	}
	// TimeoutStopSec по умолчанию 90 секунд — меньше, чем ждёт сервис.
	line := ""
	for _, l := range strings.Split(got, "\n") {
		if strings.HasPrefix(l, "TimeoutStopSec=") {
			line = strings.TrimPrefix(l, "TimeoutStopSec=")
		}
	}
	n, err := strconv.Atoi(line)
	if err != nil {
		t.Fatalf("TimeoutStopSec: %q", line)
	}
	if n < int(s.Grace.Seconds()) {
		t.Errorf("TimeoutStopSec %d сек меньше, чем сервис ждёт записи (%s)", n, s.Grace)
	}
}

// Установка службы: файл на месте, система о нём знает, снятие всё убирает.
// Настоящие launchctl и systemctl подменены — заводить службу на машине, где
// идёт проверка, нельзя.
func TestAutostartOnOff(t *testing.T) {
	_, cfgPath, _ := daemonTestEnv(t)
	var calls []string
	prev := runService
	runService = func(name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil, nil
	}
	t.Cleanup(func() { runService = prev })
	capture(t)

	s, err := autostartTarget(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if autostartInstalled(s) {
		t.Fatal("служба уже стоит — тест не изолирован")
	}
	if err := autostartOn(cfgPath); err != nil {
		t.Fatalf("autostart on: %v", err)
	}
	body := mustRead(t, s.Unit)
	if !strings.Contains(body, cfgPath) {
		t.Errorf("служба запускается без -c: без него она возьмёт умолчания\n%s", body)
	}
	if !strings.Contains(body, s.Exe) {
		t.Errorf("в службе нет пути к бинарнику:\n%s", body)
	}
	if strings.Contains(body, " -d") || strings.Contains(body, "<string>-d</string>") {
		t.Errorf("служба запускает serve с -d: launchd/systemd решат, что процесс умер\n%s", body)
	}
	if !autostartInstalled(s) {
		t.Error("после установки служба не видна")
	}
	if len(calls) == 0 {
		t.Error("файл написан, но системе о нём не сказали — заработает только после перезагрузки")
	}
	if !strings.Contains(autostartLine(), "включ") {
		t.Errorf("status не видит автозапуск: %q", autostartLine())
	}

	calls = nil
	if err := autostartOff(); err != nil {
		t.Fatalf("autostart off: %v", err)
	}
	if _, err := os.Stat(s.Unit); !os.IsNotExist(err) {
		t.Errorf("файл службы остался: %v", err)
	}
	if len(calls) == 0 {
		t.Error("службу не выгрузили — она продолжит работать до перезагрузки")
	}
	if strings.Contains(autostartLine(), "включ") {
		t.Errorf("status всё ещё считает автозапуск включённым: %q", autostartLine())
	}
}

// Заведённая служба и запущенный руками экземпляр — те же два процесса на одной
// базе. Перед установкой запущенный отдаём службе.
func TestAutostartOnStopsRunningInstance(t *testing.T) {
	_, cfgPath, p := daemonTestEnv(t)
	prevPoll := stopPollInterval
	stopPollInterval = 20 * time.Millisecond
	prev := runService
	runService = func(name string, args ...string) ([]byte, error) { return nil, nil }
	t.Cleanup(func() { runService, stopPollInterval = prev, prevPoll })

	pid := spawnHelper(t, p, "graceful", "0s")
	capture(t)
	if err := autostartOn(cfgPath); err != nil {
		t.Fatalf("autostart on при запущенном steno: %v", err)
	}
	waitFor(t, "запущенный экземпляр остановится", func() bool { return !processAlive(pid) })
}
