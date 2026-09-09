package main

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// JSON-API панели.
//
// Панель — полноценное реактовое приложение, но деплой остаётся одним файлом:
// собранный фронт вшит в бинарник через go:embed, node в проде не нужен.
// Go отдаёт этот бандл и данные, всё остальное происходит в браузере.

func (p *Panel) api() http.Handler {
	mux := http.NewServeMux()

	// Вход — единственное, что доступно без сессии.
	mux.HandleFunc("POST /api/login", p.apiLogin)
	mux.HandleFunc("POST /api/logout", p.apiLogout)
	mux.HandleFunc("GET /api/session", p.apiSession)

	mux.Handle("GET /api/meetings", p.apiGuard(p.apiMeetings))
	mux.Handle("GET /api/meetings/{id}", p.apiGuard(p.apiMeeting))
	mux.Handle("GET /api/search", p.apiGuard(p.apiSearch))
	mux.Handle("GET /api/tasks", p.apiGuard(p.apiTasks))
	mux.Handle("GET /api/projects", p.apiGuard(p.apiProjects))
	mux.Handle("GET /api/projects/{name}", p.apiGuard(p.apiProject))
	mux.Handle("GET /api/settings", p.apiGuard(p.apiSettings))
	mux.Handle("GET /api/schedule", p.apiGuard(p.apiSchedule))

	mux.Handle("POST /api/projects", p.apiGuard(p.apiSaveProject))
	mux.Handle("DELETE /api/projects/{name}", p.apiGuard(p.apiDeleteProject))
	mux.Handle("POST /api/projects/{name}/context", p.apiGuard(p.apiBuildContext))
	mux.Handle("POST /api/channels/{key}", p.apiGuard(p.apiSaveChannel))
	mux.Handle("POST /api/items/{id}/close", p.apiGuard(p.apiCloseItem))
	mux.Handle("POST /api/items/{id}/reopen", p.apiGuard(p.apiReopenItem))
	mux.Handle("POST /api/schedule/{key}/override", p.apiGuard(p.apiScheduleOverride))
	mux.Handle("POST /api/invite", p.apiGuard(p.apiInvite))
	mux.Handle("POST /api/upload", p.apiGuard(p.apiUpload))
	return mux
}

// apiGuard отвечает кодом, а не редиректом: редирект на HTML-страницу входа в
// ответ на fetch выглядит как успешный ответ с мусором вместо данных.
func (p *Panel) apiGuard(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil || !p.valid(c.Value) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "нужен вход"})
			return
		}
		h(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (p *Panel) apiFail(w http.ResponseWriter, err error) {
	p.log.Printf("панель: %v", err)
	writeJSON(w, http.StatusInternalServerError,
		map[string]string{"error": "что-то сломалось, смотри лог сервиса"})
}

// --- вход -------------------------------------------------------------------

func (p *Panel) apiSession(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(cookieName)
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": err == nil && p.valid(c.Value)})
}

func (p *Panel) apiLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "плохой запрос"})
		return
	}
	if !p.passwordOK(body.Password) {
		p.log.Printf("панель: неверный пароль с %s", clientIP(r))
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "не тот пароль"})
		return
	}
	exp := time.Now().Add(30 * 24 * time.Hour).Unix()
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: p.sign(exp), Path: "/",
		Expires: time.Unix(exp, 0), HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: p.cfg.Panel.Secure,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": true})
}

func (p *Panel) apiLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1})
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": false})
}

// --- данные -----------------------------------------------------------------

func (p *Panel) apiMeetings(w http.ResponseWriter, r *http.Request) {
	const perPage = 50
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	rows, err := p.st.ListMeetings(perPage, (page-1)*perPage)
	if err != nil {
		p.apiFail(w, err)
		return
	}
	total, _ := p.st.CountMeetings()
	type item struct {
		ID           string   `json:"id"`
		Title        string   `json:"title"`
		StartedAt    int64    `json:"startedAt"`
		DurationSec  int      `json:"durationSec"`
		Participants []string `json:"participants"`
		Status       string   `json:"status"`
		LeftReason   string   `json:"leftReason"`
		Tasks        int      `json:"tasks"`
		HasAudio     bool     `json:"hasAudio"`
	}
	out := make([]item, 0, len(rows))
	for _, m := range rows {
		out = append(out, item{m.ID, m.Title, m.StartedAt.Unix(), int(m.Duration.Seconds()),
			m.Participants, m.Status, m.LeftReason, m.Tasks, m.HasAudio})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"meetings": out, "total": total, "page": page, "perPage": perPage,
	})
}

