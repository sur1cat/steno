package panel

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/spec"
)

func testSpecPanel(t *testing.T) (*httptest.Server, *core.Store, *spec.Store) {
	t.Helper()
	t.Setenv("STENO_PANEL_PASSWORD", "тайна")
	// Конфига в окружении нет: тогда раздел agent не читается ниоткуда, и
	// исполнение выключено — как у всякого, кто steno только поставил.
	t.Setenv("STENO_CONFIG", t.TempDir()+"/нет.json")

	dir := t.TempDir()
	st, err := core.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := core.DefaultConfig()
	cfg.DataDir = dir

	p, err := NewPanel(cfg, st, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/api/", p.api())
	p.specRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	store, err := spec.Open(st)
	if err != nil {
		t.Fatal(err)
	}
	return srv, st, store
}

// Ни одна ручка ТЗ не отдаётся без входа. Та, что запускает агента, — тем
// более: она пишет файлы на машине, где стоит сервис.
func TestSpecAPIRequiresLogin(t *testing.T) {
	srv, _, _ := testSpecPanel(t)
	a := &apiClient{t: t, c: srv.Client(), url: srv.URL}
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/specs"},
		{"GET", "/api/specs/S-1"},
		{"POST", "/api/items/T-1/spec"},
		{"POST", "/api/specs/S-1/run"},
	} {
		if code, _ := a.do(c.method, c.path, nil); code != http.StatusUnauthorized {
			t.Errorf("%s %s отдался без входа: %d", c.method, c.path, code)
		}
	}
}

// Требование первое, со стороны панели: пока в настройках не включено, кнопка
// отвечает отказом, а не запускает агента.
func TestSpecRunRefusedWhenDisabled(t *testing.T) {
	srv, _, store := testSpecPanel(t)
	sp := &spec.Spec{
		ID: "S-1", ItemID: "T-1", Project: "p", Repo: t.TempDir(), Status: spec.StatusDraft,
		Title:    "проба",
		Places:   []spec.Place{{Path: "a.py", Found: true}},
		Steps:    []string{"сделать"},
		Unknowns: []spec.Unknown{{Question: "что именно?", Why: "не сказано", Ask: "автор"}},
	}
	if err := store.Save(sp); err != nil {
		t.Fatal(err)
	}
	a := login(t, srv, "тайна")
	code, body := a.do("POST", "/api/specs/S-1/run", nil)
	if code != http.StatusForbidden {
		t.Fatalf("запуск при выключенной настройке: код %d, тело %s", code, body)
	}
}

// А негодное ТЗ не запускается и при включённой: отказ приходит сразу, с
// причинами, а не молча через минуту в журнале.
func TestSpecRunRefusesUngated(t *testing.T) {
	srv, _, store := testSpecPanel(t)
	dir := t.TempDir()
	writeFile(t, dir+"/steno.json", `{"agent":{"enabled":true}}`)
	t.Setenv("STENO_CONFIG", dir+"/steno.json")

	sp := &spec.Spec{ // без единого вопроса — то есть придуманное
		ID: "S-2", ItemID: "T-2", Project: "p", Repo: dir, Status: spec.StatusDraft,
		Title:  "проба",
		Places: []spec.Place{{Path: "a.py", Found: true}},
		Steps:  []string{"сделать"},
	}
	if err := store.Save(sp); err != nil {
		t.Fatal(err)
	}
	a := login(t, srv, "тайна")
	code, body := a.do("POST", "/api/specs/S-2/run", nil)
	if code != http.StatusBadRequest {
		t.Fatalf("запуск по ТЗ без открытых вопросов: код %d, тело %s", code, body)
	}
	if !strings.Contains(string(body), "открытого вопроса") {
		t.Fatalf("причина отказа не названа: %s", body)
	}
}

func TestSpecShowsGateReasons(t *testing.T) {
	srv, _, store := testSpecPanel(t)
	sp := &spec.Spec{ID: "S-3", ItemID: "T-3", Project: "p", Status: spec.StatusDraft,
		Title: "проба"}
	if err := store.Save(sp); err != nil {
		t.Fatal(err)
	}
	a := login(t, srv, "тайна")
	var out struct {
		Spec struct {
			Blocked []string `json:"blocked"`
		} `json:"spec"`
		Markdown string `json:"markdown"`
	}
	a.get("/api/specs/S-3", &out)
	if len(out.Spec.Blocked) == 0 {
		t.Fatal("панель не сказала, что по ТЗ работать нельзя")
	}
	if !strings.Contains(out.Markdown, "Чего не хватает") {
		t.Fatalf("в разметке нет обязательного раздела:\n%s", out.Markdown)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
