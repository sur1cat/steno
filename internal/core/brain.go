package core

import (
	"net"
	"net/url"
	"strings"

	"github.com/sur1cat/steno/internal/i18n"
)

// Кто делает разбор.
//
// До появления этого файла ответ был один — Claude, — и он же был препятствием:
// чтобы попробовать steno, нужно было завести ключ у конкретного вендора. Теперь
// провайдеров три вида, и различаются они не вендором, а тем, чем человек платит
// и что он уже поставил себе на машину:
//
//	claude  — ключ Anthropic или подписка Claude Code через `claude -p`
//	openai  — всё, что говорит на диалекте OpenAI: сама OpenAI, Groq, OpenRouter,
//	          Together, DeepSeek, а из местных — Ollama, LM Studio, llama.cpp
//	codex   — подписка ChatGPT через `codex exec`
//	command — внешний скрипт, как transcribe.cmd у распознавания
//
// Захардкоженного списка вендоров нет и не будет: у второго пути формат запроса
// один на всех, поэтому в настройке лежат адрес, модель и имя переменной с
// ключом. Заготовки ниже — только чтобы не набирать адрес руками. А четвёртый
// путь снимает вопрос совсем: что не говорит ни на одном из этих языков,
// подключается скриптом и без единой строки Go.

const (
	ProviderClaude  = "claude"
	ProviderOpenAI  = "openai"
	ProviderCodex   = "codex"
	ProviderCommand = "command"
)

// BrainProvider — выбранный провайдер, приведённый к одному из трёх имён.
// Пустое и незнакомое значение — это claude: конфиг, написанный до того, как
// провайдеров стало больше одного, обязан вести себя ровно как раньше.
func (c *Config) BrainProvider() string {
	switch strings.ToLower(strings.TrimSpace(c.Brain.Provider)) {
	case ProviderOpenAI:
		return ProviderOpenAI
	case ProviderCodex:
		return ProviderCodex
	case ProviderCommand, "cmd", "script":
		return ProviderCommand
	}
	return ProviderClaude
}

// BrainModel — какой моделью работает выбранный провайдер. Одна функция на
// всех, потому что имя модели уходит в учёт расхода и в базу к follow-up:
// записать туда claude.model при работе через Groq значит однажды показать в
// `steno cost` модель, которой запрос никогда не видел.
func (c *Config) BrainModel() string {
	switch c.BrainProvider() {
	case ProviderOpenAI:
		if m := strings.TrimSpace(c.Brain.OpenAI.Model); m != "" {
			return m
		}
		if p, ok := OpenAIPresetByKey(c.Brain.OpenAI.Preset); ok {
			return p.Model
		}
		return ""
	case ProviderCodex:
		return FirstNonEmpty(strings.TrimSpace(c.Brain.Codex.Model), "codex")
	case ProviderCommand:
		// Имя модели у скрипта — то, что он сам про себя скажет; до ответа мы
		// знаем только то, что попросили. Пусто — так и оставляем: выдумывать
		// имя, которого никто не называл, значит однажды показать его в
		// `steno cost` как настоящее.
		return strings.TrimSpace(c.LLM.Model)
	}
	return c.Claude.Model
}

// BrainSummary — выбранный разбор одной строкой: провайдер, модель и, если
// провайдер этого требует, адрес.
//
// Заведено ради одного сравнения — «в конфиге написано одно, а в базе выбрано
// другое», — и потому включает адрес: заготовка и base_url меняют, куда пойдёт
// запрос, не трогая ни провайдера, ни имя модели. Строка без них сравнивала бы
// две разные настройки и молча признавала их одинаковыми.
func (c *Config) BrainSummary() string {
	s := c.BrainProvider() + " · " + OrDash(c.BrainModel())
	switch c.BrainProvider() {
	case ProviderOpenAI:
		s += " · " + OrDash(c.OpenAIBaseURL())
	case ProviderCommand:
		s += " · " + OrDash(strings.Join(c.LLM.Cmd, " "))
	}
	return s
}

// OpenAIPreset — заготовка настройки для известного адреса. Ничего, кроме трёх
// строк, за ней не стоит: preset выбирают, чтобы не набирать адрес руками, и
// любой из них полностью заменяется своим base_url.
type OpenAIPreset struct {
	Key     string
	Name    string
	BaseURL string
	KeyEnv  string
	// Модель по умолчанию — подсказка, а не обещание: у провайдеров они
	// меняются чаще, чем выходят релизы steno. Там, где угадать нельзя вовсе
	// (OpenRouter, местные модели), пусто, и человек называет её сам.
	Model string
	Hint  string
	// Local — модель крутится на этой же машине. Отсюда следует не адрес, а
	// цена: у местной модели она ноль, и это правда, а не незнание.
	Local bool
}

// PresetCustom — «свой адрес»: ничего не подставляем, всё берётся из base_url.
const PresetCustom = "custom"

