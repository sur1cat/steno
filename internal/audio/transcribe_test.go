package audio

import (
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sur1cat/steno/internal/brain"
	"github.com/sur1cat/steno/internal/core"
)

func TestAlignSpeakersUsesCaptionNames(t *testing.T) {
	segs := []core.Segment{
		{Start: 0, End: 4, Text: "давайте начнём с релиза"},
		{Start: 5, End: 9, Text: "я закончу миграцию к пятнице"},
	}
	// Субтитры отстают от речи, поэтому таймкоды в них больше на ~1.5 с.
	utts := []Utterance{
		{Speaker: "Участник А", Text: "давайте начнём", Start: 1.5, End: 5.5},
		{Speaker: "Участник Б", Text: "я закончу миграцию", Start: 6.5, End: 10.5},
	}
	got := AlignSpeakers(segs, utts)
	if got[0].Speaker != "Участник А" || got[1].Speaker != "Участник Б" {
		t.Fatalf("имена разъехались: %+v", got)
	}
}

func TestAlignSpeakersLeavesBlankWhenFarAway(t *testing.T) {
	segs := []core.Segment{{Start: 100, End: 104, Text: "а это уже другое"}}
	utts := []Utterance{{Speaker: "Участник А", Text: "начало", Start: 0, End: 4}}
	if got := AlignSpeakers(segs, utts); got[0].Speaker != "" {
		t.Fatalf("приписали чужое имя: %q", got[0].Speaker)
	}
}

func TestAlignSpeakersNoCaptions(t *testing.T) {
	segs := []core.Segment{{Start: 0, End: 2, Text: "тест"}}
	if got := AlignSpeakers(segs, nil); len(got) != 1 || got[0].Speaker != "" {
		t.Fatalf("получили %+v", got)
	}
}

func TestRenderTranscriptMergesSameSpeaker(t *testing.T) {
	out := brain.RenderTranscript([]core.Segment{
		{Start: 0, End: 2, Speaker: "Участник А", Text: "привет"},
		{Start: 2, End: 4, Speaker: "Участник А", Text: "давайте начнём"},
		{Start: 5, End: 7, Speaker: "Участник Б", Text: "да"},
	})
	want := "[00:00:00] Участник А: привет давайте начнём\n[00:00:05] Участник Б: да\n"
	if out != want {
		t.Fatalf("получили %q, ожидали %q", out, want)
	}
}

// Быстрое выравнивание по окну должно давать ровно то же, что и лобовой
// перебор: имена — самая ценная часть follow-up, и «оптимизировали, но чуть
// иначе» здесь не годится.
func TestAlignSpeakersMatchesNaive(t *testing.T) {
	rnd := rand.New(rand.NewSource(7))
	names := []string{"Участник А", "Участник Б", "Участник В", "Участник Г"}

	for run := 0; run < 200; run++ {
		var segs []core.Segment
		at := 0.0
		for i := 0; i < 1+rnd.Intn(40); i++ {
			at += rnd.Float64() * 8
			segs = append(segs, core.Segment{Start: at, End: at + rnd.Float64()*6,
				Text: "реплика"})
		}
		var utts []Utterance
		at = 0
		for i := 0; i < rnd.Intn(40); i++ {
			at += rnd.Float64() * 8
			utts = append(utts, Utterance{Speaker: names[rnd.Intn(len(names))],
				Start: at, End: at + rnd.Float64()*6})
		}
		want := alignSpeakersNaive(segs, utts)
		got := AlignSpeakers(segs, utts)
		for i := range want {
			if got[i].Speaker != want[i].Speaker {
				t.Fatalf("прогон %d, сегмент %d (%.2f..%.2f): быстрый дал %q, лобовой %q",
					run, i, segs[i].Start, segs[i].End, got[i].Speaker, want[i].Speaker)
			}
		}
	}
}

