package brain

import (
	"context"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
)

// Одна точка, через которую steno спрашивает модель.
//
// Ниже по течению никто не знает, кто отвечал: там ждут JSON по схеме, и всё.
// Поэтому провайдеров можно добавлять, ничего больше не трогая, — и их четыре
// вида, различающихся не вендором, а тем, чем человек платит:
//
//   - ключ Anthropic — для сервера. Работает без человека, размечает ответ
//     схемой и считает расход по токенам.
//   - `claude -p` — подписка Claude Code, которая у человека уже есть. Схему
//     приходится просить словами: параметра формата у CLI нет.
//   - совместимый с OpenAI адрес — OpenAI, Groq, OpenRouter, Together,
//     DeepSeek, а из местных Ollama, LM Studio, llama.cpp. Один формат на всех,
//     схема параметром.
//   - `codex exec` — подписка ChatGPT. Схема задаётся файлом и соблюдается.
//   - внешний скрипт — то же, чем подключается распознавание. Договор описан
//     в llm_cmd.go и словами в adapters/ollama.sh.
//
// Внутри claude "auto" по-прежнему выбирает сам: есть ключ — берёт API, нет —
// смотрит, есть ли CLI со сделанным входом, — а нет и его, берёт ключ
// OpenAI, Groq или OpenRouter из окружения, см. AutoOpenAI.

type llmVia string

const (
	viaAPI    llmVia = "api"
	viaCLI    llmVia = "cli"
	viaOpenAI llmVia = "openai"
	viaCodex  llmVia = "codex"
	viaCmd    llmVia = "command"
)

// ProviderTitle — как называть выбранного провайдера человеку. Одно слово,
// которое видно в doctor и в журнале сервиса: «делаю follow-up» с именем
// Claude при работе через Groq — это ровно то враньё, из-за которого потом
// полдня ищут не там.
func ProviderTitle(cfg *core.Config) string {
	if eff, _, ok := AutoOpenAI(cfg); ok {
		cfg = eff
	}
	switch cfg.BrainProvider() {
	case core.ProviderOpenAI:
		if p, ok := core.OpenAIPresetByKey(cfg.Brain.OpenAI.Preset); ok && p.Key != core.PresetCustom {
			return p.Name
		}
		return "OpenAI API"
	case core.ProviderCodex:
		return "Codex"
	case core.ProviderCommand:
		return i18n.Tr("скрипт")
	}
	return "Claude"
}

func ResolveVia(cfg *core.Config) (llmVia, string, error) {
	switch cfg.BrainProvider() {
	case core.ProviderOpenAI:
		ok, note := OpenAIReady(cfg)
		if !ok {
			return "", "", fmt.Errorf(i18n.Tr("провайдер openai выбран, но не готов: %s"), note)
		}
		return viaOpenAI, note, nil
	case core.ProviderCodex:
		ok, note := CodexCLIAvailable()
		if !ok {
			return "", "", fmt.Errorf(
				i18n.Tr("провайдер codex выбран, но не готов: %s\n")+
					i18n.Tr("  → подписка: запусти `codex` и войди\n")+
					i18n.Tr("  → или ключ: export CODEX_API_KEY=…"), note)
		}
		return viaCodex, i18n.Tr("подписка через codex exec (") + note + ")", nil
	case core.ProviderCommand:
		ok, note := LLMCmdAvailable(cfg)
		if !ok {
			return "", "", fmt.Errorf(
				i18n.Tr("провайдер command выбран, но скрипт не готов: %s\n")+
					i18n.Tr("  → договор описан в adapters/ollama.sh — с него же удобно списать свой"), note)
		}
		return viaCmd, note, nil
	}

	switch cfg.Claude.Via {
	case "api":
		if _, _, err := ClaudeClient(cfg); err != nil {
			return "", "", err
		}
		return viaAPI, i18n.Tr("ключ ") + cfg.Claude.APIKeyEnv, nil
	case "cli":
		if ok, why := ClaudeCLIAvailable(); !ok {
			return "", "", fmt.Errorf(i18n.Tr("claude.via=cli, но CLI не готов: %s"), why)
		}
		return viaCLI, i18n.Tr("подписка через claude -p"), nil
	}

	// auto
	if _, how, err := ClaudeClient(cfg); err == nil {
		return viaAPI, how, nil
	}
	if ok, note := ClaudeCLIAvailable(); ok {
		return viaCLI, i18n.Tr("подписка через claude -p (") + note + ")", nil
	}
	if _, note, ok := AutoOpenAI(cfg); ok {
		return viaOpenAI, note, nil
	}
	return "", "", fmt.Errorf(
		i18n.Tr("нет доступа к Claude. Годится любое из двух:\n")+
			i18n.Tr("  → ключ API: console.anthropic.com → API keys, потом export %s=sk-ant-…\n")+
			i18n.Tr("  → или подписка: поставь Claude Code и войди — steno возьмёт её через `claude -p`\n")+
			i18n.Tr("  → а можно вовсе не Claude: brain.provider = openai или codex"),
		cfg.Claude.APIKeyEnv)
}

