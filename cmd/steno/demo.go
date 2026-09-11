package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
	"github.com/sur1cat/steno/internal/panel"
)

// steno demo — панель на правдоподобных данных, без настройки и без созвона.
//
// Первый вопрос у того, кто зашёл в README, — «как это выглядит», и ответ
// «поставь, настрой девять вопросов и дождись созвона» его теряет. Здесь
// ответ — одна команда: временная база, три-четыре созвона с задачами и
// решениями, расписание на пару дней и панель в браузере. По Ctrl+C всё
// исчезает: это витрина, а не рабочая база, и путать их не нужно.
func cmdDemo(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("demo", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:8423", i18n.Tr("адрес панели"))
	noOpen := fs.Bool("no-open", false, i18n.Tr("не открывать браузер"))
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}

	dir, err := os.MkdirTemp("", "steno-demo-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	st, err := core.OpenStore(dir)
	if err != nil {
		return err
	}
	defer st.Close()
	projects, n, err := seedDemo(st, i18n.UILang)
	if err != nil {
		return fmt.Errorf(i18n.Tr("демо-данные: %w"), err)
	}

	// Пароль у демо свой и всем известный: панель без пароля не поднимается,
	// а спрашивать его у человека, который просто хочет посмотреть, незачем.
	const pass = "demo"
	cfg := core.DefaultConfig()
	cfg.DataDir = dir
	cfg.Projects = projects
	cfg.Panel.Enabled = true
	cfg.Panel.Addr = *addr
	cfg.Panel.PasswordEnv = "STENO_DEMO_PASSWORD"
	if err := os.Setenv(cfg.Panel.PasswordEnv, pass); err != nil {
		return err
	}

	lg := log.New(os.Stderr, "", log.Ltime)
	p, err := panel.NewPanel(cfg, st, lg)
	if err != nil {
		return fmt.Errorf(i18n.Tr("панель: %w"), err)
	}
	url := demoURL(*addr)
	fmt.Printf(i18n.Tr("демо: %s, панель на %s, пароль %s. Ctrl+C — закрыть, данные исчезнут\n"),
		i18n.Plural(n, i18n.Tr("созвон"), i18n.Tr("созвона"), i18n.Tr("созвонов")), url, pass)
	if !*noOpen {
		// Браузер открывается чуть позже старта сервера: иначе первая
		// вкладка успевает получить «соединение отклонено».
		go func() {
			select {
			case <-time.After(400 * time.Millisecond):
				openBrowser(url)
			case <-ctx.Done():
			}
		}()
	}
	return p.Run(ctx)
}

// demoURL превращает адрес, на котором слушает панель, в тот, что можно
// открыть: «:8423» сам по себе в браузер не вставить.
func demoURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://" + addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// openBrowser — на маке open, на Linux xdg-open. Не вышло — не беда: адрес
// уже напечатан, человек откроет сам.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		if _, err := exec.LookPath("xdg-open"); err != nil {
			return
		}
		cmd = exec.Command("xdg-open", url)
	default:
		return
	}
	cmd.Stdout, cmd.Stderr = nil, nil
	if err := cmd.Start(); err != nil {
		return
	}
	go func() { _ = cmd.Wait() }()
}
