package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/sur1cat/steno/internal/core"
)

// Агент в doctor: выключен — не поломка, а «—» с подсказкой, как включить;
// включён при текстовом провайдере — поломка, потому что исполнять некому и
// кнопка в панели ответит отказом ровно тогда, когда её нажмут.
func TestDoctorExplainsAgentState(t *testing.T) {
	cfg := core.DefaultConfig()
	off := checkAgent(cfg)
	if off.state != "off" || !strings.Contains(strings.Join(off.fix, "\n"), "steno agent on") {
		t.Fatalf("выключенный агент описан не так: %+v", off)
	}

	cfgPath := t.TempDir() + "/steno.json"
	if err := os.WriteFile(cfgPath, []byte(`{"agent":{"enabled":true,"auto_spec":true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg.Path = cfgPath
	cfg.Brain.Provider = core.ProviderOpenAI
	bad := checkAgent(cfg)
	if bad.state != "fail" || bad.blocking {
		t.Fatalf("включённый агент без исполнителя должен быть поломкой, но не блокирующей: %+v", bad)
	}
	if !strings.Contains(bad.note, "ТЗ собираются сами") {
		t.Errorf("про автосборку не сказано: %q", bad.note)
	}
	if len(bad.fix) == 0 || !strings.Contains(strings.Join(bad.fix, "\n"), "claude") {
		t.Errorf("не сказано, чем чинить: %v", bad.fix)
	}
}

// doctor обязан говорить, кем steno разбирает созвон и доступен ли он. Раньше
// он всегда говорил «Claude» — на установке через Groq это отправляло искать
// поломку не туда.
func TestDoctorNamesTheProvider(t *testing.T) {
	// Поддельный сервер вместо настоящего: doctor теперь ходит к провайдеру
	// по-настоящему, и без подмены адреса тест зависел бы от сети и от того,
	// что чужой сервер ответит на выдуманный ключ.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("doctor спросил не список моделей: %s", r.URL.Path)
		}
		// Ключ уезжает только там, где он задан: у местной модели его нет.
		if got := r.Header.Get("Authorization"); got != "" && got != "Bearer ключ-groq" {
			t.Errorf("ключ уехал не тот: %q", got)
		}
		w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	cfg := core.DefaultConfig()
	cfg.Brain.Provider = core.ProviderOpenAI
	cfg.Brain.OpenAI.Preset = "groq"
	cfg.Brain.OpenAI.BaseURL = srv.URL + "/v1"
	cfg.Brain.OpenAI.Model = "openai/gpt-oss-120b"
	t.Setenv("GROQ_API_KEY", "ключ-groq")

	c := checkBrain(cfg, "")
	if c.state != "ok" {
		t.Fatalf("настроенный провайдер объявлен нерабочим: %+v", c)
	}
	for _, want := range []string{"Groq", "openai/gpt-oss-120b", "GROQ_API_KEY"} {
		if !strings.Contains(c.note, want) {
			t.Errorf("в строке doctor нет %q: %q", want, c.note)
		}
	}
	if strings.Contains(c.note, "Claude") {
		t.Errorf("doctor всё ещё зовёт провайдера Claude: %q", c.note)
	}
	// Ключ уехал и адрес спросили тот — это проверил сам обработчик выше.
	// А вот местная модель ключа не требует и до сервера тоже должна дойти.
	local := core.DefaultConfig()
	local.Brain.Provider = core.ProviderOpenAI
	local.Brain.OpenAI.Preset = "ollama"
	local.Brain.OpenAI.BaseURL = srv.URL + "/v1" // httptest тоже на петле, то есть «местный»
	local.Brain.OpenAI.Model = "llama3.3"
	if got := checkBrain(local, ""); got.state != "ok" {
		t.Errorf("местная модель, которая отвечает, объявлена нерабочей: %+v", got)
	}
}

// Молчать про неизвестную цену нельзя: `steno cost` тогда покажет ноль там, где
// счёт придёт. Проверяем отдельно от doctor — здесь нужен чужой адрес, а
// поддельный сервер живёт только на петле, то есть считается местным.
func TestDoctorWarnsAboutUnknownPrice(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Brain.Provider = core.ProviderOpenAI
	cfg.Brain.OpenAI.Preset = "groq"
	cfg.Brain.OpenAI.Model = "openai/gpt-oss-120b"
	if got := strings.Join(brainPriceFix(cfg), "\n"); !strings.Contains(got, "brain.openai.prices") {
		t.Errorf("doctor не предупредил про неизвестную цену: %q", got)
	}

	// Цена задана — предупреждать не о чем.
	cfg.Brain.OpenAI.Prices = map[string]core.Price{"openai/gpt-oss-120b": {Input: 1, Output: 2}}
	if got := brainPriceFix(cfg); got != nil {
		t.Errorf("doctor ворчит про цену, которая задана: %v", got)
	}

	// У модели на этой же машине ноль — правда, а не незнание.
	local := core.DefaultConfig()
	local.Brain.Provider = core.ProviderOpenAI
	local.Brain.OpenAI.Preset = "ollama"
	local.Brain.OpenAI.Model = "llama3.3"
	if got := brainPriceFix(local); got != nil {
		t.Errorf("у местной модели цена известна — это ноль: %v", got)
	}

	// И у Claude этого предупреждения быть не должно: его цены steno знает.
	if got := brainPriceFix(core.DefaultConfig()); got != nil {
		t.Errorf("предупреждение вылезло у Claude: %v", got)
	}
}

// Недонастроенный провайдер — это отказ с объяснением, а не «нет доступа» без
// подробностей: человеку чинить настройку, а не гадать.
func TestDoctorExplainsBrokenProvider(t *testing.T) {
	// OpenRouter — тот случай, где модель угадать нельзя: их там сотни, и
	// заготовка не подставляет ни одной.
	cfg := core.DefaultConfig()
	cfg.Brain.Provider = core.ProviderOpenAI
	cfg.Brain.OpenAI.Preset = "openrouter"
	t.Setenv("OPENROUTER_API_KEY", "ключ")
	// До сети дело не дойдёт: чинить тут настройку, и doctor обязан сказать это
	// раньше, чем полезет к провайдеру.

	c := checkBrain(cfg, "")
	if c.state != "fail" {
		t.Fatalf("недонастроенный провайдер объявлен рабочим: %+v", c)
	}
	if !strings.Contains(strings.Join(c.fix, "\n"), "brain.openai.model") {
		t.Errorf("doctor не сказал, чего не хватает: %v", c.fix)
	}

	// Пустая переменная с ключом — тоже поломка, и назвать её надо по имени.
	noKey := core.DefaultConfig()
	noKey.Brain.Provider = core.ProviderOpenAI
	noKey.Brain.OpenAI.Preset = "openai"
	t.Setenv("OPENAI_API_KEY", "")
	got := checkBrain(noKey, "")
	if got.state != "fail" || !strings.Contains(strings.Join(got.fix, "\n"), "OPENAI_API_KEY") {
		t.Errorf("doctor молчит про пустой ключ: %+v", got)
	}
}
