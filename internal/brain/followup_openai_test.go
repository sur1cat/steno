package brain

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sur1cat/steno/internal/core"
)

// Весь путь follow-up через совместимого с OpenAI провайдера, от промпта до
// разобранной структуры.
//
// Отдельно от тестов клиента: там проверяется, что мы правильно шлём и
// понимаем, а здесь — что схема, промпт и разбор сходятся друг с другом. Это
// то место, где ошибка выглядит не как отказ, а как пустой follow-up на
// настоящем созвоне.
func TestFollowupThroughOpenAI(t *testing.T) {
	answer := map[string]any{
		"title": "Планёрка по платежам",
		"tldr":  []string{"Вебхуки провайдера падают", "Решили чинить до пятницы"},
		"decisions": []any{map[string]any{
			"what": "Чиним вебхуки", "why": "теряем оплаты", "at": 42.5, "project": "биллинг"}},
		"action_items": []any{map[string]any{
			"owner": "Рустем", "what": "Починить ретраи", "due": "2026-09-12",
			"quote": "я возьму ретраи", "at": 61.0, "project": "биллинг"}},
		"open_questions": []any{map[string]any{
			"question": "Кто отвечает за алерты", "waiting_on": "Аня", "at": 90.0, "project": "биллинг"}},
		"risks":    []string{"Провайдер может не успеть"},
		"timeline": []any{map[string]any{"at": 0.0, "title": "Начали", "summary": "обсудили вебхуки"}},
		"updates": []any{map[string]any{
			"id": "T-0001", "status": "done", "note": "закрыли коммитом"}},
	}
	raw, _ := json.Marshal(answer)

	var sentSystem, sentUser string
	rec, cfg := oaServe(t, func(n int, w http.ResponseWriter) {
		fmt.Fprint(w, okBody(string(raw)))
	})
	_ = rec

	cfg.Claude.OutputLanguage = "русский"
	m := &core.Meeting{
		ID: "m1", Title: "Планёрка", StartedAt: time.Date(2026, 9, 8, 11, 0, 0, 0, time.UTC),
		Participants: []string{"Рустем", "Аня"},
	}
	segs := []core.Segment{
		{Start: 42.5, Speaker: "Рустем", Text: "вебхуки биллинга падают"},
		{Start: 61, Speaker: "Рустем", Text: "я возьму ретраи"},
	}
	// Проект называется «Платежи», а вслух звучит «биллинг»: модель вернёт то,
	// что слышала, и приведение к настоящему имени — часть этого пути.
	projects := []core.Project{{Name: "Платежи", Aliases: []string{"биллинг"}}}

	f, spend, err := MakeFollowup(context.Background(), cfg, m, segs, projects, "", "")
	if err != nil {
		t.Fatalf("follow-up через совместимого провайдера не собрался: %v", err)
	}

	got := rec.req(t, 0)
	msgs, _ := got["messages"].([]any)
	if len(msgs) == 2 {
		sentSystem, _ = msgs[0].(map[string]any)["content"].(string)
		sentUser, _ = msgs[1].(map[string]any)["content"].(string)
	}
	// Промпт должен доехать целиком: правила, шапка со списком проектов и сама
	// расшифровка. Пустой промпт дал бы связный, но выдуманный follow-up.
	if !strings.Contains(sentSystem, "follow-up") {
		t.Errorf("правила не доехали до модели:\n%.200s", sentSystem)
	}
	if !strings.Contains(sentUser, "Платежи") || !strings.Contains(sentUser, "я возьму ретраи") {
		t.Errorf("шапка или расшифровка не доехали:\n%.400s", sentUser)
	}
	// Промпт не переводится: его читает модель, а не человек, — и правила в нём
	// ссылаются на собственные слова.
	if !strings.Contains(sentUser, "Расшифровка:") {
		t.Errorf("шапка промпта переведена или потерялась:\n%.400s", sentUser)
	}

	if f.Title != "Планёрка по платежам" || len(f.TLDR) != 2 {
		t.Errorf("разбор поехал: %+v", f)
	}
	if len(f.ActionItems) != 1 || f.ActionItems[0].Owner != "Рустем" {
		t.Fatalf("задачи: %+v", f.ActionItems)
	}
	// «биллинг» должен стать «Платежами» — иначе задачи по одному проекту
	// разъедутся по двум разным именам.
	if f.ActionItems[0].Project != "Платежи" {
		t.Errorf("проект не приведён к настоящему имени: %q", f.ActionItems[0].Project)
	}
	if f.Decisions[0].Project != "Платежи" || f.OpenQuestions[0].Project != "Платежи" {
		t.Errorf("решения и вопросы остались с псевдонимом: %+v %+v", f.Decisions, f.OpenQuestions)
	}
	if len(f.Updates) != 1 || f.Updates[0].Status != "done" {
		t.Errorf("судьба открытых пунктов потерялась: %+v", f.Updates)
	}

	// Расход: модель — та, что настроена у провайдера, а не claude.model.
	if spend.Model != "test-model" {
		t.Errorf("в расход записана чужая модель: %q", spend.Model)
	}
	// Поддельный сервер живёт на петле, то есть считается моделью на этой же
	// машине: ноль у неё — правда. Что бывает с чужим адресом без цены,
	// проверяет core.TestSpendWithoutPriceStaysUnknown — httptest туда не
	// дотянется, он всегда местный.
	if !spend.PriceKnown || spend.USD != 0 {
		t.Errorf("у местной модели расход — известный ноль: %+v", spend)
	}
}
