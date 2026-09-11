// Пакет mcp — MCP-сервер steno: те же созвоны, follow-up и открытые пункты,
// что видят CLI и панель, но инструментами для модели. Claude Code, Claude
// Desktop и Cursor подключают его строкой `steno mcp` и дальше отвечают на
// «что решили про миграцию?» и «что открыто по платежам?» сами, из базы.
//
// Сервис для этого не нужен: сервер открывает базу так же, как `steno show`,
// и живёт, пока жив клиент. Всё, кроме close_item, только читает — модели,
// которая роется в созвонах, незачем уметь их удалять или звать бота.
//
// Описания инструментов и ошибки здесь по-английски и без i18n: их читает
// модель, а не человек, и язык интерфейса тут ни при чём. Сами данные —
// заголовки, реплики, задачи — идут как записаны, на языке созвона.
package mcp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sur1cat/steno/internal/core"
)

// instructions — что клиент показывает модели при подключении: с чего начать
// и какой инструмент за каким вопросом. Без этого модель зовёт get_transcript
// на каждый вопрос и тонет в репликах.
const instructions = `steno records meetings and keeps what came out of them: the transcript, the follow-up (summary, decisions, tasks with owners and due dates, open questions, risks) and the live state per project — what is still open across all meetings. Everything comes from the local steno database; no service needs to be running.

Where to start: list_meetings for recent meetings and their ids, projects for the project names. "What did we decide about X?" — search for X, then get_followup for the meeting that matched, or get_transcript with from_sec/to_sec around the hit to read the exact words. "What is open on X?" — open_items with the project name. Text is in whatever language the meeting was held in; ids and field names are stable.

close_item is the only tool that changes anything. Call it only when the user explicitly asks to close, finish or drop an item.`

// Окно расшифровки за один вызов. Часовой созвон — это шестьсот реплик и
// шестьдесят тысяч символов; отдать их целиком на вопрос «что там про откат»
// значит забить контекст модели тем, что она не спрашивала. Двести реплик —
// это пятнадцать-двадцать минут разговора, и на них помещается любой один
// сюжет; дальше модель листает по next_from_sec.
const maxLines = 200

// New собирает сервер с инструментами поверх открытой базы. Транспорт выбирает
// вызывающий: stdio — в `steno mcp`, память — в тестах.
func New(st *core.Store) *sdk.Server {
	s := &server{st: st}
	srv := sdk.NewServer(&sdk.Implementation{Name: "steno", Version: core.StenoVersion()},
		&sdk.ServerOptions{Instructions: instructions})

	// Подсказки клиенту: всё, кроме close_item, только читает, и никто из
	// инструментов не ходит наружу. Claude Code по ним решает, спрашивать ли
	// человека перед вызовом.
	no := false
	readOnly := &sdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &no}

	sdk.AddTool(srv, &sdk.Tool{
		Name: "list_meetings",
		Description: "Recent meetings, newest first: id, title, when, how long, who was there, status and how many tasks came out. " +
			"Status tells how far the meeting got: recording, recorded, transcribed, summarized, published, publish_failed or failed. " +
			"Pass project to keep only the meetings where that project came up.",
		Annotations: readOnly,
	}, s.listMeetings)
	sdk.AddTool(srv, &sdk.Tool{
		Name: "get_followup",
		Description: "The follow-up of one meeting, as written up after the call: title, tldr, decisions with reasons, " +
			"tasks with owners and due dates, open questions with who they wait on, risks, timeline. " +
			"Every entry carries `at`, the second in the recording it was said — pass it to get_transcript to read the exact words. " +
			"Omit id for the most recent meeting.",
		Annotations: readOnly,
	}, s.getFollowup)
	sdk.AddTool(srv, &sdk.Tool{
		Name: "get_transcript",
		Description: "The transcript of one meeting: lines with the second they start at, the speaker and the text. " +
			"Returns at most 200 lines per call; narrow it with from_sec and to_sec (for example 60 seconds before " +
			"and 180 after a search hit or a follow-up `at`), and continue from next_from_sec when truncated is true. " +
			"Do not read a whole meeting unless the user asks for it.",
		Annotations: readOnly,
	}, s.getTranscript)
	sdk.AddTool(srv, &sdk.Tool{
		Name: "search",
		Description: "Full-text search over every transcript and follow-up. Each hit names the meeting, the second and the passage; " +
			"kind says whether it comes from what was said (transcript) or from the write-up (followup). " +
			"Every word must match and prefixes count, so `migrat` finds `migration`. " +
			"For the context around a hit call get_transcript with from_sec/to_sec around its `at`.",
		Annotations: readOnly,
	}, s.search)
	sdk.AddTool(srv, &sdk.Tool{
		Name: "projects",
		Description: "Every project steno knows: name, what it is, how it is called out loud, who takes part, " +
			"and how many tasks, questions and decisions are open for it. " +
			"The one flagged unassigned is not a project but the bucket for items that matched none.",
		Annotations: readOnly,
	}, s.projects)
	sdk.AddTool(srv, &sdk.Tool{
		Name: "open_items",
		Description: "What is still open: tasks, questions and decisions in force, across all meetings, with the owner, the due date, " +
			"the meeting each came from and the quote it rests on. Filter by project (name or alias) and by kind. " +
			"Item ids look like T-3f2a, Q-…, D-… and are what close_item takes.",
		Annotations: readOnly,
	}, s.openItems)
	sdk.AddTool(srv, &sdk.Tool{
		Name: "close_item",
		Description: "Close one open item by id with a reason: done, or dropped when it will not be done. " +
			"The only tool that writes; use it only when the user explicitly asks to close something. " +
			"Returns the item as it is now.",
		Annotations: &sdk.ToolAnnotations{IdempotentHint: true, DestructiveHint: &no, OpenWorldHint: &no},
	}, s.closeItem)
	return srv
}

