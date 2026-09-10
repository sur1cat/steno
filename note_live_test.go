//go:build live

package main

// Ручная проверка на живом голосе. Обычные прогоны её не видят: тег live.
//
//	go test -tags live -run TestLiveNote -v -timeout 40m .
//
// STENO_LIVE_CONFIG — конфиг (whisper и доступ к Claude настоящие).
// STENO_LIVE_SAY    — файл с текстом, который проговорить вслух через `say`.

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestLiveNote(t *testing.T) {
	cfgPath := os.Getenv("STENO_LIVE_CONFIG")
	if cfgPath == "" {
		t.Skip("нужен STENO_LIVE_CONFIG")
	}
	cfg, st, err := open(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	lg := log.New(os.Stderr, "", log.Ltime)
	// Разобрать уже записанное — тем же промптом, без нового захода к микрофону.
	if id := os.Getenv("STENO_LIVE_ID"); id != "" {
		if err := processNote(context.Background(), cfg, st, id, true); err != nil {
			t.Fatal(err)
		}
		show(t, st, id)
		return
	}
	s, err := notes.Start(cfg, st, lg, NoteOptions{
		Author: "Рустем", Max: 3 * time.Minute, NoPublish: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("пишу %s → %s", s.ID, st.RecordingDir(s.ID))

	// Даём микрофону раскачаться, потом говорим вслух.
	time.Sleep(2 * time.Second)
	if say := os.Getenv("STENO_LIVE_SAY"); say != "" {
		cmd := exec.Command("say", "-v", "Milena", "-r", "165", "-f", say)
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("say: %v", err)
		}
	} else {
		t.Log("говори сейчас — 45 секунд")
		time.Sleep(45 * time.Second)
	}
	time.Sleep(1500 * time.Millisecond)

	if _, err := notes.Stop(context.Background(), cfg, st, lg, true); err != nil {
		t.Fatal(err)
	}
	show(t, st, s.ID)
}

func show(t *testing.T, st *Store, id string) {
	t.Helper()
	m, err := st.Meeting(id)
	if err != nil {
		t.Fatal(err)
	}
	segs, _ := st.Segments(id)
	fmt.Println("\n===== ЧТО УСЛЫШАЛ WHISPER =====")
	fmt.Print(renderMonologue(segs))
	f, err := st.Followup(id)
	if err != nil {
		t.Fatalf("разбора нет: %v", err)
	}
	fmt.Println("\n===== ЧТО ВЫШЛО ПОСЛЕ РАЗБОРА =====")
	fmt.Println(renderPlain(m, f))
	sp, _ := st.Spend(id)
	fmt.Printf("\nрасход: %s\nстатус: %s\nназвание: %s\n", sp, m.Status, m.Title)
}

// Тот же путь, но через настоящий HTTP — ровно так, как по нему пойдёт кнопка
// в строке меню: вход по паролю, старт, опрос состояния, стоп.
//
//	STENO_LIVE_CONFIG=… STENO_LIVE_SAY=… go test -tags live -run TestLiveNoteAPI -v .
func TestLiveNoteAPI(t *testing.T) {
	cfgPath := os.Getenv("STENO_LIVE_CONFIG")
	if cfgPath == "" {
		t.Skip("нужен STENO_LIVE_CONFIG")
	}
	t.Setenv("STENO_PANEL_PASSWORD", "тайна")
	cfg, st, err := open(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg.Panel.PasswordEnv = "STENO_PANEL_PASSWORD"

	p, err := newPanel(cfg, st, log.New(os.Stderr, "панель: ", log.Ltime))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/api/", p.api())
	p.noteRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a := login(t, srv, "тайна")
	code, body := a.do("POST", "/api/note/start", map[string]any{"author": "Рустем"})
	t.Logf("старт: %d %s", code, body)
	if code != 200 {
		t.Fatal("микрофон не включился")
	}
	var state struct {
		Recording bool   `json:"recording"`
		ID        string `json:"id"`
		Seconds   int    `json:"seconds"`
	}
	a.get("/api/note", &state)
	t.Logf("состояние: %+v", state)
	if !state.Recording {
		t.Fatal("панель не показывает идущую заметку")
	}

	time.Sleep(1500 * time.Millisecond)
	if say := os.Getenv("STENO_LIVE_SAY"); say != "" {
		cmd := exec.Command("say", "-v", "Milena", "-r", "165", "-f", say)
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("say: %v", err)
		}
	}
	time.Sleep(time.Second)

	id := state.ID
	code, body = a.do("POST", "/api/note/stop", nil)
	t.Logf("стоп: %d %s", code, body)
	if code != 200 {
		t.Fatal("не остановилось")
	}
	a.get("/api/note", &state)
	if state.Recording {
		t.Error("после стопа панель всё ещё показывает запись")
	}
	// Разбор ушёл в фон — ждём, как ждала бы кнопка.
	for i := 0; i < 600; i++ {
		m, err := st.Meeting(id)
		if err == nil && (m.Status == "summarized" || m.Status == "published" ||
			m.Status == "failed" || m.Status == "publish_failed") {
			show(t, st, id)
			if m.Status == "failed" {
				t.Fatalf("заметка сорвалась: %s", m.Error)
			}
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatal("разбор не закончился за десять минут")
}
