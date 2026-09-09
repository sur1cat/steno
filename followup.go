package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// Followup — то, ради чего всё затевалось. Структура выбрана под то, как это
// читают: сначала три строки, дальше решения и задачи с владельцем и сроком.
// Пункт без владельца и срока не читают вообще, поэтому оба поля обязательны в
// схеме, а модели запрещено их выдумывать.
type Followup struct {
	Title         string         `json:"title"`
	TLDR          []string       `json:"tldr"`
	Decisions     []Decision     `json:"decisions"`
	ActionItems   []ActionItem   `json:"action_items"`
	OpenQuestions []OpenQuestion `json:"open_questions"`
	Risks         []string       `json:"risks"`
	Timeline      []TimelineItem `json:"timeline"`
	// Что случилось с тем, что уже висело по проектам. Без этого каждый созвон
	// заводил бы новые копии тех же задач, и живое состояние проекта тонуло бы
	// в дублях.
	Updates []ItemUpdate `json:"updates"`
}

// ItemUpdate — судьба пункта, который висел открытым до этого созвона.
type ItemUpdate struct {
	ID     string `json:"id"`
	Status string `json:"status"` // done | dropped | still_open
	Note   string `json:"note"`
}

type Decision struct {
	What    string  `json:"what"`
	Why     string  `json:"why"`
	At      float64 `json:"at"`
	Project string  `json:"project"`
}

type ActionItem struct {
	Owner   string  `json:"owner"`
	What    string  `json:"what"`
	Due     string  `json:"due"`   // YYYY-MM-DD или "" если срок не назвали
	Quote   string  `json:"quote"` // дословно из транскрипта — чтобы можно было проверить
	At      float64 `json:"at"`
	Project string  `json:"project"`
}

type OpenQuestion struct {
	Question  string  `json:"question"`
	WaitingOn string  `json:"waiting_on"`
	At        float64 `json:"at"`
	Project   string  `json:"project"`
}

type TimelineItem struct {
	At      float64 `json:"at"`
	Title   string  `json:"title"`
	Summary string  `json:"summary"`
}

const followupSystem = `Ты делаешь follow-up по расшифровке рабочего созвона.

Читать его будут те, кто на созвоне не был, и те, кто был, но забыл. Пиши так,
чтобы через месяц по этому тексту можно было восстановить, что решили и кто
что должен сделать.

Жёсткие правила:

1. Ничего не выдумывай. Каждый пункт должен опираться на конкретное место в
   расшифровке. Если решения не приняли — оставь decisions пустым.
2. owner в action_items — имя из списка участников, ровно как оно там написано.
   Если по расшифровке непонятно, на ком задача, пиши owner: "не назначен".
   Никогда не назначай задачу человеку, который её не брал.
3. due заполняй, только если срок прозвучал. Относительные сроки («к пятнице»,
   «через неделю») переводи в дату YYYY-MM-DD от даты созвона. Не прозвучал —
   пустая строка.
4. quote — дословный фрагмент расшифровки, из которого видно задачу. Не
   пересказ. Не длиннее двух предложений.
5. at — таймкод в секундах от начала записи, взятый из ближайшей строки
   расшифровки. Он превращается в ссылку на момент в записи, поэтому важен.
6. tldr — от двух до четырёх строк. Не пересказ повестки, а то, что изменилось:
   что решили, что застряло, что дальше.
7. open_questions — то, что осталось без ответа. waiting_on — от кого ждут
   ответ, или "не определено".
8. Расшифровка машинная: в ней есть оговорки, обрывы и неверно распознанные
   слова. Восстанавливай смысл по контексту, но не додумывай факты.
9. Пиши на том же языке, на котором говорили. Никаких вводных вроде «в этом
   созвоне обсуждалось» — сразу по делу.

Про проекты:

10. У каждого решения, задачи и открытого вопроса проставь project — ровно то
    название из списка проектов, к которому это относится. Вслух проект часто
    называют иначе, чем он записан: ориентируйся на суть разговора, а не на
    совпадение слов. Если даны справки о проектах, разбирайся по ним — в них
    записано, какими словами команда говорит о каждом. Не относится ни к
    одному или непонятно — пиши "не определён". Лучше "не определён", чем
    приписать чужому проекту.
11. Если в списке «что уже висит открытым» есть пункт, судьба которого
    решилась на этом созвоне, добавь его в updates с его идентификатором:
    done — сделано, dropped — отменили или стало неактуально,
    still_open — обсуждали, но не закрыли (тогда добавь note, что изменилось).
    Не выдумывай идентификаторы: только те, что даны в списке.
12. Не заводи в decisions/action_items/open_questions то, что уже висит
    открытым и не изменилось. Новый пункт — это новое, а не повтор старого.`

