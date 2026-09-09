package main

import "testing"

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
