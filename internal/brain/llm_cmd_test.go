package brain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/sur1cat/steno/internal/core"
)

// Договор со скриптом модели — это обещание: опубликованную форму менять
// нельзя, не сломав чужие адаптеры. Поэтому проверяется он целиком и с обеих
// сторон: что именно steno кладёт в stdin и как понимает то, что вернулось.

// fakeAdapter кладёт на диск скрипт и возвращает конфиг, который его зовёт.
// Скрипт сохраняет полученный запрос рядом с собой — так тест видит, что
// доехало.
func fakeAdapter(t *testing.T, body string) (*core.Config, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("скрипты на sh — не для Windows")
	}
	dir := t.TempDir()
	got := filepath.Join(dir, "запрос.json")
	path := filepath.Join(dir, "adapter.sh")
	script := "#!/bin/sh\ncat > " + strconv.Quote(got) + "\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := core.DefaultConfig()
	cfg.Brain.Provider = core.ProviderCommand
	cfg.LLM.Cmd = []string{path}
	cfg.LLM.Model = "своя-модель"
	return cfg, got
}

// То, что steno кладёт скрипту в stdin. Промпт большой, поэтому он идёт именно
// туда, а не аргументом: расшифровка часового созвона в командную строку не
// влезает. Системная и пользовательская части при этом раздельны — склеить их
// значило бы лишить скрипт возможности разложить их по ролям.
func TestCmdAdapterGetsWholeRequestOnStdin(t *testing.T) {
	cfg, got := fakeAdapter(t, `echo '{"text":"{\"title\":\"готово\"}"}'`)

	big := strings.Repeat("расшифровка созвона, реплика за репликой. ", 4000)
	out, _, err := askViaCmd(context.Background(), cfg, "правила разбора", big, testSchema(), 16000)
	if err != nil {
		t.Fatalf("скрипт не отработал: %v", err)
	}
	if out != `{"title":"готово"}` {
		t.Errorf("ответ скрипта поехал: %q", out)
	}

	raw, err := os.ReadFile(got)
	if err != nil {
		t.Fatal(err)
	}
	var req struct {
		System    string         `json:"system"`
		User      string         `json:"user"`
		Schema    map[string]any `json:"schema"`
		Model     string         `json:"model"`
		MaxTokens int64          `json:"max_tokens"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("steno положил в stdin не JSON: %v", err)
	}
	if req.System != "правила разбора" {
		t.Errorf("системная часть: %q", req.System)
	}
	if req.User != big {
		t.Errorf("пользовательская часть доехала не целиком: %d байт из %d", len(req.User), len(big))
	}
	// Ради этого stdin и выбран: аргументом такое не послать.
	if len(raw) < 100_000 {
		t.Errorf("запрос подозрительно мал: %d байт", len(raw))
	}
	if req.Model != "своя-модель" || req.MaxTokens != 16000 {
		t.Errorf("модель и потолок: %q %d", req.Model, req.MaxTokens)
	}
	// Схема едет там же и целиком — иначе скрипту нечего передать модели.
	sent, _ := json.Marshal(req.Schema)
	want, _ := json.Marshal(testSchema())
	if string(sent) != string(want) {
		t.Errorf("схема доехала не та:\n  %s\n  %s", sent, want)
	}
}

// Без схемы поле обязано быть null, а не отсутствовать и не быть пустым
// объектом: по нему скрипт отличает «нужен JSON» от «нужен обычный текст», и
// пустой объект заставил бы Ollama требовать JSON там, где нужна справка.
func TestCmdAdapterGetsNullSchemaWithoutOne(t *testing.T) {
	cfg, got := fakeAdapter(t, `echo '{"text":"Проект про платежи."}'`)
	out, _, err := askViaCmd(context.Background(), cfg, "s", "u", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if out != "Проект про платежи." {
		t.Errorf("текст без схемы поехал: %q", out)
	}
	raw, _ := os.ReadFile(got)
	var req map[string]any
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}
	v, ok := req["schema"]
	if !ok {
		t.Fatal("поля schema нет вовсе — скрипту нечем отличить текст от JSON")
	}
	if v != nil {
		t.Errorf("без схемы там должен быть null, а лежит %#v", v)
	}
}

// Учёт: «usd»: 0 и отсутствие «usd» — разные вещи, и путать их дороже всего.
func TestCmdAdapterSpend(t *testing.T) {
	t.Run("сказал ноль — значит бесплатно", func(t *testing.T) {
		cfg, _ := fakeAdapter(t, `echo '{"text":"ok","usd":0,"usage":{"input":900,"output":40},"model":"llama3.1:8b"}'`)
		_, spend, err := askViaCmd(context.Background(), cfg, "s", "u", nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		if !spend.PriceKnown || spend.USD != 0 {
			t.Errorf("ноль от скрипта — это знание, а не пустота: %+v", spend)
		}
		if spend.Input != 900 || spend.Output != 40 {
			t.Errorf("токены: %+v", spend)
		}
		// Имя модели берём то, которое назвал скрипт: он один знает, чем считал.
		if spend.Model != "llama3.1:8b" {
			t.Errorf("модель: %q", spend.Model)
		}
	})

	t.Run("промолчал — значит не знаем", func(t *testing.T) {
		cfg, _ := fakeAdapter(t, `echo '{"text":"ok","usage":{"input":900,"output":40}}'`)
		_, spend, err := askViaCmd(context.Background(), cfg, "s", "u", nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		if spend.PriceKnown {
			t.Errorf("скрипт про деньги молчал, а steno решил, что знает: %+v", spend)
		}
		if !strings.Contains(spend.String(), "неизвестна") {
			t.Errorf("человек не увидит, что цена неизвестна: %q", spend.String())
		}
		// Не назвал модель — остаётся та, что просили.
		if spend.Model != "своя-модель" {
			t.Errorf("модель: %q", spend.Model)
		}
	})

	t.Run("цена из конфига, если скрипт молчит", func(t *testing.T) {
		cfg, _ := fakeAdapter(t, `echo '{"text":"ok","usage":{"input":1000000,"output":1000000}}'`)
		cfg.LLM.Prices = map[string]core.Price{"своя-модель": {Input: 1, Output: 2}}
		_, spend, err := askViaCmd(context.Background(), cfg, "s", "u", nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		if !spend.PriceKnown || spend.USD != 3 {
			t.Errorf("таблица цен не применилась: %+v", spend)
		}
	})
}

// Отказ должен читаться человеком: код возврата плюс stderr, а не молчание.
func TestCmdAdapterFailures(t *testing.T) {
	t.Run("ненулевой код", func(t *testing.T) {
		cfg, _ := fakeAdapter(t, "echo 'модель не скачана: ollama pull llama3.1' >&2\nexit 1")
		_, _, err := askViaCmd(context.Background(), cfg, "s", "u", nil, 0)
		if err == nil {
			t.Fatal("отказ скрипта проглочен")
		}
		if !strings.Contains(err.Error(), "ollama pull llama3.1") {
			t.Errorf("объяснение скрипта потерялось: %v", err)
		}
	})

	t.Run("мусор вместо JSON", func(t *testing.T) {
		cfg, _ := fakeAdapter(t, "echo 'здравствуйте'")
		_, _, err := askViaCmd(context.Background(), cfg, "s", "u", nil, 0)
		if err == nil || !strings.Contains(err.Error(), "не тот JSON") {
			t.Errorf("мусор принят за ответ: %v", err)
		}
	})

	t.Run("пустой ответ", func(t *testing.T) {
		cfg, _ := fakeAdapter(t, `echo '{"text":"  "}'`)
		_, _, err := askViaCmd(context.Background(), cfg, "s", "u", nil, 0)
		if err == nil || !strings.Contains(err.Error(), "пустой ответ") {
			t.Errorf("пустой ответ принят за ответ: %v", err)
		}
	})

	t.Run("скрипта нет", func(t *testing.T) {
		cfg := core.DefaultConfig()
		cfg.Brain.Provider = core.ProviderCommand
		cfg.LLM.Cmd = []string{"/такого/точно/нет.sh"}
		if ok, why := LLMCmdAvailable(cfg); ok {
			t.Fatal("несуществующий скрипт объявлен рабочим")
		} else if !strings.Contains(why, "нет.sh") {
			t.Errorf("не сказали, чего нет: %s", why)
		}
		if _, _, err := askViaCmd(context.Background(), cfg, "s", "u", nil, 0); err == nil {
			t.Error("запуск несуществующего скрипта прошёл")
		}
	})

	t.Run("llm.cmd не задан", func(t *testing.T) {
		cfg := core.DefaultConfig()
		cfg.Brain.Provider = core.ProviderCommand
		if _, _, err := askViaCmd(context.Background(), cfg, "s", "u", nil, 0); err == nil {
			t.Fatal("пустой llm.cmd принят")
		}
		if _, _, err := ResolveVia(cfg); err == nil || !strings.Contains(err.Error(), "llm.cmd") {
			t.Errorf("doctor не скажет, чего не хватает: %v", err)
		}
	})
}

// Ответ, обёрнутый в блок кода, — обычное дело у моделей послабее. Разбор не
// должен на этом падать, как не падает у остальных провайдеров.
func TestCmdAdapterUnwrapsCodeFence(t *testing.T) {
	cfg, _ := fakeAdapter(t, "printf '%s' '{\"text\":\"```json\\n{\\\"title\\\":\\\"в блоке\\\"}\\n```\"}'")
	out, _, err := askViaCmd(context.Background(), cfg, "s", "u", testSchema(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if out != `{"title":"в блоке"}` {
		t.Errorf("JSON не вынули из блока кода: %q", out)
	}
}

// Скрипт говорит с человеком на языке steno — как и адаптеры расшифровки.
func TestCmdAdapterGetsLanguage(t *testing.T) {
	cfg, _ := fakeAdapter(t, `echo "{\"text\":\"$STENO_LANG\"}"`)
	out, _, err := askViaCmd(context.Background(), cfg, "s", "u", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if out != "ru" {
		t.Errorf("STENO_LANG до скрипта не доехал: %q", out)
	}
}
