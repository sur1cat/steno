package main

import (
	"encoding/json"
	"net/http"
	"time"
)

// Ручки заметки. Их дёргает кнопка в строке меню: клик — пишет, клик —
// остановил и разобрал.
//
// Заметку пишет сервис, а не приложение: ffmpeg должен пережить закрытие меню,
// а разбор — перезапуск приложения. Приложению остаётся кнопка и секундомер;
// то, что запись идёт, оно и так видит в базе.
//
// Отсюда же следует граница: писать микрофон умеет только та машина, на
// которой сервис запущен. Панель, поднятая на сервере, запишет микрофон
// сервера — то есть тишину, и скажет об этом на первой же пустой расшифровке.

// noteRoutes — все маршруты заметки одной строкой.
//
// Отдельной функцией, потому что panel_api.go сейчас правит другой человек:
// когда строка появится там, здесь ничего не изменится, а тест, который зовёт
// эту же функцию, продолжит проверять ровно те маршруты, что и сервис.
func (p *Panel) noteRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/note", p.apiGuard(p.apiNoteState))
	mux.Handle("GET /api/note/devices", p.apiGuard(p.apiNoteDevices))
	mux.Handle("POST /api/note/start", p.apiGuard(p.apiNoteStart))
	mux.Handle("POST /api/note/stop", p.apiGuard(p.apiNoteStop))
	mux.Handle("POST /api/note/cancel", p.apiGuard(p.apiNoteCancel))
}

// noteState — то, что кнопка спрашивает, чтобы знать, чем она сейчас является:
// «начать» или «остановить».
func noteState(s *noteSession) map[string]any {
	if s == nil {
		return map[string]any{"recording": false}
	}
	return map[string]any{
		"recording": true,
		"id":        s.ID,
		"title":     s.Title,
		"author":    s.Author,
		"startedAt": s.Started.Unix(),
		"seconds":   int(s.Elapsed().Seconds()),
		// Сколько осталось до потолка. Приложению это нужно, чтобы предупредить
		// заранее, а не показать оборванную запись постфактум.
		"until": s.Until.Unix(),
	}
}

func (p *Panel) apiNoteState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, noteState(notes.Live()))
}

func (p *Panel) apiNoteDevices(w http.ResponseWriter, r *http.Request) {
	devs, err := micDevices()
	if err != nil {
		// Не знать список микрофонов — не поломка: на Linux его отдаёт
		// pulseaudio, и запись всё равно возможна с устройством по умолчанию.
		writeJSON(w, http.StatusOK, map[string]any{
			"devices": []MicDevice{}, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": devs})
}

func (p *Panel) apiNoteStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title  string `json:"title"`
		Author string `json:"author"`
		Device string `json:"device"`
		// Потолок в секундах. Ноль — noteMaxDefault.
		MaxSeconds int `json:"maxSeconds"`
	}
	// Пустое тело — обычный случай: кнопка «наговорить» ничего не спрашивает.
	if r.ContentLength > 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": tr("плохой запрос")})
			return
		}
	}
	s, err := notes.Start(p.cfg, p.st, p.log, NoteOptions{
		Title: body.Title, Author: body.Author, Device: body.Device,
		Max: time.Duration(body.MaxSeconds) * time.Second,
	})
	if err != nil {
		// Причина всегда человеческая — занятый микрофон, запрет macOS,
		// уже идущая заметка, — и показать её надо словами, а не кодом.
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, noteState(s))
}

func (p *Panel) apiNoteStop(w http.ResponseWriter, r *http.Request) {
	s, err := notes.Stop(r.Context(), p.cfg, p.st, p.log, false)
	if err != nil {
		if s == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		// Запись была, но закрылась плохо. Строка созвона уже помечена
		// сорвавшейся, и человеку важнее увидеть, что именно сломалось.
		writeJSON(w, http.StatusOK, map[string]any{
			"id": s.ID, "status": "failed", "error": err.Error(),
			"seconds": int(s.Elapsed().Seconds())})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": s.ID, "status": "processing", "title": s.Title,
		"seconds": int(s.Elapsed().Seconds()),
		"message": tr("расшифровываю и разбираю"),
	})
}

func (p *Panel) apiNoteCancel(w http.ResponseWriter, r *http.Request) {
	s, err := notes.Cancel(p.st, p.log)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": s.ID, "status": "cancelled", "message": tr("заметка выброшена")})
}
