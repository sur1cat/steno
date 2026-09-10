package main

// Активный говорящий: кто подсвечен в интерфейсе площадки прямо сейчас.
//
// Зачем это отдельно от субтитров. Текст расшифровки берётся у whisper, а имена
// — у субтитров площадки: whisper слышит речь, но не знает, кто говорит. На
// живом созвоне это подвело целиком: Meet слушал русскую речь английским
// распознаванием и выдал 31 букву за 232 секунды. Имена в этих крохах были
// верные, но вешать их было не на что — follow-up приписал все шесть задач
// одному человеку, потому что видел только его.
//
// Подсветка говорящего — сигнал другого рода. Это состояние интерфейса, а не
// результат распознавания: оно не зависит ни от языка, ни от качества
// распознавания, ни от того, включены ли субтитры вообще. На Jitsi с публичным
// сервером субтитров нет в принципе, и это единственный способ получить там
// имена.
//
// Сигнал слабее субтитров, и это важно понимать до того, как начнёшь ему
// верить. Он привязан не к фразе, а к громкости: подсветка загорается с
// небольшим опозданием, держится после того, как человек замолчал, а при
// перекрёстных репликах скачет. Поэтому лента здесь не заменяет субтитры, а
// достраивает их — см. alignSpeakers в transcribe.go.
//
// Сшивка живёт здесь, а не в странице: в скрипте страницы состояния нет
// намеренно, поэтому её перезагрузка ничего не ломает. Страница отвечает на
// один вопрос — «кто подсвечен сейчас», — а отрезки собираются тут.

import (
	"fmt"
	"sort"
	"strings"
)

