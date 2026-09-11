package core

import "testing"

func conf(v float64) *float64 { return &v }

// Знак пометки записан ещё в одном месте — словами, в правиле 9 системного
// промпта (internal/brain/followup.go): модели объясняют, что «(?)» значит.
// Промпт — константа, подставить в неё UncertainMark нечем, и разъехаться они
// могут молча: расшифровка придёт с новым знаком, правило будет объяснять
// старый, и модель примет пометку за часть речи. Меняешь знак здесь — правь
// правило там.
func TestUncertainMarkMatchesThePrompt(t *testing.T) {
	if UncertainMark != "(?)" {
		t.Fatalf("знак пометки стал %q — правило 9 в internal/brain/followup.go "+
			"всё ещё объясняет «(?)»", UncertainMark)
	}
}

// Главное свойство всего договора об уверенности: «не знаю» и «ноль» — разные
// вещи. Адаптер, написанный до всей этой истории, не пишет confidence вовсе, и
// принять его молчание за ноль значило бы объявить каждое слово хорошей
// расшифровки сомнительным.
func TestUnknownIsNotZero(t *testing.T) {
	if Uncertain(nil) {
		t.Error("отсутствие уверенности принято за нулевую")
	}
	if !Uncertain(conf(0)) {
		t.Error("нулевая уверенность не считается низкой")
	}
	if Uncertain(conf(UncertainBelow)) {
		t.Error("ровно порог — это ещё не «плохо расслышано»")
	}
	if !Uncertain(conf(UncertainBelow - 0.001)) {
		t.Error("чуть ниже порога должно считаться низким")
	}

	s := Segment{Text: "Ануару нужно закончить Сапар",
		Words: []Word{{Word: "Ануару"}, {Word: "нужно"}}}
	if got := MarkUncertain(s); got != s.Text {
		t.Errorf("слова без уверенности получили пометку: %q", got)
	}
}

// Пометка встаёт рядом со словом, а сам текст не меняется ни на символ:
// пунктуация, пробелы и порядок слов остаются теми, что пришли от движка.
func TestMarkUncertainMarksTheWord(t *testing.T) {
	s := Segment{
		Text: "чтобы участники нормальные и пешные могли участвовать.",
		Words: []Word{
			{Word: "чтобы", Conf: conf(1)},
			{Word: "участники", Conf: conf(0.998)},
			{Word: "нормальные", Conf: conf(0.297)},
			{Word: "и", Conf: conf(0.503)},
			{Word: "пешные", Conf: conf(0.84)},
			{Word: "могли", Conf: conf(0.782)},
			{Word: "участвовать.", Conf: conf(1)},
		},
	}
	want := "чтобы участники нормальные" + UncertainMark + " и пешные могли участвовать."
	if got := MarkUncertain(s); got != want {
		t.Errorf("получили %q, ожидали %q", got, want)
	}
}

// Одно и то же слово в реплике встречается дважды, а плохо расслышано только
// второе. Ищем по порядку, а не первое попавшееся: иначе пометка садится на
// верное слово, а неверное остаётся чистым — то есть врёт дважды.
func TestMarkUncertainPicksTheRightRepeat(t *testing.T) {
	s := Segment{
		Text: "проверка проверка",
		Words: []Word{
			{Word: "проверка", Conf: conf(0.9)},
			{Word: "проверка", Conf: conf(0.1)},
		},
	}
	want := "проверка проверка" + UncertainMark
	if got := MarkUncertain(s); got != want {
		t.Errorf("получили %q, ожидали %q", got, want)
	}
}

// Разбивка на слова у движка своя, и она не обязана сходиться с текстом
// реплики. Слово, которого в тексте нет, пропускается молча: текст дороже
// пометки, и терять из-за неё реплику нельзя.
func TestMarkUncertainKeepsTextWhenWordsDoNotFit(t *testing.T) {
	s := Segment{
		Text: "давайте начнём с релиза",
		Words: []Word{
			{Word: "совсем", Conf: conf(0.01)},
			{Word: "другое", Conf: conf(0.01)},
		},
	}
	if got := MarkUncertain(s); got != s.Text {
		t.Errorf("текст пострадал от чужих слов: %q", got)
	}
}

// Движок, у которого уверенность есть только на реплику целиком (так отвечают
// движки с OpenAI-совместимым API), тоже должен быть услышан.
func TestMarkUncertainMarksWholeReply(t *testing.T) {
	s := Segment{Text: "и после нужно сделать", Conf: conf(0.2)}
	if got, want := MarkUncertain(s), "и после нужно сделать"+UncertainMark; got != want {
		t.Errorf("получили %q, ожидали %q", got, want)
	}
	// Слова есть — они точнее, и пометка вешается на них, а не на всю реплику.
	s.Words = []Word{{Word: "и", Conf: conf(0.9)}, {Word: "после", Conf: conf(0.9)},
		{Word: "нужно", Conf: conf(0.9)}, {Word: "сделать", Conf: conf(0.9)}}
	if got := MarkUncertain(s); got != "и после нужно сделать" {
		t.Errorf("низкая уверенность реплики перебила уверенные слова: %q", got)
	}
}

// Счётчик нужен, чтобы человек видел в логе, работает ли уверенность вообще.
// Движок без неё даёт 0 из 0, и это сразу отличается от «всё расслышано
// хорошо» — то есть от 0 из 300.
func TestCountUncertain(t *testing.T) {
	segs := []Segment{
		{Text: "а", Words: []Word{{Word: "Анвару", Conf: conf(0.334)}, {Word: "нужно", Conf: conf(0.863)}}},
		{Text: "б", Words: []Word{{Word: "сапар", Conf: conf(0.276)}, {Word: "  ", Conf: conf(0)}}},
		{Text: "в", Words: []Word{{Word: "релиз"}}}, // движок не сказал — но слово есть
	}
	marked, total := CountUncertain(segs)
	if marked != 2 || total != 4 {
		t.Errorf("насчитали %d из %d, ожидали 2 из 4", marked, total)
	}
	if m, n := CountUncertain([]Segment{{Text: "без слов"}}); m != 0 || n != 0 {
		t.Errorf("движок без уверенности дал %d из %d, ожидали 0 из 0", m, n)
	}
}
