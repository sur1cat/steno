package panel

import (
	"context"
	"net/http"
	"sync"
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
//
// Выключателя исполнения в панели нет и не будет: пароль панели общий на
// команду, а разрешение писать файлы на машине с сервисом даёт тот, кто за
// ней сидит, — `steno agent on` или кнопка в строке меню. Панель его
// показывает и объясняет, где он.

func (p *Panel) specRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/specs", p.apiGuard(p.apiSpecs))
	mux.Handle("GET /api/specs/{id}", p.apiGuard(p.apiSpec))
	mux.Handle("GET /api/agent", p.apiGuard(p.apiAgent))
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
	Repo     string    `json:"repo,omitempty"`
	RunBy    string    `json:"run_by,omitempty"`
	RunError string    `json:"run_error,omitempty"`
	At       time.Time `json:"at"`
	USD      float64   `json:"usd"`
	Model    string    `json:"model,omitempty"`
}

func row(sp *spec.Spec) specRow {
	return specRow{
		ID: sp.ID, ItemID: sp.ItemID, Project: sp.Project, Title: sp.Title,
		Status: sp.Status, Reject: sp.Reject, Blocked: sp.Gate(),
		Unknowns: len(sp.Unknowns), Branch: sp.Branch, Repo: sp.Repo, RunBy: sp.RunBy,
		RunError: sp.RunError, At: sp.CreatedAt, USD: sp.USD, Model: sp.Model,
	}
}

// specBody — само задание, по разделам. Панель рисует его сама, а не
// показывает разметку: путь, которого нет в репозитории, должен быть помечен
// цветом, а не звёздочками, и вопрос с адресатом — стоять рядом с ним.
type specBody struct {
	Summary  []string          `json:"summary"`
	Known    []string          `json:"known"`
	Places   []spec.Place      `json:"places"`
	Found    []bool            `json:"found"`
	Steps    []string          `json:"steps"`
	Checks   []string          `json:"checks"`
	Unknowns []spec.Unknown    `json:"unknowns"`
	Guesses  []spec.Assumption `json:"guesses"`
	NotHere  []string          `json:"not_here"`
	Blocked  bool              `json:"blocked"`
	Why      string            `json:"why"`
}

