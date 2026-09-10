package audio

// Лента активного говорящего и то, как она складывается с субтитрами.
//
// Проверяется на цифрах, а не в браузере: браузер здесь не поднимается — ни в
// headless, ни тем более в headful. Разбор разметки живёт в meet.js и jitsi.js
// и проверяется на фикстурах DOM (meet_test.mjs, jitsi_test.mjs).

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sur1cat/steno/internal/core"
)

// poll прогоняет ленту через трекер так, как это делает бот: раз в
// CaptionPollInterval. Возвращает всё, что закрылось, включая хвост.
//
// steps — по одному набору подсвеченных имён на такт опроса.
func poll(steps [][]string) []SpeakerSpan {
	var t SpeakerTracker
	var out []SpeakerSpan
	at := 0.0
	step := CaptionPollInterval.Seconds()
	for _, names := range steps {
		out = append(out, t.Update(names, at)...)
		at += step
	}
	return append(out, t.Flush(at)...)
}

func names(spans []SpeakerSpan) []string {
	out := make([]string, 0, len(spans))
	for _, s := range spans {
		out = append(out, s.Speaker)
	}
	return out
}

func one(n string) []string { return []string{n} }

// --- сама лента --------------------------------------------------------------

// Говорит один.
func TestOneSpeakerBecomesOneSpan(t *testing.T) {
	spans := poll([][]string{
		nil, one("А"), one("А"), one("А"), one("А"), nil, nil, nil, nil,
	})
	if len(spans) != 1 {
		t.Fatalf("отрезков %d, ожидали 1: %+v", len(spans), spans)
	}
	if spans[0].Speaker != "А" {
		t.Fatalf("имя %q", spans[0].Speaker)
	}
	// Подсветка была видна с 0.5 по 2.0 — отрезок должен лечь ровно туда, а не
	// растянуться на тишину после себя: время, взятое лишним, отбирает у
	// следующего говорящего его же слова.
	if spans[0].Start != 0.5 || spans[0].End != 2.0 {
		t.Fatalf("отрезок %.2f–%.2f, ожидали 0.50–2.00", spans[0].Start, spans[0].End)
	}
}

// Говорят двое подряд.
func TestTwoSpeakersInARow(t *testing.T) {
	spans := poll([][]string{
		one("А"), one("А"), one("А"), nil, nil, nil,
		one("Б"), one("Б"), one("Б"), nil, nil, nil,
	})
	if got := names(spans); len(got) != 2 || got[0] != "А" || got[1] != "Б" {
		t.Fatalf("получили %v, ожидали [А Б]", got)
	}
	if spans[0].End > spans[1].Start {
		t.Fatalf("отрезки налезли друг на друга: %+v", spans)
	}
}

// Подсветка гаснет на паузе между словами и зажигается снова. Без склейки одна
// реплика рассыпалась бы на десяток отрезков, и каждый стык давал бы шанс
// уехать имени.
func TestShortBlinkStaysOneSpan(t *testing.T) {
	spans := poll([][]string{
		one("А"), one("А"), nil, one("А"), one("А"), nil, nil, nil, nil,
	})
	if len(spans) != 1 {
		t.Fatalf("мигание разорвало реплику: %+v", spans)
	}
}

// А долгая тишина — это уже конец реплики.
func TestLongGapSplitsSpans(t *testing.T) {
	spans := poll([][]string{
		one("А"), one("А"), nil, nil, nil, nil, nil, one("А"), one("А"), nil, nil, nil, nil,
	})
	if len(spans) != 2 {
		t.Fatalf("отрезков %d, ожидали 2: %+v", len(spans), spans)
	}
}

// Говорят двое одновременно — обоих надо удержать: молча выбрать одного значит
// потерять половину перекрёстной реплики.
func TestSimultaneousSpeakersBothKept(t *testing.T) {
	spans := poll([][]string{
		{"А", "Б"}, {"А", "Б"}, {"А", "Б"}, nil, nil, nil,
	})
	if len(spans) != 2 {
		t.Fatalf("отрезков %d, ожидали 2: %+v", len(spans), spans)
	}
}

// Пустые имена в ленту не идут: пустое имя в follow-up — это строка без
// автора, которую невозможно ни проверить, ни исправить.
func TestBlankNamesAreDropped(t *testing.T) {
	if spans := poll([][]string{{"", "   "}, {"", "   "}, nil, nil, nil}); len(spans) != 0 {
		t.Fatalf("пустое имя доехало до ленты: %+v", spans)
	}
}

