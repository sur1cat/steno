package audio

import (
	"sort"
	"strings"
	"unicode"

	"github.com/sur1cat/steno/internal/core"
)

// Слияние двух проходов распознавания.
//
// Словарь созвона (vocab.go) уходит whisper подсказкой, и подсказка делает две
// вещи сразу, одну хорошую и одну плохую. Хорошая: она чинит имена и названия
// («Анвару» → «Ануар», «сапар» → «Сапар») и возвращает слова, которые без неё
// пропали («релиз», «послезавтра», «SDK»). Плохая: whisper продолжает подсказку
// как текст, и от этого едет разбивка на реплики — на записи созвона
// тринадцать реплик слипались в две, а от границ реплик зависит, кому
// AlignSpeakers припишет имя говорящего.
//
// Разрешается это двумя проходами. Первый — без подсказки: от него берутся
// реплики, их границы, таймкоды и уверенность по словам. Второй — с подсказкой:
// от него берутся только слова, и только там, где первый проход в своих словах
// не уверен. Слова двух проходов выравниваются по тексту, и каждое расхождение
// решается по уверенности первого прохода:
//
//   - заменить или удалить слова первого прохода можно, только если ВСЕ
//     затронутые слова ниже mergeBelow. Одно сомнительное слово не тянет за
//     собой уверенных соседей: первая версия без этого правила удалила две
//     верные реплики, потому что whisper со словарём переписал их иначе, а
//     одно слово в них было ниже порога;
//   - вставить слова второго прохода можно, если сосед по первому проходу ниже
//     mergeBelow — или если реплика первого прохода кончилась неуверенно (см.
//     endedEarlyBelow): так вернулось «SDK», которое первый проход просто не
//     дописал в конце фразы;
//   - слово с той же основой, но другой формой («сапар,» и «Сапар.») берётся у
//     второго прохода, если первый в нём не уверен.
//
// Измерено на 63 прогонах whisper по двум настоящим записям: пять искажений из
// шести починены, тринадцать границ из тринадцати сохранены, ни одно уверенное
// слово не тронуто. Тест с записанными выходами whisper (twopass_test.go)
// держит ровно этот результат.

// mergeBelow — ниже этого слово первого прохода можно заменить словом второго.
//
// Это отдельная ручка от core.UncertainBelow (0.5), которая помечает слова для
// модели, и число другое не случайно: обрывок «после» из «послезавтра» имеет
// 0.524. При 0.5 «послезавтра» не чинится, при 0.6 лишних замен на тех же
// записях не прибавилось. Пометка для модели и право на замену — разные
// вопросы: пометка стоит одного лишнего «(?)», а замена берёт слово из
// прохода, где whisper видел словарь, — и цена ошибки у них разная.
const mergeBelow = 0.6

// endedEarlyBelow — ниже этого уверенность движка в конце реплики означает
// «реплика оборвана, не договорив», и после её последнего слова можно вставить
// то, что дописал второй проход.
//
// Порог намного ниже mergeBelow, и это измерено: у whisper закрывающий токен
// таймкода почти всегда неуверенный — на записи из тринадцати реплик у
// одиннадцати он ниже 0.5. Порог 0.5 разрешил бы вставки почти в каждой
// реплике. А там, где пропало «SDK», он был 0.034.
const endedEarlyBelow = 0.1

// pass — один проход адаптера: реплики и то, что нужно только для слияния и
// потому не живёт в core.Segment.
type pass struct {
	segs []core.Segment
	// endConf — по реплике: уверенность движка, что реплика на этом кончилась.
	// nil — движок не сказал.
	endConf []*float64
	// vocabulary — как адаптер применил словарь: "prompt" или "keyterms".
	// Пусто — не сказал, считается "prompt".
	vocabulary string
	notes      string
}

// mergeWord — слово одного из проходов с тем, что нужно для слияния.
type mergeWord struct {
	w    core.Word
	seg  int  // реплика первого прохода, откуда слово
	last bool // последнее слово своей реплики
}