func followupSchema() map[string]any {
	str := map[string]any{"type": "string"}
	num := map[string]any{"type": "number"}
	obj := func(props map[string]any, req ...string) map[string]any {
		return map[string]any{
			"type": "object", "properties": props,
			"required": req, "additionalProperties": false,
		}
	}
	arr := func(items map[string]any) map[string]any {
		return map[string]any{"type": "array", "items": items}
	}
	return obj(map[string]any{
		"title": str,
		"tldr":  arr(str),
		"decisions": arr(obj(map[string]any{"what": str, "why": str, "at": num, "project": str},
			"what", "why", "at", "project")),
		"action_items": arr(obj(map[string]any{
			"owner": str, "what": str, "due": str, "quote": str, "at": num, "project": str},
			"owner", "what", "due", "quote", "at", "project")),
		"open_questions": arr(obj(map[string]any{
			"question": str, "waiting_on": str, "at": num, "project": str},
			"question", "waiting_on", "at", "project")),
		"risks": arr(str),
		"timeline": arr(obj(map[string]any{"at": num, "title": str, "summary": str},
			"at", "title", "summary")),
		"updates": arr(obj(map[string]any{
			"id":     str,
			"status": map[string]any{"type": "string", "enum": []string{"done", "dropped", "still_open"}},
			"note":   str,
		}, "id", "status", "note")),
	}, "title", "tldr", "decisions", "action_items", "open_questions", "risks", "timeline", "updates")
}

// renderTranscript готовит расшифровку в виде, который модель читает лучше
// всего: таймкод, имя, реплика. Соседние реплики одного человека склеиваются.
func renderTranscript(segs []Segment) string {
	var b strings.Builder
	last := ""
	for _, s := range segs {
		txt := strings.TrimSpace(s.Text)
		if txt == "" {
			continue
		}
		sp := s.Speaker
		if sp == "" {
			sp = "неизвестно"
		}
		if sp == last {
			b.WriteString(" ")
			b.WriteString(txt)
			continue
		}
		if last != "" {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "[%s] %s: %s", clock(s.Start), sp, txt)
		last = sp
	}
	b.WriteString("\n")
	return b.String()
}

func clock(sec float64) string {
	s := int(sec)
	return fmt.Sprintf("%02d:%02d:%02d", s/3600, (s%3600)/60, s%60)
}

