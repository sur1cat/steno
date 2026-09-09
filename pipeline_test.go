package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Проверяем весь путь от выхода адаптера расшифровки до готовых текстов для
// Docs, Slack и Telegram. Единственное, чего здесь нет, — вызова Claude:
// он стоит денег и требует сети.
func TestPipelineWithoutClaude(t *testing.T) {
	dir := t.TempDir()

	whisper := filepath.Join(dir, "whisper.json")
	mustWrite(t, whisper, `{"segments":[
      {"start":0.0,"end":4.0,"text":"давайте начнём с релиза"},
      {"start":5.0,"end":9.5,"text":"я закончу миграцию к пятнице"},
      {"start":600.0,"end":602.0,"text":"на этом всё"}
    ]}`)

	captions := filepath.Join(dir, "captions.jsonl")
	mustWrite(t, captions, strings.Join([]string{
		`{"speaker":"Участник А","text":"давайте начнём","start":1.5,"end":5.5}`,
		`{"speaker":"Участник Б","text":"я закончу миграцию","start":6.5,"end":11.0}`,
	}, "\n"))

	cfg := defaultConfig()
	cfg.DataDir = dir
	cfg.Transcribe.Cmd = []string{"cat", "{{audio}}"}

	st, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m := &Meeting{
		ID:           "test-1",
		Title:        "Планёрка",
		MeetURL:      "https://meet.google.com/abc-defg-hij",
		StartedAt:    time.Date(2026, 9, 8, 11, 0, 0, 0, time.UTC),
		AudioPath:    whisper,
		CaptionsPath: captions,
		Participants: []string{"Участник А", "Участник Б"},
		Status:       "recorded",
	}
	if err := st.CreateMeeting(m); err != nil {
		t.Fatal(err)
	}

	segs, _, err := runTranscriber(context.Background(), cfg, m.AudioPath)
	if err != nil {
		t.Fatalf("адаптер расшифровки: %v", err)
	}
	if len(segs) != 3 {
		t.Fatalf("ожидали 3 сегмента, получили %d", len(segs))
	}

	utts, err := readUtterances(m.CaptionsPath)
	if err != nil {
		t.Fatal(err)
	}
	segs = alignSpeakers(segs, utts)
	if segs[0].Speaker != "Участник А" || segs[1].Speaker != "Участник Б" {
		t.Fatalf("имена не легли на сегменты: %+v", segs)
	}
	// Реплика через десять минут после последних субтитров не должна получить
	// чужое имя.
	if segs[2].Speaker != "" {
		t.Fatalf("далёкому сегменту приписали %q", segs[2].Speaker)
	}

	if err := st.SaveSegments(m.ID, segs); err != nil {
		t.Fatal(err)
	}
	back, err := st.Segments(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != 3 || back[1].Speaker != "Участник Б" {
		t.Fatalf("из базы вернулось не то: %+v", back)
	}

	f := &Followup{
		Title: "Планёрка по релизу",
		TLDR:  []string{"Релиз сдвинули на пятницу"},
		ActionItems: []ActionItem{
			{Owner: "Участник Б", What: "закончить миграцию", Due: "2026-09-11",
				Quote: "я закончу миграцию к пятнице", At: 5},
			{Owner: "не назначен", What: "обновить changelog", At: 120},
		},
		Decisions:     []Decision{{What: "релиз в пятницу", Why: "миграция не успевает раньше", At: 5}},
		OpenQuestions: []OpenQuestion{{Question: "кто пишет changelog", WaitingOn: "Участник А", At: 130}},
	}
	if err := st.SaveFollowup(m.ID, "test", f); err != nil {
		t.Fatal(err)
	}
	if got, err := st.Followup(m.ID); err != nil || got.Title != f.Title || len(got.ActionItems) != 2 {
		t.Fatalf("follow-up не пережил базу: %+v (%v)", got, err)
	}

	html := renderHTML(m, f, segs)
	for _, want := range []string{
		"Планёрка по релизу", "2026-09-11", "закончить миграцию",
		"«я закончу миграцию к пятнице»", "Участник А: давайте начнём с релиза",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("в HTML нет %q", want)
		}
	}

	slack := renderSlack(m, f, "https://docs.google.com/d/1")
	if !strings.Contains(slack, "*Участник Б* — закончить миграцию") {
		t.Errorf("Slack без владельца задачи:\n%s", slack)
	}
	if !strings.Contains(slack, "https://docs.google.com/d/1") {
		t.Errorf("Slack без ссылки на документ")
	}

	// `steno show` печатает то же самое в терминал — без разметки.
	plain := renderPlain(m, f)
	if strings.Contains(plain, "<") {
		t.Errorf("в выводе для терминала осталась разметка:\n%s", plain)
	}
	if !strings.Contains(plain, "[00:00:05] Участник Б — закончить миграцию (2026-09-11)") {
		t.Errorf("задача отрендерилась не так:\n%s", plain)
	}

	tg := renderTelegram(m, f, "https://docs.google.com/d/1")
	if !strings.Contains(tg, "<b>Участник Б</b>") || !strings.Contains(tg, "срок не назван") {
		t.Errorf("Telegram отрендерился не так:\n%s", tg)
	}
}

