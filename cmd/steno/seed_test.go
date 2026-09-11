package main

import (
	"os"
	"testing"

	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/demo"
)

// Наполняет базу правдоподобными данными, чтобы панель можно было посмотреть
// глазами. Верстать список созвонов на пустой базе бессмысленно.
//
//	SEED_DIR=./demo go test -run TestSeed .
//	STENO_PANEL_PASSWORD=... steno serve -c demo.json
func TestSeed(t *testing.T) {
	dir := os.Getenv("SEED_DIR")
	if dir == "" {
		t.Skip("нет SEED_DIR — это не проверка, а наполнение демо-базы")
	}
	st, err := core.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, n, err := demo.Seed(st, "ru")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("наполнено: %d созвонов в %s", n, dir)
}
