package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Запуск при входе в систему.
//
// Фоновый режим переживает закрытое окно терминала, но не перезагрузку. Дальше
// человек либо каждое утро набирает `steno serve -d`, либо однажды забывает — и
// узнаёт об этом по ненаписанному follow-up. Поэтому службу заводит сама
// программа: на macOS это launchd, на Linux — systemd user unit.
//
// Служба запускает `steno serve` без -d: и launchd, и systemd хотят видеть
// процесс в переднем плане, иначе они решат, что он умер, и будут перезапускать
// его без конца.

const autostartLabel = "com.github.sur1cat.steno"

type autostartSpec struct {
	Label   string
	Exe     string
	Config  string
	Dir     string        // рабочий каталог службы
	Log     string        // куда служба пишет вывод
	Path    string        // PATH: без него служба не найдёт docker
	Grace   time.Duration // сколько дать на дописывание записей при остановке
	Unit    string        // путь к plist или unit-файлу
	Manager string        // launchd | systemd
}

// runService — запуск launchctl и systemctl. Переменная ради тестов: заводить
// настоящую службу на машине, где идёт проверка, нельзя.
var runService = func(name string, args ...string) ([]byte, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return out, err
}

func cmdAutostart(args []string) error {
	fs := newFlagSet("autostart")
	cfgPath := setupFlags(fs)
	rest, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	verb := ""
	if len(rest) > 0 {
		verb = strings.ToLower(strings.TrimSpace(rest[0]))
	}
	switch verb {
	case "", "status", "показать":
		return autostartStatus()
	case "on", "включить", "install", "enable":
		return autostartOn(*cfgPath)
	case "off", "выключить", "uninstall", "disable", "remove":
		return autostartOff()
	}
	return fmt.Errorf(tr("не понял «%s»: steno autostart on | off | status"), verb)
}

// autostartTarget собирает описание службы для этой машины.
func autostartTarget(cfgPath string) (autostartSpec, error) {
	exe, err := daemonExecutable()
	if err != nil {
		return autostartSpec{}, fmt.Errorf(tr("не нашёл собственный бинарник: %w"), err)
	}
	// Симлинки не разворачиваем нарочно: у поставленного через brew steno
	// /opt/homebrew/bin/steno ведёт внутрь каталога с номером версии, и
	// записанный в службу путь перестал бы существовать после brew upgrade.
	p := daemonPathsFor(cfgPath)
	grace := 15 * time.Minute
	if cfg, err := loadConfig(p.Config); err == nil && cfg.Bot.ShutdownGrace.D() > 0 {
		grace = cfg.Bot.ShutdownGrace.D()
	}
	s := autostartSpec{
		Label: autostartLabel, Exe: exe, Config: p.Config, Dir: p.Dir,
		Log: p.Log, Path: servicePath(), Grace: grace,
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return autostartSpec{}, err
	}
	switch runtime.GOOS {
	case "darwin":
		s.Manager = "launchd"
		s.Unit = filepath.Join(home, "Library", "LaunchAgents", s.Label+".plist")
	case "linux":
		s.Manager = "systemd"
		s.Unit = filepath.Join(home, ".config", "systemd", "user", "steno.service")
	default:
		return autostartSpec{}, errors.New(tr("автозапуск умею заводить только на macOS и Linux"))
	}
	return s, nil
}

// servicePath — PATH для службы.
//
// launchd даёт процессу /usr/bin:/bin:/usr/sbin:/sbin и больше ничего. docker
// лежит в /usr/local/bin или /opt/homebrew/bin, и без этой строки бот не
// поднимется — причём только после перезагрузки, когда человек уже уверен, что
// всё работает.
func servicePath() string {
	seen := map[string]bool{}
	var out []string
	add := func(dirs ...string) {
		for _, d := range dirs {
			if d == "" || seen[d] {
				continue
			}
			seen[d] = true
			out = append(out, d)
		}
	}
	add(filepath.SplitList(os.Getenv("PATH"))...)
	add("/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin")
	return strings.Join(out, ":")
}

