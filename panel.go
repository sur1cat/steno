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
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Собранный фронт. all: нужен, чтобы каталог попал в бинарник даже когда в нём
// нет ничего, кроме .gitkeep: на чистом клоне бандла ещё не существует, а без
// каталога go:embed не собирается вовсе. Пустой бандл честно скажет об этом на
// первой же странице — это лучше, чем несобирающийся проект.
//
//go:embed all:web/dist
var distFS embed.FS

// Панель — единственное место, где можно найти «что мы решили полгода назад».
// Google Docs и Slack хороши в момент, когда созвон только закончился; через
// месяц никто не помнит, в каком из них искать.
//
// Сама панель — реактовое приложение: страницы, модалки и плеер живут в
// браузере, Go отдаёт бандл и JSON. Деплой при этом остаётся одним файлом —
// бандл вшит в бинарник, node в проде не нужен.

type Panel struct {
	cfg *Config
	st  *Store
	log *log.Logger
	key []byte // ключ подписи cookie, выводится из пароля

	// Диспетчер нужен, чтобы позвать бота на созвон прямо из панели. У команд
	// без запущенного сервиса его нет — тогда кнопка честно отвечает отказом,
	// а не роняет процесс.
	d *Dispatcher

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
	return &Panel{cfg: cfg, st: st, log: lg, key: sum[:]}, nil
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
//
// Маршрутов всего три вида: данные, запись и сам бандл. Всё остальное —
// /m/{id}, /tasks, /settings — разбирает браузер, сервер про эти адреса не
// знает и знать не должен.
func (p *Panel) handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/", p.api())
	mux.Handle("GET /audio/{id}", p.apiGuard(p.audio))
	dist, err := fs.Sub(distFS, "web/dist")
	if err != nil {
		// Сюда не попасть: путь зашит в go:embed выше и проверен компилятором.
		p.log.Printf("панель: бандл не читается: %v", err)
		dist = distFS
	}
	mux.Handle("/", spaHandler(dist))
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

// passwordOK сверяет пароль и придерживает неудачную попытку. Пауза берётся
// под общим замком, а не просто time.Sleep: параллельные попытки иначе
// укладываются в ту же секунду, и ограничение перестаёт ограничивать.
func (p *Panel) passwordOK(given string) bool {
	pass, err := secret(p.cfg.Panel.PasswordEnv, "панель")
	if err != nil {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(given), []byte(pass)) == 1 {
		return true
	}
	p.loginMu.Lock()
	time.Sleep(time.Second)
	p.loginMu.Unlock()
	return false
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