func (p *Panel) apiMeeting(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	m, err := p.st.Meeting(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "нет такого созвона"})
		return
	}
	f, ferr := p.st.Followup(id)
	segs, err := p.st.Segments(id)
	if err != nil {
		p.apiFail(w, err)
		return
	}
	links, _ := p.st.Publications(id)
	spend, _ := p.st.Spend(id)
	dur := 0
	if m.EndedAt != nil {
		dur = int(m.EndedAt.Sub(m.StartedAt).Seconds())
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": m.ID, "title": m.Title, "startedAt": m.StartedAt.Unix(),
		"durationSec": dur, "participants": m.Participants, "status": m.Status,
		"leftReason": m.LeftReason, "meetUrl": m.MeetURL,
		"followup": f, "hasFollowup": ferr == nil,
		"segments": segs, "links": links,
		"hasAudio": m.AudioPath != "" && fileExists(m.AudioPath),
		"spend":    spend,
	})
}

func (p *Panel) apiSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	hits, err := p.st.Search(q, 200)
	if err != nil {
		p.apiFail(w, err)
		return
	}
	// Подсветку отдаём разметкой сама панель: в индексе лежит сырой текст, и
	// класть в JSON готовый HTML значило бы отдать браузеру чужую разметку.
	type hit struct {
		MeetingID string  `json:"meetingId"`
		Title     string  `json:"title"`
		StartedAt int64   `json:"startedAt"`
		Kind      string  `json:"kind"`
		At        float64 `json:"at"`
		Speaker   string  `json:"speaker"`
		Parts     []part  `json:"parts"`
	}
	out := make([]hit, 0, len(hits))
	for _, h := range hits {
		out = append(out, hit{h.MeetingID, h.Title, h.StartedAt, h.Kind, h.At, h.Speaker,
			splitHighlight(h.Snippet)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"q": q, "hits": out})
}

// part — кусок сниппета: подсвеченный или обычный. Панель рисует их сама, и
// разметке из базы взяться неоткуда.
type part struct {
	Text string `json:"text"`
	Hit  bool   `json:"hit"`
}

func splitHighlight(s string) []part {
	var out []part
	for {
		before, rest, found := strings.Cut(s, markStart)
		if before != "" {
			out = append(out, part{Text: before})
		}
		if !found {
			return out
		}
		inside, after, ok := strings.Cut(rest, markEnd)
		if inside != "" {
			out = append(out, part{Text: inside, Hit: true})
		}
		if !ok {
			return out
		}
		s = after
	}
}