func flatten(p pass) []mergeWord {
	var out []mergeWord
	for i, s := range p.segs {
		for k, w := range s.Words {
			out = append(out, mergeWord{w: w, seg: i, last: k == len(s.Words)-1})
		}
	}
	return out
}

// normWord — основа для сравнения слов двух проходов: без регистра и без
// знаков препинания. «Сапар.» и «сапар,» — одно слово; разница между ними
// решается отдельно, как форма, а не как замена.
func normWord(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// mergePasses — реплики первого прохода со словами, взятыми у второго там, где
// первый не уверен. Возвращает ещё и число взятых слов: человек должен видеть в
// журнале, что второй проход дал.
//
// Всё, что не слова — границы, таймкоды, имя говорящего, уверенность реплики,
// — остаётся от первого прохода. Текст реплики пересобирается из слов только
// там, где слова менялись: у нетронутой реплики он остаётся байт в байт.
func mergePasses(plain, hinted pass) ([]core.Segment, int) {
	aw, bw := flatten(plain), flatten(hinted)
	if len(aw) == 0 || len(bw) == 0 {
		return plain.segs, 0
	}
	an := make([]string, len(aw))
	for i, w := range aw {
		an[i] = normWord(w.w.Word)
	}
	bn := make([]string, len(bw))
	for i, w := range bw {
		bn[i] = normWord(w.w.Word)
	}

	unsure := func(w mergeWord) bool { return w.w.Conf != nil && *w.w.Conf < mergeBelow }
	// canReplace — заменить или удалить aw[i1:i2] можно, только если ВСЕ
	// затронутые слова неуверенные.
	canReplace := func(i1, i2 int) bool {
		for _, w := range aw[i1:i2] {
			if !unsure(w) {
				return false
			}
		}
		return true
	}
	// canInsert — вставить перед aw[i] (или в самый конец, если i == len(aw)).
	canInsert := func(i int) bool {
		for _, j := range []int{i - 1, i} {
			if j >= 0 && j < len(aw) && unsure(aw[j]) {
				return true
			}
		}
		if i-1 >= 0 && aw[i-1].last {
			if ec := plain.endConf[aw[i-1].seg]; ec != nil && *ec < endedEarlyBelow {
				return true
			}
		}
		return false
	}
	// segmentFor — в какую реплику первого прохода кладутся слова второго.
	segmentFor := func(i1, i2 int) int {
		switch {
		case i1 < len(aw) && i2 > i1:
			return aw[i1].seg
		case i1-1 >= 0:
			return aw[i1-1].seg
		case i1 < len(aw):
			return aw[i1].seg
		}
		return 0
	}

	words := make([][]core.Word, len(plain.segs))
	changed := make([]bool, len(plain.segs))
	taken := 0
	for _, op := range alignWords(an, bn) {
		if op.tag == opEqual {
			for k := 0; k < op.i2-op.i1; k++ {
				wa, wb := aw[op.i1+k], bw[op.j1+k]
				if wa.w.Word != wb.w.Word && unsure(wa) {
					// Та же основа, другая форма: регистр, знак препинания.
					words[wa.seg] = append(words[wa.seg], wb.w)
					changed[wa.seg] = true
					taken++
					continue
				}
				words[wa.seg] = append(words[wa.seg], wa.w)
			}
			continue
		}
		var ok bool
		if op.i2 > op.i1 {
			ok = canReplace(op.i1, op.i2)
		} else {
			ok = canInsert(op.i1)
		}
		if !ok {
			for _, w := range aw[op.i1:op.i2] {
				words[w.seg] = append(words[w.seg], w.w)
			}
			continue
		}
		seg := segmentFor(op.i1, op.i2)
		for _, w := range aw[op.i1:op.i2] {
			changed[w.seg] = true
		}
		for _, w := range bw[op.j1:op.j2] {
			words[seg] = append(words[seg], w.w)
			changed[seg] = true
			taken++
		}
	}

	out := make([]core.Segment, 0, len(plain.segs))
	for i, s := range plain.segs {
		if changed[i] {
			s.Words = words[i]
			s.Text = joinWords(words[i])
			if s.Text == "" {
				// Все слова реплики ушли — реплики больше нет.
				continue
			}
		}
		out = append(out, s)
	}
	return out, taken
}

func joinWords(ws []core.Word) string {
	parts := make([]string, 0, len(ws))
	for _, w := range ws {
		parts = append(parts, w.Word)
	}
	return strings.Join(parts, " ")
}

// --- выравнивание -----------------------------------------------------------

// Выравнивание двух последовательностей слов — перенос difflib.SequenceMatcher
// из стандартной библиотеки Python (autojunk=False, без «мусорных» элементов),
// на котором слияние и мерилось. Алгоритм: найти самый длинный общий кусок —
// при равной длине тот, что раньше в первой последовательности, а при равном
// начале раньше во второй, — и рекурсивно повторить слева и справа от него.
// Это не самая короткая правка (как у Майерса), зато самая «человеческая»:
// длинные совпадающие куски держатся вместе, а расхождения выходят
// компактными, — и, главное, ровно та, на которой получен измеренный результат.

type opTag int

const (
	opEqual opTag = iota
	opReplace
	opDelete
	opInsert
)

// op — одно расхождение: a[i1:i2] против b[j1:j2].
type op struct {
	tag            opTag
	i1, i2, j1, j2 int
}

type matchBlock struct{ i, j, k int }

func alignWords(a, b []string) []op {
	b2j := map[string][]int{}
	for j, s := range b {
		b2j[s] = append(b2j[s], j)
	}
	longest := func(alo, ahi, blo, bhi int) matchBlock {
		best := matchBlock{alo, blo, 0}
		j2len := map[int]int{}
		for i := alo; i < ahi; i++ {
			next := map[int]int{}
			for _, j := range b2j[a[i]] {
				if j < blo {
					continue
				}
				if j >= bhi {
					break
				}
				k := j2len[j-1] + 1
				next[j] = k
				if k > best.k {
					best = matchBlock{i - k + 1, j - k + 1, k}
				}
			}
			j2len = next
		}
		return best
	}

	var blocks []matchBlock
	queue := [][4]int{{0, len(a), 0, len(b)}}
	for len(queue) > 0 {
		q := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		alo, ahi, blo, bhi := q[0], q[1], q[2], q[3]
		m := longest(alo, ahi, blo, bhi)
		if m.k == 0 {
			continue
		}
		blocks = append(blocks, m)
		if alo < m.i && blo < m.j {
			queue = append(queue, [4]int{alo, m.i, blo, m.j})
		}
		if m.i+m.k < ahi && m.j+m.k < bhi {
			queue = append(queue, [4]int{m.i + m.k, ahi, m.j + m.k, bhi})
		}
	}
	sort.Slice(blocks, func(x, y int) bool {
		if blocks[x].i != blocks[y].i {
			return blocks[x].i < blocks[y].i
		}
		return blocks[x].j < blocks[y].j
	})
	// Соседние куски склеиваются в один.
	var joined []matchBlock
	cur := matchBlock{}
	for _, m := range blocks {
		if cur.i+cur.k == m.i && cur.j+cur.k == m.j {
			cur.k += m.k
			continue
		}
		if cur.k > 0 {
			joined = append(joined, cur)
		}
		cur = m
	}
	if cur.k > 0 {
		joined = append(joined, cur)
	}
	joined = append(joined, matchBlock{len(a), len(b), 0}) // страж

	var ops []op
	i, j := 0, 0
	for _, m := range joined {
		switch {
		case i < m.i && j < m.j:
			ops = append(ops, op{opReplace, i, m.i, j, m.j})
		case i < m.i:
			ops = append(ops, op{opDelete, i, m.i, j, m.j})
		case j < m.j:
			ops = append(ops, op{opInsert, i, m.i, j, m.j})
		}
		i, j = m.i+m.k, m.j+m.k
		if m.k > 0 {
			ops = append(ops, op{opEqual, m.i, i, m.j, j})
		}
	}
	return ops
}
