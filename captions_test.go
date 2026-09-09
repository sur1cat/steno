package main

import (
	"strings"
	"testing"
	"time"
)

func lines(pairs ...string) []CaptionLine {
	var out []CaptionLine
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, CaptionLine{Speaker: pairs[i], Text: pairs[i+1]})
	}
	return out
}

func TestGrowingLineIsOneUtterance(t *testing.T) {
	var tr CaptionTracker
	if got := tr.Update(lines("Участник А", "давайте"), 1); len(got) != 0 {
		t.Fatalf("рано закрыли реплику: %v", got)
	}
	if got := tr.Update(lines("Участник А", "давайте начнём с"), 2); len(got) != 0 {
		t.Fatalf("рано закрыли реплику: %v", got)
	}
	done := tr.Update(lines("Участник Б", "секунду"), 3)
	if len(done) != 1 || done[0].Text != "давайте начнём с" || done[0].Speaker != "Участник А" {
		t.Fatalf("ожидали одну реплику Ани целиком, получили %+v", done)
	}
	if done[0].Start != 1 || done[0].End != 3 {
		t.Fatalf("границы реплики %v..%v, ожидали 1..3", done[0].Start, done[0].End)
	}
}

// Meet переписывает строку целиком, когда распознавание уточняется. Общего
// префикса при этом может почти не быть — реплика всё равно та же.
func TestRewriteKeepsSameUtterance(t *testing.T) {
	var tr CaptionTracker
	tr.Update(lines("Участник А", "привет всем"), 1)
	if got := tr.Update(lines("Участник А", "Привет, всем коллегам"), 2); len(got) != 0 {
		t.Fatalf("переписанную строку приняли за новую реплику: %v", got)
	}
	done := tr.Flush(3)
	if len(done) != 1 || done[0].Text != "Привет, всем коллегам" {
		t.Fatalf("получили %+v", done)
	}
}

// Две подряд идущие реплики одного человека видны одновременно — это разные
// реплики, а не одна.
func TestTwoVisibleLinesFromSameSpeaker(t *testing.T) {
	var tr CaptionTracker
	tr.Update(lines("Участник А", "первая мысль про сроки"), 1)
	tr.Update(lines("Участник А", "первая мысль про сроки", "Участник А", "и ещё одно"), 2)
	done := tr.Update(lines("Участник А", "и ещё одно замечание"), 3)
	if len(done) != 1 || done[0].Text != "первая мысль про сроки" {
		t.Fatalf("ожидали закрытие первой реплики, получили %+v", done)
	}
	rest := tr.Flush(4)
	if len(rest) != 1 || rest[0].Text != "и ещё одно замечание" {
		t.Fatalf("ожидали вторую реплику, получили %+v", rest)
	}
}

func TestFlushEmpty(t *testing.T) {
	var tr CaptionTracker
	if got := tr.Flush(1); len(got) != 0 {
		t.Fatalf("ожидали пусто, получили %v", got)
	}
}

// Meet рисует строку субтитров с именем говорящего раньше, чем распознавание
// выдаёт слова. Такая пустая строка не должна уносить с собой уже накопленную
// реплику: без неё имя пропадёт из транскрипта, и alignSpeakers припишет эти
// слова соседу — с дословной цитатой, будто всё проверено.
func TestEmptyLineDoesNotDestroyUtterance(t *testing.T) {
	var tr CaptionTracker
	tr.Update(lines("Участник А", "важное решение по релизу"), 1)
	if got := tr.Update(lines("Участник А", ""), 2); len(got) != 0 {
		t.Fatalf("пустая строка закрыла реплику: %+v", got)
	}
	tr.Update(lines("Участник А", "важное решение по релизу в пятницу"), 3)

	done := tr.Flush(4)
	if len(done) != 1 {
		t.Fatalf("ожидали одну реплику, получили %+v", done)
	}
	if done[0].Text != "важное решение по релизу в пятницу" {
		t.Fatalf("текст реплики: %q", done[0].Text)
	}
	if done[0].Speaker != "Участник А" {
		t.Fatalf("имя потерялось: %q", done[0].Speaker)
	}
}

