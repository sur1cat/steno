package main

import (
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAlignSpeakersUsesCaptionNames(t *testing.T) {
	segs := []Segment{
		{Start: 0, End: 4, Text: "давайте начнём с релиза"},
		{Start: 5, End: 9, Text: "я закончу миграцию к пятнице"},
	}
	// Субтитры отстают от речи, поэтому таймкоды в них больше на ~1.5 с.
	utts := []Utterance{
		{Speaker: "Аня", Text: "давайте начнём", Start: 1.5, End: 5.5},
		{Speaker: "Боря", Text: "я закончу миграцию", Start: 6.5, End: 10.5},
	}
	got := alignSpeakers(segs, utts)
	if got[0].Speaker != "Аня" || got[1].Speaker != "Боря" {
		t.Fatalf("имена разъехались: %+v", got)
	}
}

func TestAlignSpeakersLeavesBlankWhenFarAway(t *testing.T) {
	segs := []Segment{{Start: 100, End: 104, Text: "а это уже другое"}}
	utts := []Utterance{{Speaker: "Аня", Text: "начало", Start: 0, End: 4}}
	if got := alignSpeakers(segs, utts); got[0].Speaker != "" {
		t.Fatalf("приписали чужое имя: %q", got[0].Speaker)
	}
}

func TestAlignSpeakersNoCaptions(t *testing.T) {
	segs := []Segment{{Start: 0, End: 2, Text: "тест"}}
	if got := alignSpeakers(segs, nil); len(got) != 1 || got[0].Speaker != "" {
		t.Fatalf("получили %+v", got)
	}
}

func TestRenderTranscriptMergesSameSpeaker(t *testing.T) {
	out := renderTranscript([]Segment{
		{Start: 0, End: 2, Speaker: "Аня", Text: "привет"},
		{Start: 2, End: 4, Speaker: "Аня", Text: "давайте начнём"},
		{Start: 5, End: 7, Speaker: "Боря", Text: "да"},
	})
	want := "[00:00:00] Аня: привет давайте начнём\n[00:00:05] Боря: да\n"
	if out != want {
		t.Fatalf("получили %q, ожидали %q", out, want)
	}
}

// Быстрое выравнивание по окну должно давать ровно то же, что и лобовой
// перебор: имена — самая ценная часть follow-up, и «оптимизировали, но чуть
// иначе» здесь не годится.
func TestAlignSpeakersMatchesNaive(t *testing.T) {
	rnd := rand.New(rand.NewSource(7))
	names := []string{"Аня", "Боря", "Вика", "Дима"}

	for run := 0; run < 200; run++ {
		var segs []Segment
		at := 0.0
		for i := 0; i < 1+rnd.Intn(40); i++ {
			at += rnd.Float64() * 8
			segs = append(segs, Segment{Start: at, End: at + rnd.Float64()*6,
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
		got := alignSpeakers(segs, utts)
		for i := range want {
			if got[i].Speaker != want[i].Speaker {
				t.Fatalf("прогон %d, сегмент %d (%.2f..%.2f): быстрый дал %q, лобовой %q",
					run, i, segs[i].Start, segs[i].End, got[i].Speaker, want[i].Speaker)
			}
		}
	}
}

// alignSpeakersNaive — та самая лобовая версия, только для сверки в тесте.
func alignSpeakersNaive(segs []Segment, utts []Utterance) []Segment {
	if len(utts) == 0 {
		return segs
	}
	shifted := make([]Utterance, len(utts))
	for i, u := range utts {
		u.Start -= captionLag
		u.End -= captionLag
		shifted[i] = u
	}
	out := make([]Segment, len(segs))
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
		`{"speaker":"Боря","text":"я закончу миграцию","start":20.0,"end":26.0}`,
		`{"speaker":"Аня","text":"давайте начнём","start":1.5,"end":5.5}`,
		`{"speaker":"Вика","text":"   ","start":30.0,"end":31.0}`,
		`битая строка`,
	}, "\n"))

	segs, err := segmentsFromCaptions(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 2 {
		t.Fatalf("ожидали две реплики, получили %+v", segs)
	}
	// Порядок по времени, а не по порядку в файле.
	if segs[0].Speaker != "Аня" || segs[1].Speaker != "Боря" {
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
	if _, err := segmentsFromCaptions(path); err == nil {
		t.Fatal("пустые субтитры должны быть внятной ошибкой, а не пустой расшифровкой")
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