func autostartOn(cfgPath string) error {
	s, err := autostartTarget(cfgPath)
	if err != nil {
		return err
	}
	// Запущенный руками экземпляр отдаём службе. Иначе она поднимется, упрётся
	// в замок, выйдет с ошибкой — и будет перезапускаться по кругу, засоряя лог.
	if _, _, state := findDaemon(cfgPath); state == daemonRunning {
		fmt.Fprintln(daemonOut, tr("steno уже работает — передаю его службе"))
		if err := stopDaemon(cfgPath, 0); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(s.Unit), 0o755); err != nil {
		return err
	}
	body := launchdPlist(s)
	if s.Manager == "systemd" {
		body = systemdUnit(s)
	}
	if err := os.WriteFile(s.Unit, []byte(body), 0o644); err != nil {
		return err
	}
	if err := loadService(s); err != nil {
		return err
	}
	fmt.Fprintln(daemonOut, tr("steno будет запускаться при входе в систему"))
	fmt.Fprintf(daemonOut, tr("  служба       %s\n"), s.Unit)
	fmt.Fprintf(daemonOut, tr("  лог          %s\n"), s.Log)
	fmt.Fprint(daemonOut, tr("  проверить    steno status\n"))
	fmt.Fprint(daemonOut, tr("  убрать       steno autostart off\n"))
	if s.Manager == "systemd" {
		// Без linger systemd гасит пользовательские службы при выходе из
		// сеанса: сервис, заведённый по ssh, умрёт вместе с сессией.
		fmt.Fprintln(daemonOut, dim(tr("  чтобы работало и без входа в систему:")))
		fmt.Fprintln(daemonOut, dim("    sudo loginctl enable-linger "+os.Getenv("USER")))
	}
	return nil
}

