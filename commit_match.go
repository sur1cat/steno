package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// Сверка открытых задач с новыми коммитами.
//
// Задача, сделанная тихо и не упомянутая ни на одном созвоне, висит открытой
// вечно — закрыть её сейчас можно только руками или случайно, если о ней
// зайдёт речь. А в коммитах ровно это и написано: что сделали.
//
// Закрывать по догадке опасно: «начал миграцию» — не «закончил», и задача,
// закрытая ошибочно, исчезает из списка, и её никто не сделает. Поэтому у
// вывода есть уверенность, и закрывается только уверенное; остальное уходит
// сообщением, чтобы человек решил сам.

type commitVerdict struct {
	TaskID     string `json:"task_id"`
	Commit     string `json:"commit"`
	Confidence string `json:"confidence"` // high | medium | low
	Why        string `json:"why"`
}

const commitMatchSystem = `Тебе дают открытые задачи по проекту и коммиты,
появившиеся в его репозитории с прошлой проверки. Определи, какие задачи этими
коммитами сделаны.

Правила:

1. Отвечай только про задачи из списка и только про данные коммиты. Не выдумывай
   ни идентификаторов задач, ни хешей.
2. confidence: "high" — коммит прямо делает то, что описано в задаче, и работа
   выглядит завершённой. "medium" — похоже, но не наверняка: коммит делает часть,
   или формулировка допускает другое прочтение. "low" — совпали слова, но это
   разные вещи.
3. Начало работы — не её конец. «Начал», «черновик», «WIP», «часть 1» — это
   максимум medium, чаще low.
4. Правка опечатки, форматирование, обновление зависимостей — не выполнение
   задачи, даже если слова совпали.
5. Задачу, которой в коммитах не видно, не упоминай вовсе. Пустой список —
   нормальный ответ и самый частый.
6. why — одна фраза по-русски: почему ты считаешь, что это оно.`

func matchCommitsToTasks(ctx context.Context, cfg *Config, project string,
	tasks []ProjectItem, commits []Commit) ([]commitVerdict, Spend, error) {

	if len(tasks) == 0 || len(commits) == 0 {
		return nil, Spend{}, nil
	}
	key, err := secret(cfg.Claude.APIKeyEnv, "Claude")
	if err != nil {
		return nil, Spend{}, err
	}
	client := anthropic.NewClient(option.WithAPIKey(key))

	var b strings.Builder
	fmt.Fprintf(&b, "Проект: %s\n\nОткрытые задачи:\n", project)
	for _, t := range tasks {
		fmt.Fprintf(&b, "  [%s] %s", t.ID, t.Text)
		if t.Owner != "" {
			fmt.Fprintf(&b, " (на ком: %s)", t.Owner)
		}
		fmt.Fprintf(&b, " — с %s\n", t.OpenedAt.Format("2006-01-02"))
	}
	b.WriteString("\nНовые коммиты:\n")
	for _, c := range commits {
		fmt.Fprintf(&b, "  %s  %s", c.Short(), c.Subject)
		if c.Author != "" {
			fmt.Fprintf(&b, "  (%s)", c.Author)
		}
		b.WriteString("\n")
		if c.Body != "" {
			fmt.Fprintf(&b, "      %s\n", cut(strings.ReplaceAll(c.Body, "\n", " "), 300))
		}
	}

	str := map[string]any{"type": "string"}
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"verdicts": map[string]any{"type": "array", "items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": str,
					"commit":  str,
					"confidence": map[string]any{"type": "string",
						"enum": []string{"high", "medium", "low"}},
					"why": str,
				},
				"required":             []string{"task_id", "commit", "confidence", "why"},
				"additionalProperties": false,
			}},
		},
		"required":             []string{"verdicts"},
		"additionalProperties": false,
	}

	adaptive := anthropic.ThinkingConfigAdaptiveParam{}
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(cfg.Claude.Model),
		MaxTokens: 4000,
		System: []anthropic.TextBlockParam{{
			Text:         commitMatchSystem,
			CacheControl: anthropic.NewCacheControlEphemeralParam(),
		}},
		Thinking: anthropic.ThinkingConfigParamUnion{OfAdaptive: &adaptive},
		// Сверка идёт раз в час по всем проектам — разоряться тут незачем.
		OutputConfig: anthropic.OutputConfigParam{
			Effort: anthropic.OutputConfigEffortMedium,
			Format: anthropic.JSONOutputFormatParam{Schema: schema},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(b.String())),
		},
	}

	stream := client.Messages.NewStreaming(ctx, params)
	defer stream.Close()
	var msg anthropic.Message
	for stream.Next() {
		if err := msg.Accumulate(stream.Current()); err != nil {
			return nil, Spend{}, err
		}
	}
	if err := stream.Err(); err != nil {
		return nil, Spend{}, fmt.Errorf("Claude: %w", err)
	}
	if msg.StopReason == anthropic.StopReasonRefusal {
		return nil, Spend{}, fmt.Errorf("Claude отказался: %s", msg.StopDetails.Explanation)
	}
	var out strings.Builder
	for _, block := range msg.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			out.WriteString(t.Text)
		}
	}
	var parsed struct {
		Verdicts []commitVerdict `json:"verdicts"`
	}
	if err := json.Unmarshal([]byte(out.String()), &parsed); err != nil {
		return nil, Spend{}, fmt.Errorf("разбор ответа: %w", err)
	}

	// Отсекаем выдуманное: задача и коммит должны быть из тех, что мы дали.
	known := map[string]bool{}
	for _, t := range tasks {
		known[t.ID] = true
	}
	hashes := map[string]string{}
	for _, c := range commits {
		hashes[c.Short()] = c.Subject
		hashes[c.Hash] = c.Subject
	}
	var kept []commitVerdict
	for _, v := range parsed.Verdicts {
		if !known[v.TaskID] {
			continue
		}
		if _, ok := hashes[v.Commit]; !ok {
			continue
		}
		kept = append(kept, v)
	}

	spend := computeSpend(cfg, cfg.Claude.Model,
		msg.Usage.InputTokens, msg.Usage.OutputTokens,
		msg.Usage.CacheReadInputTokens, msg.Usage.CacheCreationInputTokens)
	return kept, spend, nil
}

