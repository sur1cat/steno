package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Загрузка записи созвона, на котором бота не было.
//
// Бот приходит не на всё: созвон мог пройти в Zoom, разговор мог быть по
// телефону, встреча могла случиться до того, как steno поставили. Запись при
// этом обычно есть — и без этой ручки она остаётся мёртвым файлом, хотя весь
// остальной конвейер к ней применим целиком.
//
// Имён говорящих здесь не будет: они приходят из субтитров Meet, а в чужом
// файле их нет. Текст даст whisper, follow-up — Claude, разметка по проектам
// работает как обычно.

const maxUploadBytes = 4 << 30 // четырёхчасовая встреча в видео — это гигабайты

func (p *Panel) apiUpload(w http.ResponseWriter, r *http.Request) {
	if p.cfg.Transcribe.Source == "captions" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": tr("сейчас текст берётся из субтитров Meet, а в загруженном файле их нет. ") +
				tr("Для загрузок нужен whisper или Groq — поменяй это в настройках расшифровки"),
		})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	// Файл сразу пишем на диск: держать двухчасовое видео в памяти нельзя.
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest,
			map[string]string{"error": tr("не смог прочитать файл: ") + err.Error()})
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": tr("не приложен файл")})
		return
	}
	defer file.Close()

	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		title = strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename))
	}
	if title == "" {
		title = tr("Загруженная запись")
	}

	m := &Meeting{
		ID:        newID(time.Now()),
		Title:     title,
		StartedAt: time.Now(),
		Status:    "uploading",
	}
	dir := p.st.RecordingDir(m.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		p.apiFail(w, err)
		return
	}

	// Кладём как есть, а потом приводим к тому, что ждёт расшифровка. Формат
	// приходит какой угодно: mp4 с созвона, m4a с диктофона, wav.
	rawPath := filepath.Join(dir, "upload"+filepath.Ext(header.Filename))
	raw, err := os.Create(rawPath)
	if err != nil {
		p.apiFail(w, err)
		return
	}
	n, copyErr := io.Copy(raw, file)
	raw.Close()
	if copyErr != nil {
		os.RemoveAll(dir)
		writeJSON(w, http.StatusBadRequest,
			map[string]string{"error": tr("файл не долился: ") + copyErr.Error()})
		return
	}
	if n == 0 {
		os.RemoveAll(dir)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": tr("файл пустой")})
		return
	}

	m.AudioPath = filepath.Join(dir, "audio.ogg")
	m.CaptionsPath = filepath.Join(dir, "captions.jsonl")
	if err := p.st.CreateMeeting(m); err != nil {
		os.RemoveAll(dir)
		p.apiFail(w, err)
		return
	}
	p.log.Printf(tr("загружено: %s (%.1f МБ) → %s"), header.Filename, float64(n)/(1<<20), m.ID)

	// Перекодирование и расшифровка идут в фоне: часовая запись — это минуты
	// работы, и держать на них запрос браузера незачем.
	go p.processUpload(m, rawPath)

	writeJSON(w, http.StatusAccepted, map[string]any{
		"id": m.ID, "title": m.Title, "bytes": n,
	})
}

func (p *Panel) processUpload(m *Meeting, rawPath string) {
	ctx, cancel := context.WithTimeout(context.Background(),
		p.cfg.Transcribe.Timeout.D()+time.Hour)
	defer cancel()

	if err := toStenoAudio(ctx, rawPath, m.AudioPath); err != nil {
		p.log.Printf(tr("загрузка %s: %v"), m.ID, err)
		_ = p.st.SetStatus(m.ID, "failed", err.Error())
		return
	}
	// Исходник больше не нужен: он мог быть видео на гигабайт, а нужен звук.
	_ = os.Remove(rawPath)

	if err := p.st.FinishMeeting(m.ID, time.Time{}, time.Now(), nil,
		"recorded", "", tr("загружено файлом")); err != nil {
		p.log.Printf(tr("загрузка %s: %v"), m.ID, err)
		return
	}
	if err := processMeeting(ctx, p.cfg, p.st, m.ID, false); err != nil {
		p.log.Printf(tr("загрузка %s: %v"), m.ID, err)
	}
}

// toStenoAudio приводит что угодно к тому, что ждёт расшифровка: 16 кГц моно
// opus. Видео при этом теряет картинку — она никому здесь не нужна, а гигабайты
// хранить незачем.
func toStenoAudio(ctx context.Context, src, dst string) error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf(tr("нужен ffmpeg, чтобы разобрать загруженный файл: %w"), err)
	}
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-nostdin", "-loglevel", "error",
		"-i", src,
		"-vn", // видео выбрасываем
		"-ac", "1", "-ar", "16000",
		"-c:a", "libopus", "-b:a", "32k",
		"-y", dst)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf(tr("ffmpeg не разобрал файл: %w\n%s"), err, tail(errBuf.String(), 400))
	}
	st, err := os.Stat(dst)
	if err != nil {
		return fmt.Errorf(tr("аудио не получилось: %w"), err)
	}
	// Пустой ogg — это только заголовки, около двухсот байт. Порог выше был бы
	// строже нужного и браковал короткие записи, а поймать надо ровно случай
	// «ffmpeg отработал, а звука не получилось».
	if st.Size() < 256 {
		return fmt.Errorf(tr("в файле не нашлось звука (%d байт на выходе)"), st.Size())
	}
	return nil
}
