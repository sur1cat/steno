package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Мастер установки — первое, что видит человек, развернувший steno. Если он
// врёт или молча теряет ответы, дальше уже неважно, насколько хорош остальной
// проект. Прогоняем его целиком на подставленном вводе.
func withStdin(t *testing.T, input string, fn func()) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old })

	go func() {
		_, _ = io.WriteString(w, input)
		w.Close()
	}()
	fn()
	r.Close()
}

func TestSetupWritesConfigAndSecrets(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "steno.json")

	// Небольшая команда · данные по умолчанию · Groq · ключ · Claude по ключу ·
	// ключ · opus · без календаря · без почты · Telegram да · токен · чат ·
	// follow-up в Telegram · без Docs · без Slack · панель · пароль.
	input := strings.Join([]string{
		"2", "", "1", "groq-ключ", "2", "sk-ant-ключ", "1",
		"n", "n", "y", "телеграм-токен", "-100500",
		"y", "n", "n",
		"127.0.0.1:9090", "пароль-панели",
	}, "\n") + "\n"

	withStdin(t, input, func() {
		if err := cmdSetup(context.Background(), []string{"-o", out}); err != nil {
			t.Fatalf("мастер сорвался: %v", err)
		}
	})

	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("мастер записал невалидный JSON: %v", err)
	}

	// Профиль «небольшая команда» должен был проставить пределы.
	if cfg.Calendar.MaxConcurrent != 2 || cfg.Transcribe.MaxConcurrent != 1 {
		t.Errorf("профиль не применился: созвонов %d, расшифровок %d",
			cfg.Calendar.MaxConcurrent, cfg.Transcribe.MaxConcurrent)
	}
	if cfg.Transcribe.Source != "command" || !strings.Contains(cfg.Transcribe.Cmd[0], "groq") {
		t.Errorf("Groq не выбрался: %+v", cfg.Transcribe)
	}
	if cfg.Claude.Model != "claude-opus-5" {
		t.Errorf("модель: %q", cfg.Claude.Model)
	}
	// Сервер обязан ходить по ключу: на нём некому выполнить вход в Claude Code,
	// и «auto» подобрал бы подписку разработчика, которой там нет.
	if cfg.Claude.Via != "api" {
		t.Errorf("сервер настроился не на ключ: %q", cfg.Claude.Via)
	}
	if !cfg.Telegram.Listen || cfg.Telegram.ChatID != "-100500" {
		t.Errorf("Telegram настроился не так: %+v", cfg.Telegram)
	}
	// Чат должен попасть и в список разрешённых: без него приём не стартует.
	if len(cfg.Telegram.AllowedChats) != 1 || cfg.Telegram.AllowedChats[0] != "-100500" {
		t.Errorf("чат не попал в разрешённые: %v", cfg.Telegram.AllowedChats)
	}
	if !cfg.Panel.Enabled || cfg.Panel.Addr != "127.0.0.1:9090" {
		t.Errorf("панель: %+v", cfg.Panel)
	}

	// Секреты — отдельным файлом с правами 0600 и ни одного в конфиге.
	envPath := filepath.Join(dir, ".env")
	st, err := os.Stat(envPath)
	if err != nil {
		t.Fatalf("не записан .env: %v", err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Errorf("права на .env %o, ожидали 600", perm)
	}
	env, _ := os.ReadFile(envPath)
	for _, want := range []string{"GROQ_API_KEY=groq-ключ", "ANTHROPIC_API_KEY=sk-ant-ключ",
		"TELEGRAM_BOT_TOKEN=телеграм-токен", "STENO_PANEL_PASSWORD=пароль-панели"} {
		if !strings.Contains(string(env), want) {
			t.Errorf("в .env нет %q", want)
		}
	}
	for _, secret := range []string{"groq-ключ", "sk-ant-ключ", "телеграм-токен", "пароль-панели"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("секрет %q утёк в конфиг — его кладут в репозиторий", secret)
		}
	}
	// И .gitignore, чтобы .env не уехал первым же коммитом.
	gi, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil || !strings.Contains(string(gi), ".env") {
		t.Error("не создан .gitignore с .env")
	}
}

// Профиль «для себя»: субтитры Meet и подписка Claude. Установка, которой не
// нужно ни одного ключа и ни одного отдельного счёта.
func TestSetupPersonalProfile(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "steno.json")
	// Личный профиль · данные по умолчанию · субтитры · подписка Claude ·
	// sonnet · дальше всё «нет».
	input := strings.Join([]string{
		"1", "", "3", "1", "2",
		"n", "n", "n",
		"n", "n", "n",
		"", "",
	}, "\n") + "\n"

	withStdin(t, input, func() {
		if err := cmdSetup(context.Background(), []string{"-o", out}); err != nil {
			t.Fatalf("мастер сорвался: %v", err)
		}
	})
	raw, _ := os.ReadFile(out)
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Transcribe.Source != "captions" {
		t.Errorf("субтитры не выбрались: %q", cfg.Transcribe.Source)
	}
	if cfg.Claude.Model != "claude-sonnet-5" {
		t.Errorf("модель: %q", cfg.Claude.Model)
	}
	// Ради этого профиль и заводился: для себя никакой второй подписки не нужно,
	// follow-up идёт через ту, что уже оплачена. Ключ спрашивать нельзя — иначе
	// человек решит, что без него не заработает.
	if cfg.Claude.Via != "cli" {
		t.Errorf("личная установка не встала на подписку: %q", cfg.Claude.Via)
	}
	if strings.Contains(string(raw), "sk-ant") {
		t.Error("мастер всё-таки спросил ключ")
	}
	if cfg.Calendar.MaxConcurrent != 1 {
		t.Errorf("личный профиль не применился: %d", cfg.Calendar.MaxConcurrent)
	}
	// Пароль не введён — должен сгенерироваться, а не остаться пустым.
	env, _ := os.ReadFile(filepath.Join(dir, ".env"))
	line := ""
	for _, l := range strings.Split(string(env), "\n") {
		if strings.HasPrefix(l, "STENO_PANEL_PASSWORD=") {
			line = strings.TrimPrefix(l, "STENO_PANEL_PASSWORD=")
		}
	}
	if len(line) < 8 {
		t.Errorf("пароль панели не сгенерирован: %q", line)
	}
}

// Уже заданное окружение главнее файла: в проде переменные приходят от systemd
// или docker, и .env не должен их перебивать.
func TestLoadDotEnvDoesNotOverrideEnvironment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path,
		[]byte("# комментарий\nA=из-файла\nB=\"в кавычках\"\n\nСЛОМАННАЯ СТРОКА\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("A", "из-окружения")
	if err := loadDotEnv(path); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("A"); got != "из-окружения" {
		t.Errorf("файл перебил окружение: %q", got)
	}
	if got := os.Getenv("B"); got != "в кавычках" {
		t.Errorf("кавычки не сняты: %q", got)
	}
}