// Serve — сервер по stdio до конца ввода или отмены контекста. stdout занят
// протоколом: всё, что хочется сказать человеку, идёт в stderr.
func Serve(ctx context.Context, st *core.Store) error {
	return New(st).Run(ctx, &sdk.StdioTransport{})
}

type server struct {
	st *core.Store
}

// meeting — шапка созвона, одна и та же в списке, follow-up и расшифровке:
// модели не приходится сверять два формата.
type meeting struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	StartedAt    string   `json:"started_at" jsonschema:"RFC 3339, local time"`
	DurationSec  int      `json:"duration_sec"`
	Participants []string `json:"participants"`
	Status       string   `json:"status"`
	LeftReason   string   `json:"left_reason,omitempty" jsonschema:"why the bot left the call, when it did not stay to the end"`
	Tasks        int      `json:"tasks" jsonschema:"how many tasks came out of it"`
}

func stamp(t time.Time) string { return t.Format(time.RFC3339) }

func fromRow(r core.MeetingRow) meeting {
	return meeting{
		ID: r.ID, Title: r.Title, StartedAt: stamp(r.StartedAt),
		DurationSec: int(r.Duration.Seconds()), Participants: orEmpty(r.Participants),
		Status: r.Status, LeftReason: r.LeftReason, Tasks: r.Tasks,
	}
}

func fromMeeting(m *core.Meeting, tasks int) meeting {
	dur := 0
	if m.EndedAt != nil {
		dur = int(m.EndedAt.Sub(m.StartedAt).Seconds())
	}
	return meeting{
		ID: m.ID, Title: m.Title, StartedAt: stamp(m.StartedAt),
		DurationSec: dur, Participants: orEmpty(m.Participants),
		Status: m.Status, LeftReason: m.LeftReason, Tasks: tasks,
	}
}

// orEmpty — [] вместо null: список без участников модель читает как «никого»,
// а null — как «поле не заполнено», и переспрашивает.
func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// meeting — созвон по id, а без id — последний: тот же уговор, что у `steno
// show` и `steno transcript`. Ошибки говорят, откуда брать id: модель, которой
// сказали только «нет такого», выдумает следующий.
func (s *server) meeting(id string) (*core.Meeting, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		latest, _, err := s.st.LatestMeeting()
		if err != nil {
			return nil, err
		}
		if latest == "" {
			return nil, errors.New("no meetings recorded yet")
		}
		id = latest
	}
	m, err := s.st.Meeting(id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("no meeting with id %q — ids come from list_meetings or search", id)
	}
	return m, err
}

// clamp — предел на размер ответа: ноль значит «по умолчанию», а просьба о
// тысяче строк режется до потолка молча, потому что тысяча строк в контексте
// модели — это не ответ, а шум.
func clamp(n, def, ceiling int) int {
	switch {
	case n <= 0:
		return def
	case n > ceiling:
		return ceiling
	}
	return n
}

