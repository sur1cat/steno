package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// steno doctor — предполётная проверка. Первый запуск сервиса упирается в
// десяток мелочей подряд: нет образа, пуста переменная, не читается ключ.
// Ловить их по одной, каждый раз заново заводя созвон, — это часы.

type check struct {
	name string
	// state: ok | fail | off
	state string
	note  string
	fix   []string
	// Без этого нельзя даже записать созвон.
	blocking bool
}

func cmdDoctor(args []string) error {
	fs := newFlagSet("doctor")
	cfgPath := setupFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, st, err := open(*cfgPath)
	if err != nil {
		return err
	}
	defer st.Close()

	var cs []check
	cs = append(cs, checkData(cfg))
	cs = append(cs, checkBot(cfg))
	cs = append(cs, checkTranscribe(cfg))
	cs = append(cs, checkClaude(cfg))
	cs = append(cs, checkSources(cfg)...)
	cs = append(cs, checkTargets(cfg)...)
	cs = append(cs, checkPanel(cfg))

	fmt.Printf("steno doctor · конфиг %s\n\n", orDash(*cfgPath))
	blocked, broken := false, false
	for _, c := range cs {
		mark := map[string]string{"ok": "✓", "fail": "✗", "off": "—"}[c.state]
		fmt.Printf("  %s %-16s %s\n", mark, c.name, c.note)
		for _, f := range c.fix {
			fmt.Printf("      %s\n", f)
		}
		if c.state == "fail" {
			broken = true
			if c.blocking {
				blocked = true
			}
		}
	}

	fmt.Println()
	if blocked {
		fmt.Println("Записать созвон пока нельзя — см. отмеченное выше.")
		return fmt.Errorf("проверка не пройдена")
	}
	if broken {
		fmt.Println("Записать созвон можно. Часть шагов после записи не отработает — см. выше.")
		return nil
	}
	fmt.Println("Всё готово.")
	return nil
}

func checkData(cfg *Config) check {
	probe := filepath.Join(cfg.DataDir, ".doctor")
	if err := os.WriteFile(probe, []byte("x"), 0o644); err != nil {
		return check{"данные", "fail", cfg.DataDir + " — нет записи", []string{
			"→ " + err.Error(),
		}, true}
	}
	os.Remove(probe)
	return check{"данные", "ok", cfg.DataDir, nil, true}
}

func checkBot(cfg *Config) check {
	if cfg.Bot.Local {
		var missing []string
		for _, bin := range []string{"chromium", "ffmpeg", "pactl"} {
			if _, err := exec.LookPath(bin); err != nil {
				missing = append(missing, bin)
			}
		}
		if _, err := exec.LookPath("google-chrome"); err == nil && len(missing) > 0 {
			missing = removeString(missing, "chromium")
		}
		if len(missing) > 0 {
			return check{"бот (local)", "fail",
				"не хватает: " + strings.Join(missing, ", "),
				[]string{"→ либо поставь их, либо убери bot.local — тогда бот пойдёт в docker"}, true}
		}
		return check{"бот (local)", "ok", "chromium, ffmpeg и pulseaudio на месте", nil, true}
	}

	if _, err := exec.LookPath("docker"); err != nil {
		return check{"бот (docker)", "fail", "docker не найден",
			[]string{"→ поставь Docker Desktop или задай bot.local = true"}, true}
	}
	out, err := exec.Command("docker", "image", "inspect", cfg.Bot.Image, "--format", "{{.Id}}").Output()
	if err != nil || len(out) == 0 {
		return check{"бот (docker)", "fail", "нет образа " + cfg.Bot.Image,
			[]string{"→ make bot-image"}, true}
	}
	return check{"бот (docker)", "ok", "образ " + cfg.Bot.Image + " на месте", nil, true}
}

func checkTranscribe(cfg *Config) check {
	if cfg.Transcribe.Source == "captions" {
		return check{"расшифровка", "ok",
			"из субтитров Meet — ставить ничего не нужно",
			[]string{"качество ниже whisper; для продакшена смени transcribe.source на command"}, false}
	}
	if len(cfg.Transcribe.Cmd) == 0 {
		return check{"расшифровка", "fail", "не задан transcribe.cmd",
			[]string{`→ или поставь "source": "captions", чтобы взять текст из субтитров Meet`}, false}
	}
	bin := cfg.Transcribe.Cmd[0]
	if _, err := exec.LookPath(bin); err != nil {
		return check{"расшифровка", "fail", "не запускается " + bin,
			[]string{
				"→ " + err.Error(),
				`→ или поставь "source": "captions" — текст возьмётся из субтитров Meet`,
			}, false}
	}
	// Найти файл мало. Адаптеру нужна модель на полгигабайта, и без неё он
	// падает — а doctor до этого рапортовал «ok». Поэтому прогоняем его
	// по-настоящему на полусекунде тишины: это проверяет и бинарник, и модель,
	// и то, что на выходе получается обещанный JSON.
	out, err := probeTranscriber(cfg)
	if err != nil {
		// Показываем хвост ошибки, а не начало: адаптер пишет туда, чего ему
		// не хватило и какой командой это ставится, — а начало занято
		// служебным «exit status 1».
		fix := []string{}
		for _, l := range lastLines(err.Error(), 5) {
			fix = append(fix, "→ "+l)
		}
		fix = append(fix, `→ или поставь "source": "captions" — текст возьмётся из субтитров Meet`)
		return check{"расшифровка", "fail", "адаптер не отработал", fix, false}
	}
	return check{"расшифровка", "ok", out, nil, false}
}