// Flush закрывает то, что горело до самого конца звонка. Иначе последний
// говорящий пропал бы из ленты целиком — а это чаще всего тот, кто раздавал
// задачи в конце созвона.
func TestFlushClosesLivingSpans(t *testing.T) {
	var tr SpeakerTracker
	tr.Update(one("А"), 0)
	tr.Update(one("А"), 1)
	spans := tr.Flush(2)
	if len(spans) != 1 || spans[0].Speaker != "А" {
		t.Fatalf("получили %+v", spans)
	}
	if spans[0].End != 1 {
		t.Fatalf("конец %.2f, ожидали 1.00 — время последней подсветки, а не время выхода", spans[0].End)
	}
}

// Порядок отрезков не должен зависеть от обхода map: одна и та же запись должна
// давать один и тот же speakers.jsonl, иначе расхождение придётся разбирать
// вручную.
func TestSpanOrderIsStable(t *testing.T) {
	first := poll([][]string{{"Я", "А", "Б"}, {"Я", "А", "Б"}, nil, nil, nil})
	for i := 0; i < 50; i++ {
		if got := names(poll([][]string{{"Я", "А", "Б"}, {"Я", "А", "Б"}, nil, nil, nil})); !eq(got, names(first)) {
			t.Fatalf("порядок поплыл: %v против %v", got, names(first))
		}
	}
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// --- чистка ленты ------------------------------------------------------------

// Главный случай: подсветка не погасла после конца речи и висит, пока говорит
// следующий. Без чистки имя первого заедет на слова второго — ровно та беда,
// из-за которой follow-up приписывает задачи не тем людям.
func TestStuckHighlightDoesNotEatNextSpeaker(t *testing.T) {
	spans := normalizeSpans([]SpeakerSpan{
		{Speaker: "А", Start: 0, End: 25}, // залипла на весь кусок разговора
		{Speaker: "Б", Start: 12, End: 21},
	})
	if len(spans) != 2 {
		t.Fatalf("отрезков %d: %+v", len(spans), spans)
	}
	a, b := spans[0], spans[1]
	if a.Speaker != "А" || b.Speaker != "Б" {
		t.Fatalf("порядок: %+v", spans)
	}
	if a.End > b.Start {
		t.Fatalf("хвост А (%.2f) залез на Б (%.2f)", a.End, b.Start)
	}
	if a.End > 12 {
		t.Fatalf("А кончается на %.2f — позже, чем заговорил Б", a.End)
	}
}

// Хвост снимается и тогда, когда следующего говорящего нет вовсе: подсветка
// гаснет позже речи всегда, а не только на смене говорящего.
func TestTailIsTrimmedWithoutNextSpeaker(t *testing.T) {
	spans := normalizeSpans([]SpeakerSpan{{Speaker: "А", Start: 10, End: 20}})
	if len(spans) != 1 {
		t.Fatalf("%+v", spans)
	}
	if spans[0].End >= 20 {
		t.Fatalf("хвост не снят: конец %.2f", spans[0].End)
	}
	// А начало, наоборот, подтягивается назад: подсветка загорается позже
	// первого слова.
	if spans[0].Start >= 10 {
		t.Fatalf("начало не подтянуто: %.2f", spans[0].Start)
	}
}

// Мелькнувшая на один опрос подсветка не должна выворачиваться наизнанку.
func TestBlipStaysPositive(t *testing.T) {
	spans := normalizeSpans([]SpeakerSpan{{Speaker: "А", Start: 5, End: 5}})
	if len(spans) != 1 || spans[0].End <= spans[0].Start {
		t.Fatalf("отрицательный отрезок: %+v", spans)
	}
}

// Начало записи — ноль. Отрицательного времени в ленте быть не может: с ним
// пересечение с сегментами whisper считалось бы по несуществующему куску.
func TestSpansDoNotGoNegative(t *testing.T) {
	spans := normalizeSpans([]SpeakerSpan{{Speaker: "А", Start: 0, End: 3}})
	if spans[0].Start < 0 {
		t.Fatalf("отрицательное начало: %+v", spans)
	}
}

// --- сложение двух источников ------------------------------------------------

func quiet(t *testing.T) {
	t.Helper()
	prev := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(prev) })
}

// Тот самый случай, ради которого всё затевалось: субтитры пусты — Meet слушал
// не тот язык, — а имена всё равно должны проставиться.
func TestNamesComeFromTilesWhenCaptionsAreEmpty(t *testing.T) {
	quiet(t)
	segs := []core.Segment{
		{Start: 1, End: 4, Text: "давайте начнём с релиза"},
		{Start: 10, End: 14, Text: "я закончу миграцию к пятнице"},
	}
	tiles := []SpeakerSpan{
		{Speaker: "Участник А", Start: 0.5, End: 5},
		{Speaker: "Участник Б", Start: 9.5, End: 15},
	}
	got := AlignSpeakers(segs, nil, tiles...)
	if got[0].Speaker != "Участник А" || got[1].Speaker != "Участник Б" {
		t.Fatalf("имена не проставились: %+v", got)
	}
}

