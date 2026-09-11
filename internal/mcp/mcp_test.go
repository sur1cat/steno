package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/demo"
	"github.com/sur1cat/steno/internal/spec"
)

// Инструменты проверяются на демо-наборе, а не на трёх строках, заведённых на
// месте: вопросы, ради которых сервер и нужен, — «что решили про миграцию?»,
// «что открыто по платежам?» — имеют смысл только на базе, похожей на живую.
// Идентификаторы созвонов ниже — из internal/demo, русский набор.
const (
	planning = "2026-09-08-1100-a1b2" // планёрка по релизу: follow-up, 4 задачи
	incident = "2026-09-07-1530-c3d4" // разбор инцидента: закрыл задачу по Платежам
	live     = "2026-09-08-1400-g7h8" // идёт прямо сейчас: ни расшифровки, ни follow-up
)

// client поднимает сервер на демо-базе и подключает к нему клиента через
// память: тот же протокол, что по stdio, без процесса и труб.
func client(t *testing.T) (*sdk.ClientSession, *core.Store) {
	t.Helper()
	st, err := core.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	projects, _, err := demo.Seed(st, "ru")
	if err != nil {
		t.Fatal(err)
	}
	// Демо-набор отдаёт проекты для конфига, а в базу их переносит open() при
	// первом запуске — здесь это делается за него, иначе псевдонимы и описания
	// проектам неоткуда взять.
	cfg := core.DefaultConfig()
	cfg.Projects = projects
	if _, err := core.ImportProjects(st, cfg); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	serverT, clientT := sdk.NewInMemoryTransports()
	if _, err := New(st).Connect(ctx, serverT, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs, st
}

// call зовёт инструмент и разбирает структурированный ответ в out. Ошибка
// инструмента — это не ошибка протокола: она приходит текстом с isError, и
// именно её видит модель, поэтому тесты сверяют слова.
func call(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any, out any) string {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var text string
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			text += tc.Text
		}
	}
	if res.IsError {
		return text
	}
	if out != nil {
		b, err := json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, out); err != nil {
			t.Fatalf("%s: разбор ответа: %v\n%s", name, err, b)
		}
	}
	return ""
}

func TestToolsListAndAnnotations(t *testing.T) {
	cs, _ := client(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"list_meetings": true, "get_followup": true, "get_transcript": true, "search": true,
		"projects": true, "open_items": true, "close_item": false,
		"list_specs": true, "get_spec": true,
	}
	if len(res.Tools) != len(want) {
		t.Errorf("инструментов %d, ждали %d", len(res.Tools), len(want))
	}
	for _, tool := range res.Tools {
		readOnly, known := want[tool.Name]
		if !known {
			t.Errorf("лишний инструмент %s", tool.Name)
			continue
		}
		// Единственная запись в базу — close_item, и клиент должен видеть это
		// по подсказкам, а не по описанию: по ним он решает, спрашивать ли.
		if tool.Annotations == nil || tool.Annotations.ReadOnlyHint != readOnly {
			t.Errorf("%s: readOnlyHint должен быть %v", tool.Name, readOnly)
		}
		if tool.Description == "" {
			t.Errorf("%s: без описания модель не поймёт, когда его звать", tool.Name)
		}
	}
	if !strings.Contains(cs.InitializeResult().Instructions, "close_item") {
		t.Error("инструкции сервера не предупреждают про close_item")
	}
}