// Длинный follow-up не должен упереться в лимит Telegram на 4096 символов.
func TestSplitForTelegramKeepsEverything(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 600; i++ {
		b.WriteString("• строка про задачу и её владельца\n")
	}
	parts := splitForTelegram(b.String())
	if len(parts) < 2 {
		t.Fatalf("не порезали: %d частей", len(parts))
	}
	total := 0
	for _, p := range parts {
		if n := len([]rune(p)); n > 3900 {
			t.Fatalf("часть длиной %d символов", n)
		}
		total += strings.Count(p, "•")
	}
	if total != 600 {
		t.Fatalf("потеряли строки: %d из 600", total)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Обратный адрес должен пережить базу: запись созвона идёт час, а отвечать
// нужно тому, кто попросил, и туда, где он попросил.
func TestReplyToSurvivesStore(t *testing.T) {
	dir := t.TempDir()
	st, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m := &Meeting{
		ID: "r-1", MeetURL: "https://meet.google.com/abc-defg-hij",
		StartedAt: time.Now(), Status: "recording",
		ReplyTo: ReplyTo{Kind: "telegram", Addr: "-100private"},
	}
	if err := st.CreateMeeting(m); err != nil {
		t.Fatal(err)
	}
	got, err := st.Meeting("r-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ReplyTo.Kind != "telegram" || got.ReplyTo.Addr != "-100private" {
		t.Fatalf("обратный адрес потерялся: %+v", got.ReplyTo)
	}

	// Созвон из календаря обратного адреса не имеет — и не должен.
	m2 := &Meeting{ID: "r-2", MeetURL: "https://meet.google.com/qwe-rtyu-iop",
		StartedAt: time.Now(), Status: "recording"}
	if err := st.CreateMeeting(m2); err != nil {
		t.Fatal(err)
	}
	got2, err := st.Meeting("r-2")
	if err != nil {
		t.Fatal(err)
	}
	if !got2.ReplyTo.empty() {
		t.Fatalf("взялся обратный адрес из ниоткуда: %+v", got2.ReplyTo)
	}
}

// В одну базу пишет не только сервис: `steno prune` из cron и `steno process`
// в соседнем терминале открывают её своим подключением, мимо пула сервиса.
// Внутри процесса конкуренцию снимает SetMaxOpenConns(1) — а вот между
// процессами её снимает только busy_timeout, и без него второй писатель
// получает SQLITE_BUSY мгновенно.
//
// Поэтому здесь два независимых Store на одной папке: это и есть два процесса.
func TestConcurrentWritesFromTwoProcesses(t *testing.T) {
	dir := t.TempDir()
	a, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	segs := make([]Segment, 300)
	for i := range segs {
		segs[i] = Segment{Start: float64(i), End: float64(i) + 1,
			Speaker: "Участник А", Text: "реплика номер такой-то, подлиннее для веса"}
	}

	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for i, st := range []*Store{a, b, a, b} {
		wg.Add(1)
		go func(i int, st *Store) {
			defer wg.Done()
			id := fmt.Sprintf("p-%d", i)
			errs <- st.CreateMeeting(&Meeting{ID: id,
				MeetURL:   "https://meet.google.com/abc-defg-hij",
				StartedAt: time.Now(), Status: "recording"})
			errs <- st.SaveSegments(id, segs)
		}(i, st)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("запись из второго процесса сорвалась: %v", err)
		}
	}
}

// Отдельно — конкуренция внутри процесса: четыре записи и четыре источника
// пишут в базу одновременно.
func TestConcurrentWritesDoNotFail(t *testing.T) {
	dir := t.TempDir()
	st, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	const n = 8
	errs := make(chan error, n*3)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("c-%d", i)
			errs <- st.CreateMeeting(&Meeting{
				ID: id, MeetURL: "https://meet.google.com/abc-defg-hij",
				StartedAt: time.Now(), Status: "recording",
			})
			segs := make([]Segment, 200)
			for j := range segs {
				segs[j] = Segment{Start: float64(j), End: float64(j) + 1,
					Speaker: "Участник А", Text: "реплика номер такой-то"}
			}
			errs <- st.SaveSegments(id, segs)
			_, err := st.MarkEventSeen("key-"+id, id)
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("конкурентная запись сорвалась: %v", err)
		}
	}
}