// Новый говорящий, у которого ещё нет слов, не должен создавать пустую реплику.
func TestEmptyLineFromNewSpeakerIsIgnored(t *testing.T) {
	var tr CaptionTracker
	tr.Update(lines("Участник Б", ""), 1)
	if got := tr.Flush(2); len(got) != 0 {
		t.Fatalf("создали реплику из пустоты: %+v", got)
	}
}

// Meet может убрать законченную строку и нарисовать следующую реплику того же
// человека за один такт опроса. Это две разные реплики: склеив их, мы теряем
// первую целиком, а выжившая забирает себе её время — и вместе с ним чужие
// сегменты whisper при выравнивании.
func TestConsecutiveUtterancesFromSameSpeakerDoNotMerge(t *testing.T) {
	var tr CaptionTracker
	tr.Update(lines("Участник А", "хорошо, договорились по срокам"), 10)
	done := tr.Update(lines("Участник А", "теперь давайте про бюджет"), 11.5)
	if len(done) != 1 || done[0].Text != "хорошо, договорились по срокам" {
		t.Fatalf("первая реплика не закрылась: %+v", done)
	}
	if done[0].Start != 10 || done[0].End != 11.5 {
		t.Errorf("границы первой реплики %v..%v", done[0].Start, done[0].End)
	}
	rest := tr.Flush(20)
	if len(rest) != 1 || rest[0].Text != "теперь давайте про бюджет" {
		t.Fatalf("вторая реплика потерялась: %+v", rest)
	}
	// Главное: вторая началась там, где появилась, а не там, где первая.
	if rest[0].Start != 11.5 {
		t.Errorf("вторая реплика присвоила себе время первой: start=%v", rest[0].Start)
	}
}

func TestSameUtterance(t *testing.T) {
	same := [][2]string{
		{"привет всем", "Привет, всем коллегам"},
		{"давайте", "давайте начнём с релиза"},
		{"", "первые слова"},
	}
	for _, c := range same {
		if !sameUtterance(c[0], c[1]) {
			t.Errorf("уточнение приняли за новую реплику: %q → %q", c[0], c[1])
		}
	}
	other := [][2]string{
		{"хорошо, договорились по срокам", "теперь давайте про бюджет"},
		{"да, конечно", "но есть нюанс"},
		{"первое", "второе"},
	}
	for _, c := range other {
		if sameUtterance(c[0], c[1]) {
			t.Errorf("две разные реплики склеили: %q → %q", c[0], c[1])
		}
	}
}

// При непрерывном монологе Meet дописывает одну и ту же строку минутами, и она
// не исчезает из области. Ждать её конца — значит не записать ни слова до
// самого выхода из звонка, а при падении бота потерять всё.
func TestLongMonologueIsWrittenInPieces(t *testing.T) {
	var tr CaptionTracker
	tr.Update(lines("Участник А", "тени высохли на пол"), 10)
	// Речь идёт — текст растёт, отдавать пока нечего.
	if got := tr.Update(lines("Участник А", "тени высохли на пол и вся в них"), 12); len(got) != 0 {
		t.Fatalf("отдали кусок посреди фразы: %+v", got)
	}
	// Пауза: тот же текст держится дольше idleFinalize.
	if got := tr.Update(lines("Участник А", "тени высохли на пол и вся в них"), 17); len(got) != 1 {
		t.Fatalf("после паузы кусок не отдан: %+v", got)
	} else if got[0].Text != "тени высохли на пол и вся в них" {
		t.Fatalf("отдали %q", got[0].Text)
	}

	// Человек продолжил ту же строку — отдать надо только новое, без повтора.
	tr.Update(lines("Участник А", "тени высохли на пол и вся в них вечное если знаешь"), 20)
	done := tr.Update(lines("Участник А", "тени высохли на пол и вся в них вечное если знаешь"), 25)
	if len(done) != 1 {
		t.Fatalf("продолжение не отдано: %+v", done)
	}
	if done[0].Text != "вечное если знаешь" {
		t.Fatalf("продолжение отдано с повтором: %q", done[0].Text)
	}
	if done[0].Start != 17 {
		t.Errorf("продолжение начинается с %v, ожидали 17 — конец прошлого куска", done[0].Start)
	}
	// Ничего не осталось: всё уже отдано.
	if rest := tr.Flush(30); len(rest) != 0 {
		t.Fatalf("Flush отдал дубль: %+v", rest)
	}
}

