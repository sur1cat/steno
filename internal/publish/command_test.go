package publish

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sur1cat/steno/internal/core"
)

// Адаптеры публикации проверяются на скриптах-заглушках: договор — это то, что
// адаптер получает на stdin и печатает в stdout, и проверить его можно только
// настоящим запуском.

func script(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "adapter.sh")
	if err := os.WriteFile(p, []byte("#!/usr/bin/env bash\nset -euo pipefail\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func testMeeting() (*core.Meeting, *core.Followup, []core.Segment) {
	ended := time.Date(2026, 9, 8, 16, 12, 0, 0, time.UTC)
	m := &core.Meeting{
		ID:           "20260908-1530-abc",
		Title:        "Планёрка",
		MeetURL:      "https://meet.google.com/abc-defg-hij",
		StartedAt:    time.Date(2026, 9, 8, 15, 30, 0, 0, time.UTC),
		EndedAt:      &ended,
		Participants: []string{"Рустем", "Орынгали"},
	}
	f := &core.Followup{
		Title: "Про биллинг и радио",
		TLDR:  []string{"Вебхуки переносим на следующую неделю"},
		ActionItems: []core.ActionItem{
			{Owner: "Орынгали", What: "доделать вебхуки", Due: "2026-09-12", Project: "биллинг", At: 120},
		},
		Decisions:     []core.Decision{{What: "берём вариант Б", Why: "дешевле в поддержке", Project: "радио"}},
		OpenQuestions: []core.OpenQuestion{{Question: "кто платит за домен", WaitingOn: "Рустем", Project: "радио"}},
		Risks:         []string{"успеваем впритык"},
	}
	segs := []core.Segment{
		{Start: 1, End: 3, Speaker: "Рустем", Text: "начнём с биллинга"},
		{Start: 3, End: 6, Speaker: "Орынгали", Text: "вебхуки почти готовы"},
	}
	return m, f, segs
}

// Адаптер получает и сырой разбор, и готовый текст. Без сырого нельзя завести
// задачу в трекере, без готового — написать адаптер в пять строк.
func TestPayloadCarriesRawAndRendered(t *testing.T) {
	m, f, segs := testMeeting()
	p := NewPayload(m, f, segs, "https://docs.google.com/d/1")

	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"meeting", "followup", "text", "links", "transcript"} {
		if _, ok := got[key]; !ok {
			t.Errorf("в договоре нет раздела %q", key)
		}
	}
	if p.Meeting.ID != m.ID || p.Meeting.URL != m.MeetURL || p.Meeting.Title != m.Title {
		t.Errorf("созвон доехал не тот: %+v", p.Meeting)
	}
	if p.Meeting.StartedAt != "2026-09-08T15:30:00Z" || p.Meeting.DurationSec != 42*60 {
		t.Errorf("время созвона: %q, %d секунд", p.Meeting.StartedAt, p.Meeting.DurationSec)
	}
	// Проекты собираем мы, а не адаптер: они разложены по трём спискам разбора.
	if strings.Join(p.Meeting.Projects, ",") != "биллинг,радио" {
		t.Errorf("проекты созвона: %v", p.Meeting.Projects)
	}
	if len(p.Followup.ActionItems) != 1 || p.Followup.ActionItems[0].Owner != "Орынгали" {
		t.Errorf("сырой разбор не доехал: %+v", p.Followup)
	}
	for name, text := range map[string]string{
		"plain": p.Text.Plain, "markdown": p.Text.Markdown, "html": p.Text.HTML,
	} {
		if !strings.Contains(text, "доделать вебхуки") {
			t.Errorf("в тексте %s нет задачи с созвона:\n%s", name, text)
		}
	}
	if !strings.Contains(p.Transcript, "вебхуки почти готовы") {
		t.Errorf("расшифровка не доехала: %q", p.Transcript)
	}
	if p.Links["google_doc"] != "https://docs.google.com/d/1" {
		t.Errorf("ссылка на документ не доехала: %v", p.Links)
	}
}

func TestRunCommandFeedsStdinAndTakesLink(t *testing.T) {
	m, f, segs := testMeeting()
	dump := filepath.Join(t.TempDir(), "stdin.json")
	sh := script(t, `cat > "$1"
echo "адаптер отработал" >&2
echo https://discord.com/channels/1/2/3`)

	link, notes, err := RunCommand(context.Background(),
		Command{Cmd: []string{sh, dump}}, NewPayload(m, f, segs, ""))
	if err != nil {
		t.Fatalf("адаптер не отработал: %v", err)
	}
	if link != "https://discord.com/channels/1/2/3" {
		t.Errorf("ссылка не взялась из stdout: %q", link)
	}
	if !strings.Contains(notes, "адаптер отработал") {
		t.Errorf("stderr адаптера потерян: %q", notes)
	}
	raw, err := os.ReadFile(dump)
	if err != nil {
		t.Fatalf("адаптер не получил ничего на stdin: %v", err)
	}
	var back Payload
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("на stdin приехал не JSON: %v\n%s", err, raw)
	}
	if back.Meeting.ID != m.ID || len(back.Followup.ActionItems) != 1 {
		t.Errorf("на stdin приехал не тот созвон: %+v", back.Meeting)
	}
}

