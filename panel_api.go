package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
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
	mux.Handle("GET /api/browse", p.apiGuard(p.apiBrowse))
	mux.Handle("GET /api/google/status", p.apiGuard(p.apiGoogleStatus))
	mux.Handle("GET /api/google/callback", p.apiGuard(p.apiGoogleCallback))
	mux.Handle("POST /api/google/connect", p.apiGuard(p.apiGoogleConnect))
	mux.Handle("POST /api/google/disconnect", p.apiGuard(p.apiGoogleDisconnect))

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

	// Секретов здесь нет — ни значений, ни имён переменных, ни отметки «задан».
	// Их задаёт `steno setup` в .env, и человеку, который зашёл посмотреть, что
	// решили на прошлой неделе, имя переменной окружения не объясняет ничего и
	// починить по нему он всё равно ничего не может.
	writeJSON(w, http.StatusOK, map[string]any{
		"projects": out,
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
	//
	// Уже лежащее в базе при этом сохраняем. Там могут быть поля, которых в
	// форме больше нет, — путь к ключу организации например, — и терять их
	// из-за правки соседнего поля нельзя.
	values := map[string]string{}
	if have, err := p.st.ChannelSettings(); err == nil {
		for k, v := range have[key].Values {
			values[k] = v
		}
	}
	for _, f := range def.fields {
		if f.input() {
			values[f.Key] = strings.TrimSpace(body.Values[f.Key])
		}
	}
	if err := p.st.SaveChannel(key, body.Enabled, values); err != nil {
		p.apiFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- выбор папки ---------------------------------------------------------------
//
// Материал проекта чаще всего лежит тут же, на этой машине: склонированный
// репозиторий, каталог с документами. Просить человека набрать путь руками
// значит получить опечатку и «источник не читается» — притом что глазами он эту
// папку узнаёт сразу. Поэтому панель водит по каталогам, а он тыкает.
//
// Границы жёсткие: наружу из домашнего каталога не выпускаем никак. Панель
// может стоять и на сервере компании, а там обзор всей файловой системы — это
// содержимое /etc через браузер, и открывать его тому, кто зашёл описать проект,
// незачем.

type browseDir struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// Лежит ли внутри .git. Человек ищет глазами именно репозиторий, и отметить
	// его сразу дешевле, чем дать зайти внутрь и вернуться ни с чем.
	IsRepo bool `json:"isRepo"`
}

func (p *Panel) apiBrowse(w http.ResponseWriter, r *http.Request) {
	home, err := os.UserHomeDir()
	if err != nil {
		p.apiFail(w, err)
		return
	}
	dir, root, err := browseResolve(home, r.URL.Query().Get("path"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "в эту папку не пускают"})
		return
	}

	dirs := make([]browseDir, 0, len(entries))
	for _, e := range entries {
		// Ссылки не показываем вовсе: половина из них ведёт за пределы дома, и
		// разбирать, какая куда, — лишний риск ради редкого случая.
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		repo := isRepoDir(path)
		// Скрытые каталоги человеку не нужны — кроме тех, которые сами и есть
		// репозиторий: у кого-то настройки лежат в ~/.dotfiles, и это ровно то,
		// что он ищет.
		if strings.HasPrefix(e.Name(), ".") && !repo {
			continue
		}
		// Каталог, куда не пустят, — это пропущенная строка, а не сломанный
		// запрос: одна чужая папка в домашнем каталоге не повод не показать
		// остальные.
		if !readableDir(path) {
			continue
		}
		dirs = append(dirs, browseDir{Name: e.Name(), Path: path, IsRepo: repo})
	}

	// Пустой parent — выше идти некуда: дом и есть верх. Сравнивать надо с
	// разобранным домом: на маке он лежит за ссылкой, и стрелка «наверх»
	// показывалась бы прямо в нём, ведя в место, куда всё равно не пустят.
	parent := ""
	if dir != root {
		parent = filepath.Dir(dir)
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": dir, "parent": parent, "dirs": dirs})
}

// browseResolve приводит запрошенный путь к настоящему и не выпускает его за
// пределы домашнего каталога.
//
// Проверять надо именно настоящий путь, после разбора ссылок: «..» отсекается
// очисткой, но ссылка ~/наружу → /etc выглядит как обычная папка внутри дома и
// прошла бы любую проверку по строке. Сам дом тоже разбираем: на маке он лежит
// за ссылкой, и сравнение с неразобранным путём отсекало бы вообще всё.
func browseResolve(home, ask string) (dir, root string, err error) {
	root, err = filepath.EvalSymlinks(home)
	if err != nil {
		return "", "", fmt.Errorf("домашний каталог не читается")
	}
	dir = strings.TrimSpace(ask)
	if dir == "" {
		dir = root
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	dir, err = filepath.EvalSymlinks(filepath.Clean(dir))
	if err != nil {
		return "", "", fmt.Errorf("нет такой папки")
	}
	if !withinDir(root, dir) {
		return "", "", fmt.Errorf("наружу из домашнего каталога нельзя")
	}
	return dir, root, nil
}

func withinDir(root, path string) bool {
	if path == root {
		return true
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// isRepoDir: .git бывает и каталогом, и файлом — у рабочих копий, вынесенных
// git worktree, там одна строка со ссылкой на настоящий каталог.
func isRepoDir(path string) bool {
	_, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil
}

func readableDir(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

// --- доступ в Google ----------------------------------------------------------
//
// Четыре ручки на одну кнопку. Она для того, у кого нет ни своего домена, ни
// админки Google: он нажимает, соглашается у Google и возвращается сюда. Второй
// путь — ключ организации файлом — панели не виден и настраивается мимо неё.

type googleState struct {
	// Заведён ли вход по кнопке тем, кто ставил steno. Без этого кнопку рисовать
	// незачем: она ведёт прямиком в ошибку.
	Ready bool `json:"ready"`
	// Согласие получено, доступ есть.
	Connected bool   `json:"connected"`
	Account   string `json:"account"`
	// Что сказать человеку. Пусто — говорить нечего.
	Why string `json:"why"`
	// Какого рода это сообщение: "info" — всё в порядке, просто объясняем;
	// "warn" — что-то не работает.
	//
	// Отдельным полем, потому что по остальным трём это не вычисляется.
	// Соблазнительно думать, что «доступ выдан ключом организации» — это всегда
	// ready:false, а нехватка прав — всегда ready:true. Оба правила неверны:
	// ключ организации прекрасно уживается с заведённым входом по кнопке
	// (ready:true), а подключённый аккаунт переживает смену client secret и
	// остаётся connected при ready:false. Разбирать же слова внутри
	// предложения панель не должна вовсе.
	Severity string `json:"severity"`
}

const (
	googleInfo = "info"
	googleWarn = "warn"
)

func (p *Panel) apiGoogleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, p.googleState())
}

func (p *Panel) googleState() googleState {
	s := googleState{Ready: googleOAuthReady(p.cfg), Severity: googleInfo}
	if t, err := loadGoogleToken(p.cfg); err == nil {
		s.Connected, s.Account = true, t.Account
		// Согласие бывает частичным: галочки на экране Google снимаются
		// поодиночке. Молчать об этом нельзя — канал просто не заработает, и
		// человек будет искать причину в другом месте.
		if missing := missingScopes(t.Scopes, googleScopes); len(missing) > 0 {
			s.Why = "Доступ выдан не весь: не хватает доступа к " +
				strings.Join(humanScopes(missing), " и ") + ". Подключи ещё раз."
			s.Severity = googleWarn
		}
		return s
	}
	switch {
	// Ключ организации проверяем первым: если доступ уже есть, человеку не о чем
	// беспокоиться, даже когда кнопка рядом тоже заведена.
	case credentialsFile(p.cfg.Calendar.CredentialsFile, p.cfg.GoogleDocs.CredentialsFile) != "":
		s.Why = "Доступ уже выдан ключом организации — подключать ничего не нужно."
	case !s.Ready:
		// Доступа нет, и добыть его отсюда нельзя: кнопки не будет, пока её не
		// заведут в `steno setup`. Каналы Google при этом не работают — это
		// именно предупреждение, а не пояснение.
		//
		// Сообщение называет команду, а не того, кто её выполнит. Прошлая
		// формулировка отсылала «к тому, кто ставил steno», и первым, кто её
		// не понял, оказался человек, который steno и поставил: на личной
		// установке это один и тот же человек, и его отправляли к самому себе.
		s.Why = "Чтобы появилась кнопка, выполни в терминале `steno setup` " +
			"и выбери «По кнопке в панели» — это делается один раз."
		s.Severity = googleWarn
	}
	return s
}

func (p *Panel) apiGoogleConnect(w http.ResponseWriter, r *http.Request) {
	oc, err := oauthConfig(p.cfg, p.googleRedirect(r))
	if err != nil {
		p.log.Printf("панель: подключение Google: %v", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "вход по кнопке ещё не настроен — это делает тот, кто ставил steno"})
		return
	}
	// AccessTypeOffline и ApprovalForce вместе: длинный доступ Google выдаёт
	// только вместе с согласием и только когда его спросили. Без них steno
	// получил бы час работы и нечем было бы продлить.
	writeJSON(w, http.StatusOK, map[string]string{
		"url": oc.AuthCodeURL(p.google.issue(), oauth2.AccessTypeOffline, oauth2.ApprovalForce),
	})
}

// apiGoogleCallback принимает человека обратно от Google. Отвечает редиректом, а
// не JSON: сюда приходит браузер, а не код панели, и оставить человека на
// странице с фигурными скобками было бы концом установки.
func (p *Panel) apiGoogleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		p.log.Printf("панель: Google отказал: %s", e)
		p.googleBack(w, r, "Согласие не получено — steno остался без доступа")
		return
	}
	// Метка одноразовая: без неё чужая страница могла бы подсунуть свой ответ и
	// подключить к steno чужой ящик.
	if !p.google.take(q.Get("state")) {
		p.googleBack(w, r, "Ссылка устарела — нажми «Подключить Google» ещё раз")
		return
	}
	oc, err := oauthConfig(p.cfg, p.googleRedirect(r))
	if err != nil {
		p.log.Printf("панель: подключение Google: %v", err)
		p.googleBack(w, r, "Доступ к Google не настроен — выполни `steno setup` в терминале")
		return
	}
	tok, err := oc.Exchange(r.Context(), q.Get("code"))
	if err != nil {
		p.log.Printf("панель: обмен кода Google: %v", err)
		p.googleBack(w, r, "Google не подтвердил согласие — попробуй ещё раз")
		return
	}
	// Длинный доступ Google присылает один раз — вместе с согласием. Пустое поле
	// значит, что через час всё кончится и продлить будет нечем. Показать при
	// этом «подключено» — худшее, что можно сделать: сломается оно к вечеру, а
	// искать причину будут неделю.
	if tok.RefreshToken == "" {
		old, oerr := loadGoogleToken(p.cfg)
		if oerr != nil || old.Token.RefreshToken == "" {
			p.googleBack(w, r, "Google выдал доступ на час вместо постоянного. "+
				"Убери steno из разрешённых приложений в своём аккаунте Google и подключи заново")
			return
		}
		tok.RefreshToken = old.Token.RefreshToken
	}
	account, err := googleAccountEmail(r.Context(), oc, tok)
	if err != nil {
		p.log.Printf("панель: чей ящик подключили, узнать не вышло: %v", err)
		p.googleBack(w, r, "Не смог узнать, к какому ящику подключился — попробуй ещё раз")
		return
	}
	if err := saveGoogleToken(p.cfg, &googleToken{
		Account: account, Scopes: googleScopes, Token: tok,
	}); err != nil {
		p.log.Printf("панель: не сохранился доступ в Google: %v", err)
		p.googleBack(w, r, "Не смог сохранить доступ, смотри лог сервиса")
		return
	}
	p.log.Printf("панель: Google подключён как %s", account)
	p.googleBack(w, r, "")
}

func (p *Panel) apiGoogleDisconnect(w http.ResponseWriter, r *http.Request) {
	if err := forgetGoogleToken(p.cfg); err != nil {
		p.apiFail(w, err)
		return
	}
	p.log.Printf("панель: доступ в Google отключён")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// googleBack возвращает человека на страницу настроек: пусто — получилось, иначе
// текст, который панели остаётся только показать.
func (p *Panel) googleBack(w http.ResponseWriter, r *http.Request, why string) {
	to := "/settings?google=ok"
	if why != "" {
		to = "/settings?google=fail&why=" + url.QueryEscape(why)
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// googleRedirect — адрес, на который Google вернёт человека.
//
// Берётся из запроса, а не из конфига: панель, открытую как localhost:8080,
// браузер не считает тем же местом, что 127.0.0.1:8080, — cookie туда не
// поедет, и возврат от Google упёрся бы в форму входа. Конфиг остаётся запасным
// ответом на случай запроса без адреса.
//
// Приложению типа «Desktop app» Google разрешает возврат на любой порт
// 127.0.0.1, поэтому регистрировать этот адрес отдельно не нужно.
func (p *Panel) googleRedirect(r *http.Request) string {
	host := r.Host
	if host == "" {
		host = "127.0.0.1:" + panelPort(p.cfg.Panel.Addr)
	}
	scheme := "http"
	if p.cfg.Panel.Secure {
		scheme = "https"
	}
	return scheme + "://" + host + "/api/google/callback"
}

func panelPort(addr string) string {
	if _, port, err := net.SplitHostPort(addr); err == nil && port != "" {
		return port
	}
	return "8080"
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
