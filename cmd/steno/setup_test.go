package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
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

	// Мастер спрашивает язык и переключает его на весь процесс. Тесты идут в
	// одном бинарнике, и без возврата выбор одного теста менял бы язык всем
	// остальным.
	lang := i18n.UILang
	t.Cleanup(func() { i18n.UILang = lang })

	go func() {
		_, _ = io.WriteString(w, input)
		w.Close()
	}()
	fn()
	r.Close()
}

func TestSetupWritesConfigAndSecrets(t *testing.T) {
	dir := t.TempDir()
	// Свой HOME: мастер запоминает путь к настройке в ~/.config/steno/path, и
	// без подмены тест затирал бы указатель того, кто гоняет тесты.
	t.Setenv("HOME", t.TempDir())
	out := filepath.Join(dir, "steno.json")

	// Русский · небольшая команда · данные по умолчанию · каталог верен ·
	// Groq · ключ · Claude по ключу · ключ · opus · без календаря · без почты ·
	// Telegram да · токен · чат · follow-up в Telegram · без Docs · без Slack ·
	// панель · пароль.
	input := strings.Join([]string{
		"2",
		"2", "", "", "1", "groq-ключ", "2", "sk-ant-ключ", "1",
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
	var cfg core.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("мастер записал невалидный JSON: %v", err)
	}

	if cfg.Lang != i18n.LangRU {
		t.Errorf("язык не записан в конфиг: %q", cfg.Lang)
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
	// Свой HOME: мастер запоминает путь к настройке в ~/.config/steno/path, и
	// без подмены тест затирал бы указатель того, кто гоняет тесты.
	t.Setenv("HOME", t.TempDir())
	out := filepath.Join(dir, "steno.json")
	// English · личный профиль · данные по умолчанию · каталог верен ·
	// субтитры · подписка Claude · sonnet · дальше всё «нет».
	input := strings.Join([]string{
		"1",
		"1", "", "", "3", "1", "2",
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
	var cfg core.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	// Английская установка: язык записан, и вместе с ним переехали умолчания,
	// которые от него зависят. «ru» в языке субтитров означал бы, что Meet
	// слушает английский созвон по-русски.
	if cfg.Lang != i18n.LangEN {
		t.Errorf("язык не записан в конфиг: %q", cfg.Lang)
	}
	if cfg.Bot.CaptionLanguage != i18n.LangEN {
		t.Errorf("язык субтитров остался %q", cfg.Bot.CaptionLanguage)
	}
	if cfg.Claude.OutputLanguage != "English" {
		t.Errorf("язык follow-up: %q", cfg.Claude.OutputLanguage)
	}
	for _, m := range cfg.Calendar.SkipMarkers {
		if strings.ContainsAny(m, "абвгдеёжзийклмнопрстуфхцчшщъыьэюя") {
			t.Errorf("русский маркер календаря в английской установке: %q", m)
		}
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

// Каталог показывается целиком до того, как его заведут.
//
// Ответ на этот вопрос — свободная строка, и промахнуться в ней легко:
// заготовленный список ответов съезжает на один, цифра от нумерованного
// вопроса попадает в «Каталог», и steno заводит ./1 рядом с собой. Именно так
// в корне репозитория появились каталоги 1/ и 3/, и месяц их никто не замечал.
//
// Ловит это только подтверждение: запретить «подозрительные» ответы нельзя,
// их все не угадаешь. Проверяем обе половины — что названный каталог не
// заводится, пока на него не сказали «да», и что отказ возвращает к вопросу.
func TestSetupShowsDataDirBeforeCreating(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	// ./1 завёлся бы от текущего каталога — то есть прямо в исходниках.
	t.Chdir(t.TempDir())
	out := filepath.Join(dir, "steno.json")

	// Русский · личный профиль · каталог «1» · нет, не верно · пусто
	// (умолчание) · верно · субтитры · подписка Claude · sonnet · дальше «нет».
	input := strings.Join([]string{
		"2",
		"1", "1", "n", "", "", "3", "1", "2",
		"n", "n", "n",
		"n", "n", "n",
		"", "",
	}, "\n") + "\n"

	withStdin(t, input, func() {
		if err := cmdSetup(context.Background(), []string{"-o", out}); err != nil {
			t.Fatalf("мастер сорвался: %v", err)
		}
	})

	if _, err := os.Stat("1"); err == nil {
		t.Error("каталог «1» всё-таки заведён — подтверждение ничего не остановило")
	}

	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var cfg core.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "data")
	if cfg.DataDir != want {
		t.Errorf("данные легли в %q, а ждали %q — отказ не вернул к вопросу", cfg.DataDir, want)
	}
}

// Установка на своей модели: ключей не нужно ни одного, счёта тоже.
//
// Это тот путь, ради которого провайдеров вообще заводили: попробовать steno,
// не заведя ни одного платного ключа. Проверяем, что мастер не спрашивает ключ
// и не оставляет в конфиге ничего, что потребует его потом.
func TestSetupLocalModelNeedsNoKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	out := filepath.Join(dir, "steno.json")

	// Русский · личный профиль · каталог по умолчанию · верно · субтитры ·
	// совместимый с OpenAI · Ollama · модель · дальше всё «нет» · панель.
	input := strings.Join([]string{
		"2",
		"1", "", "", "3",
		"3", "6", "llama3.3",
		"n", "n", "n",
		"n", "n", "n",
		"", "",
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
	var cfg core.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("мастер записал невалидный JSON: %v", err)
	}
	if cfg.BrainProvider() != core.ProviderOpenAI {
		t.Fatalf("провайдер: %q", cfg.BrainProvider())
	}
	if cfg.Brain.OpenAI.Preset != "ollama" {
		t.Errorf("заготовка: %q", cfg.Brain.OpenAI.Preset)
	}
	if cfg.BrainModel() != "llama3.3" {
		t.Errorf("модель: %q", cfg.BrainModel())
	}
	if !cfg.OpenAILocal() {
		t.Errorf("адрес %q не признан местным — расход показался бы неизвестным", cfg.OpenAIBaseURL())
	}
	// Ключа быть не должно нигде: ни имени переменной в конфиге, ни строки
	// в .env. Спросить его — значит убедить человека, что без ключа не выйдет.
	if cfg.OpenAIKeyEnv() != "" {
		t.Errorf("у местной модели зачем-то требуется ключ %q", cfg.OpenAIKeyEnv())
	}
	env, _ := os.ReadFile(filepath.Join(dir, ".env"))
	for _, bad := range []string{"OPENAI_API_KEY", "STENO_LLM_API_KEY", "ANTHROPIC_API_KEY"} {
		if strings.Contains(string(env), bad) {
			t.Errorf("в .env появился %s, хотя ключ не спрашивали:\n%s", bad, env)
		}
	}
}

// Свой адрес — обязателен: смысл этого пути в том, что нового провайдера не
// надо добавлять в steno. Заодно проверяем, что ключ к нему уезжает в .env, а
// не в конфиг, который кладут в репозиторий.
func TestSetupCustomOpenAIAddress(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	out := filepath.Join(dir, "steno.json")

	// Русский · личный профиль · каталог по умолчанию · верно · субтитры ·
	// совместимый с OpenAI · свой адрес · адрес · модель · ключ · дальше «нет».
	input := strings.Join([]string{
		"2",
		"1", "", "", "3",
		"3", "9", "https://llm.example.com/v1", "своя-модель", "секретный-ключ",
		"n", "n", "n",
		"n", "n", "n",
		"", "",
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
	var cfg core.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if got := cfg.OpenAIEndpoint(); got != "https://llm.example.com/v1/chat/completions" {
		t.Errorf("адрес запроса: %q", got)
	}
	if cfg.BrainModel() != "своя-модель" {
		t.Errorf("модель: %q", cfg.BrainModel())
	}
	if strings.Contains(string(raw), "секретный-ключ") {
		t.Error("ключ утёк в конфиг — его кладут в репозиторий")
	}
	env, _ := os.ReadFile(filepath.Join(dir, ".env"))
	if !strings.Contains(string(env), "STENO_LLM_API_KEY=секретный-ключ") {
		t.Errorf("ключ не попал в .env:\n%s", env)
	}
	if cfg.OpenAIKeyEnv() != "STENO_LLM_API_KEY" {
		t.Errorf("конфиг не знает, откуда брать ключ: %q", cfg.OpenAIKeyEnv())
	}
}

// Ключ Groq спрашивается один раз: он же нужен расшифровке, и второй вопрос
// про него либо получает пустоту и стирает первый ответ, либо заставляет
// вводить одно и то же дважды.
func TestSetupAsksGroqKeyOnce(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	out := filepath.Join(dir, "steno.json")

	// Русский · команда · каталог по умолчанию · верно · Groq + ключ ·
	// совместимый с OpenAI · Groq · модель · дальше «нет».
	input := strings.Join([]string{
		"2",
		"2", "", "", "1", "ключ-groq",
		"3", "2", "gpt-oss",
		"n", "n", "n",
		"n", "n", "n",
		"", "",
	}, "\n") + "\n"

	withStdin(t, input, func() {
		if err := cmdSetup(context.Background(), []string{"-o", out}); err != nil {
			t.Fatalf("мастер сорвался: %v", err)
		}
	})
	var cfg core.Config
	raw, _ := os.ReadFile(out)
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Brain.OpenAI.Preset != "groq" || cfg.BrainModel() != "gpt-oss" {
		t.Fatalf("Groq не выбрался: %+v", cfg.Brain.OpenAI)
	}
	env, _ := os.ReadFile(filepath.Join(dir, ".env"))
	if n := strings.Count(string(env), "GROQ_API_KEY="); n != 1 {
		t.Errorf("GROQ_API_KEY в .env %d раз:\n%s", n, env)
	}
	if !strings.Contains(string(env), "GROQ_API_KEY=ключ-groq") {
		t.Errorf("ключ Groq потерялся:\n%s", env)
	}
	// Если бы мастер спросил ключ второй раз, следующий ответ («n» на вопрос
	// про календарь) уехал бы в ключ, и вся вторая половина списка сдвинулась.
	if cfg.Panel.Addr == "" || cfg.Calendar.Enabled {
		t.Errorf("ответы сдвинулись: панель %q, календарь %v", cfg.Panel.Addr, cfg.Calendar.Enabled)
	}
}

// Подписка ChatGPT: провайдер записан, ключей не спрашивают.
func TestSetupCodexProvider(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	out := filepath.Join(dir, "steno.json")

	input := strings.Join([]string{
		"2",
		"1", "", "", "3",
		"4", "gpt-5-codex",
		"n", "n", "n",
		"n", "n", "n",
		"", "",
	}, "\n") + "\n"

	withStdin(t, input, func() {
		if err := cmdSetup(context.Background(), []string{"-o", out}); err != nil {
			t.Fatalf("мастер сорвался: %v", err)
		}
	})
	var cfg core.Config
	raw, _ := os.ReadFile(out)
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.BrainProvider() != core.ProviderCodex {
		t.Fatalf("провайдер: %q", cfg.BrainProvider())
	}
	if cfg.BrainModel() != "gpt-5-codex" {
		t.Errorf("модель: %q", cfg.BrainModel())
	}
	env, _ := os.ReadFile(filepath.Join(dir, ".env"))
	if strings.Contains(string(env), "API_KEY") {
		t.Errorf("подписка не должна требовать ключей:\n%s", env)
	}
}

// Первые два ответа на вопрос «чем платить» не должны меняться от того, что к
// ним дописали ещё два: у людей записаны установки «выбери 1» и «выбери 2», и
// перестановка молча переключила бы чью-то установку на другой способ платить.
func TestSetupKeepsFirstTwoPaymentAnswers(t *testing.T) {
	for _, tc := range []struct {
		answer, wantVia string
	}{{"1", "cli"}, {"2", "api"}} {
		t.Run(tc.answer, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("HOME", t.TempDir())
			out := filepath.Join(dir, "steno.json")
			answers := []string{"2", "1", "", "", "3", tc.answer}
			if tc.answer == "2" {
				answers = append(answers, "ключ-anthropic")
			}
			answers = append(answers, "1", "n", "n", "n", "n", "n", "n", "", "")
			withStdin(t, strings.Join(answers, "\n")+"\n", func() {
				if err := cmdSetup(context.Background(), []string{"-o", out}); err != nil {
					t.Fatalf("мастер сорвался: %v", err)
				}
			})
			var cfg core.Config
			raw, _ := os.ReadFile(out)
			if err := json.Unmarshal(raw, &cfg); err != nil {
				t.Fatal(err)
			}
			if cfg.BrainProvider() != core.ProviderClaude {
				t.Errorf("провайдер уехал: %q", cfg.BrainProvider())
			}
			if cfg.Claude.Via != tc.wantVia {
				t.Errorf("ответ %q дал via=%q, ждали %q", tc.answer, cfg.Claude.Via, tc.wantVia)
			}
		})
	}
}

// Модель через свой скрипт: провайдер записан, путь к скрипту — в конфиг, а не
// в панель. Это тот путь, ради которого steno перестаёт зависеть от списка
// поддерживаемых вендоров вовсе.
func TestSetupModelScript(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	out := filepath.Join(dir, "steno.json")

	script := filepath.Join(dir, "мой-llm.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Русский · личный профиль · каталог по умолчанию · верно · субтитры ·
	// свой скрипт · путь · модель · дальше всё «нет» · панель.
	input := strings.Join([]string{
		"2",
		"1", "", "", "3",
		"5", script, "своя-модель",
		"n", "n", "n",
		"n", "n", "n",
		"", "",
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
	var cfg core.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("мастер записал невалидный JSON: %v", err)
	}
	if cfg.BrainProvider() != core.ProviderCommand {
		t.Fatalf("провайдер: %q", cfg.BrainProvider())
	}
	if len(cfg.LLM.Cmd) != 1 || cfg.LLM.Cmd[0] != script {
		t.Errorf("скрипт: %v", cfg.LLM.Cmd)
	}
	if cfg.BrainModel() != "своя-модель" {
		t.Errorf("модель: %q", cfg.BrainModel())
	}
	// Ключей у этого пути не бывает: за них отвечает сам скрипт.
	env, _ := os.ReadFile(filepath.Join(dir, ".env"))
	if strings.Contains(string(env), "API_KEY") {
		t.Errorf("мастер зачем-то спросил ключ:\n%s", env)
	}
}