// SpeakerSpan — отрезок, на котором площадка подсвечивала одного говорящего.
// Секунды считаются от старта записи, как и у реплик субтитров.
type SpeakerSpan struct {
	Speaker string  `json:"speaker"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
}

const (
	// tileBlink — сколько подсветка может пропасть, чтобы это всё ещё считалось
	// одной репликой. Между словами и на короткой паузе Meet гасит рамку и
	// зажигает снова; без склейки одна реплика рассыпалась бы на десяток
	// отрезков, и каждый стык давал бы шанс уехать имени.
	tileBlink = 1.0

	// tileTail — сколько подсветка висит после того, как человек замолчал.
	// Она гаснет не мгновенно: интерфейс держит рамку, чтобы она не мигала на
	// паузах между словами. Хвост надо снимать, иначе имя заедет на следующего
	// говорящего — ровно та же беда, из-за которой реплика субтитров
	// закрывается временем последнего изменения текста, а не временем, когда мы
	// заметили тишину.
	tileTail = 1.0

	// tileRise — насколько подсветка опаздывает за началом речи. Складывается
	// из двух задержек: сама площадка переключает активного говорящего не
	// мгновенно, и мы опрашиваем страницу раз в captionPollInterval. Начало
	// отрезка сдвигается назад ровно на это.
	tileRise = 0.5

	// tileMinSpan — короче этого отрезок не делаем. Подсветка, мелькнувшая на
	// один опрос, — это чаще всего кашель или стук по клавиатуре; после снятия
	// хвоста от неё осталась бы отрицательная длина.
	tileMinSpan = 0.3
)

// SpeakerTracker собирает ленту из ответов страницы. Устроен как
// CaptionTracker: страница отдаёт срез «кто подсвечен сейчас», а границы
// отрезков считаются здесь.
type SpeakerTracker struct {
	live map[string]*speakingNow
}

type speakingNow struct {
	start float64
	// seen — последний опрос, на котором подсветка ещё была.
	seen float64
}

// Update принимает имена, подсвеченные на этом опросе, и возвращает отрезки,
// которые с этого момента считаются законченными.
func (t *SpeakerTracker) Update(names []string, at float64) []SpeakerSpan {
	if t.live == nil {
		t.live = map[string]*speakingNow{}
	}
	on := make(map[string]bool, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		on[n] = true
	}
	for n := range on {
		if s := t.live[n]; s != nil {
			s.seen = at
			continue
		}
		t.live[n] = &speakingNow{start: at, seen: at}
	}
	var done []SpeakerSpan
	for n, s := range t.live {
		// Мигание длиной меньше tileBlink — это ещё та же реплика.
		if on[n] || at-s.seen <= tileBlink {
			continue
		}
		done = append(done, SpeakerSpan{Speaker: n, Start: s.start, End: s.seen})
		delete(t.live, n)
	}
	sortSpans(done)
	return done
}

// Flush закрывает всё, что осталось гореть, — вызывается при выходе из звонка.
func (t *SpeakerTracker) Flush(at float64) []SpeakerSpan {
	var done []SpeakerSpan
	for n, s := range t.live {
		end := s.seen
		if end > at {
			end = at
		}
		done = append(done, SpeakerSpan{Speaker: n, Start: s.start, End: end})
	}
	t.live = nil
	sortSpans(done)
	return done
}

// sortSpans — устойчивый порядок. Имя вторым ключом не косметика: отрезки
// приходят из обхода map, и без него один и тот же созвон давал бы разный
// speakers.jsonl от запуска к запуску, а разбираться в расхождении пришлось бы
// вручную.
func sortSpans(s []SpeakerSpan) {
	sort.Slice(s, func(i, j int) bool {
		if s[i].Start != s[j].Start {
			return s[i].Start < s[j].Start
		}
		if s[i].End != s[j].End {
			return s[i].End < s[j].End
		}
		return s[i].Speaker < s[j].Speaker
	})
}

// normalizeSpans приводит сырую ленту к виду, годному для выравнивания.
//
// Три правки, каждая — за свою беду.
//
// Склейка миганий: подсветка гаснет на паузах внутри реплики.
//
// Обрезка по следующему говорящему: подсветка держится после конца речи, и на
// живом интерфейсе она может залипнуть надолго. Отрезок, доехавший до начала
// чужого, укорачивается — чужие слова не должны достаться тому, кто уже
// замолчал. Одновременную подсветку двоих мы при этом теряем, и это осознанный
// размен: перекрёстные реплики редки, а залипший хвост встречается на каждом
// созвоне, и ошибается он именно там, где имя важнее всего — на смене
// говорящего.
//
// Хвост и подъём: подсветка загорается позже начала речи и гаснет позже её
// конца. Сдвигаем обе границы назад, но на разную величину.
func normalizeSpans(in []SpeakerSpan) []SpeakerSpan {
	spans := make([]SpeakerSpan, 0, len(in))
	for _, s := range in {
		s.Speaker = strings.TrimSpace(s.Speaker)
		if s.Speaker == "" || s.End < s.Start {
			continue
		}
		spans = append(spans, s)
	}
	if len(spans) == 0 {
		return nil
	}
	sortSpans(spans)

	// Склейка миганий. Отрезки уже по времени, поэтому продолжение ищем среди
	// того, что уже отобрали, — по последнему отрезку каждого имени.
	out := make([]SpeakerSpan, 0, len(spans))
	last := map[string]int{}
	for _, s := range spans {
		if i, ok := last[s.Speaker]; ok && s.Start-out[i].End <= tileBlink {
			if s.End > out[i].End {
				out[i].End = s.End
			}
			continue
		}
		out = append(out, s)
		last[s.Speaker] = len(out) - 1
	}

	// Обрезка по следующему чужому отрезку.
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j].Speaker == out[i].Speaker {
				continue
			}
			if out[j].Start < out[i].End {
				out[i].End = out[j].Start
			}
			break
		}
	}

	// Хвост и подъём.
	for i := range out {
		out[i].Start -= tileRise
		if out[i].Start < 0 {
			out[i].Start = 0
		}
		out[i].End -= tileTail
		if out[i].End < out[i].Start+tileMinSpan {
			out[i].End = out[i].Start + tileMinSpan
		}
	}
	sortSpans(out)
	return out
}

// SpeakerCoverage — сколько имён дала подсветка. Считается по ходу записи и
// печатается в лог: «отрезков ноль» бывает и у молчаливого созвона, а вот
// «участников трое, а подсветка не сработала ни разу» — это уже сломанный
// признак, и человек должен увидеть это в логе, а не в follow-up без имён.
type SpeakerCoverage struct {
	Spans int
	// Talk — сколько всего секунд подсветки, с наложениями.
	Talk float64
	// People — сколько разных имён.
	People map[string]float64
}

func (c *SpeakerCoverage) Add(s SpeakerSpan) {
	if c.People == nil {
		c.People = map[string]float64{}
	}
	d := s.End - s.Start
	if d < 0 {
		d = 0
	}
	c.Spans++
	c.Talk += d
	c.People[s.Speaker] += d
}

// Report — строка в лог. Пустая означает «сказать нечего»: записи не было.
func (c SpeakerCoverage) Report(elapsedSeconds float64) string {
	if elapsedSeconds <= 0 {
		return ""
	}
	if c.Spans == 0 {
		return tr("подсветка говорящего: ни разу — имена придётся брать только из субтитров")
	}
	names := make([]string, 0, len(c.People))
	for n := range c.People {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		if c.People[names[i]] != c.People[names[j]] {
			return c.People[names[i]] > c.People[names[j]]
		}
		return names[i] < names[j]
	})
	parts := make([]string, 0, len(names))
	for _, n := range names {
		parts = append(parts, fmt.Sprintf(tr("%s — %.0f с"), n, c.People[n]))
	}
	return fmt.Sprintf(tr("подсветка говорящего: %d отрезков, %.0f с речи из %.0f (%.0f%%); %s"),
		c.Spans, c.Talk, elapsedSeconds, 100*c.Talk/elapsedSeconds, strings.Join(parts, ", "))
}
