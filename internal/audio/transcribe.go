package audio

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
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

// SegmentsFromCaptions делает расшифровку прямо из субтитров Meet. Текст
// заметно хуже, чем у whisper, — Meet теряет окончания и путает термины, — но
// имена говорящих в нём уже проставлены, и работает это без единой установки.
// Способ довести конвейер до конца в первый же день и увидеть настоящий
// follow-up, а не заглушку.
func SegmentsFromCaptions(captionsPath string) ([]core.Segment, error) {
	utts, err := ReadUtterances(captionsPath)
	if err != nil {
		return nil, fmt.Errorf(i18n.Tr("субтитры %s: %w"), captionsPath, err)
	}
	if len(utts) == 0 {
		return nil, fmt.Errorf(i18n.Tr("субтитры пусты: в звонке они не включились, ")+
			i18n.Tr("и брать текст неоткуда (%s)"), captionsPath)
	}
	segs := make([]core.Segment, 0, len(utts))
	for _, u := range utts {
		if txt := strings.TrimSpace(u.Text); txt != "" {
			segs = append(segs, core.Segment{Start: u.Start, End: u.End, Speaker: u.Speaker, Text: txt})
		}
	}
	sort.Slice(segs, func(i, j int) bool { return segs[i].Start < segs[j].Start })
	return segs, nil
}

// Расшифровки идут по очереди. На large-v3 одна занимает машину на минуты, и
// четыре созвона, кончившиеся в одну минуту, положили бы её целиком. Ожидание
// ничего не стоит: аудио уже на диске.
var transcribeQueue = make(chan struct{}, 1)

func init() { transcribeQueue <- struct{}{} }

// ResizeTranscribeQueue настраивает, сколько расшифровок идёт одновременно.
func ResizeTranscribeQueue(n int) {
	if n < 1 {
		n = 1
	}
	transcribeQueue = make(chan struct{}, n)
	for i := 0; i < n; i++ {
		transcribeQueue <- struct{}{}
	}
}

// RunTranscriber возвращает ещё и то, что адаптер написал в stderr: там он
// сообщает, какой моделью работал и какой язык определил. Это ровно те два
// факта, по которым потом понимаешь, почему расшифровка вышла такой.
//
// Четвёртый параметр — словарь созвона (vocab.go), список слов через запятую.
// Он вариативный по той же причине, что и у AlignSpeakers: форму команды
// расшифровки менять нельзя — она записана в конфигах у людей как
// [адаптер, {{audio}}, {{language}}], и четвёртый аргумент сломал бы их все, —
// а вызов из main.go сейчас правит другой человек. Словарь уходит окружением,
// как уже уходит WHISPER_THREADS; адаптер без словаря работает ровно как
// раньше.
func RunTranscriber(ctx context.Context, cfg *core.Config, audioPath string, vocabulary ...string) ([]core.Segment, string, error) {
	if len(cfg.Transcribe.Cmd) == 0 {
		return nil, "", errors.New(i18n.Tr("не настроен transcribe.cmd"))
	}
	args := make([]string, len(cfg.Transcribe.Cmd))
	for i, a := range cfg.Transcribe.Cmd {
		a = strings.ReplaceAll(a, "{{audio}}", audioPath)
		a = strings.ReplaceAll(a, "{{language}}", cfg.Transcribe.Language)
		args[i] = a
	}
	args[0] = core.AdapterPath(args[0])

	// Ждём очереди до того, как заводить таймаут: иначе созвон, простоявший в
	// очереди час, сорвётся по таймауту, не начав расшифровываться.
	select {
	case <-transcribeQueue:
		defer func() { transcribeQueue <- struct{}{} }()
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}

	if d := cfg.Transcribe.Timeout.D(); d > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d)
		defer cancel()
	}

	// nice: расшифровка не срочная и должна уступать интерактивной работе.
	if cfg.Transcribe.Nice {
		if _, err := exec.LookPath("nice"); err == nil {
			args = append([]string{"nice", "-n", "10"}, args...)
		}
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	// Адаптер печатает диагностику человеку и потому говорит на языке steno.
	// Передаём всегда: язык мог прийти из конфига, а не из окружения, и сам
	// по себе до адаптера тогда не доедет.
	env := []string{"STENO_LANG=" + i18n.UILang}
	if n := cfg.Transcribe.Threads; n > 0 {
		// Адаптеры читают это и не занимают машину целиком.
		env = append(env, fmt.Sprintf("WHISPER_THREADS=%d", n))
	}
	// Словарь созвона. Пустой не передаём вовсе: адаптер по отсутствию
	// переменной отличает «словаря нет» от «словарь пустой» и в первом случае
	// зовёт whisper теми же ключами, что и до всей этой истории.
	if hint := vocabularyHint(vocabulary); hint != "" {
		env = append(env, "STENO_PROMPT="+hint)
		log.Printf(i18n.Tr("словарь для распознавания: %s"), i18n.Cut(hint, 200))
	}
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	// По таймауту гасим всю группу процессов мягко, чтобы обёртка успела
	// убрать временный WAV, а whisper не остался сиротой.
	core.SetProcessGroup(cmd)
	cmd.Cancel = func() error { return core.TerminateGroup(cmd) }
	cmd.WaitDelay = 15 * time.Second

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, "", fmt.Errorf(i18n.Tr("адаптер расшифровки %v: %w\n%s"), args, err, i18n.Tail(stderr.String(), 800))
	}

	var out transcriptOut
	if err := json.Unmarshal([]byte(stdout.String()), &out); err != nil {
		return nil, "", fmt.Errorf(i18n.Tr("адаптер вернул не тот JSON: %w\n%s"), err, i18n.Tail(stdout.String(), 400))
	}
	segs := make([]core.Segment, 0, len(out.Segments))
	for _, s := range out.Segments {
		txt := strings.TrimSpace(s.Text)
		if txt == "" {
			continue
		}
		segs = append(segs, core.Segment{Start: s.Start, End: s.End, Text: txt})
	}
	sort.Slice(segs, func(i, j int) bool { return segs[i].Start < segs[j].Start })
	return segs, strings.TrimSpace(stderr.String()), nil
}

