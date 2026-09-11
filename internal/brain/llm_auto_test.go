package brain

import (
	"strings"
	"testing"

	"github.com/sur1cat/steno/internal/core"
)

// auto обязан подхватывать ключ провайдера из окружения без единого вопроса —
// ради этого он и расширялся: человек с ключом Groq в профиле оболочки не
// должен настраивать «разбор» руками, чтобы наговорить первую заметку.
//
// Окружение подчищаем целиком: тест гоняют на машине, где Claude Code вошёл,
// а в ~/.config/anthropic может лежать профиль, — и то и другое стоит выше
// чужих ключей, и на такой машине auto честно выбрал бы Claude.
func noClaude(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir()) // нет команды claude — нет и входа
	t.Setenv("HOME", t.TempDir()) // нет профиля Anthropic
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	for _, k := range []string{"OPENAI_API_KEY", "GROQ_API_KEY", "OPENROUTER_API_KEY"} {
		t.Setenv(k, "")
	}
}

func TestAutoTakesProviderKeyFromEnvironment(t *testing.T) {
	noClaude(t)
	t.Setenv("GROQ_API_KEY", "gsk_тест")

	cfg := core.DefaultConfig()
	via, note, err := ResolveVia(cfg)
	if err != nil {
		t.Fatalf("auto с ключом Groq в окружении отказал: %v", err)
	}
	if via != viaOpenAI {
		t.Fatalf("auto выбрал %q, а не совместимый с OpenAI путь", via)
	}
	if !strings.Contains(note, "GROQ_API_KEY") {
		t.Errorf("в объяснении нет имени переменной: %q", note)
	}
	// Заголовок — тоже: «Claude» в doctor при работе через Groq отправляет
	// искать поломку не туда.
	if got := ProviderTitle(cfg); got != "Groq" {
		t.Errorf("провайдер назван %q, а отвечать будет Groq", got)
	}
	// А сам конфиг при этом не тронут: ResolveVia зовётся перед каждым
	// запросом и менять настройку не вправе.
	if cfg.BrainProvider() != core.ProviderClaude {
		t.Errorf("ResolveVia переписал провайдера в конфиге: %q", cfg.Brain.Provider)
	}

	// Кому нужен конфиг с ответом — зовёт ApplyAutoOpenAI и получает его:
	// модель для учёта расхода должна быть та, что отвечала.
	if _, ok := ApplyAutoOpenAI(cfg); !ok {
		t.Fatal("ApplyAutoOpenAI ничего не применил")
	}
	if cfg.BrainProvider() != core.ProviderOpenAI || cfg.Brain.OpenAI.Preset != "groq" {
		t.Errorf("после ApplyAutoOpenAI провайдер %q, заготовка %q",
			cfg.Brain.Provider, cfg.Brain.OpenAI.Preset)
	}
	if cfg.BrainModel() != "openai/gpt-oss-120b" {
		t.Errorf("модель не подставилась из заготовки: %q", cfg.BrainModel())
	}
}

// Приоритет: ключ OpenAI выше Groq, Groq выше OpenRouter; у OpenRouter модель
// подставляется из auto — у заготовки её нет, а без неё запрос не уйдёт.
func TestAutoKeyOrderAndOpenRouterModel(t *testing.T) {
	noClaude(t)
	t.Setenv("OPENROUTER_API_KEY", "sk-or-тест")
	t.Setenv("GROQ_API_KEY", "gsk_тест")

	eff, _, ok := AutoOpenAI(core.DefaultConfig())
	if !ok || eff.Brain.OpenAI.Preset != "groq" {
		t.Fatalf("при двух ключах взят не Groq: ok=%v %+v", ok, eff)
	}

	t.Setenv("GROQ_API_KEY", "")
	eff, _, ok = AutoOpenAI(core.DefaultConfig())
	if !ok || eff.Brain.OpenAI.Preset != "openrouter" {
		t.Fatalf("остался один OpenRouter, а взят не он: ok=%v %+v", ok, eff)
	}
	if eff.BrainModel() == "" {
		t.Error("для OpenRouter не подставлена модель — запрос без неё не уйдёт")
	}
}

// Ключ провайдера в окружении не перебивает Claude: GROQ_API_KEY лежит у
// многих ради расшифровки через Groq, и человек с ключом Anthropic не ждёт, что
// разбор молча переедет на другую модель.
func TestAutoPrefersClaudeOverProviderKeys(t *testing.T) {
	noClaude(t)
	t.Setenv("GROQ_API_KEY", "gsk_тест")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-тест")

	if _, _, ok := AutoOpenAI(core.DefaultConfig()); ok {
		t.Error("с ключом Anthropic auto всё равно ушёл на Groq")
	}
	via, _, err := ResolveVia(core.DefaultConfig())
	if err != nil || via != viaAPI {
		t.Errorf("с ключом Anthropic ждали api, получили %q, %v", via, err)
	}
}

// Названный провайдер и явный claude.via — не auto, и трогать их нельзя.
func TestAutoRespectsExplicitChoice(t *testing.T) {
	noClaude(t)
	t.Setenv("GROQ_API_KEY", "gsk_тест")

	cfg := core.DefaultConfig()
	cfg.Claude.Via = "cli"
	if _, _, ok := AutoOpenAI(cfg); ok {
		t.Error("claude.via=cli — выбор сделан, а auto его подменил")
	}
	cfg = core.DefaultConfig()
	cfg.Brain.Provider = core.ProviderCodex
	if _, _, ok := AutoOpenAI(cfg); ok {
		t.Error("brain.provider=codex — выбор сделан, а auto его подменил")
	}
	// И без единого ключа — честное «нет», а не выдуманный провайдер.
	t.Setenv("GROQ_API_KEY", "")
	if _, _, ok := AutoOpenAI(core.DefaultConfig()); ok {
		t.Error("ключей нет, а auto что-то выбрал")
	}
}