// См. uiTabTitles: таблица собирается лениво, уже на известном языке.
func OpenAIPresets() []OpenAIPreset {
	return []OpenAIPreset{
		{Key: "openai", Name: "OpenAI", BaseURL: "https://api.openai.com/v1",
			KeyEnv: "OPENAI_API_KEY", Model: "gpt-5",
			Hint: i18n.Tr("Ключ: platform.openai.com → API keys.")},
		{Key: "groq", Name: "Groq", BaseURL: "https://api.groq.com/openai/v1",
			KeyEnv: "GROQ_API_KEY", Model: "openai/gpt-oss-120b",
			Hint: i18n.Tr("Быстрее всех и дешевле большинства. Ключ: console.groq.com.")},
		{Key: "openrouter", Name: "OpenRouter", BaseURL: "https://openrouter.ai/api/v1",
			KeyEnv: "OPENROUTER_API_KEY",
			Hint:   i18n.Tr("Один ключ на сотни моделей. Имя модели — как на openrouter.ai/models.")},
		{Key: "together", Name: "Together", BaseURL: "https://api.together.xyz/v1",
			KeyEnv: "TOGETHER_API_KEY",
			Hint:   i18n.Tr("Ключ: api.together.ai.")},
		{Key: "deepseek", Name: "DeepSeek", BaseURL: "https://api.deepseek.com/v1",
			KeyEnv: "DEEPSEEK_API_KEY", Model: "deepseek-chat",
			Hint: i18n.Tr("Ключ: platform.deepseek.com.")},
		{Key: "ollama", Name: "Ollama", BaseURL: "http://localhost:11434/v1", Local: true,
			Hint: i18n.Tr("Модель на этой же машине: ключ не нужен, расход нулевой. ") +
				i18n.Tr("Имя модели — как в `ollama list`.")},
		{Key: "lmstudio", Name: "LM Studio", BaseURL: "http://localhost:1234/v1", Local: true,
			Hint: i18n.Tr("Включи в LM Studio локальный сервер и возьми имя модели оттуда.")},
		{Key: "llamacpp", Name: "llama.cpp", BaseURL: "http://localhost:8080/v1", Local: true,
			Hint: i18n.Tr("Сервер llama-server. Имя модели он обычно принимает любое.")},
		{Key: PresetCustom, Name: i18n.Tr("Свой адрес"),
			Hint: i18n.Tr("Любой сервер, отвечающий по /chat/completions как OpenAI.")},
	}
}

func OpenAIPresetByKey(key string) (OpenAIPreset, bool) {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return OpenAIPreset{}, false
	}
	for _, p := range OpenAIPresets() {
		if p.Key == key {
			return p, true
		}
	}
	return OpenAIPreset{}, false
}

// OpenAIBaseURL — адрес, по которому steno будет стучаться. Свой base_url
// главнее заготовки: заготовка нужна, чтобы не набирать адрес руками, а не
// чтобы запрещать чужие.
func (c *Config) OpenAIBaseURL() string {
	if u := strings.TrimSpace(c.Brain.OpenAI.BaseURL); u != "" {
		return strings.TrimRight(u, "/")
	}
	if p, ok := OpenAIPresetByKey(c.Brain.OpenAI.Preset); ok {
		return p.BaseURL
	}
	return ""
}

// OpenAIKeyEnv — имя переменной с ключом. Пусто — ключ не посылается вовсе, и
// это не поломка: местным моделям он не нужен.
func (c *Config) OpenAIKeyEnv() string {
	if e := strings.TrimSpace(c.Brain.OpenAI.APIKeyEnv); e != "" {
		return e
	}
	if p, ok := OpenAIPresetByKey(c.Brain.OpenAI.Preset); ok {
		return p.KeyEnv
	}
	return ""
}

// OpenAIEndpoint — полный адрес запроса. Полный адрес в base_url принимаем как
// есть: его туда вставляют из чужой документации, и получать за это 404 с
// /chat/completions/chat/completions на конце — плохая шутка.
func (c *Config) OpenAIEndpoint() string {
	base := c.OpenAIBaseURL()
	if base == "" {
		return ""
	}
	if strings.HasSuffix(base, "/chat/completions") {
		return base
	}
	return base + "/chat/completions"
}

// OpenAILocal — модель на этой же машине. Считается по адресу, а не по
// заготовке: человек, поставивший Ollama на соседний порт своим base_url,
// платит за неё ровно столько же, то есть ничего.
func (c *Config) OpenAILocal() bool { return LocalURL(c.OpenAIBaseURL()) }

// LocalURL — адрес ведёт на эту же машину.
//
// Только петля и явные имена своей машины. «Похоже на локальную сеть» сюда не
// годится: соврать о цене в другую сторону — сказать «ноль» там, где счёт
// придёт, — хуже, чем не знать её вовсе.
func LocalURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	switch strings.ToLower(host) {
	case "localhost", "host.docker.internal":
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