// Ради этого follow-up и уехал на stdin: аргументом такое не передать — упрётся
// в предел длины командной строки.
func TestRunCommandCarriesHugePayload(t *testing.T) {
	m, f, segs := testMeeting()
	for i := 0; i < 4000; i++ {
		segs = append(segs, core.Segment{Start: float64(i), End: float64(i) + 1,
			Speaker: "Рустем", Text: "длинная реплика про биллинг и вебхуки номер такой-то"})
	}
	p := NewPayload(m, f, segs, "")
	if len(p.Transcript) < 256*1024 {
		t.Fatalf("расшифровка вышла всего %d байт — проверка ничего не проверяет", len(p.Transcript))
	}
	dump := filepath.Join(t.TempDir(), "stdin.json")
	sh := script(t, `wc -c > "$1"`)

	if _, _, err := RunCommand(context.Background(), Command{Cmd: []string{sh, dump}}, p); err != nil {
		t.Fatalf("большой созвон не доехал: %v", err)
	}
	raw, err := os.ReadFile(dump)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(p)
	if got := strings.TrimSpace(string(raw)); got != strconv.Itoa(len(want)) {
		t.Errorf("адаптер получил %s байт вместо %d", got, len(want))
	}
}

// Отказ должен читаться человеком, а не быть пустым кодом возврата.
func TestRunCommandRefusalIsReadable(t *testing.T) {
	m, f, segs := testMeeting()
	p := NewPayload(m, f, segs, "")

	t.Run("адаптер вернул не ноль", func(t *testing.T) {
		sh := script(t, `echo "вебхук ответил 404 — его удалили" >&2
exit 3`)
		link, notes, err := RunCommand(context.Background(), Command{Cmd: []string{sh}}, p)
		if err == nil {
			t.Fatal("отказ адаптера принят за успех")
		}
		if !strings.Contains(err.Error(), "вебхук ответил 404") {
			t.Errorf("в ошибке нет того, что сказал адаптер: %v", err)
		}
		if link != "" {
			t.Errorf("при отказе вернулась ссылка %q", link)
		}
		if !strings.Contains(notes, "вебхук ответил 404") {
			t.Errorf("stderr адаптера потерян: %q", notes)
		}
	})

	t.Run("адаптера нет", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "нет-такого.sh")
		_, _, err := RunCommand(context.Background(), Command{Cmd: []string{missing}}, p)
		if err == nil {
			t.Fatal("несуществующий адаптер отработал")
		}
		if !strings.Contains(err.Error(), "нет") || !strings.Contains(err.Error(), missing) {
			t.Errorf("ошибка не говорит, какого файла нет: %v", err)
		}
	})

	t.Run("адаптер без права на запуск", func(t *testing.T) {
		p2 := filepath.Join(t.TempDir(), "adapter.sh")
		if err := os.WriteFile(p2, []byte("#!/usr/bin/env bash\necho ok\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, _, err := RunCommand(context.Background(), Command{Cmd: []string{p2}}, p)
		if err == nil {
			t.Fatal("файл без бита запуска отработал")
		}
		if !strings.Contains(err.Error(), "chmod +x") {
			t.Errorf("ошибка не подсказывает, чем это чинится: %v", err)
		}
	})

	t.Run("адаптер молчит вечно", func(t *testing.T) {
		sh := script(t, `sleep 60`)
		started := time.Now()
		_, _, err := RunCommand(context.Background(),
			Command{Cmd: []string{sh}, Timeout: 300 * time.Millisecond}, p)
		if err == nil {
			t.Fatal("зависший адаптер отработал")
		}
		if !strings.Contains(err.Error(), "не уложился") {
			t.Errorf("ошибка не про срок: %v", err)
		}
		if d := time.Since(started); d > 20*time.Second {
			t.Errorf("зависший адаптер держал публикацию %s", d)
		}
	})

	t.Run("команды нет вовсе", func(t *testing.T) {
		if _, _, err := RunCommand(context.Background(), Command{}, p); err == nil {
			t.Fatal("пустая команда принята")
		}
	})
}

// Адаптер отработал, но напечатал в stdout свой лог вместо ссылки. Это не
// отказ — но и не молчание: иначе «ссылка не появилась» ищут в steno.
func TestRunCommandSeesJunkInStdout(t *testing.T) {
	m, f, segs := testMeeting()
	sh := script(t, `echo "всё ушло, спасибо"`)
	link, notes, err := RunCommand(context.Background(),
		Command{Cmd: []string{sh}}, NewPayload(m, f, segs, ""))
	if err != nil {
		t.Fatalf("болтливый адаптер принят за отказ: %v", err)
	}
	if link != "" {
		t.Errorf("в базу уехала не ссылка: %q", link)
	}
	if !strings.Contains(notes, "всё ушло, спасибо") {
		t.Errorf("про мусор в stdout не сказано ни слова: %q", notes)
	}
}

// Язык интерфейса доезжает до адаптера — на нём он говорит с человеком.
func TestRunCommandPassesLangAndArgs(t *testing.T) {
	m, f, segs := testMeeting()
	sh := script(t, `echo "язык=$STENO_LANG адресат=$1" >&2`)
	_, notes, err := RunCommand(context.Background(),
		Command{Cmd: []string{sh, "#созвоны"}}, NewPayload(m, f, segs, ""))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(notes, "адресат=#созвоны") {
		t.Errorf("аргументы адаптера не доехали: %q", notes)
	}
	if !strings.Contains(notes, "язык=ru") {
		t.Errorf("язык интерфейса не доехал: %q", notes)
	}
}

func TestCommandName(t *testing.T) {
	cases := []struct {
		cmd  []string
		want string
	}{
		{[]string{"./adapters/discord.sh"}, "discord"},
		{[]string{"/opt/homebrew/share/steno/adapters/notes-to-file.sh", "~/notes"}, "notes-to-file"},
		{[]string{"curl"}, "curl"},
		{nil, "cmd"},
		{[]string{""}, "cmd"},
	}
	for _, c := range cases {
		if got := CommandName(c.cmd); got != c.want {
			t.Errorf("CommandName(%v) = %q, ожидали %q", c.cmd, got, c.want)
		}
	}
	if got := (Command{Name: "мой чат", Cmd: []string{"./adapters/discord.sh"}}).Target(); got != "мой чат" {
		t.Errorf("заданное имя перебито вычисленным: %q", got)
	}
}

// Отказ одного адаптера не отменяет остальных, и каждый ложится в publications
// под своим именем — иначе два адресата затирали бы ссылку друг друга.
func TestPublishCommandsRecordsEachTarget(t *testing.T) {
	m, f, segs := testMeeting()
	st, err := core.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ok := script(t, `echo https://discord.com/channels/1/2/3`)
	bad := script(t, `echo "вебхук ответил 404" >&2
exit 1`)
	after := script(t, `echo https://mattermost.example/1`)

	errs := PublishCommands(context.Background(), st, log.New(io.Discard, "", 0),
		[]Command{{Name: "discord", Cmd: []string{ok}},
			{Name: "notion", Cmd: []string{bad}},
			{Name: "mattermost", Cmd: []string{after}}},
		NewPayload(m, f, segs, ""))

	if len(errs) != 1 {
		t.Fatalf("ошибок %d, ожидали одну: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0].Error(), "notion") {
		t.Errorf("ошибка не называет адресата: %v", errs[0])
	}
	links, err := st.Publications(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if links["discord"] != "https://discord.com/channels/1/2/3" {
		t.Errorf("ссылка первого адаптера не записана: %v", links)
	}
	if links["mattermost"] != "https://mattermost.example/1" {
		t.Errorf("после отказа второго третий не отработал: %v", links)
	}
	var stored string
	if err := st.DB.QueryRow(`SELECT error FROM publications WHERE meeting_id=? AND target=?`,
		m.ID, "notion").Scan(&stored); err != nil {
		t.Fatalf("отказ не записан в publications: %v", err)
	}
	if !strings.Contains(stored, "вебхук ответил 404") {
		t.Errorf("в publications.error нет причины: %q", stored)
	}
}

func TestRenderMarkdownSectionsAndEscaping(t *testing.T) {
	m, f, _ := testMeeting()
	f.ActionItems[0].Owner = "Ор*ынгали_"
	f.TLDR = append(f.TLDR, "вставить <script>alert(1)</script> нельзя")
	out := RenderMarkdown(m, f, "https://docs.google.com/d/1")

	for _, want := range []string{"## Про биллинг и радио", "### Задачи", "### Решения",
		"### Открытые вопросы", "### Риски", "(https://docs.google.com/d/1)"} {
		if !strings.Contains(out, want) {
			t.Errorf("в markdown нет %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Ор*ынгали_") {
		t.Errorf("разметка в имени не погашена:\n%s", out)
	}
	if strings.Contains(out, "<script>") {
		t.Errorf("живой HTML уехал в markdown:\n%s", out)
	}
	if !strings.Contains(out, "2026-09-12") {
		t.Errorf("срок задачи потерян:\n%s", out)
	}
}