// --- list_meetings -----------------------------------------------------------

type listMeetingsArgs struct {
	Limit   int    `json:"limit,omitempty" jsonschema:"how many meetings to return, newest first; default 20, at most 100"`
	Project string `json:"project,omitempty" jsonschema:"only meetings where this project came up — a task, decision or question was opened or closed for it; name or alias as listed by projects"`
}

type listMeetingsResult struct {
	Meetings []meeting `json:"meetings"`
	Total    int       `json:"total" jsonschema:"how many meetings the database holds in total, regardless of the filter"`
}

func (s *server) listMeetings(_ context.Context, _ *sdk.CallToolRequest, in listMeetingsArgs) (*sdk.CallToolResult, listMeetingsResult, error) {
	limit := clamp(in.Limit, 20, 100)
	var rows []core.MeetingRow
	var err error
	if p := strings.TrimSpace(in.Project); p != "" {
		name, rerr := s.resolveProject(p)
		if rerr != nil {
			return nil, listMeetingsResult{}, rerr
		}
		rows, err = s.st.ProjectMeetings(name, limit)
	} else {
		rows, err = s.st.ListMeetings(limit, 0)
	}
	if err != nil {
		return nil, listMeetingsResult{}, err
	}
	total, err := s.st.CountMeetings()
	if err != nil {
		return nil, listMeetingsResult{}, err
	}
	out := listMeetingsResult{Meetings: make([]meeting, 0, len(rows)), Total: total}
	for _, r := range rows {
		out.Meetings = append(out.Meetings, fromRow(r))
	}
	return nil, out, nil
}

// --- get_followup ------------------------------------------------------------

type meetingArgs struct {
	ID string `json:"id,omitempty" jsonschema:"meeting id from list_meetings or search; omit for the most recent meeting"`
}

type followupResult struct {
	Meeting  meeting           `json:"meeting"`
	Followup *core.Followup    `json:"followup"`
	Links    map[string]string `json:"links,omitempty" jsonschema:"where the follow-up was published: target to url"`
}

func (s *server) getFollowup(_ context.Context, _ *sdk.CallToolRequest, in meetingArgs) (*sdk.CallToolResult, followupResult, error) {
	m, err := s.meeting(in.ID)
	if err != nil {
		return nil, followupResult{}, err
	}
	f, err := s.st.Followup(m.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, followupResult{}, fmt.Errorf("meeting %s (%s) has no follow-up yet: its status is %q",
			m.ID, core.OrDash(m.Title), m.Status)
	}
	if err != nil {
		return nil, followupResult{}, err
	}
	links, err := s.st.Publications(m.ID)
	if err != nil {
		return nil, followupResult{}, err
	}
	return nil, followupResult{Meeting: fromMeeting(m, len(f.ActionItems)), Followup: f, Links: links}, nil
}

// --- get_transcript ----------------------------------------------------------

type transcriptArgs struct {
	ID      string  `json:"id,omitempty" jsonschema:"meeting id from list_meetings or search; omit for the most recent meeting"`
	FromSec float64 `json:"from_sec,omitempty" jsonschema:"start of the window, seconds from the beginning of the recording; default 0"`
	ToSec   float64 `json:"to_sec,omitempty" jsonschema:"end of the window in seconds; default: to the end, still capped at 200 lines"`
}

type line struct {
	At      float64 `json:"at" jsonschema:"seconds from the start of the recording"`
	Speaker string  `json:"speaker,omitempty"`
	Text    string  `json:"text"`
}

type transcriptResult struct {
	Meeting    meeting `json:"meeting"`
	Lines      []line  `json:"lines"`
	TotalLines int     `json:"total_lines" jsonschema:"lines in the whole transcript"`
	// Окно не вместило всё: с какой секунды продолжать.
	Truncated   bool    `json:"truncated" jsonschema:"true when the 200-line cap cut the window short"`
	NextFromSec float64 `json:"next_from_sec,omitempty" jsonschema:"pass as from_sec to continue"`
}

