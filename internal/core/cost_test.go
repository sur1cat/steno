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

	usd, in, out, n, err := st.TotalSpend(started.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || usd != 0.09 || in != 6 || out != 2982 {
		t.Fatalf("учёт потерял расход: %d follow-up, $%.3f, вход %d, выход %d", n, usd, in, out)
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
