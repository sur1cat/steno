package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Конфиг, написанный до появления провайдеров, обязан вести себя ровно как
// раньше. Это не пожелание: раздел "claude" написан руками у всех, кто уже
// поставил steno, и молча переехать на другого провайдера — это чужие созвоны,
// разобранные не тем и не так.
func TestOldConfigStaysOnClaude(t *testing.T) {
	old := `{
	  "data_dir": "./data",
	  "claude": {"api_key_env": "ANTHROPIC_API_KEY", "via": "cli",
	             "model": "claude-sonnet-5", "effort": "low", "max_tokens": 12000}
	}`
	path := writeTemp(t, old)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BrainProvider() != ProviderClaude {
		t.Errorf("старый конфиг уехал на %q", cfg.BrainProvider())
	}
	if cfg.BrainModel() != "claude-sonnet-5" {
		t.Errorf("модель: %q", cfg.BrainModel())
	}
	// Ручки, общие для всех провайдеров, остались там же, где были.
	if cfg.Claude.Via != "cli" || cfg.Claude.MaxTokens != 12000 {
		t.Errorf("настройки Claude поехали: via=%q max_tokens=%d", cfg.Claude.Via, cfg.Claude.MaxTokens)
	}
}

// Незнакомое значение provider — тоже claude, а не отказ работать. Опечатка в
// конфиге не должна оставлять созвон нерасшифрованным.
func TestUnknownProviderFallsBackToClaude(t *testing.T) {
	for _, v := range []string{"", "  ", "gemini", "CLAUDE", "OpenAI", "codex"} {
		cfg := DefaultConfig()
		cfg.Brain.Provider = v
		got := cfg.BrainProvider()
		want := ProviderClaude
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "openai":
			want = ProviderOpenAI
		case "codex":
			want = ProviderCodex
		}
		if got != want {
			t.Errorf("provider=%q → %q, ждали %q", v, got, want)
		}
	}
}

// Адрес: заготовка, свой base_url поверх неё и полный адрес как есть.
func TestOpenAIEndpointBuilding(t *testing.T) {
	cases := []struct {
		name    string
		preset  string
		base    string
		want    string
		wantKey string
	}{
		{"по заготовке", "groq", "", "https://api.groq.com/openai/v1/chat/completions", "GROQ_API_KEY"},
		{"свой адрес главнее", "openai", "https://proxy.example.com/v1",
			"https://proxy.example.com/v1/chat/completions", "OPENAI_API_KEY"},
		{"хвостовой слэш", "", "http://localhost:11434/v1/",
			"http://localhost:11434/v1/chat/completions", ""},
		{"полный адрес принимаем как есть", "", "https://x.example/v1/chat/completions",
			"https://x.example/v1/chat/completions", ""},
		{"без заготовки и без адреса", "", "", "", ""},
		{"заготовка custom ничего не подставляет", PresetCustom, "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Brain.OpenAI.Preset = tc.preset
			cfg.Brain.OpenAI.BaseURL = tc.base
			cfg.Brain.OpenAI.APIKeyEnv = ""
			if got := cfg.OpenAIEndpoint(); got != tc.want {
				t.Errorf("адрес запроса: %q, ждали %q", got, tc.want)
			}
			if got := cfg.OpenAIKeyEnv(); got != tc.wantKey {
				t.Errorf("переменная с ключом: %q, ждали %q", got, tc.wantKey)
			}
		})
	}
}

// Своё имя переменной перекрывает заготовку: один ключ на два сервиса — обычное
// дело, и заставлять человека звать его так, как решили мы, незачем.
func TestOpenAIKeyEnvOverride(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Brain.OpenAI.Preset = "groq"
	cfg.Brain.OpenAI.APIKeyEnv = "МОЙ_КЛЮЧ"
	if got := cfg.OpenAIKeyEnv(); got != "МОЙ_КЛЮЧ" {
		t.Errorf("своё имя переменной не победило: %q", got)
	}
}

// «Местная модель» — это про цену, а не про адрес, и ошибиться тут можно только
// в одну сторону: назвав чужой сервер своим, steno показал бы нулевой расход
// там, где счёт придёт.
func TestLocalURL(t *testing.T) {
	local := []string{
		"http://localhost:11434/v1", "http://127.0.0.1:1234/v1",
		"http://[::1]:8080/v1", "https://localhost/v1",
		"http://host.docker.internal:11434/v1", "http://127.2.3.4:9/v1",
	}
	remote := []string{
		"https://api.openai.com/v1", "https://api.groq.com/openai/v1",
		"http://192.168.1.10:11434/v1", // своя сеть — но не эта машина
		"http://10.0.0.5/v1", "http://ollama.local/v1",
		"http://localhost.example.com/v1", // чужой домен, начинающийся так же
		"", "не адрес вовсе",
	}
	for _, u := range local {
		if !LocalURL(u) {
			t.Errorf("%q не признали местным", u)
		}
	}
	for _, u := range remote {
		if LocalURL(u) {
			t.Errorf("%q приняли за местный — расход показался бы нулевым", u)
		}
	}
}

