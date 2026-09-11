package main

import (
	"os"
	"testing"

	"github.com/sur1cat/steno/internal/core"
)

// Английский демо-набор. Тот же смысл, что у TestSeed, но на языке, на котором
// сняты картинки в README: пустая база на витрине не показывает ничего, а
// переводить русскую на ходу — значит каждый раз получать другой текст.
//
//	SEED_DIR=./demo STENO_LANG=en go test -run TestSeedEN .
//	STENO_LANG=en STENO_PANEL_PASSWORD=… steno serve -c demo.json
func TestSeedEN(t *testing.T) {
	dir := os.Getenv("SEED_DIR")
	if dir == "" {
		t.Skip("нет SEED_DIR — это не проверка, а наполнение демо-базы")
	}
	st, err := core.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, n, err := seedDemo(st, "en")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("наполнено: %d созвонов в %s", n, dir)
}