func autostartOff() error {
	s, err := autostartTarget(defaultConfigPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(s.Unit); err != nil {
		fmt.Fprintln(daemonOut, tr("автозапуск и так не заведён"))
		return nil
	}
	if err := unloadService(s); err != nil {
		return err
	}
	if err := os.Remove(s.Unit); err != nil && !os.IsNotExist(err) {
		return err
	}
	fmt.Fprintln(daemonOut, tr("steno больше не запускается при входе в систему"))
	fmt.Fprintln(daemonOut, tr("  запустить вручную:  steno start"))
	return nil
}

func autostartStatus() error {
	s, err := autostartTarget(defaultConfigPath)
	if err != nil {
		return err
	}
	if !autostartInstalled(s) {
		fmt.Fprintln(daemonOut, tr("автозапуск выключен"))
		fmt.Fprintln(daemonOut, tr("  включить:  steno autostart on"))
		return nil
	}
	fmt.Fprintf(daemonOut, tr("автозапуск включён (%s)\n"), s.Manager)
	fmt.Fprintf(daemonOut, tr("  служба     %s\n"), s.Unit)
	fmt.Fprint(daemonOut, tr("  выключить  steno autostart off\n"))
	return nil
}

func autostartInstalled(s autostartSpec) bool {
	_, err := os.Stat(s.Unit)
	return err == nil
}

// autostartLine — одна строка для `steno status`.
func autostartLine() string {
	s, err := autostartTarget(defaultConfigPath)
	if err != nil {
		return dim(tr("не умею на этой системе"))
	}
	if autostartInstalled(s) {
		return fmt.Sprintf(tr("включён (%s)"), s.Manager)
	}
	return tr("выключен  ") + dim("steno autostart on")
}

func loadService(s autostartSpec) error {
	if s.Manager == "systemd" {
		if out, err := runService("systemctl", "--user", "daemon-reload"); err != nil {
			return fmt.Errorf("systemctl daemon-reload: %s", serviceErr(out, err))
		}
		if out, err := runService("systemctl", "--user", "enable", "--now",
			filepath.Base(s.Unit)); err != nil {
			return fmt.Errorf("systemctl enable: %s", serviceErr(out, err))
		}
		return nil
	}
	target := "gui/" + strconv.Itoa(os.Getuid())
	// Старую копию снимаем молча: bootstrap на уже загруженную службу отвечает
	// «Service already loaded» и не перечитывает файл.
	_, _ = runService("launchctl", "bootout", target+"/"+s.Label)
	out, err := runService("launchctl", "bootstrap", target, s.Unit)
	if err == nil {
		return nil
	}
	// bootstrap появился в macOS 10.11; на всём, что старше, работает load -w.
	if out2, err2 := runService("launchctl", "load", "-w", s.Unit); err2 == nil {
		return nil
	} else {
		_ = out2
	}
	return fmt.Errorf("launchctl bootstrap: %s", serviceErr(out, err))
}

func unloadService(s autostartSpec) error {
	if s.Manager == "systemd" {
		if out, err := runService("systemctl", "--user", "disable", "--now",
			filepath.Base(s.Unit)); err != nil {
			return fmt.Errorf("systemctl disable: %s", serviceErr(out, err))
		}
		_, _ = runService("systemctl", "--user", "daemon-reload")
		return nil
	}
	target := "gui/" + strconv.Itoa(os.Getuid())
	if _, err := runService("launchctl", "bootout", target+"/"+s.Label); err == nil {
		return nil
	}
	// На старых системах — unload; ошибку не поднимаем: службы может уже не
	// быть в памяти, а файл мы всё равно удалим.
	_, _ = runService("launchctl", "unload", "-w", s.Unit)
	return nil
}

func serviceErr(out []byte, err error) string {
	s := strings.TrimSpace(string(out))
	if s == "" {
		return err.Error()
	}
	return tail(s, 300)
}

// launchdPlist собирает описание службы для macOS.
//
// ExitTimeOut — не украшение: по умолчанию launchd ждёт после SIGTERM двадцать
// секунд и добивает процесс SIGKILL. Идущая запись созвона на этом обрывается,
// и час разговора теряется ровно в момент перезагрузки, когда человек этого не
// видит. Ставим столько же, сколько сервис отвёл себе на дописывание, плюс две
// минуты на закрытие базы.
func launchdPlist(s autostartSpec) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" ` +
		`"http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString("<plist version=\"1.0\">\n<dict>\n")
	b.WriteString("  <key>Label</key>\n  <string>" + xmlEsc(s.Label) + "</string>\n")
	b.WriteString("  <key>ProgramArguments</key>\n  <array>\n")
	for _, a := range serviceArgv(s) {
		b.WriteString("    <string>" + xmlEsc(a) + "</string>\n")
	}
	b.WriteString("  </array>\n")
	b.WriteString("  <key>RunAtLoad</key>\n  <true/>\n")
	// KeepAlive только на падение: после `steno stop` служба обязана остаться
	// выключенной, иначе остановить её станет нечем.
	b.WriteString("  <key>KeepAlive</key>\n  <dict>\n" +
		"    <key>SuccessfulExit</key>\n    <false/>\n  </dict>\n")
	b.WriteString("  <key>ThrottleInterval</key>\n  <integer>60</integer>\n")
	b.WriteString("  <key>ExitTimeOut</key>\n  <integer>" +
		strconv.Itoa(int((s.Grace + 2*time.Minute).Seconds())) + "</integer>\n")
	b.WriteString("  <key>WorkingDirectory</key>\n  <string>" + xmlEsc(s.Dir) + "</string>\n")
	b.WriteString("  <key>StandardOutPath</key>\n  <string>" + xmlEsc(s.Log) + "</string>\n")
	b.WriteString("  <key>StandardErrorPath</key>\n  <string>" + xmlEsc(s.Log) + "</string>\n")
	b.WriteString("  <key>EnvironmentVariables</key>\n  <dict>\n")
	b.WriteString("    <key>PATH</key>\n    <string>" + xmlEsc(s.Path) + "</string>\n")
	// STENO_DAEMON включает присмотр за размером лога: файл открыл launchd, и
	// резать его больше некому.
	b.WriteString("    <key>" + daemonEnv + "</key>\n    <string>1</string>\n")
	b.WriteString("  </dict>\n</dict>\n</plist>\n")
	return b.String()
}

// systemdUnit собирает user unit для Linux.
//
// TimeoutStopSec по умолчанию 90 секунд — меньше, чем сервис ждёт идущие
// записи. Оставить как есть значит получить SIGKILL посреди созвона на каждой
// перезагрузке.
func systemdUnit(s autostartSpec) string {
	var b strings.Builder
	b.WriteString("[Unit]\n")
	b.WriteString(tr("Description=steno — заметки и follow-up с созвонов\n"))
	b.WriteString("After=network-online.target\n\n")
	b.WriteString("[Service]\n")
	b.WriteString("Type=simple\n")
	b.WriteString("ExecStart=" + strings.Join(quoteArgs(serviceArgv(s)), " ") + "\n")
	b.WriteString("WorkingDirectory=" + s.Dir + "\n")
	b.WriteString("Environment=PATH=" + s.Path + "\n")
	b.WriteString("Environment=" + daemonEnv + "=1\n")
	b.WriteString("StandardOutput=append:" + s.Log + "\n")
	b.WriteString("StandardError=append:" + s.Log + "\n")
	b.WriteString("KillSignal=SIGTERM\n")
	b.WriteString("TimeoutStopSec=" + strconv.Itoa(int((s.Grace + 2*time.Minute).Seconds())) + "\n")
	b.WriteString("Restart=on-failure\n")
	b.WriteString("RestartSec=60\n\n")
	b.WriteString("[Install]\n")
	b.WriteString("WantedBy=default.target\n")
	return b.String()
}

// serviceArgv — чем служба запускает steno. Путь к настройке обязателен: у
// службы нет текущего каталога человека, и без -c она взяла бы умолчания —
// выключенную панель и базу в чужом месте.
func serviceArgv(s autostartSpec) []string {
	argv := []string{s.Exe, "serve"}
	if s.Config != "" {
		argv = append(argv, "-c", s.Config)
	}
	return argv
}

func quoteArgs(argv []string) []string {
	out := make([]string, 0, len(argv))
	for _, a := range argv {
		if strings.ContainsAny(a, " \t\"'") {
			a = `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
		}
		out = append(out, a)
	}
	return out
}

func xmlEsc(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}