func (p *Panel) apiTasks(w http.ResponseWriter, r *http.Request) {
	owner := r.URL.Query().Get("owner")
	rows, err := p.st.Tasks(owner)
	if err != nil {
		p.apiFail(w, err)
		return
	}
	owners, _ := p.st.TaskOwners()
	type task struct {
		Owner        string  `json:"owner"`
		What         string  `json:"what"`
		Due          string  `json:"due"`
		Quote        string  `json:"quote"`
		At           float64 `json:"at"`
		MeetingID    string  `json:"meetingId"`
		MeetingTitle string  `json:"meetingTitle"`
		MeetingAt    int64   `json:"meetingAt"`
	}
	out := make([]task, 0, len(rows))
	for _, t := range rows {
		out = append(out, task{t.Owner, t.What, t.Due, t.Quote, t.At,
			t.MeetingID, t.MeetingTitle, t.MeetingAt.Unix()})
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": out, "owners": owners, "owner": owner})
}

func (p *Panel) apiProjects(w http.ResponseWriter, r *http.Request) {
	names, err := p.st.KnownProjects()
	if err != nil {
		p.apiFail(w, err)
		return
	}
	type row struct {
		Name      string `json:"name"`
		Tasks     int    `json:"tasks"`
		Questions int    `json:"questions"`
		Decisions int    `json:"decisions"`
		Closed    int    `json:"closed"`
	}
	out := make([]row, 0, len(names))
	for _, n := range names {
		items, err := p.st.ProjectItems(n)
		if err != nil {
			p.apiFail(w, err)
			return
		}
		x := row{Name: n}
		for _, it := range items {
			if it.Status != "open" {
				x.Closed++
				continue
			}
			switch it.Kind {
			case KindTask:
				x.Tasks++
			case KindQuestion:
				x.Questions++
			case KindDecision:
				x.Decisions++
			}
		}
		out = append(out, x)
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": out})
}

func (p *Panel) apiProject(w http.ResponseWriter, r *http.Request) {
	items, err := p.st.ProjectItems(r.PathValue("name"))
	if err != nil {
		p.apiFail(w, err)
		return
	}
	type item struct {
		ID        string `json:"id"`
		Kind      string `json:"kind"`
		Text      string `json:"text"`
		Owner     string `json:"owner"`
		Due       string `json:"due"`
		Status    string `json:"status"`
		Quote     string `json:"quote"`
		OpenedAt  int64  `json:"openedAt"`
		UpdatedAt int64  `json:"updatedAt"`
		OpenedIn  string `json:"openedIn"`
		ClosedIn  string `json:"closedIn"`
		Note      string `json:"note"`
	}
	out := make([]item, 0, len(items))
	for _, it := range items {
		out = append(out, item{it.ID, string(it.Kind), it.Text, it.Owner, it.Due,
			it.Status, it.Quote, it.OpenedAt.Unix(), it.UpdatedAt.Unix(),
			it.OpenedIn, it.ClosedIn, it.Note})
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": r.PathValue("name"), "items": out})
}

// --- настройки ---------------------------------------------------------------

func (p *Panel) apiSettings(w http.ResponseWriter, r *http.Request) {
	projects, err := p.st.Projects()
	if err != nil {
		p.apiFail(w, err)
		return
	}
	type proj struct {
		Name        string   `json:"name"`
		Aliases     []string `json:"aliases"`
		About       string   `json:"about"`
		Sources     []Source `json:"sources"`
		PrimerChars int      `json:"primerChars"`
		BuiltAt     int64    `json:"builtAt"`
		Primer      string   `json:"primer"`
	}
	out := make([]proj, 0, len(projects))
	for _, pr := range projects {
		x := proj{Name: pr.Name, Aliases: pr.Aliases, About: pr.About, Sources: pr.Sources}
		if c, err := p.st.ProjectContext(pr.Name); err == nil {
			x.PrimerChars = len([]rune(c.Primer))
			x.BuiltAt = c.BuiltAt.Unix()
			x.Primer = c.Primer
		}
		out = append(out, x)
	}

	// Значения секретов панель не видит и видеть не должна: токен рядом с
	// расшифровками всех разговоров — плохой размен.
	type secretRow struct {
		What string `json:"what"`
		Env  string `json:"env"`
		Set  bool   `json:"set"`
	}
	secrets := []secretRow{
		{"Claude", p.cfg.Claude.APIKeyEnv, envSet(p.cfg.Claude.APIKeyEnv)},
		{"Панель", p.cfg.Panel.PasswordEnv, envSet(p.cfg.Panel.PasswordEnv)},
		{"Slack", p.cfg.Slack.TokenEnv, envSet(p.cfg.Slack.TokenEnv)},
		{"Подпись Slack", p.cfg.Slack.SigningSecretEnv, envSet(p.cfg.Slack.SigningSecretEnv)},
		{"Telegram", p.cfg.Telegram.TokenEnv, envSet(p.cfg.Telegram.TokenEnv)},
		{"HTTP", p.cfg.HTTP.TokenEnv, envSet(p.cfg.HTTP.TokenEnv)},
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"projects": out, "secrets": secrets,
		"channels": panelChannels(p.st, p.cfg),
	})
}

// apiSaveChannel сохраняет настройки одного канала. Значения приходят строками
// ровно теми ключами, которые панель получила в описании полей, — разбирает их
// сам канал, а не форма.
func (p *Panel) apiSaveChannel(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	def, ok := channelByKey(key)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "нет такого канала"})
		return
	}
	var body struct {
		Enabled bool              `json:"enabled"`
		Values  map[string]string `json:"values"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "плохой запрос"})
		return
	}
	// Берём только описанные поля: лишние ключи из браузера в базу не кладём,
	// иначе настройка канала станет свалкой, куда можно дописать что угодно.
	values := map[string]string{}
	for _, f := range def.fields {
		values[f.Key] = strings.TrimSpace(body.Values[f.Key])
	}
	if err := p.st.SaveChannel(key, body.Enabled, values); err != nil {
		p.apiFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- расписание --------------------------------------------------------------

func (p *Panel) apiSchedule(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days < 1 || days > 60 {
		days = 7
	}
	// Назад берём два часа: только что начавшийся созвон человек ещё считает
	// сегодняшним делом, и пропадать из списка он не должен.
	rows, err := p.st.Schedule(time.Now().Add(-2*time.Hour), time.Now().AddDate(0, 0, days))
	if err != nil {
		p.apiFail(w, err)
		return
	}
	type entry struct {
		Key        string   `json:"key"`
		CalendarID string   `json:"calendarId"`
		Title      string   `json:"title"`
		MeetURL    string   `json:"meetUrl"`
		StartsAt   int64    `json:"startsAt"`
		EndsAt     int64    `json:"endsAt"`
		Attendees  []string `json:"attendees"`
		Skip       string   `json:"skip"`
		Override   string   `json:"override"`
		Recorded   string   `json:"recorded"`
		WillAttend bool     `json:"willAttend"`
	}
	out := make([]entry, 0, len(rows))
	for _, e := range rows {
		end := int64(0)
		if !e.EndsAt.IsZero() {
			end = e.EndsAt.Unix()
		}
		out = append(out, entry{e.Key, e.CalendarID, e.Title, e.MeetURL,
			e.StartsAt.Unix(), end, e.Attendees, e.Skip, e.Override, e.Recorded,
			e.WillAttend()})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entries": out, "days": days, "calendarOn": p.cfg.Calendar.Enabled,
	})
}

func (p *Panel) apiScheduleOverride(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Decision string `json:"decision"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "плохой запрос"})
		return
	}
	if err := p.st.SetScheduleOverride(r.PathValue("key"), body.Decision); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// apiInvite зовёт бота на созвон по ссылке. Три исхода различаются словами, а
