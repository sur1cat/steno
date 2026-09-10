package panel

import (
	"bytes"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sur1cat/steno/internal/audio"
	"github.com/sur1cat/steno/internal/core"
)

func uploadRequest(t *testing.T, a *apiClient, name string, data []byte, title string) (*http.Response, string) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if title != "" {
		_ = w.WriteField("title", title)
	}
	fw, err := w.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatal(err)
	}
	w.Close()

	req, err := http.NewRequest("POST", a.url+"/api/upload", &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := a.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(raw)
}

// Бот приходит не на всё: созвон мог пройти в Zoom, разговор — по телефону.
// Запись при этом обычно есть, и без загрузки она остаётся мёртвым файлом.
func TestUploadCreatesMeeting(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("нужен ffmpeg")
	}
	srv, st, _ := testPanel(t)
	c := login(t, srv, "тайна")

	// Настоящий WAV — тот же, которым doctor проверяет адаптер.
	resp, body := uploadRequest(t, c, "созвон.wav", audio.SilentWAV(2*1e9), "Разговор по телефону")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("код %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "Разговор по телефону") {
		t.Errorf("в ответе нет названия: %s", body)
	}

	// Созвон должен появиться сразу, не дожидаясь расшифровки.
	rows, err := st.ListMeetings(10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Title != "Разговор по телефону" {
		t.Fatalf("созвон не завёлся: %+v", rows)
	}
}

// Название берётся из имени файла, если его не дали: заставлять придумывать
// заголовок ради загрузки записи — лишний шаг.
func TestUploadTitleFromFilename(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("нужен ffmpeg")
	}
	srv, st, _ := testPanel(t)
	c := login(t, srv, "тайна")
	if resp, body := uploadRequest(t, c, "Планёрка 12 марта.m4a",
		audio.SilentWAV(1e9), ""); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("код %d: %s", resp.StatusCode, body)
	}
	rows, _ := st.ListMeetings(10, 0)
	if len(rows) != 1 || rows[0].Title != "Планёрка 12 марта" {
		t.Fatalf("название: %+v", rows)
	}
}

func TestUploadRejectsEmptyAndMissing(t *testing.T) {
	srv, _, _ := testPanel(t)
	c := login(t, srv, "тайна")

	if resp, _ := uploadRequest(t, c, "пусто.wav", nil, ""); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("пустой файл принят, код %d", resp.StatusCode)
	}
	// Совсем без файла.
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("title", "без файла")
	w.Close()
	req, _ := http.NewRequest("POST", srv.URL+"/api/upload", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := c.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("запрос без файла принят, код %d", resp.StatusCode)
	}
}

// В режиме субтитров загрузка бессмысленна: в чужом файле субтитров Meet нет,
// и текст брать неоткуда. Об этом надо сказать словами, а не молча завести
// созвон, который потом упадёт.
func TestUploadRefusedInCaptionsMode(t *testing.T) {
	t.Setenv("STENO_PANEL_PASSWORD", "тайна")
	dir := t.TempDir()
	st, err := core.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := core.DefaultConfig()
	cfg.DataDir = dir
	cfg.Transcribe.Source = "captions"
	p, err := NewPanel(cfg, st, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(p.handler())
	defer srv.Close()

	c := login(t, srv, "тайна")
	resp, body := uploadRequest(t, c, "x.wav", audio.SilentWAV(1e9), "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("код %d", resp.StatusCode)
	}
	if !strings.Contains(body, "субтитр") {
		t.Errorf("причина отказа невнятная: %s", body)
	}
	if rows, _ := st.ListMeetings(10, 0); len(rows) != 0 {
		t.Error("завели созвон, который всё равно не расшифруется")
	}
}

// Видео должно превращаться в звук: держать гигабайты картинки незачем.
func TestToStenoAudioStripsVideo(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("нужен ffmpeg")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "in.mp4")
	// Секунда чёрного видео с тишиной.
	cmd := exec.Command("ffmpeg", "-nostdin", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=black:s=320x240:d=1",
		"-f", "lavfi", "-i", "anullsrc=r=44100:cl=stereo",
		"-t", "1", "-c:v", "libx264", "-c:a", "aac", "-y", src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("не собралось тестовое видео: %s", out)
	}
	dst := filepath.Join(dir, "out.ogg")
	if err := toStenoAudio(t.Context(), src, dst); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() == 0 {
		t.Fatal("на выходе пусто")
	}
	// В результате не должно остаться видеодорожки.
	probe, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v",
		"-show_entries", "stream=codec_type", "-of", "csv=p=0", dst).Output()
	if err == nil && strings.Contains(string(probe), "video") {
		t.Error("видеодорожка осталась в аудиофайле")
	}
}