// Субтитров нет и подсветки нет — имён взять неоткуда, и выдумывать их нельзя.
func TestNoSourcesLeavesNames(t *testing.T) {
	quiet(t)
	segs := []core.Segment{{Start: 1, End: 4, Text: "тишина"}}
	if got := AlignSpeakers(segs, nil); got[0].Speaker != "" {
		t.Fatalf("имя взялось из ниоткуда: %q", got[0].Speaker)
	}
}

// Субтитры есть и совпадают с подсветкой — имя одно, и расхождений быть не
// должно.
func TestSourcesAgree(t *testing.T) {
	quiet(t)
	segs := []core.Segment{{Start: 10, End: 14, Text: "я закончу миграцию"}}
	// Реплика субтитров отстаёт от речи на captionLag — так она и записана.
	utts := []Utterance{{Speaker: "Участник Б", Text: "я закончу миграцию",
		Start: 10 + captionLag, End: 14 + captionLag}}
	tiles := []SpeakerSpan{{Speaker: "Участник Б", Start: 9.5, End: 15}}
	got := AlignSpeakers(segs, utts, tiles...)
	if got[0].Speaker != "Участник Б" {
		t.Fatalf("получили %q", got[0].Speaker)
	}
}

// Субтитры есть и НЕ совпадают с подсветкой. Выигрывают субтитры: строка
// субтитров появляется потому, что площадка распознала речь и привязала её к
// конкретному участнику, — это имя привязано к фразе. Подсветка реагирует на
// громкость, её ловит кашель и стук по клавиатуре.
func TestCaptionsWinOnDisagreement(t *testing.T) {
	quiet(t)
	segs := []core.Segment{{Start: 10, End: 14, Text: "я закончу миграцию"}}
	utts := []Utterance{{Speaker: "Участник Б", Text: "я закончу миграцию",
		Start: 10 + captionLag, End: 14 + captionLag}}
	tiles := []SpeakerSpan{{Speaker: "Участник В", Start: 9.5, End: 15}}
	got := AlignSpeakers(segs, utts, tiles...)
	if got[0].Speaker != "Участник В" && got[0].Speaker != "Участник Б" {
		t.Fatalf("получили %q", got[0].Speaker)
	}
	if got[0].Speaker != "Участник Б" {
		t.Fatalf("подсветка перебила субтитры: %q", got[0].Speaker)
	}
}

// Расхождение надо посчитать и назвать. Молча выбранный источник — это чужие
// фамилии в follow-up без единого следа в логе.
func TestDisagreementIsCounted(t *testing.T) {
	prev := log.Writer()
	var out strings.Builder
	log.SetOutput(&out)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(prev) })

	segs := []core.Segment{{Start: 10, End: 14, Text: "я закончу миграцию"}}
	utts := []Utterance{{Speaker: "Участник Б", Start: 10 + captionLag, End: 14 + captionLag}}
	tiles := []SpeakerSpan{{Speaker: "Участник В", Start: 9.5, End: 15}}
	AlignSpeakers(segs, utts, tiles...)
	if !strings.Contains(out.String(), "разошлись") {
		t.Fatalf("о расхождении промолчали: %q", out.String())
	}
}

// Реплика субтитров, задевшая сегмент краем, не должна перебивать подсветку,
// накрывшую его целиком: это не спор двух мнений об одном отрезке, а чужая
// реплика из соседнего куска разговора.
func TestMarginalCaptionLosesToSolidTile(t *testing.T) {
	quiet(t)
	segs := []core.Segment{{Start: 10, End: 14, Text: "я закончу миграцию"}}
	// Реплика Б кончается ровно на 10.05 после сдвига — касание краем.
	utts := []Utterance{{Speaker: "Участник Б",
		Start: 6 + captionLag, End: 10.05 + captionLag}}
	tiles := []SpeakerSpan{{Speaker: "Участник В", Start: 9.5, End: 15}}
	got := AlignSpeakers(segs, utts, tiles...)
	if got[0].Speaker != "Участник В" {
		t.Fatalf("касание краем перебило накрывшую подсветку: %q", got[0].Speaker)
	}
}

