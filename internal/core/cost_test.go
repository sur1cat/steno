package core

import (
	"strings"
	"testing"
	"time"
)

// Расход дописывается в строку follow-up, а не заводит свою. Значит порядок
// вызовов — единственное, что отделяет честный учёт от нуля: UPDATE без строки
// SQL считает успехом, и $0.09 за созвон уходили в никуда. Ловится это только
// на первом follow-up: на повторном строка уже есть, и всё сходится.
func TestSpendIsLostIfSavedBeforeFollowup(t *testing.T) {
	st, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	started := time.Now().Add(-time.Hour)
	if err := st.CreateMeeting(&Meeting{
		ID: "m1", Title: "созвон", MeetURL: "https://meet.google.com/x",
		StartedAt: started,
	}); err != nil {
		t.Fatal(err)
	}

	sp := Spend{Model: "claude-sonnet-5", Input: 6, Output: 2982,
		CacheRead: 50385, CacheWrite: 11641, USD: 0.09, PriceKnown: true}

	// Неправильный порядок обязан быть слышным.
	if err := st.SaveSpend("m1", sp); err == nil {
		t.Fatal("SaveSpend до SaveFollowup молча проглотил расход")
	}

	f := &Followup{Title: "созвон", TLDR: []string{"коротко"}}
	if err := st.SaveFollowup("m1", "claude-sonnet-5", f); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveSpend("m1", sp); err != nil {
		t.Fatal(err)
	}

	usd, in, out, n, unpriced, err := st.TotalSpend(started.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || usd != 0.09 || in != 6 || out != 2982 {
		t.Fatalf("учёт потерял расход: %d follow-up, $%.3f, вход %d, выход %d", n, usd, in, out)
	}
	if unpriced != 0 {
		t.Errorf("расход с известной ценой посчитан неизвестным: %d", unpriced)
	}
}

// Ноль в деньгах бывает двух видов, и путать их дороже всего: у модели на
// своей машине это правда, а у провайдера, цены которого steno не знает, — это
// незнание, и счёт всё равно придёт. Пока разница жила только в памяти,
// `steno cost` показывал «$0.00» в обоих случаях, то есть врал ровно в том
// числе, ради которого его завели.
func TestUnknownPriceSurvivesTheDatabase(t *testing.T) {
	st, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	started := time.Now().Add(-time.Hour)
	for _, id := range []string{"m1", "m2", "m3"} {
		if err := st.CreateMeeting(&Meeting{ID: id, Title: id,
			MeetURL: "https://meet.google.com/x", StartedAt: started}); err != nil {
			t.Fatal(err)
		}
		if err := st.SaveFollowup(id, "gpt-5", &Followup{Title: id}); err != nil {
			t.Fatal(err)
		}
	}
	// Цену одного знаем, другого — нет.
	if err := st.SaveSpend("m1", Spend{Model: "gpt-5", Input: 100, Output: 10,
		USD: 0.5, PriceKnown: true}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveSpend("m2", Spend{Model: "gpt-5", Input: 100, Output: 10}); err != nil {
		t.Fatal(err)
	}
	// А у третьего цена известна и равна нулю: так считает модель на этой же
	// машине. Это тот самый случай, в котором «ноль значит не знаем» и было бы
	// враньём в обратную сторону.
	if err := st.SaveSpend("m3", Spend{Model: "llama3.1", Input: 100, Output: 10,
		USD: 0, PriceKnown: true}); err != nil {
		t.Fatal(err)
	}

	got, err := st.Spend("m2")
	if err != nil {
		t.Fatal(err)
	}
	if got.PriceKnown {
		t.Error("незнание цены не пережило запись в базу")
	}
	if known, err := st.Spend("m1"); err != nil || !known.PriceKnown {
		t.Errorf("известная цена перестала быть известной: %+v %v", known, err)
	}
	if free, err := st.Spend("m3"); err != nil || !free.PriceKnown || free.USD != 0 {
		t.Errorf("известный ноль превратился в незнание: %+v %v", free, err)
	}

	usd, _, _, n, unpriced, err := st.TotalSpend(started.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 || unpriced != 1 {
		t.Errorf("follow-up %d, без цены %d — ждали 3 и 1", n, unpriced)
	}
	if usd != 0.5 {
		t.Errorf("в сумму попало то, чего мы не знаем: $%v", usd)
	}
}

// «вход 0, выход 0 — $0.069» читается как сломанный учёт, хотя цена верная:
// так выглядел расход, пришедший из claude -p, пока токены оттуда не разбирались.
func TestSpendStringWithoutTokens(t *testing.T) {
	got := Spend{Model: "claude-sonnet-5", USD: 0.069, PriceKnown: true}.String()
	if strings.Contains(got, "вход 0") {
		t.Fatalf("расход без токенов печатается как нулевой: %q", got)
	}
	if !strings.Contains(got, "0.069") {
		t.Fatalf("цена потерялась: %q", got)
	}
}