func ReadUtterances(path string) ([]Utterance, error) {
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
//
// У подсветки говорящего задержка своя и другая — она загорается почти сразу и
// гаснет с опозданием. Поэтому два источника нельзя сложить в одну кучу и
// сдвинуть общим числом: их приводят к времени речи по отдельности, здесь и в
// normalizeSpans, и только потом сравнивают.
const captionLag = 1.5

// nearestWindow — насколько далеко от сегмента ещё допустимо брать имя, если
// прямого пересечения нет. Дальше лучше без имени, чем с чужим.
const nearestWindow = 3.0

// minSolidOverlap — пересечение короче этого считаем касанием краёв, а не
// попаданием. Нужно ровно в одном месте: реплика субтитров, задевшая сегмент
// на пять сотых секунды краем, не должна перебивать подсветку, накрывшую тот же
// сегмент целиком.
const minSolidOverlap = 0.25

// namedSpan — отрезок с именем. Сюда одинаково ложатся и реплика субтитров, и
// подсветка говорящего: дальше их различает только то, кто в каком списке.
type namedSpan struct {
	speaker    string
	start, end float64
}

// spanFinder — окно, едущее по отсортированной ленте. Обе последовательности
// идут по времени, поэтому смотреть каждый отрезок для каждого сегмента
// незачем. На часовом созвоне это тысячи сегментов против тысяч отрезков.
type spanFinder struct {
	spans []namedSpan
	lo    int
}

// match — что лента дала для одного сегмента whisper. overlap > 0 означает
// настоящее пересечение, dist > 0 — что имя взято от ближайшего по времени
// отрезка, а не от накрывающего.
type match struct {
	speaker string
	overlap float64
	dist    float64
	ok      bool
}

func (f *spanFinder) find(s core.Segment) match {
	// Отрезки, кончившиеся до окна допуска, больше не понадобятся никогда:
	// сегменты дальше только позже.
	for f.lo < len(f.spans) && f.spans[f.lo].end < s.Start-nearestWindow {
		f.lo++
	}
	best, bestOverlap := -1, 0.0
	nearest, dist := -1, nearestWindow
	for j := f.lo; j < len(f.spans) && f.spans[j].start <= s.End+nearestWindow; j++ {
		u := f.spans[j]
		if ov := overlap(s.Start, s.End, u.start, u.end); ov > bestOverlap {
			best, bestOverlap = j, ov
		}
		if d := gap(s.Start, s.End, u.start, u.end); d < dist {
			nearest, dist = j, d
		}
	}
	if best >= 0 {
		return match{speaker: f.spans[best].speaker, overlap: bestOverlap, ok: true}
	}
	// Пересечения нет — берём ближайший по времени отрезок, но только если он
	// рядом: иначе лучше без имени, чем с чужим.
	if nearest >= 0 {
		return match{speaker: f.spans[nearest].speaker, dist: dist, ok: true}
	}
	return match{}
}

// solidOverlap — сколько пересечения довольно, чтобы считать попадание
// настоящим. У очень коротких сегментов порогом становится их половина: иначе
// реплика в четверть секунды не смогла бы попасть никуда.
func solidOverlap(s core.Segment) float64 {
	if half := (s.End - s.Start) / 2; half < minSolidOverlap {
		return half
	}
	return minSolidOverlap
}

// AlignSpeakers ставит имена на сегменты whisper: текст берём у whisper (он
// точнее), имя — у площадки.
//
// Источников имени два, и они складываются, а не заменяют друг друга.
//
// Субтитры точнее: строка субтитров появляется потому, что площадка распознала
// речь и привязала её к конкретному участнику, — это имя привязано к фразе.
// Поэтому субтитры остаются основным источником.
//
// Подсветка говорящего слабее: она реагирует на громкость, а не на слова.
// Её ловит кашель и стук по клавиатуре, при перекрёстных репликах она скачет,
// а границы у неё грубее — опрос идёт раз в полсекунды. Зато она не зависит от
// языка распознавания и работает там, где субтитров нет вовсе (публичный
// Jitsi). Поэтому подсветка — запасной источник там, где субтитры молчат.
//
// При разногласии на одном отрезке выигрывают субтитры. Единственное
// исключение — когда субтитры лишь задели сегмент краем (меньше
// solidOverlap), а подсветка накрыла его по-настоящему: тогда речь идёт не о
// разногласии двух мнений об одном отрезке, а о том, что реплика субтитров
// относится к соседнему куску разговора.
//
// Расхождения не заминаются, а считаются: если субтитры систематически
// разъезжаются с подсветкой, это видно строкой в логе, а не по чужим фамилиям
// в follow-up.
//
// Третий параметр вариативный не для красоты: main.go зовёт AlignSpeakers
// двумя аргументами, и над ним сейчас работают, — так подсветка включается
// правкой одной строки, а сборка не ломается ни на минуту.
func AlignSpeakers(segs []core.Segment, utts []Utterance, tiles ...SpeakerSpan) []core.Segment {
	caps := make([]namedSpan, 0, len(utts))
	for _, u := range utts {
		// Субтитры отстают от речи — сдвигаем их назад к тому времени, когда
		// слова были сказаны.
		caps = append(caps, namedSpan{u.Speaker, u.Start - captionLag, u.End - captionLag})
	}
	sort.Slice(caps, func(i, j int) bool { return caps[i].start < caps[j].start })

	// Лента подсветки к времени речи приводится своим способом — в
	// normalizeSpans. Общим сдвигом их путать нельзя: у субтитров задержка
	// одна, у подсветки другая, и смешать их значит получить имена, съехавшие
	// ровно на эту разницу.
	norm := normalizeSpans(tiles)
	tls := make([]namedSpan, 0, len(norm))
	for _, s := range norm {
		tls = append(tls, namedSpan{s.Speaker, s.Start, s.End})
	}

	if len(caps) == 0 && len(tls) == 0 {
		return segs
	}

	capFinder := &spanFinder{spans: caps}
	tileFinder := &spanFinder{spans: tls}
	var mix speakerMix

	out := make([]core.Segment, len(segs))
	for i, s := range segs {
		out[i] = s
		c := capFinder.find(s)
		t := tileFinder.find(s)
		solid := solidOverlap(s)
		capSolid := c.ok && c.overlap > 0 && c.overlap >= solid
		tileSolid := t.ok && t.overlap > 0 && t.overlap >= solid
		if capSolid && tileSolid && c.speaker != t.speaker {
			mix.Conflicts++
		}
		switch {
		case capSolid:
			out[i].Speaker = c.speaker
			mix.FromCaptions++
		case t.ok && t.overlap > 0:
			// Субтитры сюда не попали или только задели краем, а подсветка
			// накрыла — это её случай.
			out[i].Speaker = t.speaker
			mix.FromTiles++
		case c.ok:
			out[i].Speaker = c.speaker
			mix.FromCaptions++
		case t.ok:
			out[i].Speaker = t.speaker
			mix.FromTiles++
		default:
			mix.Unnamed++
		}
	}
	if len(tls) > 0 {
		log.Printf("%s", mix.Report())
	}
	return out
}

// speakerMix — откуда взялись имена. Нужен, чтобы человек видел, работает ли
// подсветка вообще и не разъезжается ли она с субтитрами: молча выбранный
// источник — это чужие фамилии в follow-up без единого следа в логе.
type speakerMix struct {
	FromCaptions int
	FromTiles    int
	Unnamed      int
	Conflicts    int
}

func (m speakerMix) Report() string {
	msg := fmt.Sprintf(i18n.Tr("имена: %d от субтитров, %d от подсветки говорящего, %d без имени"),
		m.FromCaptions, m.FromTiles, m.Unnamed)
	if m.Conflicts > 0 {
		msg += fmt.Sprintf(i18n.Tr("; разошлись на %d сегментах — там взято имя из субтитров"), m.Conflicts)
	}
	return msg
}

// speakerTimelinePath — лента подсветки лежит рядом с субтитрами и пишется тем
// же ботом. Отдельным файлом, а не строчками в captions.jsonl: реплика
// субтитров и отрезок подсветки — разные вещи с разными задержками, и
// смешивать их в одном файле значит потом гадать, что было чем. Заодно
// «реплик в субтитрах» остаётся честным числом.
func speakerTimelinePath(captionsPath string) string {
	return filepath.Join(filepath.Dir(captionsPath), SpeakersFileName)
}

// SpeakersFileName — имя ленты. Одно на двоих: бот его пишет (meet_bot.go),
// расшифровка читает. Разъехавшиеся строковые литералы здесь означали бы, что
// лента пишется и молча никем не читается.
const SpeakersFileName = "speakers.jsonl"

// ReadSpeakerSpans читает ленту подсветки. Ошибку не возвращает намеренно:
// её отсутствие — обычное дело (старая запись, площадка без подсветки), и
// расшифровка от этого не должна падать.
func ReadSpeakerSpans(captionsPath string) []SpeakerSpan {
	f, err := os.Open(speakerTimelinePath(captionsPath))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []SpeakerSpan
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var s SpeakerSpan
		if err := json.Unmarshal([]byte(line), &s); err != nil {
			continue // одна битая строка не повод терять всю ленту
		}
		out = append(out, s)
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

// Пометки, которыми движки расшифровки обозначают отсутствие речи. Они не
// текст, и принимать их за расшифровку нельзя.
var noSpeechMarkers = regexp.MustCompile(`(?i)^[\[\(](blank_?audio|silence|music|музыка|тишина|inaudible|неразборчиво|звук[^\]\)]*)[\]\)]$`)

// CheckTranscript ловит вырожденную расшифровку до того, как за неё заплатят.
//
// Настоящий случай: whisper определяет язык по первым тридцати секундам, а
// запись созвона начинается с тишины — бот заходит раньше людей. Определился
// не тот язык, и весь разговор вышел пятнадцатью строками [BLANK_AUDIO].
// Формально это валидный JSON и нулевой код возврата: без проверки сервис
// отдал бы это Claude и выдал пустой follow-up как настоящий.
func CheckTranscript(segs []core.Segment) error {
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
		return fmt.Errorf(i18n.Tr("в расшифровке нет речи — %d строк, и все пустые или «нет звука». ")+
			i18n.Tr("Обычно это значит, что определился не тот язык или запись почти вся тишина"), len(segs))
	}
	if chars < minChars {
		return fmt.Errorf(i18n.Tr("расшифровка почти пустая: %d значимых строк, %d символов. ")+
			i18n.Tr("Проверь запись и язык распознавания"), real, chars)
	}
	return nil
}
