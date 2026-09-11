package audio

import (
	"log"
	"strings"
	"testing"

	"github.com/sur1cat/steno/internal/core"
)

// Метка диаризации и имя человека — разные вещи, и путать их нельзя в обе
// стороны: «A» нельзя оставить в follow-up вместо имени, а «Ануар Смагулов»
// нельзя принять за метку и затереть догадкой по субтитрам.
func TestLooksLikeSpeakerID(t *testing.T) {
	ids := []string{"A", "B", "a", "0", "12", "speaker 1", "Speaker_2", "SPEAKER_00",
		"spk_1", "spk-3", "S1", "s 0"}
	for _, s := range ids {
		if !looksLikeSpeakerID(s) {
			t.Errorf("%q — это метка диаризации, а её приняли за имя", s)
		}
	}
	names := []string{"Ануар", "Ян", "Bob", "Rustem Turgeldin", "Гость", "AB",
		"speaker Ануар", "Спикер"}
	for _, s := range names {
		if looksLikeSpeakerID(s) {
			t.Errorf("%q — это имя, а его приняли за метку", s)
		}
	}
}

// Движок с диаризацией слушает звук, а мы разглядываем DOM площадки. Если он
// вернул имя, а не метку, оно и остаётся: он знает то, чего мы знать не можем.
func TestEngineNameIsNeverOverwritten(t *testing.T) {
	quiet(t)
	segs := []core.Segment{{Start: 10, End: 14, Speaker: "Ануар Смагулов", Text: "я закончу миграцию"}}
	utts := []Utterance{{Speaker: "Участник Б", Start: 10 + captionLag, End: 14 + captionLag}}
	tiles := []SpeakerSpan{{Speaker: "Участник В", Start: 9.5, End: 15}}
	if got := AlignSpeakers(segs, utts, tiles...)[0].Speaker; got != "Ануар Смагулов" {
		t.Fatalf("имя от движка затёрли: %q", got)
	}
}

// Ради чего всё затевалось. Метка — это ключ, а не имя: голоса за имя
// собираются по всем репликам метки сразу, и реплика, которую субтитры задели
// чужим именем из-за задержки, получает имя своих соседей по голосу.
func TestClusterInheritsTheMajorityName(t *testing.T) {
	quiet(t)
	segs := []core.Segment{
		{Start: 10, End: 14, Speaker: "A", Text: "давайте начнём"},
		{Start: 20, End: 24, Speaker: "A", Text: "я закончу миграцию"},
		{Start: 30, End: 34, Speaker: "A", Text: "к пятнице сделаю"},
	}
	utts := []Utterance{
		{Speaker: "Рустем", Start: 10 + captionLag, End: 14 + captionLag},
		{Speaker: "Рустем", Start: 20 + captionLag, End: 24 + captionLag},
		// Третью реплику субтитры задели чужим именем: у Meet своя задержка, и
		// на стыке реплик она уезжает. Раньше эта реплика уходила в follow-up
		// на «Гостя» — вместе с задачей.
		{Speaker: "Гость", Start: 30 + captionLag, End: 34 + captionLag},
	}
	got := AlignSpeakers(segs, utts)
	for i, s := range got {
		if s.Speaker != "Рустем" {
			t.Errorf("реплика %d ушла к %q, а весь голос — Рустема", i, s.Speaker)
		}
	}
}

// Голоса взвешиваются пересечением, а не считаются по головам. Реплика,
// накрытая субтитрами целиком, знает про говорящего больше, чем две реплики,
// задетые чужим именем краем на треть секунды, — а по головам последние
// выигрывают две к одной и уводят весь голос вместе с задачами.
func TestClusterVotesAreWeighedByOverlap(t *testing.T) {
	quiet(t)
	segs := []core.Segment{
		{Start: 10, End: 14, Speaker: "A", Text: "я закончу миграцию к пятнице"},
		{Start: 20, End: 24, Speaker: "A", Text: "и стейджинг тоже"},
		{Start: 30, End: 34, Speaker: "A", Text: "к пятнице сделаю"},
	}
	utts := []Utterance{
		// Накрывает первую реплику целиком — четыре секунды.
		{Speaker: "Рустем", Start: 10 + captionLag, End: 14 + captionLag},
		// Задевает вторую и третью краем — по трети секунды на каждую.
		{Speaker: "Гость", Start: 19.7 + captionLag, End: 20.3 + captionLag},
		{Speaker: "Гость", Start: 29.7 + captionLag, End: 30.3 + captionLag},
	}
	got := AlignSpeakers(segs, utts)
	for i, s := range got {
		if s.Speaker != "Рустем" {
			t.Errorf("реплика %d ушла к %q: голоса посчитали по головам", i, s.Speaker)
		}
	}
}