// autoKeys — какие ключи из окружения подхватывает auto, кроме ключа Anthropic,
// и в каком порядке. Порядок — это приоритет: у кого лежит и ключ OpenAI, и
// ключ Groq, тот получает OpenAI.
//
// Модель у заготовки OpenRouter не названа намеренно — там их сотни, — а auto
// без модели не работает; поэтому у него она стоит здесь. Та же, что у Groq:
// открытая, дешёвая и умеющая схему параметром.
var autoKeys = []struct {
	preset string
	model  string
}{
	{preset: "openai"},
	{preset: "groq"},
	{preset: "openrouter", model: "openai/gpt-oss-120b"},
}

// AutoOpenAI — что auto выбрал бы вместо Claude, если Claude нет.
//
// Возвращает копию конфига с проставленным провайдером и заготовкой, строку
// «откуда взялось» для doctor и журнала, и ok=false, если менять нечего: в
// конфиге назван провайдер, или у Claude есть ключ либо вход, или ни одной из
// переменных нет.
//
// Копия, а не правка на месте: сюда ходит ResolveVia перед каждым запросом, и
// менять конфиг оттуда значило бы, что doctor и запрос видят разное. Кому нужен
// сам конфиг с ответом — open() в cmd/steno, — тот зовёт ApplyAutoOpenAI.
//
// Порядок проверок не совпадает с порядком приоритета, и это нарочно: проверка
// входа в Claude Code — это запуск процесса, четверть секунды, и делать её
// стоит только когда ответ от неё зависит, то есть когда ключ провайдера в
// окружении вообще есть. Приоритет при этом прежний: ключ Anthropic, потом
// вход в Claude Code, потом ключи остальных.
func AutoOpenAI(cfg *core.Config) (*core.Config, string, bool) {
	if cfg.BrainProvider() != core.ProviderClaude || cfg.Claude.Via != "auto" {
		return nil, "", false
	}
	for _, k := range autoKeys {
		p, ok := core.OpenAIPresetByKey(k.preset)
		if !ok {
			continue
		}
		if _, err := core.Secret(p.KeyEnv, ""); err != nil {
			continue
		}
		if _, _, err := ClaudeClient(cfg); err == nil {
			return nil, "", false
		}
		if ok, _ := ClaudeCLIAvailable(); ok {
			return nil, "", false
		}
		eff := *cfg
		eff.Brain.Provider = core.ProviderOpenAI
		eff.Brain.OpenAI.Preset = p.Key
		eff.Brain.OpenAI.Model = core.FirstNonEmpty(k.model, p.Model)
		return &eff, i18n.Tr("ключ ") + p.KeyEnv + i18n.Tr(" из окружения — без Claude беру ") + p.Name, true
	}
	return nil, "", false
}