// probeTranscriber прогоняет адаптер на полусекунде тишины и проверяет, что он
// вернул обещанный JSON. Тишина — самый безобидный вход, а проверяется весь
// путь целиком.
func probeTranscriber(cfg *Config) (string, error) {
	dir, err := os.MkdirTemp("", "steno-doctor")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	wav := filepath.Join(dir, "silence.wav")
	if err := os.WriteFile(wav, silentWAV(time.Second/2), 0o644); err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	probe := *cfg
	probe.Transcribe.Timeout = Duration(3 * time.Minute)
	_, notes, err := runTranscriber(ctx, &probe, wav)
	if err != nil {
		return "", err
	}
	// Адаптер сообщает, какой моделью работал. Мелкая модель — рабочая, но на
	// русском созвоне выдаёт кашу, из которой Claude уверенно сочинит смысл,
	// которого не было. Это опаснее пустоты, и молчать об этом нельзя.
	if m := modelRe.FindStringSubmatch(notes); m != nil {
		if smallModelRe.MatchString(m[1]) {
			return "работает на " + m[1] + " — для русских созвонов мало, нужна large-v3", nil
		}
		return "адаптер отработал, модель " + m[1], nil
	}
	return "адаптер отработал на пробной записи", nil
}

var (
	modelRe      = regexp.MustCompile(`модель\s+(\S+)`)
	smallModelRe = regexp.MustCompile(`(?i)tiny|base|small`)
)