// Задержки у источников разные, и общим сдвигом их путать нельзя. Субтитры
// отстают от речи на captionLag и сдвигаются назад; подсветка идёт по времени
// речи и сдвигается по-своему. Сдвинуть ленту ещё и на captionLag значит
// получить имена, съехавшие ровно на эти полторы секунды.
//
// Проверяется на подсветке без субтитров: пока субтитры на месте, они всё
// равно выигрывают, и ошибка сдвига ленты осталась бы незаметной. Реплики
// здесь короткие и идут подряд — на них полторы секунды и решают.
func TestTileTimelineIsNotShiftedLikeCaptions(t *testing.T) {
	quiet(t)
	segs := []core.Segment{
		{Start: 0, End: 3, Text: "первый говорит"},
		{Start: 3.5, End: 6, Text: "второй говорит"},
	}
	tiles := []SpeakerSpan{
		{Speaker: "Участник А", Start: 0, End: 3.2},
		{Speaker: "Участник Б", Start: 3.4, End: 6.6},
	}
	got := AlignSpeakers(segs, nil, tiles...)
	if got[0].Speaker != "Участник А" {
		t.Fatalf("первый сегмент достался %q — лента сдвинута не туда", got[0].Speaker)
	}
	if got[1].Speaker != "Участник Б" {
		t.Fatalf("второй сегмент достался %q", got[1].Speaker)
	}
}

// А когда работают оба источника, они описывают одну и ту же речь — и
// расходиться не должны. Расхождение здесь означало бы, что задержки сведены
// неверно: одно и то же сказанное слово два источника относят к разным людям.
func TestBothSourcesDescribeTheSameSpeech(t *testing.T) {
	prev := log.Writer()
	var out strings.Builder
	log.SetOutput(&out)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(prev) })

	segs := []core.Segment{
		{Start: 0, End: 3, Text: "первый говорит"},
		{Start: 3.5, End: 6, Text: "второй говорит"},
	}
	utts := []Utterance{
		{Speaker: "Участник А", Start: 0 + captionLag, End: 3 + captionLag},
		{Speaker: "Участник Б", Start: 3.5 + captionLag, End: 6 + captionLag},
	}
	tiles := []SpeakerSpan{
		{Speaker: "Участник А", Start: 0, End: 3.2},
		{Speaker: "Участник Б", Start: 3.4, End: 6.6},
	}
	got := AlignSpeakers(segs, utts, tiles...)
	if got[0].Speaker != "Участник А" || got[1].Speaker != "Участник Б" {
		t.Fatalf("имена съехали: %+v", got)
	}
	if strings.Contains(out.String(), "разошлись") {
		t.Fatalf("источники разошлись на одной и той же речи: %s", out.String())
	}
}

// Подсветка не должна отменять того, что уже работало: без ленты выравнивание
// обязано вести себя ровно как раньше.
func TestTilesDoNotChangeCaptionOnlyBehaviour(t *testing.T) {
	quiet(t)
	segs := []core.Segment{
		{Start: 0, End: 3, Text: "раз"},
		{Start: 100, End: 104, Text: "а это уже другое"},
	}
	utts := []Utterance{{Speaker: "Участник А", Start: 0, End: 4}}
	got := AlignSpeakers(segs, utts)
	if got[0].Speaker != "Участник А" {
		t.Fatalf("имя из субтитров потерялось: %+v", got)
	}
	if got[1].Speaker != "" {
		t.Fatalf("приписали чужое имя далёкому сегменту: %q", got[1].Speaker)
	}
}

// --- бот ---------------------------------------------------------------------

// Молчащий бот — тоже участник, и площадка иногда подсвечивает его плитку.
// Своё имя в ленте — это своё имя в follow-up.
func TestWithoutSelfDropsTheBot(t *testing.T) {
	got := WithoutSelf([]string{"Участник А", "Steno · идёт запись", "Участник Б"},
		"Steno · идёт запись")
	if !eq(got, []string{"Участник А", "Участник Б"}) {
		t.Fatalf("получили %v", got)
	}
}

// Имя бота не настроено — вычёркивать некого, и молча выкинуть первого
// попавшегося нельзя.
func TestWithoutSelfKeepsEverythingWhenNameIsEmpty(t *testing.T) {
	in := []string{"Участник А", "Участник Б"}
	if got := WithoutSelf(in, ""); !eq(got, in) {
		t.Fatalf("получили %v", got)
	}
}

// Фильтр не должен портить входной срез: он приходит из разбора ответа
// страницы, и переписанный на месте он молча испортил бы соседние поля.
func TestWithoutSelfDoesNotWriteOverInput(t *testing.T) {
	in := []string{"Steno", "Участник А"}
	WithoutSelf(in, "Steno")
	if in[0] != "Steno" || in[1] != "Участник А" {
		t.Fatalf("входной срез переписан: %v", in)
	}
}

