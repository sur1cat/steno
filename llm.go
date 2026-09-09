package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

// Одна точка, через которую steno спрашивает Claude.
//
// Путей два, и оба настоящие:
//
//   - через ключ API — для сервера. Работает без человека, отдаёт точную
//     разметку ответа схемой и считает расход по токенам.
//   - через CLI `claude -p` — для своего ноутбука. Использует подписку, которая
//     у человека уже есть; заводить ради собственных созвонов отдельный биллинг
//     незачем. Схему приходится просить словами, а не задавать параметром, —
//     это единственная плата.
//
// "auto" выбирает сам: есть ключ — берёт API, нет — смотрит, есть ли CLI со
// сделанным входом. Так на сервере ничего не меняется, а на ноутбуке ничего не
// требуется.

type llmVia string

const (
	viaAPI llmVia = "api"
	viaCLI llmVia = "cli"
)

func resolveVia(cfg *Config) (llmVia, string, error) {
	switch cfg.Claude.Via {
	case "api":
		if _, _, err := claudeClient(cfg); err != nil {
			return "", "", err
		}
		return viaAPI, "ключ " + cfg.Claude.APIKeyEnv, nil
	case "cli":
		if ok, why := claudeCLIAvailable(); !ok {
			return "", "", fmt.Errorf("claude.via=cli, но CLI не готов: %s", why)
		}
		return viaCLI, "подписка через claude -p", nil
	}

	// auto
	if _, how, err := claudeClient(cfg); err == nil {
		return viaAPI, how, nil
	}
	if ok, note := claudeCLIAvailable(); ok {
		return viaCLI, "подписка через claude -p (" + note + ")", nil
	}
	return "", "", fmt.Errorf(
		"нет доступа к Claude. Годится любое из двух:\n"+
			"  → ключ API: console.anthropic.com → API keys, потом export %s=sk-ant-…\n"+
			"  → или подписка: поставь Claude Code и войди — steno возьмёт её через `claude -p`",
		cfg.Claude.APIKeyEnv)
}

// askLLM отправляет запрос тем путём, который доступен, и возвращает текст.
// Если задана схема, ответ обязан быть объектом по ней.
func askLLM(ctx context.Context, cfg *Config, system, user string,
	schema map[string]any, maxTokens int64) (string, Spend, error) {

	via, _, err := resolveVia(cfg)
	if err != nil {
		return "", Spend{}, err
	}
	if via == viaCLI {
		return askViaCLI(ctx, cfg, system, user, schema)
	}
	return askViaAPI(ctx, cfg, system, user, schema, maxTokens)
}

func askViaAPI(ctx context.Context, cfg *Config, system, user string,
	schema map[string]any, maxTokens int64) (string, Spend, error) {

	client, _, err := claudeClient(cfg)
	if err != nil {
		return "", Spend{}, err
	}
	effort, err := claudeEffort(cfg)
	if err != nil {
		return "", Spend{}, err
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
			return "", Spend{}, fmt.Errorf("сборка ответа: %w", err)
		}
	}
	if err := stream.Err(); err != nil {
		return "", Spend{}, fmt.Errorf("Claude: %w", err)
	}
	if msg.StopReason == anthropic.StopReasonRefusal {
		return "", Spend{}, fmt.Errorf("Claude отказался: %s", msg.StopDetails.Explanation)
	}
	if msg.StopReason == anthropic.StopReasonMaxTokens {
		return "", Spend{}, fmt.Errorf("ответ не поместился в claude.max_tokens (%d) — "+
			"подними его или поставь claude.effort пониже", maxTokens)
	}
	var text strings.Builder
	for _, block := range msg.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			text.WriteString(t.Text)
		}
	}
	if strings.TrimSpace(text.String()) == "" {
		return "", Spend{}, fmt.Errorf("Claude вернул пустой ответ (stop_reason=%s)", msg.StopReason)
	}
	spend := computeSpend(cfg, cfg.Claude.Model,
		msg.Usage.InputTokens, msg.Usage.OutputTokens,
		msg.Usage.CacheReadInputTokens, msg.Usage.CacheCreationInputTokens)
	return text.String(), spend, nil
}

func askViaCLI(ctx context.Context, cfg *Config, system, user string,
	schema map[string]any) (string, Spend, error) {

	// У `claude -p` нет параметра формата, поэтому схему просим словами. Ответ
	// всё равно проверяется разбором — выдумать структуру мимо схемы не выйдет
	// незамеченным.
	if schema != nil {
		raw, _ := json.MarshalIndent(schema, "", "  ")
		system += "\n\nОтветь одним объектом JSON строго по этой схеме, без пояснений " +
			"и без обрамления в блок кода:\n\n" + string(raw)
	}
	res, err := runClaudeCLI(ctx, cfg, system, user, cfg.Claude.MaxUSDPerCall)
	if err != nil {
		return "", Spend{}, err
	}
	text := res.Text
	if schema != nil {
		text = extractJSON(text)
	}
	// Цену CLI считает сам и отдаёт в ответе — это точнее наших таблиц, где
	// цена подписочных моделей вообще не прописана.
	return text, Spend{
		Model:      cfg.Claude.Model,
		Input:      res.Usage.Input,
		Output:     res.Usage.Output,
		CacheRead:  res.Usage.CacheRead,
		CacheWrite: res.Usage.CacheWrite,
		USD:        res.USD,
		PriceKnown: res.USD > 0,
	}, nil
}

func claudeEffort(cfg *Config) (anthropic.OutputConfigEffort, error) {
	switch cfg.Claude.Effort {
	case "":
		return anthropic.OutputConfigEffortHigh, nil
	case "low", "medium", "high", "xhigh", "max":
		return anthropic.OutputConfigEffort(cfg.Claude.Effort), nil
	}
	return "", fmt.Errorf("claude.effort=%q — допустимы low, medium, high, xhigh, max",
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