func (s *server) getTranscript(_ context.Context, _ *sdk.CallToolRequest, in transcriptArgs) (*sdk.CallToolResult, transcriptResult, error) {
	if in.ToSec > 0 && in.ToSec < in.FromSec {
		return nil, transcriptResult{}, fmt.Errorf("to_sec (%v) is before from_sec (%v)", in.ToSec, in.FromSec)
	}
	m, err := s.meeting(in.ID)
	if err != nil {
		return nil, transcriptResult{}, err
	}
	segs, err := s.st.Segments(m.ID)
	if err != nil {
		return nil, transcriptResult{}, err
	}
	if len(segs) == 0 {
		return nil, transcriptResult{}, fmt.Errorf("meeting %s (%s) has no transcript yet: its status is %q",
			m.ID, core.OrDash(m.Title), m.Status)
	}
	tasks := 0
	if f, err := s.st.Followup(m.ID); err == nil {
		tasks = len(f.ActionItems)
	}
	out := transcriptResult{Meeting: fromMeeting(m, tasks), Lines: []line{}, TotalLines: len(segs)}
	for _, sg := range segs {
		if sg.Start < in.FromSec {
			continue
		}
		if in.ToSec > 0 && sg.Start > in.ToSec {
			break
		}
		if len(out.Lines) == maxLines {
			out.Truncated, out.NextFromSec = true, sg.Start
			break
		}
		out.Lines = append(out.Lines, line{At: sg.Start, Speaker: sg.Speaker, Text: sg.Text})
	}
	return nil, out, nil
}

// --- search ------------------------------------------------------------------

type searchArgs struct {
	Query string `json:"query" jsonschema:"words to look for; every word must match, prefixes count"`
	Limit int    `json:"limit,omitempty" jsonschema:"at most this many hits; default 20, at most 100"`
}

type hit struct {
	MeetingID string  `json:"meeting_id"`
	Title     string  `json:"title"`
	StartedAt string  `json:"started_at"`
	Kind      string  `json:"kind" jsonschema:"transcript or followup"`
	At        float64 `json:"at" jsonschema:"second in the recording where the passage starts; 0 for a follow-up hit"`
	Speaker   string  `json:"speaker,omitempty"`
	Quote     string  `json:"quote" jsonschema:"the matching passage, matches wrapped in **"`
}

type searchResult struct {
	Query string `json:"query"`
	Hits  []hit  `json:"hits"`
}

// В индексе вид записи назван по-русски — это ключ, по которому переиндексация
// стирает своё; модели он ни о чём, поэтому наружу идёт английское слово.
func kindName(k string) string {
	switch k {
	case "расшифровка":
		return "transcript"
	case "follow-up":
		return "followup"
	}
	return k
}

// Найденное в сниппете обрамлено U+0002/U+0003, потому что в индексе лежит
// сырой текст и разметку в него не кладут. Модели ставим **…**: она читает
// это как выделение, а управляющие символы — как мусор.
var markHit = strings.NewReplacer(core.MarkStart, "**", core.MarkEnd, "**")

func (s *server) search(_ context.Context, _ *sdk.CallToolRequest, in searchArgs) (*sdk.CallToolResult, searchResult, error) {
	q := strings.TrimSpace(in.Query)
	if q == "" {
		return nil, searchResult{}, errors.New("query is empty")
	}
	hits, err := s.st.Search(q, clamp(in.Limit, 20, 100))
	if err != nil {
		return nil, searchResult{}, err
	}
	out := searchResult{Query: q, Hits: make([]hit, 0, len(hits))}
	for _, h := range hits {
		out.Hits = append(out.Hits, hit{
			MeetingID: h.MeetingID, Title: h.Title, StartedAt: stamp(time.Unix(h.StartedAt, 0)),
			Kind: kindName(h.Kind), At: h.At, Speaker: h.Speaker, Quote: strings.TrimSpace(markHit.Replace(h.Snippet)),
		})
	}
	return nil, out, nil
}

// --- projects ----------------------------------------------------------------

type projectRow struct {
	Name          string   `json:"name"`
	About         string   `json:"about,omitempty"`
	Aliases       []string `json:"aliases,omitempty" jsonschema:"how the project is called out loud; open_items accepts these too"`
	People        []string `json:"people,omitempty"`
	OpenTasks     int      `json:"open_tasks"`
	OpenQuestions int      `json:"open_questions"`
	Decisions     int      `json:"decisions" jsonschema:"decisions in force"`
	Closed        int      `json:"closed" jsonschema:"items closed as done or dropped"`
	Unassigned    bool     `json:"unassigned,omitempty" jsonschema:"true for the bucket of items that matched no project"`
}