// alignSpeakersNaive — та самая лобовая версия, только для сверки в тесте.
func alignSpeakersNaive(segs []core.Segment, utts []Utterance) []core.Segment {
	if len(utts) == 0 {
		return segs
	}
	shifted := make([]Utterance, len(utts))
	for i, u := range utts {
		u.Start -= captionLag
		u.End -= captionLag
		shifted[i] = u
	}
	out := make([]core.Segment, len(segs))
	for i, s := range segs {
		out[i] = s
		best, bestOverlap := -1, 0.0
		for j, u := range shifted {
			if ov := overlap(s.Start, s.End, u.Start, u.End); ov > bestOverlap {
				best, bestOverlap = j, ov
			}
		}
		if best < 0 {
			nearest, dist := -1, nearestWindow
			for j, u := range shifted {
				if d := gap(s.Start, s.End, u.Start, u.End); d < dist {
					nearest, dist = j, d
				}
			}
			best = nearest
		}
		if best >= 0 {
			out[i].Speaker = shifted[best].Speaker
		}
	}
	return out
}

// Режим «текст из субтитров» — способ довести конвейер до конца без установки
// whisper. Имена в нём уже проставлены Meet, выравнивать ничего не нужно.
func TestSegmentsFromCaptions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "captions.jsonl")
	mustWriteFile(t, path, strings.Join([]string{
		`{"speaker":"Участник Б","text":"я закончу миграцию","start":20.0,"end":26.0}`,
		`{"speaker":"Участник А","text":"давайте начнём","start":1.5,"end":5.5}`,
		`{"speaker":"Участник В","text":"   ","start":30.0,"end":31.0}`,
		`битая строка`,
	}, "\n"))

	segs, err := SegmentsFromCaptions(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 2 {
		t.Fatalf("ожидали две реплики, получили %+v", segs)
	}
	// Порядок по времени, а не по порядку в файле.
	if segs[0].Speaker != "Участник А" || segs[1].Speaker != "Участник Б" {
		t.Errorf("реплики не отсортированы по времени: %+v", segs)
	}
	if segs[0].Text != "давайте начнём" {
		t.Errorf("текст: %q", segs[0].Text)
	}
}

func TestSegmentsFromCaptionsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "captions.jsonl")
	mustWriteFile(t, path, "")
	if _, err := SegmentsFromCaptions(path); err == nil {
		t.Fatal("пустые субтитры должны быть внятной ошибкой, а не пустой расшифровкой")
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// whisper определяет язык по первым тридцати секундам, а запись созвона
// начинается с тишины — бот заходит раньше людей. Определился не тот язык, и
// весь разговор вышел строками [BLANK_AUDIO]: валидный JSON, нулевой код
// возврата, пустой follow-up как настоящий. Это надо ловить до Claude.
func TestCheckTranscriptCatchesSilence(t *testing.T) {
	blank := make([]core.Segment, 15)
	for i := range blank {
		blank[i] = core.Segment{Start: float64(i * 30), End: float64(i*30 + 30), Text: "[BLANK_AUDIO]"}
	}
	err := CheckTranscript(blank)
	if err == nil {
		t.Fatal("расшифровка из одной тишины принята за настоящую")
	}
	if !strings.Contains(err.Error(), "нет речи") {
		t.Errorf("невнятная ошибка: %v", err)
	}

	for _, markers := range [][]string{
		{"[музыка]"}, {"(Music)"}, {"[тишина]"}, {"[inaudible]"}, {""},
	} {
		var segs []core.Segment
		for _, m := range markers {
			segs = append(segs, core.Segment{Text: m})
		}
		if CheckTranscript(segs) == nil {
			t.Errorf("пометка %v принята за речь", markers)
		}
	}

	// Пометка внутри настоящей реплики её не отменяет.
	ok := []core.Segment{
		{Text: "[шум] так, давайте начнём с релиза, что там с миграцией схемы"},
		{Text: "миграция готова процентов на восемьдесят, но на стейджинг не успеваю"},
	}
	if err := CheckTranscript(ok); err != nil {
		t.Errorf("настоящая расшифровка забракована: %v", err)
	}

	// Одно слово на часовой созвон — тоже не расшифровка.
	if CheckTranscript([]core.Segment{{Text: "ага"}}) == nil {
		t.Error("расшифровка из одного слова принята")
	}
}

// doctor должен проверять адаптер по-настоящему: найти файл скрипта мало, ему
// нужна модель на полгигабайта, и без неё он падает.
func TestSilentWAVIsValid(t *testing.T) {
	b := SilentWAV(time.Second / 2)
	if len(b) != 44+16000 {
		t.Fatalf("длина %d, ожидали %d", len(b), 44+16000)
	}
	if string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" || string(b[36:40]) != "data" {
		t.Fatal("не похоже на WAV")
	}
	// 16 кГц, моно, 16 бит — то, что ждут адаптеры.
	rate := uint32(b[24]) | uint32(b[25])<<8 | uint32(b[26])<<16 | uint32(b[27])<<24
	if rate != 16000 {
		t.Errorf("частота %d", rate)
	}
	if ch := uint16(b[22]) | uint16(b[23])<<8; ch != 1 {
		t.Errorf("каналов %d", ch)
	}
}