// Диаризация ошибается тоже: два человека сливаются в один голос. Наследовать
// имя большинства тогда нельзя — половина реплик получит чужое. Такая метка
// разбирается по-старому, по каждой реплике отдельно.
func TestSplitClusterFallsBackToPerSegment(t *testing.T) {
	prev := log.Writer()
	var out strings.Builder
	log.SetOutput(&out)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(prev) })

	segs := []core.Segment{
		{Start: 10, End: 14, Speaker: "A", Text: "давайте начнём"},
		{Start: 20, End: 24, Speaker: "A", Text: "я закончу миграцию"},
	}
	utts := []Utterance{
		{Speaker: "Рустем", Start: 10 + captionLag, End: 14 + captionLag},
		{Speaker: "Гость", Start: 20 + captionLag, End: 24 + captionLag},
	}
	got := AlignSpeakers(segs, utts)
	if got[0].Speaker != "Рустем" || got[1].Speaker != "Гость" {
		t.Fatalf("слитый голос назвали одним именем: %q и %q", got[0].Speaker, got[1].Speaker)
	}
	if !strings.Contains(out.String(), "не сошлось") {
		t.Errorf("о слитом голосе промолчали: %q", out.String())
	}
}

// Метка есть, а назвать её некому: субтитров нет, подсветки нет. Метка остаётся
// именем — «A: …» в follow-up хуже, чем «Ануар: …», но заметно лучше, чем все
// шесть задач на одного человека.
func TestLabelStaysWhenNobodyNamesIt(t *testing.T) {
	quiet(t)
	segs := []core.Segment{
		{Start: 10, End: 14, Speaker: "A", Text: "давайте начнём"},
		{Start: 20, End: 24, Speaker: "B", Text: "я закончу миграцию"},
	}
	// Совсем без субтитров: ранний выход, сегменты возвращаются как есть.
	got := AlignSpeakers(segs, nil)
	if got[0].Speaker != "A" || got[1].Speaker != "B" {
		t.Fatalf("метки потеряли: %q и %q", got[0].Speaker, got[1].Speaker)
	}
	// Субтитры есть, но про эти реплики молчат — метки всё равно остаются.
	utts := []Utterance{{Speaker: "Рустем", Start: 300, End: 304}}
	got = AlignSpeakers(segs, utts)
	if got[0].Speaker != "A" || got[1].Speaker != "B" {
		t.Fatalf("метки потеряли при непопавших субтитрах: %q и %q",
			got[0].Speaker, got[1].Speaker)
	}
}

// Отчёт о том, откуда взялись имена, — единственный след диаризации в логе.
// Молча выбранный источник — это чужие фамилии в follow-up и ни одной строки,
// по которой можно понять, почему.
func TestReportMentionsDiarization(t *testing.T) {
	prev := log.Writer()
	var out strings.Builder
	log.SetOutput(&out)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(prev) })

	segs := []core.Segment{
		{Start: 10, End: 14, Speaker: "A", Text: "давайте начнём"},
		{Start: 20, End: 24, Speaker: "Ануар Смагулов", Text: "я закончу миграцию"},
	}
	utts := []Utterance{{Speaker: "Рустем", Start: 10 + captionLag, End: 14 + captionLag}}
	AlignSpeakers(segs, utts)
	if !strings.Contains(out.String(), "по диаризации") {
		t.Fatalf("о диаризации промолчали: %q", out.String())
	}
}

// Спор субтитров с подсветкой на реплике, названной движком, ничего не решал —
// и в отчёт он не идёт. Строка отчёта обещает, что на спорных репликах взято имя
// из субтитров; посчитать здесь значило бы соврать в ней.
func TestDisagreementOnEngineNamedIsNotCounted(t *testing.T) {
	prev := log.Writer()
	var out strings.Builder
	log.SetOutput(&out)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(prev) })

	segs := []core.Segment{{Start: 10, End: 14, Speaker: "Ануар Смагулов", Text: "я закончу миграцию"}}
	utts := []Utterance{{Speaker: "Участник Б", Start: 10 + captionLag, End: 14 + captionLag}}
	tiles := []SpeakerSpan{{Speaker: "Участник В", Start: 9.5, End: 15}}
	AlignSpeakers(segs, utts, tiles...)
	if strings.Contains(out.String(), "разошлись") {
		t.Fatalf("посчитали спор, который ни на что не влиял: %q", out.String())
	}
}

// Реплики без метки должны разбираться ровно так, как до всей этой истории:
// диаризация ничего не должна менять там, где её нет.
func TestUnlabelledSegmentsKeepOldBehaviour(t *testing.T) {
	quiet(t)
	segs := []core.Segment{
		{Start: 10, End: 14, Text: "давайте начнём"},
		{Start: 20, End: 24, Text: "я закончу миграцию"},
	}
	utts := []Utterance{
		{Speaker: "Рустем", Start: 10 + captionLag, End: 14 + captionLag},
		{Speaker: "Гость", Start: 20 + captionLag, End: 24 + captionLag},
	}
	got := AlignSpeakers(segs, utts)
	if got[0].Speaker != "Рустем" || got[1].Speaker != "Гость" {
		t.Fatalf("поведение без меток изменилось: %q и %q", got[0].Speaker, got[1].Speaker)
	}
}