// ApplyAutoOpenAI — то же, но в сам конфиг. Зовётся один раз при загрузке:
// дальше имя модели уходит в базу рядом с follow-up и в `steno cost`, и
// брать его из конфига, в котором написан Claude, когда отвечал Groq, значит
// показать в расходах модель, которой запрос никогда не видел.
func ApplyAutoOpenAI(cfg *core.Config) (string, bool) {
	eff, note, ok := AutoOpenAI(cfg)
	if !ok {
		return "", false
	}
	*cfg = *eff
	return note, true
}

// AskLLM отправляет запрос тем путём, который доступен, и возвращает текст.
// Если задана схема, ответ обязан быть объектом по ней.
func AskLLM(ctx context.Context, cfg *core.Config, system, user string,
	schema map[string]any, maxTokens int64) (string, core.Spend, error) {

	via, _, err := ResolveVia(cfg)
	if err != nil {
		return "", core.Spend{}, err
	}
	switch via {
	case viaCLI:
		return askViaCLI(ctx, cfg, system, user, schema)
	case viaOpenAI:
		// Сюда с провайдером claude приводит только auto — тогда адрес и
		// модель берутся из той же копии, по которой auto решал.
		if eff, _, ok := AutoOpenAI(cfg); ok {
			cfg = eff
		}
		return askViaOpenAI(ctx, cfg, system, user, schema, maxTokens)
	case viaCodex:
		return askViaCodex(ctx, cfg, system, user, schema)
	case viaCmd:
		return askViaCmd(ctx, cfg, system, user, schema, maxTokens)
	}
	return askViaAPI(ctx, cfg, system, user, schema, maxTokens)
}

func askViaAPI(ctx context.Context, cfg *core.Config, system, user string,
	schema map[string]any, maxTokens int64) (string, core.Spend, error) {

	client, _, err := ClaudeClient(cfg)
	if err != nil {
		return "", core.Spend{}, err
	}
	effort, err := claudeEffort(cfg)
	if err != nil {
		return "", core.Spend{}, err
	}
	if maxTokens <= 0 {
		maxTokens = 16000
	}
	adaptive := anthropic.ThinkingConfigAdaptiveParam{}
	out := anthropic.OutputConfigParam{Effort: effort}
	if schema != nil {
		out.Format = anthropic.JSONOutputFormatParam{Schema: schema}
	}
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(cfg.Claude.Model),
		MaxTokens: maxTokens,
		System: []anthropic.TextBlockParam{{
			Text:         system,
			CacheControl: anthropic.NewCacheControlEphemeralParam(),
		}},
		Thinking:     anthropic.ThinkingConfigParamUnion{OfAdaptive: &adaptive},
		OutputConfig: out,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(user)),
		},
	}

	stream := client.Messages.NewStreaming(ctx, params)
	defer stream.Close()
	var msg anthropic.Message
	for stream.Next() {
		if err := msg.Accumulate(stream.Current()); err != nil {
			return "", core.Spend{}, fmt.Errorf(i18n.Tr("сборка ответа: %w"), err)
		}
	}
	if err := stream.Err(); err != nil {
		return "", core.Spend{}, fmt.Errorf("Claude: %w", err)
	}
	if msg.StopReason == anthropic.StopReasonRefusal {
		return "", core.Spend{}, fmt.Errorf(i18n.Tr("Claude отказался: %s"), msg.StopDetails.Explanation)
	}
	if msg.StopReason == anthropic.StopReasonMaxTokens {
		return "", core.Spend{}, fmt.Errorf(i18n.Tr("ответ не поместился в claude.max_tokens (%d) — ")+
			i18n.Tr("подними его или поставь claude.effort пониже"), maxTokens)
	}
	var text strings.Builder
	for _, block := range msg.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			text.WriteString(t.Text)
		}
	}
	if strings.TrimSpace(text.String()) == "" {
		return "", core.Spend{}, fmt.Errorf(i18n.Tr("Claude вернул пустой ответ (stop_reason=%s)"), msg.StopReason)
	}
	spend := core.ComputeSpend(cfg, cfg.Claude.Model,
		msg.Usage.InputTokens, msg.Usage.OutputTokens,
		msg.Usage.CacheReadInputTokens, msg.Usage.CacheCreationInputTokens)
	return text.String(), spend, nil
}