func TestListMeetings(t *testing.T) {
	cs, _ := client(t)
	var out listMeetingsResult
	call(t, cs, "list_meetings", nil, &out)
	if out.Total != 4 || len(out.Meetings) != 4 {
		t.Fatalf("созвонов %d из %d, ждали 4 из 4", len(out.Meetings), out.Total)
	}
	// Свежие первыми: идущий прямо сейчас звонок — верхняя строка.
	if out.Meetings[0].ID != live || out.Meetings[0].Status != "recording" {
		t.Errorf("первым должен быть идущий созвон, а не %+v", out.Meetings[0])
	}
	var plan meeting
	for _, m := range out.Meetings {
		if m.ID == planning {
			plan = m
		}
	}
	if plan.Tasks != 4 || plan.DurationSec != 47*60 || len(plan.Participants) != 4 {
		t.Errorf("планёрка: %+v", plan)
	}
	if plan.StartedAt == "" || !strings.Contains(plan.StartedAt, "T") {
		t.Errorf("дата не в RFC 3339: %q", plan.StartedAt)
	}

	call(t, cs, "list_meetings", map[string]any{"limit": 1}, &out)
	if len(out.Meetings) != 1 || out.Total != 4 {
		t.Errorf("limit=1: %d созвонов, всего %d", len(out.Meetings), out.Total)
	}

	// По проекту: планёрка завела пункты по Платежам, разбор инцидента закрыл
	// один из них; синк с дизайном и идущий звонок к Платежам не относятся.
	call(t, cs, "list_meetings", map[string]any{"project": "биллинг"}, &out)
	ids := []string{}
	for _, m := range out.Meetings {
		ids = append(ids, m.ID)
	}
	if strings.Join(ids, " ") != planning+" "+incident {
		t.Errorf("по Платежам: %v", ids)
	}
	if msg := call(t, cs, "list_meetings", map[string]any{"project": "Склад"}, nil); !strings.Contains(msg, "Платежи") {
		t.Errorf("незнакомый проект должен вернуть список известных, а не %q", msg)
	}
}

func TestGetFollowup(t *testing.T) {
	cs, _ := client(t)
	var out followupResult
	call(t, cs, "get_followup", map[string]any{"id": planning}, &out)
	if out.Followup == nil || !strings.HasPrefix(out.Followup.Title, "Релиз 2.4") {
		t.Fatalf("follow-up планёрки: %+v", out.Followup)
	}
	if len(out.Followup.ActionItems) != 4 || len(out.Followup.Decisions) != 2 ||
		len(out.Followup.OpenQuestions) != 2 || len(out.Followup.Risks) != 2 {
		t.Errorf("состав follow-up: %+v", out.Followup)
	}
	if a := out.Followup.ActionItems[0]; a.Owner != "Участник Б" || a.Due != "2026-09-11" || a.At != 412 {
		t.Errorf("первая задача: %+v", a)
	}
	if out.Meeting.ID != planning || out.Meeting.Tasks != 4 || out.Links["slack"] == "" {
		t.Errorf("шапка и ссылки: %+v %v", out.Meeting, out.Links)
	}

	// Без id — последний созвон, как у `steno show`. В демо это идущий прямо
	// сейчас звонок, и ответ должен сказать, что follow-up ещё нет и почему.
	msg := call(t, cs, "get_followup", nil, nil)
	if !strings.Contains(msg, live) || !strings.Contains(msg, "recording") {
		t.Errorf("про идущий созвон: %q", msg)
	}
	if msg := call(t, cs, "get_followup", map[string]any{"id": "нет-такого"}, nil); !strings.Contains(msg, "list_meetings") {
		t.Errorf("ошибка должна сказать, откуда брать id: %q", msg)
	}
}