// closeByCommits сверяет открытые задачи проекта с новыми коммитами, закрывает
// уверенное и сообщает обо всём, что нашлось.
func (s *syncer) closeByCommits(ctx context.Context, p Project, commits []Commit) error {
	open, err := s.st.OpenItems(p.Name)
	if err != nil {
		return err
	}
	var tasks []ProjectItem
	for _, it := range open {
		if it.Kind == KindTask {
			tasks = append(tasks, it)
		}
	}
	if len(tasks) == 0 {
		return nil
	}

	verdicts, spend, err := matchCommitsToTasks(ctx, s.cfg, p.Name, tasks, commits)
	if err != nil {
		return err
	}
	if len(verdicts) == 0 {
		return nil
	}
	s.log.Printf("репозитории: сверка задач по «%s», %s", p.Name, spend)

	byID := map[string]ProjectItem{}
	for _, t := range tasks {
		byID[t.ID] = t
	}
	subjects := map[string]string{}
	for _, c := range commits {
		subjects[c.Short()] = c.Subject
		subjects[c.Hash] = c.Subject
	}

	digest := syncDigest{Project: p.Name, Commits: len(commits)}
	for _, v := range verdicts {
		t := byID[v.TaskID]
		short := v.Commit
		if len(short) > 8 {
			short = short[:8]
		}
		line := fmt.Sprintf("%s — %s\n    коммит %s «%s»\n    %s",
			t.Text, orDash(t.Owner), short, subjects[v.Commit], v.Why)

		switch v.Confidence {
		case "high":
			if !s.cfg.Sync.AutoClose {
				digest.Maybe = append(digest.Maybe, line)
				continue
			}
			note := fmt.Sprintf("закрыто коммитом %s «%s»", short, subjects[v.Commit])
			if err := s.st.CloseItem(t.ID, "done", note, ""); err != nil {
				s.log.Printf("репозитории: не закрыл %s: %v", t.ID, err)
				continue
			}
			s.log.Printf("репозитории: закрыл %s — %s", t.ID, note)
			digest.Closed = append(digest.Closed, line)
		case "medium":
			digest.Maybe = append(digest.Maybe, line)
		}
	}
	if left, err := s.st.OpenItems(p.Name); err == nil {
		for _, it := range left {
			if it.Kind == KindTask {
				digest.StillOpen++
			}
		}
	}
	if s.cfg.Sync.Notify {
		s.notify(ctx, digest)
	}
	return nil
}