func askViaCLI(ctx context.Context, cfg *core.Config, system, user string,
	schema map[string]any) (string, core.Spend, error) {

	// У `claude -p` нет параметра формата, поэтому схему просим словами. Ответ
	// всё равно проверяется разбором — выдумать структуру мимо схемы не выйдет
	// незамеченным.
	//
	// Через i18n.Tr эта просьба больше не идёт: её читает модель, а не человек.
	// Переведённая половина промпта уже однажды разошлась с непереведёнными
	// правилами, и повторять это незачем. Тот же текст берёт нижняя ступенька
	// совместимого с OpenAI пути — отсюда общая функция.
	system = schemaInWords(system, schema)
	res, err := runClaudeCLI(ctx, cfg, system, user, cfg.Claude.MaxUSDPerCall)
	if err != nil {
		return "", core.Spend{}, err
	}
	text := res.Text
	if schema != nil {
		text = extractJSON(text)
	} else {
		text = stripPreamble(text)
	}
	// Цену CLI считает сам и отдаёт в ответе — это точнее наших таблиц, где
	// цена подписочных моделей вообще не прописана.
	return text, core.Spend{
		Model:      cfg.Claude.Model,
		Input:      res.Usage.Input,
		Output:     res.Usage.Output,
		CacheRead:  res.Usage.CacheRead,
		CacheWrite: res.Usage.CacheWrite,
		USD:        res.USD,
		PriceKnown: res.USD > 0,
	}, nil
}

func claudeEffort(cfg *core.Config) (anthropic.OutputConfigEffort, error) {
	switch cfg.Claude.Effort {
	case "":
		return anthropic.OutputConfigEffortHigh, nil
	case "low", "medium", "high", "xhigh", "max":
		return anthropic.OutputConfigEffort(cfg.Claude.Effort), nil
	}
	return "", fmt.Errorf(i18n.Tr("claude.effort=%q — допустимы low, medium, high, xhigh, max"),
		cfg.Claude.Effort)
}

// extractJSON достаёт объект из ответа, даже если модель обернула его в блок
// кода или добавила фразу до и после. Без схемы в параметрах это случается, и
// падать на этом было бы обидно.
func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "```"); i >= 0 {
		rest := s[i+3:]
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
			rest = rest[nl+1:]
		}
		if end := strings.Index(rest, "```"); end >= 0 {
			s = strings.TrimSpace(rest[:end])
		}
	}
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return s
}

// stripPreamble срезает реплику о ходе работы, если CLI начал ответ с неё.
//
// `claude -p --permission-mode plan` иногда открывает ответ рассуждением о
// самом задании («План здесь не требуется, пишу сразу») и отбивает его чертой.
// У запросов со схемой это незаметно — оттуда всё равно вырезается JSON. А вот
// справка о проекте схемы не имеет и уходит в промпт каждого созвона целиком,
// так что вводная реплика поехала бы в каждый follow-up.
//
// Режем осторожно: только если до черты остался небольшой кусок. Длинный текст
// перед чертой — это уже содержание, и терять его нельзя.
func stripPreamble(s string) string {
	t := strings.TrimSpace(s)
	i := strings.Index(t, "\n---")
	if i < 0 || i > 400 {
		return t
	}
	head := t[:i]
	// Настоящая справка не говорит о себе в первом лице и не поминает задание.
	if !strings.Contains(head, "\n\n") &&
		(strings.Contains(head, i18n.Tr("задач")) || strings.Contains(head, i18n.Tr("План")) ||
			strings.Contains(head, i18n.Tr("пишу")) || strings.Contains(head, i18n.Tr("Сейчас"))) {
		return strings.TrimSpace(strings.TrimLeft(t[i+4:], "-\n"))
	}
	return t
}