// На large-v3 одна расшифровка занимает машину на минуты. Четыре созвона,
// кончившиеся в одну минуту, положили бы её целиком, поэтому расшифровки идут
// по очереди — и очередь не должна ни терять работу, ни пропускать лишних.
func TestTranscribeQueueSerializes(t *testing.T) {
	ResizeTranscribeQueue(1)
	t.Cleanup(func() { ResizeTranscribeQueue(1) })

	dir := t.TempDir()
	// Адаптер, который отмечается о своём входе и выходе.
	script := filepath.Join(dir, "adapter.sh")
	marks := filepath.Join(dir, "marks")
	mustWriteFile(t, script, "#!/bin/sh\necho in >> "+marks+"\nsleep 0.3\necho out >> "+marks+
		"\necho '{\"segments\":[{\"start\":0,\"end\":1,\"text\":\"тест\"}]}'\n")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := core.DefaultConfig()
	cfg.Transcribe.Cmd = []string{script, "{{audio}}"}
	cfg.Transcribe.Nice = false

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := RunTranscriber(context.Background(), cfg, "нет-файла.ogg"); err != nil {
				t.Errorf("расшифровка сорвалась: %v", err)
			}
		}()
	}
	wg.Wait()

	raw, err := os.ReadFile(marks)
	if err != nil {
		t.Fatal(err)
	}
	// При очереди в один поток отметки обязаны идти строго парами in/out.
	lines := strings.Fields(string(raw))
	if len(lines) != 8 {
		t.Fatalf("отметок %d, ожидали 8: %v", len(lines), lines)
	}
	for i := 0; i < len(lines); i += 2 {
		if lines[i] != "in" || lines[i+1] != "out" {
			t.Fatalf("расшифровки пошли внахлёст: %v", lines)
		}
	}
}

// Ожидание в очереди не должно съедать таймаут расшифровки: созвон, простоявший
// час, обязан начать считаться, а не сорваться, не начавшись.
func TestQueueWaitIsNotCountedInTimeout(t *testing.T) {
	ResizeTranscribeQueue(1)
	t.Cleanup(func() { ResizeTranscribeQueue(1) })

	dir := t.TempDir()
	script := filepath.Join(dir, "adapter.sh")
	mustWriteFile(t, script, "#!/bin/sh\nsleep 0.4\necho '{\"segments\":[{\"start\":0,\"end\":1,\"text\":\"тест\"}]}'\n")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := core.DefaultConfig()
	cfg.Transcribe.Cmd = []string{script, "{{audio}}"}
	cfg.Transcribe.Nice = false
	cfg.Transcribe.Timeout = core.Duration(2 * time.Second)

	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := RunTranscriber(context.Background(), cfg, "x.ogg")
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("расшифровка сорвалась из-за ожидания в очереди: %v", err)
		}
	}
}
