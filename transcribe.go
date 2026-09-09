package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Транскрипция вынесена за границу процесса: адаптер — это любая команда,
// которая берёт путь к аудио и печатает в stdout JSON вида
//
//	{"segments":[{"start":1.2,"end":3.4,"text":"..."}]}
//
// Так движок распознавания меняется правкой конфига, а не пересборкой:
// whisper.cpp, faster-whisper, что угодно ещё.
type transcriptOut struct {
	Segments []struct {
		Start float64 `json:"start"`
		End   float64 `json:"end"`
		Text  string  `json:"text"`
	} `json:"segments"`
}

// segmentsFromCaptions делает расшифровку прямо из субтитров Meet. Текст
// заметно хуже, чем у whisper, — Meet теряет окончания и путает термины, — но
// имена говорящих в нём уже проставлены, и работает это без единой установки.
// Способ довести конвейер до конца в первый же день и увидеть настоящий
// follow-up, а не заглушку.
func segmentsFromCaptions(captionsPath string) ([]Segment, error) {
	utts, err := readUtterances(captionsPath)
	if err != nil {
		return nil, fmt.Errorf("субтитры %s: %w", captionsPath, err)
	}
	if len(utts) == 0 {
		return nil, fmt.Errorf("субтитры пусты: в звонке они не включились, "+
			"и брать текст неоткуда (%s)", captionsPath)
	}
	segs := make([]Segment, 0, len(utts))
	for _, u := range utts {
		if txt := strings.TrimSpace(u.Text); txt != "" {
			segs = append(segs, Segment{Start: u.Start, End: u.End, Speaker: u.Speaker, Text: txt})
		}
	}
	sort.Slice(segs, func(i, j int) bool { return segs[i].Start < segs[j].Start })
	return segs, nil
}

func runTranscriber(ctx context.Context, cfg *Config, audioPath string) ([]Segment, error) {
	if len(cfg.Transcribe.Cmd) == 0 {
		return nil, fmt.Errorf("не настроен transcribe.cmd")
	}
	args := make([]string, len(cfg.Transcribe.Cmd))
	for i, a := range cfg.Transcribe.Cmd {
		a = strings.ReplaceAll(a, "{{audio}}", audioPath)
		a = strings.ReplaceAll(a, "{{language}}", cfg.Transcribe.Language)
		args[i] = a
	}

	if d := cfg.Transcribe.Timeout.D(); d > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	// По таймауту гасим всю группу процессов мягко, чтобы обёртка успела
	// убрать временный WAV, а whisper не остался сиротой.
	setProcessGroup(cmd)
	cmd.Cancel = func() error { return terminateGroup(cmd) }
	cmd.WaitDelay = 15 * time.Second

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("адаптер расшифровки %v: %w\n%s", args, err, tail(stderr.String(), 800))
	}

	var out transcriptOut
	if err := json.Unmarshal([]byte(stdout.String()), &out); err != nil {
		return nil, fmt.Errorf("адаптер вернул не тот JSON: %w\n%s", err, tail(stdout.String(), 400))
	}
	segs := make([]Segment, 0, len(out.Segments))
	for _, s := range out.Segments {
		txt := strings.TrimSpace(s.Text)
		if txt == "" {
			continue
		}
		segs = append(segs, Segment{Start: s.Start, End: s.End, Text: txt})
	}
	sort.Slice(segs, func(i, j int) bool { return segs[i].Start < segs[j].Start })
	return segs, nil
}

func readUtterances(path string) ([]Utterance, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Utterance
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var u Utterance
		if err := json.Unmarshal([]byte(line), &u); err != nil {
			continue // одна битая строка не повод терять весь файл
		}
		out = append(out, u)
	}
	return out, sc.Err()
}

// captionLag — субтитры Meet появляются позже самой речи: распознаванию нужно
// время. Сдвигаем их назад, иначе имена уезжают на следующего говорящего.
const captionLag = 1.5

// nearestWindow — насколько далеко от сегмента ещё допустимо брать имя, если
// прямого пересечения нет. Дальше лучше без имени, чем с чужим.
const nearestWindow = 3.0