// Учёт там, где цены не знаем. Ноль у местной модели — правда; ноль у чужого
// провайдера — враньё, и лучше сказать «не знаю».
func TestSpendWithoutPriceStaysUnknown(t *testing.T) {
	remote := SpendWith(nil, false, "gpt-5", 1000, 200, 0, 0)
	if remote.PriceKnown {
		t.Error("цену чужой модели не знаем, а расход помечен известным")
	}
	if remote.USD != 0 {
		t.Errorf("выдумали цену: %v", remote.USD)
	}
	if !strings.Contains(remote.String(), "неизвестна") {
		t.Errorf("человек не увидит, что цена неизвестна: %q", remote.String())
	}

	local := SpendWith(nil, true, "llama3", 1000, 200, 0, 0)
	if !local.PriceKnown || local.USD != 0 {
		t.Errorf("у местной модели ноль — правда, а не незнание: %+v", local)
	}

	// Цена, вписанная руками, главнее «оно же местное»: человек знает про своё
	// электричество больше нас.
	priced := SpendWith(map[string]Price{"llama3": {Input: 1, Output: 2}}, true, "llama3", 1_000_000, 1_000_000, 0, 0)
	if !priced.PriceKnown || priced.USD != 3 {
		t.Errorf("своя цена не применилась: %+v", priced)
	}

	// Встроенная таблица Claude сюда не подставляется ни при каких условиях.
	claude := SpendWith(nil, false, "claude-opus-5", 1_000_000, 0, 0, 0)
	if claude.PriceKnown {
		t.Error("подставили цену Claude чужому провайдеру")
	}
}

// Имя модели уходит в базу к follow-up и в `steno cost`. Записать туда
// claude.model при работе через Groq значит однажды показать модель, которой
// запрос никогда не видел.
func TestBrainModelFollowsProvider(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Claude.Model = "claude-opus-5"
	if cfg.BrainModel() != "claude-opus-5" {
		t.Errorf("claude: %q", cfg.BrainModel())
	}

	cfg.Brain.Provider = ProviderOpenAI
	cfg.Brain.OpenAI.Preset = "groq"
	if cfg.BrainModel() != "openai/gpt-oss-120b" {
		t.Errorf("модель заготовки не подставилась: %q", cfg.BrainModel())
	}
	cfg.Brain.OpenAI.Model = "llama-3.3-70b"
	if cfg.BrainModel() != "llama-3.3-70b" {
		t.Errorf("своя модель не победила заготовку: %q", cfg.BrainModel())
	}

	cfg.Brain.Provider = ProviderCodex
	if cfg.BrainModel() != "codex" {
		t.Errorf("без имени модели у codex должно остаться его собственное: %q", cfg.BrainModel())
	}
	cfg.Brain.Codex.Model = "gpt-5-codex"
	if cfg.BrainModel() != "gpt-5-codex" {
		t.Errorf("codex: %q", cfg.BrainModel())
	}
}

// У каждой заготовки должно быть то, ради чего она заведена, — иначе выбор в
// мастере ведёт в никуда.
func TestPresetsAreUsable(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range OpenAIPresets() {
		if seen[p.Key] {
			t.Errorf("заготовка %q описана дважды", p.Key)
		}
		seen[p.Key] = true
		if p.Name == "" {
			t.Errorf("у заготовки %q нет имени", p.Key)
		}
		if p.Key == PresetCustom {
			continue
		}
		if p.BaseURL == "" {
			t.Errorf("у заготовки %q нет адреса", p.Key)
		}
		if p.Local != LocalURL(p.BaseURL) {
			t.Errorf("заготовка %q помечена местной как %v, а адрес %s говорит обратное",
				p.Key, p.Local, p.BaseURL)
		}
		// У местной модели ключа не бывает, у чужой он обязателен: иначе
		// первый же запрос уйдёт без ключа и вернётся 401.
		if p.Local && p.KeyEnv != "" {
			t.Errorf("местной заготовке %q зачем-то нужен ключ %s", p.Key, p.KeyEnv)
		}
		if !p.Local && p.KeyEnv == "" {
			t.Errorf("у заготовки %q не сказано, в какой переменной ключ", p.Key)
		}
	}
	if !seen[PresetCustom] {
		t.Error("нет заготовки «свой адрес» — а произвольный base_url обязателен")
	}
}

// Пути в конфиге — относительно самого конфига, а не текущего каталога. Для
// адаптера расшифровки это уже так; скрипт модели зовётся ровно так же, и
// конфиг в /etc/steno не должен искать ./adapters/ollama.sh рядом с собой в
// том каталоге, откуда его случайно запустили.
func TestScriptPathIsRelativeToConfig(t *testing.T) {
	path := writeTemp(t, `{
	  "brain": {"provider": "command"},
	  "llm": {"cmd": ["./adapters/ollama.sh"], "model": "llama3.1"},
	  "transcribe": {"cmd": ["./adapters/whisper-cpp.sh", "{{audio}}"]}
	}`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(path)
	want := filepath.Join(dir, "adapters", "ollama.sh")
	if len(cfg.LLM.Cmd) == 0 || cfg.LLM.Cmd[0] != want {
		t.Errorf("скрипт модели: %v, ждали %q", cfg.LLM.Cmd, want)
	}
	// Заодно убеждаемся, что тест не сторожит пустоту: у расшифровки это
	// работало и раньше.
	if cfg.Transcribe.Cmd[0] != filepath.Join(dir, "adapters", "whisper-cpp.sh") {
		t.Errorf("адаптер расшифровки: %v", cfg.Transcribe.Cmd)
	}
	// Имя в PATH путём не считается и трогать его нельзя.
	plain := writeTemp(t, `{"llm": {"cmd": ["mymodel", "--json"]}}`)
	c2, err := LoadConfig(plain)
	if err != nil {
		t.Fatal(err)
	}
	if c2.LLM.Cmd[0] != "mymodel" {
		t.Errorf("имя в PATH превратили в путь: %v", c2.LLM.Cmd)
	}
}

func writeTemp(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "steno.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	// Заодно убеждаемся, что это вообще JSON: тест на разбор конфига, упавший
	// на своей же строке, ищут долго.
	var probe map[string]any
	if err := json.Unmarshal([]byte(body), &probe); err != nil {
		t.Fatalf("тест написал невалидный JSON: %v", err)
	}
	return path
}