// Пауза короче порога — ещё не конец фразы.
func TestShortPauseDoesNotSplit(t *testing.T) {
	var tr CaptionTracker
	tr.Update(lines("Участник А", "давайте начнём"), 10)
	if got := tr.Update(lines("Участник А", "давайте начнём"), 12); len(got) != 0 {
		t.Fatalf("разрезали фразу на двухсекундной паузе: %+v", got)
	}
}

// --- полнота съёма и границы реплик -----------------------------------------

// Опрос реже, чем Meet переписывает строку, текста не теряет. Проверять это
// надо явно: по файлу живого созвона казалось, что мы ловим обрывки, — а на
// деле отданное всегда продолжается с того места, где кончилось прошлое.
// Если это когда-нибудь перестанет быть правдой, тест поймает.
func TestSlowPollingLosesNoText(t *testing.T) {
	full := "давайте начнём с релиза потом обсудим адаптеры и бюджет " +
		"и решим кто берёт на себя миграцию к четвергу"
	words := strings.Fields(full)

	var tr CaptionTracker
	var got []string
	at := 1.0
	// Строка растёт скачками по пять слов — как будто между опросами Meet
	// успел дописать много.
	for i := 5; i <= len(words); i += 5 {
		text := strings.Join(words[:i], " ")
		for _, u := range tr.Update(lines("Участник А", text), at) {
			got = append(got, u.Text)
		}
		at += 1.5
	}
	// Договорил и замолчал: строка стоит на месте дольше idleFinalize.
	text := strings.Join(words, " ")
	for i := 0; i < 5; i++ {
		for _, u := range tr.Update(lines("Участник А", text), at) {
			got = append(got, u.Text)
		}
		at += 1.5
	}
	for _, u := range tr.Flush(at) {
		got = append(got, u.Text)
	}

	joined := strings.Join(strings.Fields(strings.Join(got, " ")), " ")
	if joined != full {
		t.Fatalf("текст изменился при сшивке:\nждали: %q\nвышло: %q", full, joined)
	}
}

// Строку укоротили, а начало осталось прежним — значит она осталась той же
// репликой. Прежний счётчик рун указывал за конец нового текста и молча
// проглатывал его целиком: слова были на экране и не попали никуда.
func TestShorterLineDoesNotSwallowText(t *testing.T) {
	var tr CaptionTracker
	tr.Update(lines("Участник А", "давайте обсудим сроки релиза и бюджет"), 10)
	if got := tr.Update(lines("Участник А", "давайте обсудим сроки релиза и бюджет"), 15); len(got) != 1 {
		t.Fatalf("реплика не закрылась по тишине: %+v", got)
	}
	// Meet переписал строку короче — но с того же начала.
	tr.Update(lines("Участник А", "давайте по бюджету"), 16)
	done := tr.Update(lines("Участник А", "давайте по бюджету"), 21)
	if len(done) != 1 {
		t.Fatalf("новый текст пропал целиком: %+v", done)
	}
	if done[0].Text != "по бюджету" {
		t.Fatalf("отдали %q, ждали «по бюджету»", done[0].Text)
	}
}

// Meet переписывает строку целиком, уточняя распознавание, и длина при этом
// меняется. Прежний счётчик рун резал новый текст по позиции из старого — и
// в файл уходил обрубок с середины слова.
func TestRewrittenLineIsNotCutMidWord(t *testing.T) {
	var tr CaptionTracker
	tr.Update(lines("Участник А", "привет всем"), 10)
	if got := tr.Update(lines("Участник А", "привет всем"), 15); len(got) != 1 {
		t.Fatalf("реплика не закрылась: %+v", got)
	}
	tr.Update(lines("Участник А", "Привет, всем коллегам, начнём"), 16)
	done := tr.Update(lines("Участник А", "Привет, всем коллегам, начнём"), 21)
	if len(done) != 1 {
		t.Fatalf("уточнённая строка не отдана: %+v", done)
	}
	if !strings.Contains(done[0].Text, "коллегам") {
		t.Fatalf("слово разрезано пополам: %q", done[0].Text)
	}
	if strings.HasPrefix(done[0].Text, "м ") {
		t.Fatalf("текст отдан с середины слова: %q", done[0].Text)
	}
}

