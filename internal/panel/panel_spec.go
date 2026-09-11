package panel

import (
	"context"
	"net/http"
	"time"

	"github.com/sur1cat/steno/internal/i18n"
	"github.com/sur1cat/steno/internal/spec"
)

// ТЗ по задачам с созвонов — в панели.
//
// Отдельным файлом и одной функцией маршрутов, как у заметки: panel_api.go
// сейчас правят другие, и когда строка `p.specRoutes(mux)` появится там, здесь
// не изменится ничего.
//
// Здесь же проходит граница безопасности. Собрать ТЗ можно чем угодно и когда
// угодно: это чтение репозитория и один запрос к модели. Запустить по нему
// агента — только отсюда и только из терминала, потому что за этими двумя
// дверями стоит человек: пароль панели и рука на клавиатуре. Вход по токену
// (internal/sources/source_http.go), телеграм и почта к исполнению не ведут и
// вести не должны — на созвоне кто угодно может сказать «снеси репозиторий», и
// оно доедет сюда обычной задачей.

func (p *Panel) specRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/specs", p.apiGuard(p.apiSpecs))
	mux.Handle("GET /api/specs/{id}", p.apiGuard(p.apiSpec))
	mux.Handle("POST /api/items/{id}/spec", p.apiGuard(p.apiBuildSpec))
	mux.Handle("POST /api/specs/{id}/run", p.apiGuard(p.apiRunSpec))
}

func (p *Panel) specStore() (*spec.Store, error) { return spec.Open(p.st) }

type specRow struct {
	ID       string    `json:"id"`
	ItemID   string    `json:"item_id"`
	Project  string    `json:"project"`
	Title    string    `json:"title"`
	Status   string    `json:"status"`
	Reject   string    `json:"reject,omitempty"`
	Blocked  []string  `json:"blocked,omitempty"` // почему по нему нельзя работать
	Unknowns int       `json:"unknowns"`
	Branch   string    `json:"branch,omitempty"`
	RunBy    string    `json:"run_by,omitempty"`
	RunError string    `json:"run_error,omitempty"`
	At       time.Time `json:"at"`
	USD      float64   `json:"usd"`
}

func row(sp *spec.Spec) specRow {
	return specRow{
		ID: sp.ID, ItemID: sp.ItemID, Project: sp.Project, Title: sp.Title,
		Status: sp.Status, Reject: sp.Reject, Blocked: sp.Gate(),
		Unknowns: len(sp.Unknowns), Branch: sp.Branch, RunBy: sp.RunBy,
		RunError: sp.RunError, At: sp.CreatedAt, USD: sp.USD,
	}
}

// apiSpecs — по одному последнему ТЗ на задачу. Панели нужно именно это:
// список задач, у каждой отметка, есть ли задание и что с ним.
func (p *Panel) apiSpecs(w http.ResponseWriter, r *http.Request) {
	store, err := p.specStore()
	if err != nil {
		p.apiFail(w, err)
		return
	}
	latest, err := store.Latest(r.URL.Query().Get("project"))
	if err != nil {
		p.apiFail(w, err)
		return
	}
	out := map[string]specRow{}
	for item, sp := range latest {
		out[item] = row(sp)
	}
	writeJSON(w, http.StatusOK, out)
}

// apiSpec — ТЗ целиком. Markdown, а не разобранная структура: панель его
// показывает, а не редактирует, и разбирать разметку в браузере незачем.
func (p *Panel) apiSpec(w http.ResponseWriter, r *http.Request) {
	store, err := p.specStore()
	if err != nil {
		p.apiFail(w, err)
		return
	}
	sp, err := store.Get(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": i18n.Tr("нет такого ТЗ")})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"spec":     row(sp),
		"markdown": sp.Render(),
		"log":      sp.RunLog,
	})
}

// apiBuildSpec — собрать ТЗ по задаче. В фоне: разбор репозитория и запрос к
// модели занимают минуту, а браузер столько не ждёт.
func (p *Panel) apiBuildSpec(w http.ResponseWriter, r *http.Request) {
	store, err := p.specStore()
	if err != nil {
		p.apiFail(w, err)
		return
	}
	id := r.PathValue("id")
	if _, err := spec.FindItem(p.st, id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": i18n.Tr("нет такой задачи")})
		return
	}
	d := spec.Deps{Cfg: p.cfg, St: p.st, Sp: store}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		sp, err := spec.Build(ctx, d, id)
		switch {
		case err != nil:
			p.log.Printf(i18n.Tr("ТЗ по %s: %v"), id, err)
		case sp.Status == spec.StatusRejected:
			p.log.Printf(i18n.Tr("ТЗ по %s: задача не взята — %s"), id, sp.Reject)
		default:
			p.log.Printf(i18n.Tr("ТЗ по %s готово: %s, вопросов — %d"), id, sp.ID, len(sp.Unknowns))
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"status": i18n.Tr("собираю")})
}

// apiRunSpec — та самая кнопка.
//
// Ответ на вопрос «кто нажал» — сессия панели: сюда без пароля не попадают.
// Записываем адрес, с которого нажали: пароль в панели общий на команду, и
// «кто-то из наших с 192.168.1.7» — это всё, что мы честно знаем.
func (p *Panel) apiRunSpec(w http.ResponseWriter, r *http.Request) {
	set := spec.SettingsFromEnv()
	if !set.Enabled {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": i18n.Tr("исполнение выключено в настройках сервиса")})
		return
	}
	store, err := p.specStore()
	if err != nil {
		p.apiFail(w, err)
		return
	}
	id := r.PathValue("id")
	sp, err := store.Get(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": i18n.Tr("нет такого ТЗ")})
		return
	}
	// Причину отказа говорим сразу, а не через минуту в журнале: человек стоит
	// у экрана и ждёт ответа именно на своё нажатие.
	if reasons := sp.Gate(); len(reasons) > 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": i18n.Tr("по этому ТЗ нельзя работать"), "why": reasons})
		return
	}
	by := i18n.Trf("панель (%s)", clientIP(r))
	d := spec.Deps{Cfg: p.cfg, St: p.st, Sp: store}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		// Ход работы течёт в журнал сервиса и в базу (spec.Run пишет его в
		// поле ТЗ). Панель показывает его, спрашивая GET /api/specs/{id}.
		_, err := spec.Run(ctx, d, set, id, by, func(kind, text string) {
			p.log.Printf("%s %s: %s", id, kind, text)
		})
		if err != nil {
			p.log.Printf(i18n.Tr("работа по %s: %v"), id, err)
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"status": i18n.Tr("начал")})
}