// Уборка сносит аудио, но не расшифровку и не follow-up: возвращаются обычно
// за ними, а не за записью.
func TestPruneKeepsTranscript(t *testing.T) {
	dir := t.TempDir()
	st, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	cfg := defaultConfig()
	cfg.Retention.Recordings = Duration(24 * time.Hour)
	cfg.Retention.Events = Duration(24 * time.Hour)

	old := time.Now().Add(-72 * time.Hour)
	m := &Meeting{ID: "old-1", MeetURL: "https://meet.google.com/abc-defg-hij",
		StartedAt: old, Status: "published"}
	recDir := st.RecordingDir(m.ID)
	if err := os.MkdirAll(recDir, 0o755); err != nil {
		t.Fatal(err)
	}
	m.AudioPath = filepath.Join(recDir, "audio.ogg")
	mustWrite(t, m.AudioPath, "притворимся, что это опус")
	if err := st.CreateMeeting(m); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveSegments(m.ID, []Segment{{Start: 0, End: 2, Speaker: "Участник А", Text: "решили"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.MarkEventSeen("stale", m.ID); err != nil {
		t.Fatal(err)
	}
	// Свежий созвон трогать нельзя.
	fresh := &Meeting{ID: "new-1", MeetURL: "https://meet.google.com/qwe-rtyu-iop",
		StartedAt: time.Now(), Status: "published"}
	freshDir := st.RecordingDir(fresh.ID)
	if err := os.MkdirAll(freshDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fresh.AudioPath = filepath.Join(freshDir, "audio.ogg")
	mustWrite(t, fresh.AudioPath, "свежая запись")
	if err := st.CreateMeeting(fresh); err != nil {
		t.Fatal(err)
	}

	res, err := prune(st, cfg, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	if res.Recordings != 1 {
		t.Fatalf("удалили %d записей, ожидали одну", res.Recordings)
	}
	if _, err := os.Stat(recDir); !os.IsNotExist(err) {
		t.Error("старая запись осталась на диске")
	}
	if _, err := os.Stat(fresh.AudioPath); err != nil {
		t.Error("снесли свежую запись")
	}
	segs, err := st.Segments("old-1")
	if err != nil || len(segs) != 1 {
		t.Fatalf("расшифровка пропала вместе с аудио: %v / %+v", err, segs)
	}
	if got, _ := st.Meeting("old-1"); got.AudioPath != "" {
		t.Errorf("путь к удалённому аудио остался: %q", got.AudioPath)
	}
}

// Цена считается по таблице, а токены приходят из ответа API. Проверяем, что
// выход не потерян: именно он, а не вход, определяет счёт — при adaptive
// thinking рассуждение модели тарифицируется как выход.
func TestComputeSpend(t *testing.T) {
	cfg := defaultConfig()

	// Часовой созвон: ~30k вход, ~10k выход на Opus 5 ($5/$25 за миллион).
	s := computeSpend(cfg, "claude-opus-5", 30000, 10000, 0, 0)
	if !s.PriceKnown {
		t.Fatal("цена для claude-opus-5 не нашлась")
	}
	want := 30000.0/1e6*5 + 10000.0/1e6*25
	if diff := s.USD - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("посчитали $%.4f, ожидали $%.4f", s.USD, want)
	}
	// Выход должен весить больше входа при таком раскладе.
	if 10000.0/1e6*25 <= 30000.0/1e6*5 {
		t.Error("проверь таблицу цен: выход перестал доминировать")
	}

	// Sonnet заметно дешевле — это рычаг, о котором говорит steno cost.
	cheap := computeSpend(cfg, "claude-sonnet-5", 30000, 10000, 0, 0)
	if cheap.USD >= s.USD {
		t.Errorf("sonnet ($%.4f) не дешевле opus ($%.4f)", cheap.USD, s.USD)
	}

	// Неизвестная модель: токены сохраняем, цену не выдумываем.
	unknown := computeSpend(cfg, "claude-неизвестная", 100, 50, 0, 0)
	if unknown.PriceKnown || unknown.USD != 0 {
		t.Error("для неизвестной модели придумали цену")
	}
	if unknown.Input != 100 || unknown.Output != 50 {
		t.Error("токены неизвестной модели потерялись")
	}
}
