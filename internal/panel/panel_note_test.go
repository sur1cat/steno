package panel

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sur1cat/steno/internal/audio"
	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/note"
)

// --- ручки панели ------------------------------------------------------------

func testNotePanel(t *testing.T) (*httptest.Server, *core.Store, *note.NoteHub, chan string) {
	t.Helper()
	t.Setenv("STENO_PANEL_PASSWORD", "тайна")
	dir := t.TempDir()
	h, cfg, st, processed := testHub(t, dir)
	note.Notes = h // ручки ходят к общей заметке процесса
	t.Cleanup(func() { note.Notes = &note.NoteHub{} })

	p, err := NewPanel(cfg, st, noteLog())
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/api/", p.api())
	p.noteRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, st, h, processed
}

func TestNoteAPIСтартИСтоп(t *testing.T) {
	srv, st, _, processed := testNotePanel(t)
	a := login(t, srv, "тайна")

	var state struct {
		Recording bool   `json:"recording"`
		ID        string `json:"id"`
		Author    string `json:"author"`
	}
	a.get("/api/note", &state)
	if state.Recording {
		t.Fatal("до нажатия кнопки заметка уже пишется")
	}

	code, body := a.do("POST", "/api/note/start", map[string]any{"author": "Рустем"})
	if code != 200 {
		t.Fatalf("старт: код %d, тело %s", code, body)
	}
	a.get("/api/note", &state)
	if !state.Recording || state.ID == "" {
		t.Fatalf("после старта состояние %+v", state)
	}
	if state.Author != "Рустем" {
		t.Errorf("автор заметки: %q", state.Author)
	}
	id := state.ID

	// Вторая кнопка «начать» не должна ронять первую запись.
	if code, _ := a.do("POST", "/api/note/start", nil); code != http.StatusBadRequest {
		t.Errorf("повторный старт ответил %d", code)
	}
	if s := note.Notes.Live(); s == nil || s.ID != id {
		t.Fatal("повторный старт сбил идущую запись")
	}

	code, body = a.do("POST", "/api/note/stop", nil)
	if code != 200 {
		t.Fatalf("стоп: код %d, тело %s", code, body)
	}
	select {
	case got := <-processed:
		if got != id {
			t.Errorf("в разбор ушла %s вместо %s", got, id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("после стопа разбор не начался")
	}
	m, err := st.Meeting(id)
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != "recorded" {
		t.Errorf("статус заметки после стопа: %q", m.Status)
	}
	if code, _ := a.do("POST", "/api/note/stop", nil); code != http.StatusBadRequest {
		t.Errorf("стоп без записи ответил %d", code)
	}
}

// Микрофон чужой машины включается одним запросом. Без входа этого не должно
// быть можно.
func TestNoteAPIТребуетВхода(t *testing.T) {
	srv, _, _, _ := testNotePanel(t)
	a := &apiClient{t: t, c: srv.Client(), url: srv.URL}
	for _, path := range []string{"/api/note", "/api/note/devices"} {
		if code, _ := a.do("GET", path, nil); code != http.StatusUnauthorized {
			t.Errorf("GET %s без входа: %d", path, code)
		}
	}
	for _, path := range []string{"/api/note/start", "/api/note/stop", "/api/note/cancel"} {
		if code, _ := a.do("POST", path, nil); code != http.StatusUnauthorized {
			t.Errorf("POST %s без входа: %d", path, code)
		}
	}
	if note.Notes.Live() != nil {
		t.Fatal("неавторизованный запрос включил микрофон")
	}
}

func testHub(t *testing.T, dir string) (*note.NoteHub, *core.Config, *core.Store, chan string) {
	t.Helper()
	st, err := core.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := core.DefaultConfig()
	cfg.DataDir = dir

	processed := make(chan string, 4)
	h := &note.NoteHub{
		Record: func(path, device string, max time.Duration) (*audio.Recorder, error) {
			// Файл нужен настоящий: Stop у пустого пути не проверяет ничего,
			// а конвейер дальше смотрит, что запись на месте.
			if err := os.WriteFile(path, []byte(strings.Repeat("o", 2048)), 0o644); err != nil {
				return nil, err
			}
			return &audio.Recorder{Path: path, Started: time.Now()}, nil
		},
		Process: func(_ context.Context, _ *core.Config, _ *core.Store, id string, _ bool) error {
			processed <- id
			return nil
		},
	}
	return h, cfg, st, processed
}

func noteLog() *log.Logger { return log.New(io.Discard, "", 0) }
