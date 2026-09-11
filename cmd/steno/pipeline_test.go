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

	"github.com/sur1cat/steno/internal/audio"
	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/publish"
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

	cfg := core.DefaultConfig()
	cfg.DataDir = dir
	cfg.Transcribe.Cmd = []string{"cat", "{{audio}}"}

	st, err := core.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m := &core.Meeting{
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

	segs, _, err := audio.RunTranscriber(context.Background(), cfg, m.AudioPath, nil)
	if err != nil {
		t.Fatalf("адаптер расшифровки: %v", err)
	}
	if len(segs) != 3 {
		t.Fatalf("ожидали 3 сегмента, получили %d", len(segs))
	}

	utts, err := audio.ReadUtterances(m.CaptionsPath)
	if err != nil {
		t.Fatal(err)
	}
	segs = audio.AlignSpeakers(segs, utts)
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

	f := &core.Followup{
		Title: "Планёрка по релизу",
		TLDR:  []string{"Релиз сдвинули на пятницу"},
		ActionItems: []core.ActionItem{
			{Owner: "Участник Б", What: "закончить миграцию", Due: "2026-09-11",
				Quote: "я закончу миграцию к пятнице", At: 5},
			{Owner: "не назначен", What: "обновить changelog", At: 120},
		},
		Decisions:     []core.Decision{{What: "релиз в пятницу", Why: "миграция не успевает раньше", At: 5}},
		OpenQuestions: []core.OpenQuestion{{Question: "кто пишет changelog", WaitingOn: "Участник А", At: 130}},
	}
	if err := st.SaveFollowup(m.ID, "test", f); err != nil {
		t.Fatal(err)
	}
	if got, err := st.Followup(m.ID); err != nil || got.Title != f.Title || len(got.ActionItems) != 2 {
		t.Fatalf("follow-up не пережил базу: %+v (%v)", got, err)
	}

	html := publish.RenderHTML(m, f, segs)
	for _, want := range []string{
		"Планёрка по релизу", "2026-09-11", "закончить миграцию",
		"«я закончу миграцию к пятнице»", "Участник А: давайте начнём с релиза",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("в HTML нет %q", want)
		}
	}

	slack := publish.RenderSlack(m, f, "https://docs.google.com/d/1")
	if !strings.Contains(slack, "*Участник Б* — закончить миграцию") {
		t.Errorf("Slack без владельца задачи:\n%s", slack)
	}
	if !strings.Contains(slack, "https://docs.google.com/d/1") {
		t.Errorf("Slack без ссылки на документ")
	}

	// `steno show` печатает то же самое в терминал — без разметки.
	plain := publish.RenderPlain(m, f)
	if strings.Contains(plain, "<") {
		t.Errorf("в выводе для терминала осталась разметка:\n%s", plain)
	}
	if !strings.Contains(plain, "[00:00:05] Участник Б — закончить миграцию (2026-09-11)") {
		t.Errorf("задача отрендерилась не так:\n%s", plain)
	}

	tg := publish.RenderTelegram(m, f, "https://docs.google.com/d/1")
	if !strings.Contains(tg, "<b>Участник Б</b>") || !strings.Contains(tg, "срок не назван") {
		t.Errorf("Telegram отрендерился не так:\n%s", tg)
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
	st, err := core.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m := &core.Meeting{
		ID: "r-1", MeetURL: "https://meet.google.com/abc-defg-hij",
		StartedAt: time.Now(), Status: "recording",
		ReplyTo: core.ReplyTo{Kind: "telegram", Addr: "-100private"},
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
	m2 := &core.Meeting{ID: "r-2", MeetURL: "https://meet.google.com/qwe-rtyu-iop",
		StartedAt: time.Now(), Status: "recording"}
	if err := st.CreateMeeting(m2); err != nil {
		t.Fatal(err)
	}
	got2, err := st.Meeting("r-2")
	if err != nil {
		t.Fatal(err)
	}
	if !got2.ReplyTo.Empty() {
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
	a, err := core.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := core.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	segs := make([]core.Segment, 300)
	for i := range segs {
		segs[i] = core.Segment{Start: float64(i), End: float64(i) + 1,
			Speaker: "Участник А", Text: "реплика номер такой-то, подлиннее для веса"}
	}

	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for i, st := range []*core.Store{a, b, a, b} {
		wg.Add(1)
		go func(i int, st *core.Store) {
			defer wg.Done()
			id := fmt.Sprintf("p-%d", i)
			errs <- st.CreateMeeting(&core.Meeting{ID: id,
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
	st, err := core.OpenStore(dir)
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
			errs <- st.CreateMeeting(&core.Meeting{
				ID: id, MeetURL: "https://meet.google.com/abc-defg-hij",
				StartedAt: time.Now(), Status: "recording",
			})
			segs := make([]core.Segment, 200)
			for j := range segs {
				segs[j] = core.Segment{Start: float64(j), End: float64(j) + 1,
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
	st, err := core.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	cfg := core.DefaultConfig()
	cfg.Retention.Recordings = core.Duration(24 * time.Hour)
	cfg.Retention.Events = core.Duration(24 * time.Hour)

	old := time.Now().Add(-72 * time.Hour)
	m := &core.Meeting{ID: "old-1", MeetURL: "https://meet.google.com/abc-defg-hij",
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
	if err := st.SaveSegments(m.ID, []core.Segment{{Start: 0, End: 2, Speaker: "Участник А", Text: "решили"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.MarkEventSeen("stale", m.ID); err != nil {
		t.Fatal(err)
	}
	// Свежий созвон трогать нельзя.
	fresh := &core.Meeting{ID: "new-1", MeetURL: "https://meet.google.com/qwe-rtyu-iop",
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

	res, err := core.Prune(st, cfg, log.New(io.Discard, "", 0))
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
	cfg := core.DefaultConfig()

	// Часовой созвон: ~30k вход, ~10k выход на Opus 5 ($5/$25 за миллион).
	s := core.ComputeSpend(cfg, "claude-opus-5", 30000, 10000, 0, 0)
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
	cheap := core.ComputeSpend(cfg, "claude-sonnet-5", 30000, 10000, 0, 0)
	if cheap.USD >= s.USD {
		t.Errorf("sonnet ($%.4f) не дешевле opus ($%.4f)", cheap.USD, s.USD)
	}

	// Неизвестная модель: токены сохраняем, цену не выдумываем.
	unknown := core.ComputeSpend(cfg, "claude-неизвестная", 100, 50, 0, 0)
	if unknown.PriceKnown || unknown.USD != 0 {
		t.Error("для неизвестной модели придумали цену")
	}
	if unknown.Input != 100 || unknown.Output != 50 {
		t.Error("токены неизвестной модели потерялись")
	}
}

// Пишущая транзакция обязана брать блокировку сразу на BEGIN, а не при первой
// записи.
//
// Если брать её позже, транзакция открывается читателем и фиксирует снимок
// базы; попытка стать писателем после того, как записал кто-то другой, даёт
// SQLITE_BUSY_SNAPSHOT (517) немедленно — busy_timeout на этот случай не
// распространяется вовсе, и десять секунд ожидания, прописанные в настройках,
// там не действуют. Ловилось это раньше только гонкой, раз на несколько сотен
// прогонов, а такой тест начинают перезапускать, и он перестаёт ловить.
//
// Здесь проверяется само свойство: пока первый держит транзакцию, запись
// второго обязана ждать. Если она проходит мгновенно — значит блокировка
// берётся поздно, и снимок разъедется.
func TestTransactionTakesWriteLockAtBegin(t *testing.T) {
	dir := t.TempDir()
	a, err := core.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := core.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	tx, err := a.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM meetings`).Scan(&n); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}

	const held = 600 * time.Millisecond
	go func() {
		time.Sleep(held)
		_, _ = tx.Exec(`INSERT INTO meetings (id,meet_url,started_at,status) VALUES (?,?,?,?)`,
			"первый", "https://meet.google.com/abc-defg-hij", time.Now().Unix(), "recording")
		_ = tx.Commit()
	}()

	start := time.Now()
	err = b.CreateMeeting(&core.Meeting{ID: "второй",
		MeetURL:   "https://meet.google.com/abc-defg-hij",
		StartedAt: time.Now(), Status: "recording"})
	waited := time.Since(start)

	if err != nil {
		t.Fatalf("второй процесс не дождался очереди: %v", err)
	}
	// Порог с запасом вниз: важно отличить «ждал» от «прошёл сразу», а не
	// померить точное время.
	if waited < held/2 {
		t.Fatalf("запись прошла за %v, не дожидаясь чужой транзакции — "+
			"значит блокировка берётся не на BEGIN, и снимок может разъехаться", waited)
	}
}