// alignSpeakers ставит имена из субтитров Meet на сегменты whisper: текст
// берём у whisper (он точнее), имя — у Meet (оно достоверно).
func alignSpeakers(segs []Segment, utts []Utterance) []Segment {
	if len(utts) == 0 {
		return segs
	}
	shifted := make([]Utterance, len(utts))
	for i, u := range utts {
		u.Start -= captionLag
		u.End -= captionLag
		shifted[i] = u
	}
	// Обе последовательности идут по времени, поэтому смотреть каждую реплику
	// для каждого сегмента незачем: окно только едет вперёд. На часовом
	// созвоне это тысячи сегментов против тысяч реплик.
	sort.Slice(shifted, func(i, j int) bool { return shifted[i].Start < shifted[j].Start })

	out := make([]Segment, len(segs))
	lo := 0
	for i, s := range segs {
		out[i] = s
		// Реплики, закончившиеся до окна допуска, больше не понадобятся
		// никогда: сегменты дальше только позже.
		for lo < len(shifted) && shifted[lo].End < s.Start-nearestWindow {
			lo++
		}
		best, bestOverlap := -1, 0.0
		nearest, dist := -1, nearestWindow
		for j := lo; j < len(shifted) && shifted[j].Start <= s.End+nearestWindow; j++ {
			u := shifted[j]
			if ov := overlap(s.Start, s.End, u.Start, u.End); ov > bestOverlap {
				best, bestOverlap = j, ov
			}
			if d := gap(s.Start, s.End, u.Start, u.End); d < dist {
				nearest, dist = j, d
			}
		}
		if best < 0 {
			// Пересечения нет — берём ближайшую по времени реплику, но только
			// если она рядом: иначе лучше без имени, чем с чужим.
			best = nearest
		}
		if best >= 0 {
			out[i].Speaker = shifted[best].Speaker
		}
	}
	return out
}

func overlap(a1, a2, b1, b2 float64) float64 {
	lo, hi := max(a1, b1), min(a2, b2)
	if hi <= lo {
		return 0
	}
	return hi - lo
}

func gap(a1, a2, b1, b2 float64) float64 {
	if b1 > a2 {
		return b1 - a2
	}
	if a1 > b2 {
		return a1 - b2
	}
	return 0
}

// tail режет по символам, а не по байтам: обрезанный по байту текст ошибки с
// кириллицей уходит в базу с половиной буквы на стыке.
func tail(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return "…" + string(r[len(r)-n:])
}

// Пометки, которыми движки расшифровки обозначают отсутствие речи. Они не
// текст, и принимать их за расшифровку нельзя.
var noSpeechMarkers = regexp.MustCompile(`(?i)^[\[\(](blank_?audio|silence|music|музыка|тишина|inaudible|неразборчиво|звук[^\]\)]*)[\]\)]$`)

// checkTranscript ловит вырожденную расшифровку до того, как за неё заплатят.
//
// Настоящий случай: whisper определяет язык по первым тридцати секундам, а
// запись созвона начинается с тишины — бот заходит раньше людей. Определился
// не тот язык, и весь разговор вышел пятнадцатью строками [BLANK_AUDIO].
// Формально это валидный JSON и нулевой код возврата: без проверки сервис
// отдал бы это Claude и выдал пустой follow-up как настоящий.
func checkTranscript(segs []Segment) error {
	const minChars = 80
	chars, real := 0, 0
	for _, s := range segs {
		t := strings.TrimSpace(s.Text)
		if t == "" || noSpeechMarkers.MatchString(t) {
			continue
		}
		real++
		chars += len([]rune(t))
	}
	if real == 0 {
		return fmt.Errorf("в расшифровке нет речи — %d строк, и все пустые или «нет звука». "+
			"Обычно это значит, что определился не тот язык или запись почти вся тишина", len(segs))
	}
	if chars < minChars {
		return fmt.Errorf("расшифровка почти пустая: %d значимых строк, %d символов. "+
			"Проверь запись и язык распознавания", real, chars)
	}
	return nil
}