func makeFollowup(ctx context.Context, cfg *Config, m *Meeting, segs []Segment, projects []Project, primers, openItems string) (*Followup, Spend, error) {
	key, err := secret(cfg.Claude.APIKeyEnv, "Claude")
	if err != nil {
		return nil, Spend{}, err
	}
	client := anthropic.NewClient(option.WithAPIKey(key))

	var head strings.Builder
	fmt.Fprintf(&head, "Название встречи: %s\n", orDash(m.Title))
	fmt.Fprintf(&head, "Дата: %s\n", m.StartedAt.Format("2006-01-02 15:04 MST"))
	if len(m.Participants) > 0 {
		fmt.Fprintf(&head, "Участники: %s\n", strings.Join(m.Participants, ", "))
	}
	if len(m.Invitees) > 0 {
		fmt.Fprintf(&head, "Приглашены в календаре: %s\n", strings.Join(m.Invitees, ", "))
	}
	if cfg.Claude.OutputLanguage != "" {
		fmt.Fprintf(&head, "Язык follow-up: %s\n", cfg.Claude.OutputLanguage)
	}

	if len(projects) > 0 {
		head.WriteString("\nПроекты команды:\n")
		for _, p := range projects {
			fmt.Fprintf(&head, "  %s", p.Name)
			if len(p.Aliases) > 0 {
				fmt.Fprintf(&head, " (вслух: %s)", strings.Join(p.Aliases, ", "))
			}
			if p.About != "" {
				fmt.Fprintf(&head, " — %s", p.About)
			}
			head.WriteString("\n")
		}
		fmt.Fprintf(&head, "  %s — если непонятно, к чему относится\n", unassignedProject)
	}
	if primers != "" {
		head.WriteString("\n")
		head.WriteString(primers)
	}
	if openItems != "" {
		head.WriteString("\n")
		head.WriteString(openItems)
	}
	head.WriteString("\nРасшифровка:\n\n")

	effort := anthropic.OutputConfigEffortHigh
	switch cfg.Claude.Effort {
	case "":
	case "low", "medium", "high", "xhigh", "max":
		effort = anthropic.OutputConfigEffort(cfg.Claude.Effort)
	default:
		return nil, Spend{}, fmt.Errorf("claude.effort=%q — допустимы low, medium, high, xhigh, max",
			cfg.Claude.Effort)
	}
	adaptive := anthropic.ThinkingConfigAdaptiveParam{}

	maxTokens := int64(cfg.Claude.MaxTokens)
	if maxTokens <= 0 {
		maxTokens = 16000
	}
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(cfg.Claude.Model),
		MaxTokens: maxTokens,
		System: []anthropic.TextBlockParam{{
			Text:         followupSystem,
			CacheControl: anthropic.NewCacheControlEphemeralParam(),
		}},
		Thinking: anthropic.ThinkingConfigParamUnion{OfAdaptive: &adaptive},
		OutputConfig: anthropic.OutputConfigParam{
			Effort: effort,
			Format: anthropic.JSONOutputFormatParam{Schema: followupSchema()},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(head.String() + renderTranscript(segs))),
		},
	}

	// Расшифровка часового созвона — это десятки тысяч токенов на входе.
	// Стримим, чтобы не упереться в таймаут HTTP.
	stream := client.Messages.NewStreaming(ctx, params)
	// Тело ответа закрывает только Close: Next этого не делает. Выйдя из цикла
	// по ошибке, мы оставляли соединение открытым, а генерацию — идущей и
	// оплачиваемой.
	defer stream.Close()
	var msg anthropic.Message
	for stream.Next() {
		if err := msg.Accumulate(stream.Current()); err != nil {
			return nil, Spend{}, fmt.Errorf("сборка ответа: %w", err)
		}
	}
	if err := stream.Err(); err != nil {
		return nil, Spend{}, fmt.Errorf("Claude: %w", err)
	}
	if msg.StopReason == anthropic.StopReasonRefusal {
		return nil, Spend{}, fmt.Errorf("Claude отказался обрабатывать расшифровку: %s", msg.StopDetails.Explanation)
	}
	// Обрыв по потолку токенов выглядел как поломка схемы: JSON приходил
	// недописанным, и человек видел «unexpected end of JSON input» — сообщение,
	// по которому невозможно догадаться, что чинить.
	if msg.StopReason == anthropic.StopReasonMaxTokens {
		return nil, Spend{}, fmt.Errorf("ответ не поместился в claude.max_tokens (%d) — "+
			"подними его или поставь claude.effort пониже", maxTokens)
	}

	var out string
	for _, block := range msg.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			out += t.Text
		}
	}
	if strings.TrimSpace(out) == "" {
		return nil, Spend{}, fmt.Errorf("Claude вернул пустой ответ (stop_reason=%s)", msg.StopReason)
	}

	var f Followup
	if err := json.Unmarshal([]byte(out), &f); err != nil {
		return nil, Spend{}, fmt.Errorf("разбор follow-up: %w\nответ: %.400s", err, out)
	}
	// Названия проектов приводим к тем, что записаны в конфиге: модель может
	// вернуть «биллинг» там, где проект называется «Платежи».
	for i := range f.ActionItems {
		f.ActionItems[i].Project = matchProject(projects, f.ActionItems[i].Project)
	}
	for i := range f.Decisions {
		f.Decisions[i].Project = matchProject(projects, f.Decisions[i].Project)
	}
	for i := range f.OpenQuestions {
		f.OpenQuestions[i].Project = matchProject(projects, f.OpenQuestions[i].Project)
	}

	spend := computeSpend(cfg, cfg.Claude.Model,
		msg.Usage.InputTokens, msg.Usage.OutputTokens,
		msg.Usage.CacheReadInputTokens, msg.Usage.CacheCreationInputTokens)
	return &f, spend, nil
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