// не сваливаются в один «ок»: человеку, чей созвон пропустили из-за нехватки
// слотов, нельзя отвечать «уже иду».
func (p *Panel) apiInvite(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL   string `json:"url"`
		Title string `json:"title"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "плохой запрос"})
		return
	}
	if p.d == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "бота зовёт сервис, а он сейчас не запущен"})
		return
	}
	id, res, err := inviteToCall(r.Context(), p.d, body.URL, body.Title, "панель")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	switch res {
	case Started:
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "started", "meetingId": id, "message": "иду на созвон"})
	case Duplicate:
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "duplicate", "message": "на этот созвон уже иду"})
	case NoCapacity:
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "no_capacity",
			"message": "сейчас пишу максимум созвонов сразу — освободится слот, " +
				"позови ещё раз"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "не смог записать созвон, смотри лог сервиса"})
	}
}

func (p *Panel) apiSaveProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OldName string   `json:"oldName"`
		Name    string   `json:"name"`
		About   string   `json:"about"`
		Aliases []string `json:"aliases"`
		Sources []Source `json:"sources"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "плохой запрос"})
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "у проекта должно быть название"})
		return
	}
	for _, s := range body.Sources {
		switch s.Kind {
		case "text", "path", "repo", "url":
		default:
			writeJSON(w, http.StatusBadRequest,
				map[string]string{"error": "непонятный источник: " + s.Kind})
			return
		}
		if strings.TrimSpace(s.Value) == "" {
			writeJSON(w, http.StatusBadRequest,
				map[string]string{"error": "у источника «" + s.Kind + "» пустое значение"})
			return
		}
	}
	if old := strings.TrimSpace(body.OldName); old != "" && old != name {
		if err := p.st.DeleteProject(old); err != nil {
			p.apiFail(w, err)
			return
		}
	}
	if err := p.st.SaveProject(Project{
		Name: name, About: strings.TrimSpace(body.About),
		Aliases: body.Aliases, Sources: body.Sources,
	}); err != nil {
		p.apiFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": name})
}

func (p *Panel) apiDeleteProject(w http.ResponseWriter, r *http.Request) {
	if err := p.st.DeleteProject(r.PathValue("name")); err != nil {
		p.apiFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (p *Panel) apiBuildContext(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	pr, err := p.st.Project(name)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "нет такого проекта"})
		return
	}
	go p.buildContextInBackground(pr)
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "собираю"})
}

func (p *Panel) apiCloseItem(w http.ResponseWriter, r *http.Request) {
	if err := p.st.CloseItem(r.PathValue("id"), "done", "закрыто руками", ""); err != nil {
		p.apiFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (p *Panel) apiReopenItem(w http.ResponseWriter, r *http.Request) {
	if err := p.st.ReopenItem(r.PathValue("id")); err != nil {
		p.apiFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- отдача собранной панели -------------------------------------------------

// spaHandler отдаёт файлы бандла, а всё остальное — index.html: маршруты живут
// в браузере, и сервер про них знать не обязан.
func spaHandler(dist fs.FS) http.Handler {
	files := http.FileServer(http.FS(dist))
	index, indexErr := fs.ReadFile(dist, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if indexErr != nil {
			http.Error(w, "панель не собрана: запусти make panel", http.StatusInternalServerError)
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if _, err := fs.Stat(dist, p); err == nil {
				// Имена файлов бандла содержат хеш содержимого, поэтому их
				// можно кэшировать навсегда: новая сборка — новое имя. Без
				// этого браузер перекачивает четыреста килобайт на каждый
				// переход по панели.
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(index)
	})
}