type projectsResult struct {
	Projects []projectRow `json:"projects"`
}

// projectNames — заведённые проекты и те, по которым что-то накопилось, одним
// списком: первый созвон случается позже, чем заводят проект, и проект без
// пунктов — всё ещё проект. Тот же уговор, что у `steno projects`.
func (s *server) projectNames() ([]string, map[string]core.Project, error) {
	names, err := s.st.KnownProjects()
	if err != nil {
		return nil, nil, err
	}
	registered, err := s.st.Projects()
	if err != nil {
		return nil, nil, err
	}
	seen := map[string]bool{}
	for _, n := range names {
		seen[n] = true
	}
	byName := map[string]core.Project{}
	for _, p := range registered {
		byName[p.Name] = p
		if !seen[p.Name] {
			names = append(names, p.Name)
			seen[p.Name] = true
		}
	}
	// Корзина «не определён» — в конце: это не проект, а то, что не легло ни
	// в один, и первой строкой она читалась бы как самый крупный проект.
	sort.SliceStable(names, func(i, j int) bool {
		if a, b := names[i] == core.UnassignedProject, names[j] == core.UnassignedProject; a != b {
			return b
		}
		return names[i] < names[j]
	})
	return names, byName, nil
}

func (s *server) projects(_ context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, projectsResult, error) {
	names, byName, err := s.projectNames()
	if err != nil {
		return nil, projectsResult{}, err
	}
	out := projectsResult{Projects: make([]projectRow, 0, len(names))}
	for _, n := range names {
		items, err := s.st.ProjectItems(n)
		if err != nil {
			return nil, projectsResult{}, err
		}
		row := projectRow{Name: n, Unassigned: n == core.UnassignedProject}
		if p, ok := byName[n]; ok {
			row.About, row.Aliases, row.People = p.About, p.Aliases, p.People
		}
		for _, it := range items {
			switch {
			case it.Status != "open":
				row.Closed++
			case it.Kind == core.KindTask:
				row.OpenTasks++
			case it.Kind == core.KindQuestion:
				row.OpenQuestions++
			case it.Kind == core.KindDecision:
				row.Decisions++
			}
		}
		out.Projects = append(out.Projects, row)
	}
	return nil, out, nil
}

// resolveProject приводит название, как его сказала модель, к тому, как оно
// записано: псевдонимы («биллинг» → «Платежи») и регистр. Незнакомое имя —
// ошибка со списком известных, а не пустой ответ: пустой список модель
// прочтёт как «по проекту ничего не открыто» и так и скажет человеку.
func (s *server) resolveProject(said string) (string, error) {
	names, byName, err := s.projectNames()
	if err != nil {
		return "", err
	}
	for _, n := range names {
		if n == said {
			return n, nil
		}
	}
	registered := make([]core.Project, 0, len(byName))
	for _, p := range byName {
		registered = append(registered, p)
	}
	if n := core.MatchProject(registered, said); n != core.UnassignedProject {
		return n, nil
	}
	for _, n := range names {
		if strings.EqualFold(n, said) {
			return n, nil
		}
	}
	return "", fmt.Errorf("no project %q; known projects: %s", said, strings.Join(names, ", "))
}

// --- open_items --------------------------------------------------------------

type openItemsArgs struct {
	Project string `json:"project,omitempty" jsonschema:"project name or alias as listed by projects; omit for every project"`
	Kind    string `json:"kind,omitempty" jsonschema:"task, question or decision; omit for all three"`
}

type item struct {
	ID           string `json:"id"`
	Project      string `json:"project"`
	Kind         string `json:"kind" jsonschema:"task, question or decision"`
	Text         string `json:"text"`
	Owner        string `json:"owner,omitempty" jsonschema:"who does it, or who a question waits on"`
	Due          string `json:"due,omitempty" jsonschema:"YYYY-MM-DD when a date was named"`
	Status       string `json:"status" jsonschema:"open, done or dropped"`
	OpenedAt     string `json:"opened_at"`
	MeetingID    string `json:"meeting_id,omitempty" jsonschema:"the meeting it came from"`
	MeetingTitle string `json:"meeting_title,omitempty"`
	Quote        string `json:"quote,omitempty" jsonschema:"the words in the transcript it rests on; for a decision, the reason"`
	Note         string `json:"note,omitempty" jsonschema:"why it was closed"`
	ClosedIn     string `json:"closed_in,omitempty" jsonschema:"the meeting that closed it, when it was a meeting"`
}

