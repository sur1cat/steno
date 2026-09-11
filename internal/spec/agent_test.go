package spec

import (
	"strings"
	"testing"

	"github.com/sur1cat/steno/internal/core"
)

// Исполнитель идёт за выбором модели, а не прибит гвоздями к Claude. Человек,
// переведший разбор на codex, не должен обнаружить, что задачи всё равно уезжают
// в Claude: он выбирал не «чем делать follow-up», а чем и за чей счёт steno
// разговаривает с моделью.
func TestWantAgentFollowsBrainProvider(t *testing.T) {
	cases := map[string]string{
		"":                  "claude",
		core.ProviderClaude: "claude",
		core.ProviderCodex:  "codex",
	}
	for provider, want := range cases {
		cfg := core.DefaultConfig()
		cfg.Brain.Provider = provider
		got, err := wantAgent(cfg, Settings{})
		if err != nil || got != want {
			t.Errorf("brain.provider=%q → %q, %v; ждали %q", provider, got, err, want)
		}
	}
}

// Ключ по HTTP и скрипт-адаптер отвечают текстом на текст: ни открыть файл, ни
// выполнить команду они не умеют. Тихо подставить сюда Claude было бы удобнее и
// стоило бы человеку денег на счёте, которого он не открывал.
func TestWantAgentRefusesTextOnlyProviders(t *testing.T) {
	for _, provider := range []string{core.ProviderOpenAI, core.ProviderCommand} {
		cfg := core.DefaultConfig()
		cfg.Brain.Provider = provider
		got, err := wantAgent(cfg, Settings{})
		if err == nil {
			t.Fatalf("brain.provider=%q дал исполнителя %q вместо отказа", provider, got)
		}
		if !strings.Contains(err.Error(), "agent.provider") {
			t.Errorf("отказ не говорит, что делать: %v", err)
		}
	}
}

// Явно названный исполнитель важнее провайдера разбора: следить за созвонами
// дешёвой местной моделью и исполнять Claude — это осмысленный набор, а не
// недоразумение.
func TestWantAgentOverrideWinsOverProvider(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Brain.Provider = core.ProviderOpenAI
	got, err := wantAgent(cfg, Settings{Provider: "claude"})
	if err != nil || got != "claude" {
		t.Fatalf("agent.provider не перекрыл brain.provider: %q, %v", got, err)
	}
}

func TestWantAgentRejectsUnknownName(t *testing.T) {
	if _, err := wantAgent(core.DefaultConfig(), Settings{Provider: "ollama"}); err == nil {
		t.Fatal("незнакомое имя исполнителя принято")
	}
}

// Пути к исполняемому файлу в настройках нет и быть не должно: такое поле,
// выставляемое через веб-форму панели, — это выполнение произвольного кода.
func TestSettingsHaveNoExecutablePath(t *testing.T) {
	raw := `{"agent":{"enabled":true,"provider":"claude","command":"/tmp/evil.sh",
		"cmd":["/tmp/evil.sh"],"bin":"/tmp/evil.sh"}}`
	dir := t.TempDir()
	path := dir + "/steno.json"
	write(t, path, raw)
	set := LoadSettings(path)
	if !set.Enabled || set.Provider != "claude" {
		t.Fatalf("раздел не прочитался: %+v", set)
	}
	// Ни одно поле Settings не должно было принять путь.
	if _, err := wantAgent(core.DefaultConfig(), set); err != nil {
		t.Fatalf("после чужих ключей выбор сломался: %v", err)
	}
}

func TestLoadSettingsDefaultsToOff(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/steno.json"
	write(t, path, `{"data_dir":"x"}`)
	if set := LoadSettings(path); set.Enabled {
		t.Fatal("конфиг без раздела agent включил исполнение")
	}
	if set := LoadSettings("/no/such/file"); set.Enabled {
		t.Fatal("отсутствующий конфиг включил исполнение")
	}
	if set := LoadSettings(""); set.Enabled {
		t.Fatal("пустой путь включил исполнение")
	}
}