func TestGetTranscript(t *testing.T) {
	cs, st := client(t)
	var out transcriptResult
	call(t, cs, "get_transcript", map[string]any{"id": planning}, &out)
	if out.TotalLines != 12 || len(out.Lines) != 12 || out.Truncated {
		t.Fatalf("целиком: %d из %d, обрезано %v", len(out.Lines), out.TotalLines, out.Truncated)
	}
	if l := out.Lines[0]; l.At != 12 || l.Speaker != "Участник А" || !strings.HasPrefix(l.Text, "Так, давайте") {
		t.Errorf("первая реплика: %+v", l)
	}

	// Окно вокруг найденного: секунда 412 — «закончу миграцию к четвергу».
	call(t, cs, "get_transcript", map[string]any{"id": planning, "from_sec": 400, "to_sec": 800}, &out)
	if len(out.Lines) != 2 || out.Lines[0].At != 412 || out.Lines[1].At != 760 {
		t.Errorf("окно 400–800: %+v", out.Lines)
	}
	if out.TotalLines != 12 {
		t.Errorf("всего реплик должно оставаться 12, а не %d", out.TotalLines)
	}
	if msg := call(t, cs, "get_transcript", map[string]any{"id": planning, "from_sec": 800, "to_sec": 400}, nil); msg == "" {
		t.Error("окно задом наперёд должно быть ошибкой")
	}
	if msg := call(t, cs, "get_transcript", map[string]any{"id": live}, nil); !strings.Contains(msg, "no transcript") {
		t.Errorf("про созвон без расшифровки: %q", msg)
	}

	// Длинная расшифровка режется по потолку, и ответ говорит, откуда
	// продолжать: с секунды первой невошедшей реплики.
	var segs []core.Segment
	for i := 0; i < maxLines+50; i++ {
		segs = append(segs, core.Segment{Start: float64(i * 10), End: float64(i*10 + 9), Speaker: "Лектор", Text: "слово"})
	}
	if err := st.SaveSegments(planning, segs); err != nil {
		t.Fatal(err)
	}
	call(t, cs, "get_transcript", map[string]any{"id": planning}, &out)
	if !out.Truncated || len(out.Lines) != maxLines || out.NextFromSec != float64(maxLines*10) {
		t.Fatalf("обрезка: %d реплик, обрезано %v, дальше с %v", len(out.Lines), out.Truncated, out.NextFromSec)
	}
	call(t, cs, "get_transcript", map[string]any{"id": planning, "from_sec": out.NextFromSec}, &out)
	if out.Truncated || len(out.Lines) != 50 || out.Lines[0].At != float64(maxLines*10) {
		t.Errorf("продолжение: %d реплик с %v, обрезано %v", len(out.Lines), out.Lines[0].At, out.Truncated)
	}
}

func TestSearch(t *testing.T) {
	cs, _ := client(t)
	var out searchResult
	// Префикс: «миграц» находит «миграцию», «миграцией» и «миграции» — и в
	// репликах, и в follow-up, где слово стоит в другом падеже.
	call(t, cs, "search", map[string]any{"query": "миграц"}, &out)
	if len(out.Hits) == 0 {
		t.Fatal("ничего не нашлось")
	}
	var spoken, written bool
	for _, h := range out.Hits {
		if h.MeetingID != planning {
			t.Errorf("находка не с планёрки: %+v", h)
		}
		switch h.Kind {
		case "transcript":
			// Секунда 412: «я закончу миграцию к четвергу». Именно её модель
			// понесёт в get_transcript, чтобы прочитать вокруг.
			if h.At == 412 && h.Speaker == "Участник Б" && strings.Contains(h.Quote, "**миграцию**") {
				spoken = true
			}
		case "followup":
			if h.At == 0 && h.Speaker == "" {
				written = true
			}
		default:
			t.Errorf("вид находки должен быть английским словом, а не %q", h.Kind)
		}
		if strings.ContainsAny(h.Quote, core.MarkStart+core.MarkEnd) {
			t.Errorf("управляющие символы ушли модели: %q", h.Quote)
		}
	}
	if !spoken || !written {
		t.Errorf("ждали реплику с секунды 412 и находку в follow-up: %+v", out.Hits)
	}
	if msg := call(t, cs, "search", map[string]any{"query": "  "}, nil); msg == "" {
		t.Error("пустой запрос должен быть ошибкой")
	}
	call(t, cs, "search", map[string]any{"query": "такого слова нет нигде"}, &out)
	if len(out.Hits) != 0 {
		t.Errorf("нашлось лишнее: %+v", out.Hits)
	}
}