// silentWAV — 16 кГц моно PCM: ровно то, что адаптеры и ожидают на входе.
func silentWAV(d time.Duration) []byte {
	const rate = 16000
	samples := int(d.Seconds() * rate)
	data := samples * 2
	b := make([]byte, 0, 44+data)
	put32 := func(v uint32) {
		b = append(b, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
	}
	put16 := func(v uint16) { b = append(b, byte(v), byte(v>>8)) }

	b = append(b, "RIFF"...)
	put32(uint32(36 + data))
	b = append(b, "WAVEfmt "...)
	put32(16)
	put16(1) // PCM
	put16(1) // моно
	put32(rate)
	put32(rate * 2) // байт в секунду
	put16(2)        // выравнивание блока
	put16(16)       // бит на отсчёт
	b = append(b, "data"...)
	put32(uint32(data))
	return append(b, make([]byte, data)...)
}

// lastLines берёт последние непустые строки: у адаптера самое полезное — в
// конце, там он пишет, чего не хватает и как это поставить.
func lastLines(s string, n int) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

func checkClaude(cfg *Config) check {
	_, how, err := resolveVia(cfg)
	if err != nil {
		return check{"Claude", "fail", "нет доступа",
			[]string{
				"→ ключ API: console.anthropic.com → API keys, потом export " +
					cfg.Claude.APIKeyEnv + "=sk-ant-…",
				"→ либо подписка: поставь Claude Code и войди — steno возьмёт её через `claude -p`",
			}, false}
	}
	return check{"Claude", "ok",
		cfg.Claude.Model + ", effort " + orDash(cfg.Claude.Effort) + " · " + how, nil, false}
}

func checkSources(cfg *Config) []check {
	var cs []check
	if cfg.Calendar.Enabled {
		c := checkGoogleKey("календарь",
			credentialsFile(cfg.Calendar.CredentialsFile, cfg.GoogleDocs.CredentialsFile))
		if c.state == "ok" {
			if len(cfg.Calendar.Calendars) == 0 {
				c = check{"календарь", "fail", "не указан ни один календарь",
					[]string{"→ calendar.calendars: [\"ivan@example.com\", ...]"}, false}
			} else {
				c.note = fmt.Sprintf("%d календарей, ключ читается", len(cfg.Calendar.Calendars))
			}
		}
		cs = append(cs, c)
	} else {
		cs = append(cs, check{"календарь", "off", "выключен", nil, false})
	}

	if cfg.Gmail.Enabled {
		c := checkGoogleKey("почта бота",
			credentialsFile(cfg.Gmail.CredentialsFile, cfg.GoogleDocs.CredentialsFile))
		if c.state == "ok" && cfg.Gmail.Account == "" {
			c = check{"почта бота", "fail", "не указан gmail.account", nil, false}
		}
		cs = append(cs, c)
	} else {
		cs = append(cs, check{"почта бота", "off", "выключена", nil, false})
	}

	if cfg.Telegram.Listen {
		if len(cfg.Telegram.AllowedChats) == 0 && cfg.Telegram.ChatID == "" {
			cs = append(cs, check{"Telegram вход", "fail",
				"не задан ни chat_id, ни allowed_chats",
				[]string{"→ без этого сервис не стартует: принимать ссылки от кого угодно нельзя"}, false})
		} else {
			cs = append(cs, checkEnv("Telegram вход", cfg.Telegram.TokenEnv))
		}
	} else {
		cs = append(cs, check{"Telegram вход", "off", "выключен", nil, false})
	}

	if cfg.HTTP.Enabled {
		c := checkEnv("HTTP вход", cfg.HTTP.TokenEnv)
		if c.state == "ok" && os.Getenv(cfg.Slack.SigningSecretEnv) == "" {
			c.note += "; слэш-команды Slack не принимаются"
			c.fix = append(c.fix, "→ для них нужен "+cfg.Slack.SigningSecretEnv)
		}
		cs = append(cs, c)
	} else {
		cs = append(cs, check{"HTTP вход", "off", "выключен", nil, false})
	}
	return cs
}

func checkTargets(cfg *Config) []check {
	var cs []check
	if cfg.GoogleDocs.Enabled {
		c := checkGoogleKey("Google Docs", cfg.GoogleDocs.CredentialsFile)
		if c.state == "ok" && cfg.GoogleDocs.FolderID == "" {
			c.note += "; папка не указана — документы лягут в корень Drive"
		}
		cs = append(cs, c)
	} else {
		cs = append(cs, check{"Google Docs", "off", "выключен", nil, false})
	}
	if cfg.Slack.Enabled {
		c := checkEnv("Slack", cfg.Slack.TokenEnv)
		if c.state == "ok" && cfg.Slack.Channel == "" {
			c = check{"Slack", "fail", "не указан канал", nil, false}
		}
		cs = append(cs, c)
	} else {
		cs = append(cs, check{"Slack", "off", "выключен", nil, false})
	}
	if cfg.Telegram.Enabled {
		c := checkEnv("Telegram", cfg.Telegram.TokenEnv)
		if c.state == "ok" && cfg.Telegram.ChatID == "" {
			c = check{"Telegram", "fail", "не указан chat_id", nil, false}
		}
		cs = append(cs, c)
	} else {
		cs = append(cs, check{"Telegram", "off", "выключен", nil, false})
	}
	return cs
}

func checkPanel(cfg *Config) check {
	if !cfg.Panel.Enabled {
		return check{"панель", "off", "выключена", nil, false}
	}
	c := checkEnv("панель", cfg.Panel.PasswordEnv)
	if c.state == "ok" {
		c.note = "слушает " + cfg.Panel.Addr
	}
	return c
}

func checkEnv(name, env string) check {
	if env == "" {
		return check{name, "fail", "не указано имя переменной с секретом", nil, false}
	}
	if strings.TrimSpace(os.Getenv(env)) == "" {
		return check{name, "fail", "переменная " + env + " пуста",
			[]string{"→ export " + env + "=..."}, false}
	}
	return check{name, "ok", env + " задана", nil, false}
}

// checkGoogleKey читает ключ service-account и проверяет, что это он и есть.
// Самая частая ошибка здесь — скачать не тот JSON из консоли Google.
func checkGoogleKey(name, path string) check {
	if path == "" {
		return check{name, "fail", "не указан файл ключа service-account", nil, false}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return check{name, "fail", "ключ не читается", []string{"→ " + err.Error()}, false}
	}
	var key struct {
		Type        string `json:"type"`
		ClientEmail string `json:"client_email"`
	}
	if err := json.Unmarshal(raw, &key); err != nil {
		return check{name, "fail", "ключ — не JSON", []string{"→ " + err.Error()}, false}
	}
	if key.Type != "service_account" || key.ClientEmail == "" {
		return check{name, "fail", "это не ключ service-account",
			[]string{"→ в консоли Google: IAM → сервисные аккаунты → ключи → создать JSON"}, false}
	}
	return check{name, "ok", key.ClientEmail, nil, false}
}

func removeString(list []string, s string) []string {
	out := list[:0]
	for _, v := range list {
		if v != s {
			out = append(out, v)
		}
	}
	return out
}