// --- отчёт человеку ----------------------------------------------------------

// «Подсветка не сработала ни разу» и «в звонке говорили» — разные новости, и
// первую надо сказать прямо: имён не будет.
func TestCoverageReportsSilence(t *testing.T) {
	var c SpeakerCoverage
	if r := c.Report(300); !strings.Contains(r, "ни разу") {
		t.Fatalf("промолчали о неработающей подсветке: %q", r)
	}
}

func TestCoverageNamesWhoTalked(t *testing.T) {
	var c SpeakerCoverage
	c.Add(SpeakerSpan{Speaker: "Участник А", Start: 0, End: 60})
	c.Add(SpeakerSpan{Speaker: "Участник Б", Start: 60, End: 90})
	r := c.Report(300)
	for _, want := range []string{"Участник А", "Участник Б", "2 отрезк"} {
		if !strings.Contains(r, want) {
			t.Fatalf("в отчёте нет %q: %s", want, r)
		}
	}
	// Кто говорил больше — тот и первый: по этой строке человек проверяет,
	// похоже ли это на его созвон.
	if strings.Index(r, "Участник А") > strings.Index(r, "Участник Б") {
		t.Fatalf("порядок не по времени речи: %s", r)
	}
}

// Записи не было — говорить нечего, и делить на ноль тоже.
func TestCoverageSaysNothingWithoutRecording(t *testing.T) {
	var c SpeakerCoverage
	if r := c.Report(0); r != "" {
		t.Fatalf("сказали лишнего: %q", r)
	}
}

// --- файл ленты --------------------------------------------------------------

// Лента лежит рядом с субтитрами и читается по пути к ним: так расшифровка
// находит её, не зная о боте ничего лишнего.
func TestSpeakerTimelineIsReadNextToCaptions(t *testing.T) {
	dir := t.TempDir()
	caps := filepath.Join(dir, "captions.jsonl")
	if err := os.WriteFile(caps, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	body := `{"speaker":"Участник А","start":1,"end":4}` + "\n" +
		`{"это не json` + "\n" + // одна битая строка не повод терять всю ленту
		`{"speaker":"Участник Б","start":10,"end":14}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, SpeakersFileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got := ReadSpeakerSpans(caps)
	if len(got) != 2 || got[0].Speaker != "Участник А" || got[1].Speaker != "Участник Б" {
		t.Fatalf("получили %+v", got)
	}
}

// Ленты нет — это обычное дело: старая запись, площадка без подсветки.
// Расшифровка от этого падать не должна.
func TestMissingTimelineIsNotAnError(t *testing.T) {
	if got := ReadSpeakerSpans(filepath.Join(t.TempDir(), "captions.jsonl")); got != nil {
		t.Fatalf("получили %+v", got)
	}
}

// Весь путь целиком, ровно тем выражением, которым его включает правка в
// transcribeMeeting:
//
//	AlignSpeakers(segs, utts, ReadSpeakerSpans(m.CaptionsPath)...)
//
// Здесь воспроизведён случай с живого созвона: субтитры пусты — Meet слушал не
// тот язык, — текст пришёл от whisper, и имена должны взяться из ленты. Тест
// стоит отдельно от остальных потому, что проверяет не логику, а стык: файл на
// диске, чтение по пути к субтитрам и выравнивание.
func TestEmptyCaptionsStillGetNamesFromTheTimeline(t *testing.T) {
	quiet(t)
	dir := t.TempDir()
	caps := filepath.Join(dir, "captions.jsonl")
	if err := os.WriteFile(caps, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	spans := `{"speaker":"Рустем","start":0.5,"end":5}` + "\n" +
		`{"speaker":"Дориан","start":9.5,"end":15}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, SpeakersFileName), []byte(spans), 0o644); err != nil {
		t.Fatal(err)
	}

	utts, err := ReadUtterances(caps)
	if err != nil {
		t.Fatal(err)
	}
	if len(utts) != 0 {
		t.Fatalf("субтитры должны быть пусты, а их %d", len(utts))
	}
	segs := []core.Segment{
		{Start: 1, End: 4, Text: "давайте начнём с релиза"},
		{Start: 10, End: 14, Text: "я закончу миграцию к пятнице"},
	}
	got := AlignSpeakers(segs, utts, ReadSpeakerSpans(caps)...)
	if got[0].Speaker != "Рустем" || got[1].Speaker != "Дориан" {
		t.Fatalf("пустые субтитры оставили расшифровку без имён: %+v", got)
	}
}