// Реплика закрывается временем, когда текст перестал меняться, а не временем,
// когда мы заметили тишину. Разница — четыре секунды idleFinalize, и она
// решает, кому alignSpeakers отдаст следующие слова.
//
// На живом созвоне в субтитрах был только один человек: второй говорил, но
// Meet его не расшифровал. Раздутый отрезок первого дотягивался до чужих слов,
// и follow-up поставил все задачи на него — включая те, что он раздавал другим.
func TestIdleUtteranceDoesNotStealNextSpeaker(t *testing.T) {
	var tr CaptionTracker
	var utts []Utterance
	utts = append(utts, tr.Update(lines("Рустем", "Даурен, возьми на себя адаптеры"), 10)...)
	utts = append(utts, tr.Update(lines("Рустем", "Даурен, возьми на себя адаптеры"), 12)...)
	utts = append(utts, tr.Update(lines("Рустем", "Даурен, возьми на себя адаптеры"), 15)...)
	utts = append(utts, tr.Flush(16)...)

	if len(utts) != 1 {
		t.Fatalf("ожидали одну реплику, получили %+v", utts)
	}
	if utts[0].End != 10 {
		t.Fatalf("реплика закрыта на %v, а текст перестал меняться на 10", utts[0].End)
	}

	// Ответ второго человека, которого в субтитрах нет вовсе.
	answer := []Segment{{Start: 14, End: 17, Text: "да, к четвергу успею"}}
	if got := alignSpeakers(answer, utts)[0].Speaker; got != "" {
		t.Errorf("чужие слова подписали именем %q — лучше без имени, чем с чужим", got)
	}
	// А своё за ним по-прежнему числится.
	own := []Segment{{Start: 8, End: 10, Text: "Даурен, возьми на себя адаптеры"}}
	if got := alignSpeakers(own, utts)[0].Speaker; got != "Рустем" {
		t.Errorf("своя реплика осталась без имени: %q", got)
	}
}

// --- сколько текста дали субтитры -------------------------------------------

func TestCaptionYieldReport(t *testing.T) {
	// Живой созвон: 232 секунды русской речи, а в субтитрах три обрывка
	// английских слов. Про это надо сказать словами, иначе человек увидит
	// поломку только в follow-up с чужими исполнителями.
	var live CaptionYield
	for _, s := range []string{"But. Dorian.", "Show. Release.", "He."} {
		live.Add(Utterance{Text: s})
	}
	if got := live.Script(); got != "латиница" {
		t.Fatalf("письменность определена как %q", got)
	}
	r := live.Report(232*time.Second, "ru")
	for _, want := range []string{"не тот язык", "Настройки", "ru"} {
		if !strings.Contains(r, want) {
			t.Errorf("в отчёте нет %q: %s", want, r)
		}
	}

	// Нормальный созвон по-русски: жаловаться не на что.
	var ok CaptionYield
	for i := 0; i < 20; i++ {
		ok.Add(Utterance{Text: "давайте обсудим сроки релиза и бюджет проекта"})
	}
	r = ok.Report(time.Minute, "ru")
	if strings.Contains(r, "не тот язык") || strings.Contains(r, "в разы меньше") {
		t.Errorf("пожаловались на здоровые субтитры: %s", r)
	}

	// Язык не задан — про чужой язык сказать нечего, но про пустоту сказать
	// надо: молчание здесь неотличимо от рабочей записи.
	var few CaptionYield
	few.Add(Utterance{Text: "ага"})
	r = few.Report(10*time.Minute, "")
	if !strings.Contains(r, "в разы меньше") {
		t.Errorf("не сказали про пустые субтитры: %s", r)
	}

	// Порог должен что-то отделять: проверяем обе его стороны, иначе он мог бы
	// стоять где угодно, и тест бы этого не заметил.
	letters := func(n int) CaptionYield {
		var y CaptionYield
		y.Add(Utterance{Text: strings.Repeat("аб", n/2)})
		return y
	}
	if r := letters(sparseCaptions-40).Report(time.Minute, "ru"); !strings.Contains(r, "в разы меньше") {
		t.Errorf("ниже порога, а жалобы нет: %s", r)
	}
	if r := letters(sparseCaptions+40).Report(time.Minute, "ru"); strings.Contains(r, "в разы меньше") {
		t.Errorf("выше порога, а жалоба есть: %s", r)
	}
}
