package main

// Сшивка строк субтитров в реплики.
//
// Meet держит в области субтитров несколько последних строк и переписывает
// каждую по мере того, как распознавание уточняется. Состояния в странице нет,
// поэтому реплики собираем здесь: строка, исчезнувшая из области, считается
// договорённой.
//
// Сшивка общая для всех площадок и от площадки не зависит: Jitsi ведёт себя
// так же — держит последние реплики и уточняет их на месте, — а скрипт страницы
// в любом случае отдаёт сюда одинаковые CaptionLine. Отдельного разбора под
// каждую площадку здесь быть не должно: это самая дорогая в отладке часть
// конвейера, и чинить её надо в одном месте.

import (
	"fmt"
	"strings"
	"time"
	"unicode"
)

type CaptionLine struct {
	Speaker string `json:"speaker"`
	Text    string `json:"text"`
}

// Utterance — договорённая реплика. Секунды считаются от старта записи, чтобы
// потом лечь на таймкоды whisper.
type Utterance struct {
	Speaker string  `json:"speaker"`
	Text    string  `json:"text"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
}

type CaptionTracker struct {
	live []*liveUtt
}

// liveUtt — реплика, которая ещё идёт. Помимо самой реплики хранит, сколько
// текста уже отдано наружу: при непрерывном монологе Meet дописывает одну и ту
// же строку минутами, она не исчезает из области, и ждать её конца — значит
// не записать ни слова до самого выхода из звонка.
type liveUtt struct {
	speaker string
	text    string
	// segStart — начало ещё не отданного куска.
	segStart float64
	// sent — текст, уже ушедший в файл. Именно текст, а не счётчик рун:
	// счётчик — это позиция в старой строке, применённая к новой, и он врёт
	// каждый раз, когда Meet не дописывает строку, а переписывает её.
	// Строка стала короче отданного — счётчик молча глотает весь новый текст;
	// строку переписали с середины — счётчик срезает начало нового.
	sent string
	// changed — когда текст последний раз менялся.
	changed float64
}

// idleFinalize — сколько ждать без изменений, прежде чем отдать накопленное.
// Пауза в речи короче секунды бывает и в середине фразы; четыре секунды — уже
// смена мысли.
const idleFinalize = 4.0

func (l *liveUtt) pending() string {
	if l.sent == "" {
		return strings.TrimSpace(l.text)
	}
	if strings.HasPrefix(l.text, l.sent) {
		return strings.TrimSpace(l.text[len(l.sent):])
	}
	// Строку переписали или сдвинули окно. Общее начало отдавать заново не
	// надо, а вот терять хвост нельзя: лучше повтор в расшифровке, который
	// видно, чем тихо пропавшие слова, которых не хватишься.
	n := commonPrefixLen(l.sent, l.text)
	return strings.TrimSpace(string([]rune(l.text)[n:]))
}

// visibleEnd — чем закрыть реплику, строка которой ещё была на экране в
// момент at. Дальше последнего изменения текста реплика может тянуться только
// пока строка висит, и не дольше idleFinalize: ровно столько мы и сами ждём,
// прежде чем счесть, что человек замолчал.
func (l *liveUtt) visibleEnd(at float64) float64 {
	end := l.changed + idleFinalize
	if end > at {
		end = at
	}
	return end
}

// emit отдаёт накопленное и запоминает, докуда отдано. end задаётся снаружи,
// потому что «строка исчезла» и «строка стоит без изменений» закрываются
// по-разному.
//
// Это не косметика: alignSpeakers раздаёт имена по пересечению отрезков, и
// реплика, растянутая на тишину после себя, забирает слова следующего
// говорящего. На живом созвоне реплика из двух слов получила отрезок в 13,5
// секунды ровно так — потому что закрывалась временем, когда мы заметили
// тишину, а не временем, когда текст перестал меняться.
func (l *liveUtt) emit(at, end float64) (Utterance, bool) {
	txt := l.pending()
	if txt == "" {
		return Utterance{}, false
	}
	if end < l.segStart {
		end = l.segStart
	}
	u := Utterance{Speaker: l.speaker, Text: txt, Start: l.segStart, End: end}
	l.sent = l.text
	l.segStart = at
	return u, true
}

// Update принимает то, что сейчас видно в области субтитров, и возвращает
// реплики, которые с этого момента считаются законченными.
func (t *CaptionTracker) Update(lines []CaptionLine, at float64) []Utterance {
	matched := make([]*liveUtt, len(lines))
	used := make([]bool, len(t.live))

	// Частый случай: у говорящего ровно одна живая реплика и ровно одна новая
	// строка. Обычно это одно и то же — Meet переписал строку, уточнив
	// распознавание. Но не всегда: если он успел убрать законченную строку и
	// нарисовать следующую за один такт опроса, это две разные реплики.
	// Раньше они склеивались, первая пропадала целиком, а выжившая забирала
	// себе её время — и вместе с ним чужие сегменты whisper.
	liveBy := map[string][]int{}
	for i, u := range t.live {
		liveBy[u.speaker] = append(liveBy[u.speaker], i)
	}
	lineBy := map[string][]int{}
	for i, l := range lines {
		lineBy[l.Speaker] = append(lineBy[l.Speaker], i)
	}
	for sp, li := range lineBy {
		lv := liveBy[sp]
		if len(li) == 1 && len(lv) == 1 && sameUtterance(t.live[lv[0]].text, lines[li[0]].Text) {
			matched[li[0]] = t.live[lv[0]]
			used[lv[0]] = true
		}
	}

	// Остальное разводим по длине общего префикса: продолжение реплики всегда
	// начинается так же, как её предыдущая версия.
	for i, l := range lines {
		if matched[i] != nil {
			continue
		}
		best, bestScore := -1, 0
		for j, u := range t.live {
			if used[j] || u.speaker != l.Speaker {
				continue
			}
			if s := commonPrefixLen(u.text, l.Text); s > bestScore {
				best, bestScore = j, s
			}
		}
		if best >= 0 && bestScore >= 8 {
			matched[i] = t.live[best]
			used[best] = true
		}
	}

	var done []Utterance
	// Строка ушла из области — реплика закончилась.
	for j, u := range t.live {
		if !used[j] {
			// Строка исчезла: до at она ещё висела, поэтому закрываем её
			// по visibleEnd, а не временем последнего изменения — иначе
			// строка, которую нарисовали и убрали за один такт, получила бы
			// отрезок нулевой длины и не пересеклась бы ни с чем у whisper.
			if e, ok := u.emit(at, u.visibleEnd(at)); ok {
				done = append(done, e)
			}
		}
	}

	next := make([]*liveUtt, 0, len(lines))
	for i, l := range lines {
		u := matched[i]
		// Строка без текста — это отрисованное имя говорящего, для которого
		// распознавание ещё не выдало слов. Такое бывает на каждом новом
		// говорящем. Реплику при этом нельзя ни потерять, ни перетереть
		// пустотой: строка на месте, значит реплика ещё идёт.
		if l.Text == "" {
			if u != nil {
				next = append(next, u)
			}
			continue
		}
		if u == nil {
			next = append(next, &liveUtt{speaker: l.Speaker, text: l.Text,
				segStart: at, changed: at})
			continue
		}
		if u.text != l.Text {
			u.text = l.Text
			u.changed = at
		} else if at-u.changed >= idleFinalize {
			// Текст стоит на месте — человек замолчал. Отдаём накопленное, не
			// дожидаясь, пока строка исчезнет: при монологе она не исчезает
			// минутами, а падение бота унесло бы всё несохранённое. Закрываем
			// временем последнего изменения: тишина после реплики принадлежит
			// не ей, а следующему говорящему.
			if e, ok := u.emit(at, u.changed); ok {
				done = append(done, e)
			}
		}
		next = append(next, u)
	}
	t.live = next
	return done
}

// Flush закрывает всё, что осталось висеть, — вызывается при выходе из звонка.
func (t *CaptionTracker) Flush(at float64) []Utterance {
	var done []Utterance
	for _, u := range t.live {
		if e, ok := u.emit(at, u.visibleEnd(at)); ok {
			done = append(done, e)
		}
	}
	t.live = nil
	return done
}

// sameUtterance отличает уточнение уже сказанного от начала новой реплики.
// «привет всем» → «Привет, всем коллегам» — та же реплика: Meet дописал и
// расставил знаки. «хорошо, договорились по срокам» → «теперь давайте про
// бюджет» — уже другая, общего начала нет.
func sameUtterance(oldText, newText string) bool {
	a, b := normalizeForCompare(oldText), normalizeForCompare(newText)
	if a == "" || b == "" {
		return true // текста ещё нет — строка на месте, реплика та же
	}
	n := commonPrefixLen(a, b)
	if shorter := min(len([]rune(a)), len([]rune(b))); shorter < 4 {
		return n == shorter
	}
	return n >= 4
}

// normalizeForCompare убирает регистр и пунктуацию: уточняя распознанное, Meet
// меняет ровно их.
func normalizeForCompare(s string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if space && b.Len() > 0 {
				b.WriteRune(' ')
			}
			space = false
			b.WriteRune(r)
		default:
			space = true
		}
	}
	return b.String()
}

func commonPrefixLen(a, b string) int {
	ar, br := []rune(a), []rune(b)
	n := len(ar)
	if len(br) < n {
		n = len(br)
	}
	i := 0
	for i < n && ar[i] == br[i] {
		i++
	}
	return i
}

// --- сколько текста дали субтитры -------------------------------------------

// CaptionYield считает, сколько текста субтитры дали за запись и какой он
// письменности.
//
// Считать это нужно ровно за одним. Язык распознавания у Meet свой, задаётся в
// его настройках и от whisper не зависит. Когда он не тот, субтитры отдают не
// мусор, а почти ничего: на живом созвоне 232 секунды русской речи дали 31
// букву — «But. Dorian.», «Show. Release.», «He.», и всё это в первые 39
// секунд. Имена говорящих при этом были верные, поэтому снаружи запись
// выглядела рабочей, а follow-up получил одного говорящего на весь созвон и
// расставил задачи не на тех людей.
//
// Молчать об этом нельзя, а определить надёжно можно только по самому тексту:
// спросить у Meet, каким языком он слушает, не всегда выходит.
type CaptionYield struct {
	Utterances int
	Latin      int
	Cyrillic   int
}

func (y *CaptionYield) Add(u Utterance) {
	y.Utterances++
	for _, r := range u.Text {
		if !unicode.IsLetter(r) {
			continue
		}
		switch {
		case r < 128:
			y.Latin++
		case unicode.Is(unicode.Cyrillic, r):
			y.Cyrillic++
		case unicode.Is(unicode.Latin, r):
			y.Latin++
		}
	}
}

func (y CaptionYield) letters() int { return y.Latin + y.Cyrillic }

// minLettersToJudge — меньше этого о письменности говорить нечего: одно
// случайное слово ничего не значит.
const minLettersToJudge = 12

// Script — «латиница», «кириллица», «вперемешку» или пустая строка, если букв
// слишком мало, чтобы судить.
func (y CaptionYield) Script() string {
	n := y.letters()
	if n < minLettersToJudge {
		return ""
	}
	switch {
	case y.Cyrillic*5 >= n*4:
		return "кириллица"
	case y.Latin*5 >= n*4:
		return "латиница"
	}
	return "вперемешку"
}

// langScript — какой письменностью пишут на языке с этим кодом. Список
// короткий нарочно: он нужен не для классификации языков, а чтобы поймать
// самый частый и самый дорогой случай — говорят по-русски, а Meet слушает
// по-английски.
var langScript = map[string]string{
	"ru": "кириллица", "uk": "кириллица", "be": "кириллица", "bg": "кириллица",
	"sr": "кириллица", "mk": "кириллица", "kk": "кириллица", "ky": "кириллица",
	"en": "латиница", "de": "латиница", "fr": "латиница", "es": "латиница",
	"it": "латиница", "pt": "латиница", "nl": "латиница", "pl": "латиница",
	"tr": "латиница", "cs": "латиница", "ro": "латиница", "id": "латиница",
	"sv": "латиница", "fi": "латиница", "da": "латиница", "no": "латиница",
}

// sparseCaptions — букв в минуту, ниже которых субтитры перестают быть
// расшифровкой. Обычная речь даёт 600–900; 150 — это уже не тихий созвон, а
// распознавание, которое не работает.
const sparseCaptions = 150

// Report — строка в лог о том, что вышло с субтитрами: сколько текста, какой
// письменности и не разошлась ли она с языком, который мы просили у Meet.
// Пустая строка означает «сказать нечего»: записи не было.
func (y CaptionYield) Report(speech time.Duration, wantLang string) string {
	if speech <= 0 {
		return ""
	}
	n := y.letters()
	rate := float64(n) / speech.Minutes()
	msg := fmt.Sprintf("субтитры: %d реплик, %d букв за %s (%.0f букв в минуту)",
		y.Utterances, n, speech.Round(time.Second), rate)
	if s := y.Script(); s != "" {
		msg += "; письменность — " + s
	}

	want := langScript[strings.ToLower(wantLang)]
	got := y.Script()
	if want != "" && got != "" && got != want {
		return msg + fmt.Sprintf(". Meet слушает не тот язык: просили %s (%s), "+
			"а текст идёт %s. Имена говорящих от этого верные, а текст субтитров "+
			"брать нельзя — язык распознавания ставится в самом Meet: "+
			"⋮ → Настройки → Субтитры → язык встречи", wantLang, want, got)
	}
	if rate < sparseCaptions {
		return msg + ". Это в разы меньше, чем речи: либо в звонке молчали, " +
			"либо Meet распознаёт не тот язык — проверь ⋮ → Настройки → Субтитры"
	}
	return msg
}
