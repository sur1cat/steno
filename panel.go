package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed web/*
var webFS embed.FS

// Панель — единственное место, где можно найти «что мы решили полгода назад».
// Google Docs и Slack хороши в момент, когда созвон только закончился; через
// месяц никто не помнит, в каком из них искать.

type Panel struct {
	cfg *Config
	st  *Store
	log *log.Logger
	tpl *template.Template
	key []byte // ключ подписи cookie, выводится из пароля

	// Сериализует паузу после неверного пароля.
	loginMu sync.Mutex
}

// clientIP берёт адрес из X-Forwarded-For, если панель стоит за прокси.
func clientIP(r *http.Request) string {
	if f := r.Header.Get("X-Forwarded-For"); f != "" {
		if i := strings.IndexByte(f, ','); i > 0 {
			return strings.TrimSpace(f[:i])
		}
		return strings.TrimSpace(f)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (p *Panel) Name() string { return "панель" }

func newPanel(cfg *Config, st *Store, lg *log.Logger) (*Panel, error) {
	pass, err := secret(cfg.Panel.PasswordEnv, "панель")
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte("steno-panel:" + pass))
	tpl, err := template.New("").Funcs(panelFuncs).ParseFS(webFS, "web/*.html")
	if err != nil {
		return nil, fmt.Errorf("шаблоны: %w", err)
	}
	return &Panel{cfg: cfg, st: st, log: lg, tpl: tpl, key: sum[:]}, nil
}

func (p *Panel) Run(ctx context.Context) error {
	addr := p.cfg.Panel.Addr
	if addr == "" {
		addr = ":8080"
	}
	srv := &http.Server{Addr: addr, Handler: p.handler(), ReadHeaderTimeout: 10 * time.Second}
	// Shutdown обязан завершиться до выхода из Run: иначе Run возвращается,
	// вызывающий закрывает базу, а обработчики ещё дорабатывают запросы.
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
	}()
	p.log.Printf("панель: слушаю %s", addr)
	err := srv.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	<-stopped
	return nil
}

// handler вынесен из Run, чтобы маршруты можно было проверить без настоящего
// сокета.
func (p *Panel) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /static/{file}", p.static)
	mux.HandleFunc("GET /login", p.loginForm)
	mux.HandleFunc("POST /login", p.login)
	mux.HandleFunc("POST /logout", p.logout)
	mux.Handle("GET /{$}", p.guard(p.index))
	mux.Handle("GET /search", p.guard(p.search))
	mux.Handle("GET /m/{id}", p.guard(p.meeting))
	mux.Handle("GET /tasks", p.guard(p.tasks))
	mux.Handle("GET /projects", p.guard(p.projects))
	mux.Handle("GET /p/{name}", p.guard(p.project))
	mux.Handle("GET /audio/{id}", p.guard(p.audio))
	return mux
}

// --- доступ -----------------------------------------------------------------

// Пароль общий на команду: заводить на каждого учётку ради архива созвонов —
// работа, которую никто не сделает, а без неё панель просто не откроют.
// Cookie подписана ключом, выведенным из пароля: смена пароля разлогинивает
// всех, отдельного хранилища сессий не нужно.

const cookieName = "steno"

func (p *Panel) sign(exp int64) string {
	mac := hmac.New(sha256.New, p.key)
	fmt.Fprintf(mac, "%d", exp)
	return fmt.Sprintf("%d.%s", exp, hex.EncodeToString(mac.Sum(nil)))
}

func (p *Panel) valid(v string) bool {
	exp, sig, ok := strings.Cut(v, ".")
	if !ok {
		return false
	}
	n, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || time.Now().Unix() > n {
		return false
	}
	want := p.sign(n)
	return hmac.Equal([]byte(want), []byte(exp+"."+sig))
}

func (p *Panel) guard(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil || !p.valid(c.Value) {
			http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
			return
		}
		h(w, r)
	})
}

func (p *Panel) loginForm(w http.ResponseWriter, r *http.Request) {
	p.render(w, "login", map[string]any{
		"Next":  r.URL.Query().Get("next"),
		"Error": r.URL.Query().Get("error") != "",
	})
}

func (p *Panel) login(w http.ResponseWriter, r *http.Request) {
	pass, err := secret(p.cfg.Panel.PasswordEnv, "панель")
	if err != nil {
		http.Error(w, "панель не настроена", http.StatusInternalServerError)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "плохая форма", http.StatusBadRequest)
		return
	}
	next := safeNext(r.FormValue("next"))
	if subtle.ConstantTimeCompare([]byte(r.FormValue("password")), []byte(pass)) != 1 {
		// Пароль общий и потому короткий. Пауза под общим замком, а не просто
		// time.Sleep: параллельные попытки иначе укладываются в ту же секунду,
		// и ограничение перестаёт что-либо ограничивать.
		p.loginMu.Lock()
		time.Sleep(time.Second)
		p.loginMu.Unlock()
		// Неудачные входы должны быть видны: без записи в лог подбор пароля к
		// архиву всех разговоров компании проходит бесследно.
		p.log.Printf("панель: неверный пароль с %s", clientIP(r))
		http.Redirect(w, r, "/login?error=1&next="+url.QueryEscape(next), http.StatusSeeOther)
		return
	}
	exp := time.Now().Add(30 * 24 * time.Hour).Unix()
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: p.sign(exp), Path: "/",
		Expires: time.Unix(exp, 0), HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: p.cfg.Panel.Secure,
	})
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// safeNext не даёт увести человека на чужой сайт сразу после того, как он
// ввёл общий пароль команды. Проверки «начинается со /, но не с //» мало:
// браузеры разбирают обратный слэш в начале как разделитель хоста, поэтому
// /\evil.example превращается в http://evil.example.
func safeNext(next string) string {
	if next == "" || next[0] != '/' {
		return "/"
	}
	if len(next) > 1 && (next[1] == '/' || next[1] == '\\') {
		return "/"
	}
	if strings.ContainsAny(next, "\\\r\n") {
		return "/"
	}
	return next
}

func (p *Panel) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// --- страницы ---------------------------------------------------------------

func (p *Panel) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := p.tpl.ExecuteTemplate(w, name, data); err != nil {
		p.log.Printf("шаблон %s: %v", name, err)
	}
}

func (p *Panel) static(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	b, err := webFS.ReadFile("web/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if strings.HasSuffix(name, ".css") {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(b)
}

func (p *Panel) index(w http.ResponseWriter, r *http.Request) {
	const perPage = 50
	page, _ := strconv.Atoi(r.URL.Query().Get("p"))
	if page < 1 {
		page = 1
	}
	rows, err := p.st.ListMeetings(perPage, (page-1)*perPage)
	if err != nil {
		p.fail(w, err)
		return
	}
	total, _ := p.st.CountMeetings()
	p.render(w, "index", map[string]any{
		"Meetings": rows,
		"Total":    total,
		"Page":     page,
		"HasNext":  page*perPage < total,
		"Nav":      "meetings",
	})
}

func (p *Panel) search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	hits, err := p.st.Search(q, 200)
	if err != nil {
		p.fail(w, err)
		return
	}
	// Группируем по созвону: двадцать совпадений из одного разговора — это
	// один результат, а не двадцать.
	type group struct {
		ID        string
		Title     string
		StartedAt time.Time
		Hits      []SearchHit
	}
	var groups []*group
	byID := map[string]*group{}
	for _, h := range hits {
		g, ok := byID[h.MeetingID]
		if !ok {
			g = &group{ID: h.MeetingID, Title: h.Title, StartedAt: time.Unix(h.StartedAt, 0)}
			byID[h.MeetingID] = g
			groups = append(groups, g)
		}
		g.Hits = append(g.Hits, h)
	}
	p.render(w, "search", map[string]any{
		"Q": q, "Groups": groups, "Count": len(hits), "Nav": "meetings",
	})
}

func (p *Panel) meeting(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	m, err := p.st.Meeting(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	f, ferr := p.st.Followup(id)
	segs, err := p.st.Segments(id)
	if err != nil {
		p.fail(w, err)
		return
	}
	links, _ := p.st.Publications(id)
	spend, _ := p.st.Spend(id)
	dur := time.Duration(0)
	if m.EndedAt != nil {
		dur = m.EndedAt.Sub(m.StartedAt)
	}
	p.render(w, "meeting", map[string]any{
		"M": m, "F": f, "HasFollowup": ferr == nil,
		"Segments": segs, "Links": links, "Duration": dur, "Spend": spend,
		"HasAudio": m.AudioPath != "" && fileExists(m.AudioPath),
		"Seek":     r.URL.Query().Get("t"),
		"Nav":      "meetings",
	})
}

func (p *Panel) tasks(w http.ResponseWriter, r *http.Request) {
	owner := r.URL.Query().Get("owner")
	rows, err := p.st.Tasks(owner)
	if err != nil {
		p.fail(w, err)
		return
	}
	owners, _ := p.st.TaskOwners()

	type group struct {
		Owner string
		Tasks []TaskRow
	}
	var groups []*group
	byOwner := map[string]*group{}
	for _, t := range rows {
		g, ok := byOwner[t.Owner]
		if !ok {
			g = &group{Owner: t.Owner}
			byOwner[t.Owner] = g
			groups = append(groups, g)
		}
		g.Tasks = append(g.Tasks, t)
	}
	// «Не назначен» первым — это то, что вообще ни на ком не висит, и именно
	// оно требует решения. Дальше по алфавиту: тот же порядок, что у фильтра
	// сверху, иначе человека приходится искать на странице дважды.
	sort.SliceStable(groups, func(i, j int) bool {
		return lessOwner(groups[i].Owner, groups[j].Owner)
	})
	p.render(w, "tasks", map[string]any{
		"Groups": groups, "Owners": owners, "Owner": owner,
		"Count": len(rows), "Nav": "tasks",
	})
}

// Страница проекта — то, ради чего заводилось живое состояние: она отвечает
// на вопрос «что у нас сейчас по проекту», а не «что говорили на конкретном
// созвоне».
func (p *Panel) projects(w http.ResponseWriter, r *http.Request) {
	names, err := p.st.KnownProjects()
	if err != nil {
		p.fail(w, err)
		return
	}
	type row struct {
		Name              string
		Tasks, Questions  int
		Decisions, Closed int
	}
	var rows []row
	for _, n := range names {
		items, err := p.st.ProjectItems(n)
		if err != nil {
			p.fail(w, err)
			return
		}
		var x row
		x.Name = n
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
		rows = append(rows, x)
	}
	p.render(w, "projects", map[string]any{"Rows": rows, "Nav": "projects"})
}

func (p *Panel) project(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	items, err := p.st.ProjectItems(name)
	if err != nil {
		p.fail(w, err)
		return
	}
	if len(items) == 0 {
		http.NotFound(w, r)
		return
	}
	var tasks, questions, decisions, closed []ProjectItem
	for _, it := range items {
		if it.Status != "open" {
			closed = append(closed, it)
			continue
		}
		switch it.Kind {
		case KindTask:
			tasks = append(tasks, it)
		case KindQuestion:
			questions = append(questions, it)
		case KindDecision:
			decisions = append(decisions, it)
		}
	}
	p.render(w, "project", map[string]any{
		"Name": name, "Tasks": tasks, "Questions": questions,
		"Decisions": decisions, "Closed": closed, "Nav": "projects",
	})
}

// audio отдаёт запись через ServeContent: он сам умеет Range-запросы, без
// которых перемотка в плеере не работает — браузер тянул бы файл целиком.
func (p *Panel) audio(w http.ResponseWriter, r *http.Request) {
	m, err := p.st.Meeting(r.PathValue("id"))
	if err != nil || m.AudioPath == "" {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(m.AudioPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "audio/ogg")
	http.ServeContent(w, r, filepath.Base(m.AudioPath), fi.ModTime(), f)
}

func (p *Panel) fail(w http.ResponseWriter, err error) {
	p.log.Printf("панель: %v", err)
	http.Error(w, "что-то сломалось, смотри лог сервиса", http.StatusInternalServerError)
}

const unassigned = "не назначен"

func lessOwner(a, b string) bool {
	if ai, bi := a == unassigned, b == unassigned; ai != bi {
		return ai
	}
	return a < b
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