func TestProjects(t *testing.T) {
	cs, _ := client(t)
	var out projectsResult
	call(t, cs, "projects", nil, &out)
	got := map[string]projectRow{}
	for _, p := range out.Projects {
		got[p.Name] = p
	}
	// Платежи: две задачи (одна закрыта разбором инцидента), решение, вопрос.
	pay := got["Платежи"]
	if pay.OpenTasks != 1 || pay.Closed != 1 || pay.Decisions != 1 || pay.OpenQuestions != 1 {
		t.Errorf("Платежи: %+v", pay)
	}
	if pay.About == "" || len(pay.Aliases) == 0 {
		t.Errorf("описание и псевдонимы должны приезжать из реестра: %+v", pay)
	}
	if last := out.Projects[len(out.Projects)-1]; !last.Unassigned || last.Name != core.UnassignedProject {
		t.Errorf("корзина без проекта должна быть последней и помеченной: %+v", last)
	}
	if _, ok := got["Онбординг"]; !ok {
		t.Errorf("проектов: %v", out.Projects)
	}
}

func TestOpenItems(t *testing.T) {
	cs, _ := client(t)
	var out openItemsResult
	call(t, cs, "open_items", map[string]any{"project": "биллинг"}, &out)
	if out.Project != "Платежи" {
		t.Errorf("псевдоним должен привестись к имени, а не к %q", out.Project)
	}
	kinds := map[string]int{}
	for _, it := range out.Items {
		kinds[it.Kind]++
		if it.Status != "open" || it.Project != "Платежи" {
			t.Errorf("не тот пункт: %+v", it)
		}
		if it.MeetingID != planning || it.MeetingTitle != "Планёрка по релизу 2.4" {
			t.Errorf("откуда взялся: %+v", it)
		}
		if it.Kind == "task" && (it.Owner != "Участник Б" || it.Due != "2026-09-11") {
			t.Errorf("задача: %+v", it)
		}
	}
	if kinds["task"] != 1 || kinds["decision"] != 1 || kinds["question"] != 1 {
		t.Errorf("по видам: %v", kinds)
	}

	call(t, cs, "open_items", map[string]any{"kind": "question"}, &out)
	for _, it := range out.Items {
		if it.Kind != "question" {
			t.Errorf("фильтр по виду пропустил %+v", it)
		}
	}
	if len(out.Items) != 2 {
		t.Errorf("открытых вопросов по всем проектам должно быть 2: %+v", out.Items)
	}
	if msg := call(t, cs, "open_items", map[string]any{"kind": "bug"}, nil); msg == "" {
		t.Error("неизвестный вид должен быть ошибкой")
	}
}

func TestCloseItem(t *testing.T) {
	cs, st := client(t)
	var open openItemsResult
	call(t, cs, "open_items", map[string]any{"project": "Платежи", "kind": "task"}, &open)
	if len(open.Items) != 1 {
		t.Fatalf("ждали одну открытую задачу: %+v", open.Items)
	}
	id := open.Items[0].ID

	if msg := call(t, cs, "close_item", map[string]any{"id": id}, nil); !strings.Contains(msg, "reason") {
		t.Errorf("без причины закрывать нельзя: %q", msg)
	}
	var out closeItemResult
	call(t, cs, "close_item", map[string]any{"id": id, "reason": "миграция выехала на стейдж"}, &out)
	if out.Item.Status != "done" || out.Item.Note != "миграция выехала на стейдж" || out.Item.ID != id {
		t.Errorf("закрытый пункт: %+v", out.Item)
	}
	if it, err := st.Item(id); err != nil || it.Status != "done" || it.ClosedIn != "" {
		t.Errorf("в базе: %+v, %v", it, err)
	}
	// Второй раз — ошибка со словами, а не молчаливый успех: модель должна
	// увидеть, что пункт уже закрыт и почему.
	if msg := call(t, cs, "close_item", map[string]any{"id": id, "reason": "ещё раз"}, nil); !strings.Contains(msg, "already closed") {
		t.Errorf("повторное закрытие: %q", msg)
	}
	if msg := call(t, cs, "close_item", map[string]any{"id": "T-0000", "reason": "x"}, nil); !strings.Contains(msg, "open_items") {
		t.Errorf("неизвестный id: %q", msg)
	}
	if msg := call(t, cs, "close_item", map[string]any{"id": id, "reason": "x", "status": "maybe"}, nil); msg == "" {
		t.Error("статус не из списка должен быть ошибкой")
	}

	// dropped — тоже закрытие, но с другим смыслом, и оно доезжает до базы.
	call(t, cs, "open_items", map[string]any{"project": "Онбординг", "kind": "task"}, &open)
	if len(open.Items) != 1 {
		t.Fatalf("Онбординг: %+v", open.Items)
	}
	call(t, cs, "close_item", map[string]any{"id": open.Items[0].ID, "reason": "онбординг заморозили", "status": "dropped"}, &out)
	if out.Item.Status != "dropped" {
		t.Errorf("dropped: %+v", out.Item)
	}
}