type openItemsResult struct {
	Project string `json:"project,omitempty"`
	Items   []item `json:"items"`
}

// item собирает пункт для модели. Заголовки созвонов подтягиваются по одному и
// запоминаются на вызов: пунктов с одного созвона много, а созвонов — мало.
func (s *server) item(it core.ProjectItem, titles map[string]string) item {
	out := item{
		ID: it.ID, Project: it.Project, Kind: string(it.Kind), Text: it.Text,
		Owner: it.Owner, Due: it.Due, Status: it.Status,
		OpenedAt: it.OpenedAt.Format("2006-01-02"), MeetingID: it.OpenedIn,
		Quote: it.Quote, Note: it.Note, ClosedIn: it.ClosedIn,
	}
	if it.OpenedIn != "" {
		title, ok := titles[it.OpenedIn]
		if !ok {
			if m, err := s.st.Meeting(it.OpenedIn); err == nil {
				title = m.Title
			}
			titles[it.OpenedIn] = title
		}
		out.MeetingTitle = title
	}
	return out
}

func (s *server) openItems(_ context.Context, _ *sdk.CallToolRequest, in openItemsArgs) (*sdk.CallToolResult, openItemsResult, error) {
	project := ""
	if p := strings.TrimSpace(in.Project); p != "" {
		name, err := s.resolveProject(p)
		if err != nil {
			return nil, openItemsResult{}, err
		}
		project = name
	}
	kind := strings.ToLower(strings.TrimSpace(in.Kind))
	switch core.ItemKind(kind) {
	case "", core.KindTask, core.KindQuestion, core.KindDecision:
	default:
		return nil, openItemsResult{}, fmt.Errorf("kind %q is not one of task, question, decision", in.Kind)
	}
	items, err := s.st.OpenItems(project)
	if err != nil {
		return nil, openItemsResult{}, err
	}
	out := openItemsResult{Project: project, Items: make([]item, 0, len(items))}
	titles := map[string]string{}
	for _, it := range items {
		if kind != "" && string(it.Kind) != kind {
			continue
		}
		out.Items = append(out.Items, s.item(it, titles))
	}
	return nil, out, nil
}

// --- close_item --------------------------------------------------------------

type closeItemArgs struct {
	ID     string `json:"id" jsonschema:"item id from open_items, like T-3f2a"`
	Reason string `json:"reason" jsonschema:"why it is closed, one line; kept with the item"`
	Status string `json:"status,omitempty" jsonschema:"done (default) or dropped — dropped means it will not be done"`
}

type closeItemResult struct {
	Item item `json:"item"`
}

func (s *server) closeItem(_ context.Context, _ *sdk.CallToolRequest, in closeItemArgs) (*sdk.CallToolResult, closeItemResult, error) {
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return nil, closeItemResult{}, errors.New("id is required — ids come from open_items")
	}
	// Причина обязательна: закрытый без слов пункт через месяц выглядит как
	// закрытый по ошибке, и его открывают заново.
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		return nil, closeItemResult{}, errors.New("reason is required: say why the item is closed")
	}
	status := strings.ToLower(strings.TrimSpace(in.Status))
	switch status {
	case "":
		status = "done"
	case "done", "dropped":
	default:
		return nil, closeItemResult{}, fmt.Errorf("status %q is not one of done, dropped", in.Status)
	}
	it, err := s.st.Item(id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, closeItemResult{}, fmt.Errorf("no item with id %q — ids come from open_items", id)
	}
	if err != nil {
		return nil, closeItemResult{}, err
	}
	if it.Status != "open" {
		return nil, closeItemResult{}, fmt.Errorf("item %s is already closed as %s (%s)", id, it.Status, core.OrDash(it.Note))
	}
	// Созвона, на котором это закрыли, нет — закрыли руками, из разговора с
	// моделью; closed_in остаётся пустым, как у кнопки в панели.
	if err := s.st.CloseItem(id, status, reason, ""); err != nil {
		return nil, closeItemResult{}, err
	}
	if it, err = s.st.Item(id); err != nil {
		return nil, closeItemResult{}, err
	}
	return nil, closeItemResult{Item: s.item(it, map[string]string{})}, nil
}
