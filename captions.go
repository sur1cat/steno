package main

// Сшивка строк субтитров в реплики.
//
// Meet держит в области субтитров несколько последних строк и переписывает
// каждую по мере того, как распознавание уточняется. Состояния в странице нет,
// поэтому реплики собираем здесь: строка, исчезнувшая из области, считается
// договорённой.

import (
	"strings"
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
	// emitted — сколько рун текста уже ушло в файл.
	emitted int
	// changed — когда текст последний раз менялся.
	changed float64
}

// idleFinalize — сколько ждать без изменений, прежде чем отдать накопленное.
// Пауза в речи короче секунды бывает и в середине фразы; четыре секунды — уже
// смена мысли.
const idleFinalize = 4.0

func (l *liveUtt) pending() string {
	r := []rune(l.text)
	if l.emitted >= len(r) {
		return ""
	}
	return strings.TrimSpace(string(r[l.emitted:]))
}

// emit отдаёт накопленное и запоминает, докуда отдано.
func (l *liveUtt) emit(at float64) (Utterance, bool) {
	txt := l.pending()
	if txt == "" {
		return Utterance{}, false
	}
	u := Utterance{Speaker: l.speaker, Text: txt, Start: l.segStart, End: at}
	l.emitted = len([]rune(l.text))
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
			if e, ok := u.emit(at); ok {
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
			// минутами, а падение бота унесло бы всё несохранённое.
			if e, ok := u.emit(at); ok {
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
		if e, ok := u.emit(at); ok {
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