// ТЗ отдаются только на чтение: собрать и запустить модель отсюда не может, и
// список инструментов это подтверждает — ни build_spec, ни run_spec в нём нет.
func TestSpecsReadOnly(t *testing.T) {
	cs, st := client(t)
	store, err := spec.Open(st)
	if err != nil {
		t.Fatal(err)
	}
	var items openItemsResult
	call(t, cs, "open_items", map[string]any{"project": "Платежи", "kind": "task"}, &items)
	if len(items.Items) != 1 {
		t.Fatalf("ждали одну задачу по Платежам: %+v", items.Items)
	}
	task := items.Items[0]
	ok := &spec.Spec{
		ID: "S-1", ItemID: task.ID, Project: "Платежи", Repo: t.TempDir(), Status: spec.StatusDraft,
		Title:    "Вебхуки биллинга",
		Places:   []spec.Place{{Path: "billing/webhooks.py", Found: true}},
		Steps:    []string{"поправить обработчик"},
		Unknowns: []spec.Unknown{{Question: "какой провайдер?", Why: "не сказано", Ask: "Участник Б"}},
	}
	if err := store.Save(ok); err != nil {
		t.Fatal(err)
	}
	// Второе, придуманное: без единого вопроса — по нему работать нельзя.
	bad := &spec.Spec{
		ID: "S-2", ItemID: "T-нет", Project: "Платежи", Status: spec.StatusDraft, Title: "выдумка",
		Places: []spec.Place{{Path: "a.py", Found: true}}, Steps: []string{"сделать"},
	}
	if err := store.Save(bad); err != nil {
		t.Fatal(err)
	}

	var list listSpecsResult
	call(t, cs, "list_specs", map[string]any{"project": "биллинг"}, &list)
	// Своих два, и демо-набор кладёт своё по той же задаче — оно старше и
	// уступает место S-1: последнее ТЗ по задаче одно.
	if list.Project != "Платежи" || len(list.Specs) != 2 {
		t.Fatalf("list_specs: %+v", list)
	}
	byID := map[string]specRow{}
	for _, r := range list.Specs {
		byID[r.ID] = r
	}
	if !byID["S-1"].Runnable || byID["S-1"].Unknowns != 1 || byID["S-1"].ItemID != task.ID {
		t.Errorf("годное ТЗ описано неверно: %+v", byID["S-1"])
	}
	if byID["S-2"].Runnable || len(byID["S-2"].Blocked) == 0 {
		t.Errorf("ТЗ без вопросов должно быть помечено негодным: %+v", byID["S-2"])
	}

	var one getSpecResult
	call(t, cs, "get_spec", map[string]any{"id": "S-1"}, &one)
	if !strings.Contains(one.Markdown, "какой провайдер?") || !strings.Contains(one.Markdown, "billing/webhooks.py") {
		t.Errorf("get_spec не отдал само задание:\n%s", one.Markdown)
	}
	if msg := call(t, cs, "get_spec", map[string]any{"id": "S-9"}, nil); !strings.Contains(msg, "list_specs") {
		t.Errorf("неизвестный id должен отсылать к list_specs: %q", msg)
	}

	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if strings.Contains(tool.Name, "run") || strings.Contains(tool.Name, "build") {
			t.Errorf("модели не положено ни собирать, ни запускать ТЗ: %s", tool.Name)
		}
	}
}