func body(sp *spec.Spec) specBody {
	found := make([]bool, len(sp.Places))
	for i, pl := range sp.Places {
		found[i] = pl.Found
	}
	return specBody{
		Summary: sp.Summary, Known: sp.Known, Places: sp.Places, Found: found,
		Steps: sp.Steps, Checks: sp.Checks, Unknowns: sp.Unknowns,
		Guesses: sp.Guesses, NotHere: sp.NotHere, Blocked: sp.Blocked, Why: sp.Why,
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
	specs := map[string]specRow{}
	for item, sp := range latest {
		specs[item] = row(sp)
	}
	out := p.buildState()
	out["specs"] = specs
	writeJSON(w, http.StatusOK, out)
}

// apiSpec — ТЗ целиком: разделами для страницы и разметкой для «скопировать».
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
	out := map[string]any{
		"spec":     row(sp),
		"body":     body(sp),
		"markdown": sp.Render(),
		"log":      sp.RunLog,
		"agent":    p.agentState(),
	}
	// Задача, из которой выросло ТЗ, — рядом: читать задание, не видя
	// фразы с созвона, значит читать ответ без вопроса. Закрытая тоже
	// показывается — ТЗ переживает задачу.
	if it, err := p.st.Item(sp.ItemID); err == nil {
		out["item"] = map[string]any{
			"id": it.ID, "text": it.Text, "owner": it.Owner, "due": it.Due,
			"status": it.Status, "quote": it.Quote, "openedIn": it.OpenedIn,
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// apiAgent — состояние выключателя и исполнителя. Панель показывает, не
// правит: см. шапку файла.
func (p *Panel) apiAgent(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, p.agentState())
}

// agentState — spec.Check с проверкой исполнителя живьём, но не чаще раза в
// две минуты: `claude auth status` — это подпроцесс на секунду, а панель
// спрашивает состояние на каждую страницу. При выключенном исполнении не
// проверяем вовсе: запускать всё равно нечего, а подпроцесс не бесплатный.
func (p *Panel) agentState() spec.Readiness {
	set := spec.SettingsFor(p.cfg)
	p.agentMu.Lock()
	defer p.agentMu.Unlock()
	if time.Since(p.agentAt) < 2*time.Minute && p.agentSeen.Enabled == set.Enabled &&
		p.agentSeen.AutoSpec == set.AutoSpec {
		return p.agentSeen
	}
	r := spec.Check(p.cfg, set, set.Enabled)
	p.agentSeen, p.agentAt = r, time.Now()
	return r
}

// agentProbe — кэш agentState. В Panel, а не в глобальной переменной: панелей
// в тестах много, и у каждой свой конфиг.
type agentProbe struct {
	agentMu   sync.Mutex
	agentSeen spec.Readiness
	agentAt   time.Time
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
	// Второе нажатие, пока первое собирается, — не второй запрос к модели.
	if _, busy := p.building.LoadOrStore(id, true); busy {
		writeJSON(w, http.StatusAccepted, map[string]string{"status": i18n.Tr("уже собираю")})
		return
	}
	d := spec.Deps{Cfg: p.cfg, St: p.st, Sp: store}
	p.failed.Delete(id)
	go func() {
		defer p.building.Delete(id)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		sp, err := spec.Build(ctx, d, id)
		switch {
		case err != nil:
			p.log.Printf(i18n.Tr("ТЗ по %s: %v"), id, err)
			p.failed.Store(id, err.Error())
		case sp.Status == spec.StatusRejected:
			p.log.Printf(i18n.Tr("ТЗ по %s: задача не взята — %s"), id, sp.Reject)
		default:
			p.log.Printf(i18n.Tr("ТЗ по %s готово: %s, вопросов — %d"), id, sp.ID, len(sp.Unknowns))
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"status": i18n.Tr("собираю")})
}

// buildState — по каким задачам ТЗ собирается прямо сейчас и по каким сборка
// сорвалась. Панель опрашивает это, чтобы кнопка «ТЗ» превращалась в
// «собираю…» и обратно, а ошибка модели не терялась в журнале сервиса.
func (p *Panel) buildState() map[string]any {
	busy := []string{}
	p.building.Range(func(k, _ any) bool {
		busy = append(busy, k.(string))
		return true
	})
	failed := map[string]string{}
	p.failed.Range(func(k, v any) bool {
		failed[k.(string)] = v.(string)
		return true
	})
	return map[string]any{"building": busy, "failed": failed}
}

// apiRunSpec — та самая кнопка.
//
// Ответ на вопрос «кто нажал» — сессия панели: сюда без пароля не попадают.
// Записываем адрес, с которого нажали: пароль в панели общий на команду, и
// «кто-то из наших с 192.168.1.7» — это всё, что мы честно знаем.
func (p *Panel) apiRunSpec(w http.ResponseWriter, r *http.Request) {
	set := spec.SettingsFor(p.cfg)
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
	if sp.Status == spec.StatusRunning {
		writeJSON(w, http.StatusConflict, map[string]string{"error": i18n.Tr("по этому ТЗ уже идёт работа")})
		return
	}
	// Причину отказа говорим сразу, а не через минуту в журнале: человек стоит
	// у экрана и ждёт ответа именно на своё нажатие.
	if reasons := sp.Gate(); len(reasons) > 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": i18n.Tr("по этому ТЗ нельзя работать"), "why": reasons})
		return
	}
	// Исполнителя проверяем до того, как ответить «начал»: «claude не найден»
	// через секунду в журнале — это кнопка, которая молча не работает.
	if ready := spec.Check(p.cfg, set, true); ready.Executor == "" || !ready.Ready {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": ready.Why})
		return
	}
	by := i18n.Trf("панель (%s)", clientIP(r))
	d := spec.Deps{Cfg: p.cfg, St: p.st, Sp: store}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		// Ход работы течёт в журнал сервиса и в базу (spec.Run пишет его в
		// поле ТЗ по ходу дела). Панель показывает его, спрашивая GET /api/specs/{id}.
		_, err := spec.Run(ctx, d, set, id, by, func(kind, text string) {
			p.log.Printf("%s %s: %s", id, kind, text)
		})
		if err != nil {
			p.log.Printf(i18n.Tr("работа по %s: %v"), id, err)
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"status": i18n.Tr("начал")})
}
