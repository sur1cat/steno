package sources

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sur1cat/steno/internal/bot"
	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
)

// Вход для всего остального. Календарь, Telegram и почта бота знают, откуда
// брать созвоны; этот источник не знает ничего и потому годится под что угодно,
// что умеет послать POST: ярлык на телефоне, слэш-команда Slack, кнопка в чужой
// админке, cron, строка в чьём-то скрипте.
//
// Договор ровно один, и он такой же короткий, как у адаптера расшифровки:
//
//	POST /join
//	Authorization: Bearer <общий секрет>
//	Content-Type: application/json
//	{"url": "https://meet.google.com/abc-defg-hij", "title": "Планёрка"}
//
//	200 {"text": "…", "meeting_id": "…"}   бот пошёл
//	400                                    ссылки нет или площадка незнакомая
//	401                                    секрет не тот или его нет
//	405                                    не POST
//	503                                    все слоты записи заняты
//	500                                    не смог записать заявку
//
// title необязателен: без него созвон называется «Созвон по ссылке», а
// настоящее имя ему всё равно даст разбор. Тело можно слать и формой — так шлёт
// слэш-команда Slack, и так же выходит у curl без -H Content-Type; ссылку
// найдём в любом из полей text, url, link или просто в теле.
//
// Общий секрет обязателен и лежит только в .env: этот порт открыт в сеть, и по
// нему бота заводят в произвольный созвон — то есть заставляют его записывать
// чужой разговор. Без секрета источник не поднимается вовсе. В панели секрета
// нет ни в каком виде — ни значения, ни имени переменной; так же, как у всех
// остальных каналов.
//
//	curl -X POST http://steno:8787/join \
//	     -H "Authorization: Bearer $STENO_HTTP_TOKEN" \
//	     -d '{"url":"https://meet.google.com/abc-defg-hij"}'

type HTTPSource struct {
	Cfg *core.Config
	D   *Dispatcher
	Log *log.Logger

	slackSecret string
	denied      denyLog
}

func (s *HTTPSource) Name() string { return "http" }

func (s *HTTPSource) Run(ctx context.Context) error {
	token, err := core.Secret(s.Cfg.HTTP.TokenEnv, "HTTP")
	if err != nil {
		// Не поднимаемся вовсе. Открытый порт без секрета — это приглашение
		// завести бота в любой созвон, и «работает, но настройте потом»
		// означает, что никто уже не настроит.
		return fmt.Errorf("%w\n%s", err, i18n.Tr("  вход по ссылке без общего секрета не поднимается: ")+
			i18n.Tr("по нему бота заводят в произвольный созвон.\n")+
			i18n.Tr("  впиши в .env рядом с steno.json готовую строку и перезапусти сервис:\n    ")+
			s.Cfg.HTTP.TokenEnv+"="+newSecret())
	}
	if len([]rune(token)) < 16 {
		// Не отказ: секрет мог быть выбран осознанно для сети, куда никто
		// чужой не дотянется. Но сказать об этом надо один раз и вслух.
		s.Log.Printf(i18n.Tr("http: секрет короче 16 символов — такой подбирают; заменить на длинный: %s=%s"),
			s.Cfg.HTTP.TokenEnv, newSecret())
	}
	addr := s.Cfg.HTTP.Addr
	if addr == "" {
		addr = ":8787"
	}
	// Без подписывающего секрета слэш-команды Slack не принимаются: поле
	// channel_id в них ничем не подтверждено.
	s.slackSecret = os.Getenv(s.Cfg.Slack.SigningSecretEnv)
	if s.slackSecret == "" {
		s.Log.Printf(i18n.Tr("http: %s не задан — слэш-команды Slack приниматься не будут"),
			s.Cfg.Slack.SigningSecretEnv)
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           s.handler(ctx, token),
		ReadHeaderTimeout: 10 * time.Second,
		// Соединение, которое открыли и дальше цедят по байту, иначе держит
		// горутину столько, сколько захочет тот, кто его открыл.
		ReadTimeout: 30 * time.Second,
		IdleTimeout: 60 * time.Second,
	}
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

	s.Log.Printf(i18n.Tr("http: слушаю %s (POST /join, нужен общий секрет из %s)"),
		addr, s.Cfg.HTTP.TokenEnv)
	err = srv.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	<-stopped
	return nil
}

// handler — все адреса входа. Отдельно от Run, чтобы проверять его настоящими
// запросами к настоящему серверу, а не разбором на месте: половина договора —
// это коды ответов и текст отказа, и проверять их подделкой Request значит не
// проверять их вовсе.
func (s *HTTPSource) handler(ctx context.Context, token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/join", func(w http.ResponseWriter, r *http.Request) {
		s.join(ctx, w, r, token)
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	// Всё остальное — не молчаливый 404 от net/http, а адрес, по которому
	// человек с curl увидит, куда он попал и куда надо было.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, i18n.Tr("нет такого адреса. Есть POST /join и GET /healthz\n\n")+
			joinExample(r), http.StatusNotFound)
	})
	return mux
}

// join — весь разбор одной заявки.
func (s *HTTPSource) join(ctx context.Context, w http.ResponseWriter, r *http.Request, token string) {
	if r.Method != http.MethodPost {
		http.Error(w, i18n.Trf("нужен POST, а пришёл %s\n\n", r.Method)+joinExample(r),
			http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, i18n.Tr("не смог прочитать тело"), http.StatusBadRequest)
		return
	}
	via := authenticate(r, body, token, s.slackSecret)
	if via == "" {
		reason := denyReason(r)
		// Отказ виден и тому, кто стучится, и тому, кто держит сервис: без
		// строки в логе «у меня 401» превращается в переписку вслепую.
		s.denied.note(s.Log, i18n.Tr("http: отказ %s — %s"), r.RemoteAddr, reason)
		http.Error(w, reason+"\n\n"+joinExample(r), http.StatusUnauthorized)
		return
	}
	meetURL, title, reply, why := parseJoinRequest(r, body, via)
	if meetURL == "" {
		// Отказ должен называть причину: «не умею Zoom», «это не JSON» и
		// «ссылки в запросе вообще нет» чинятся по-разному.
		http.Error(w, why.Error(), http.StatusBadRequest)
		return
	}
	m := &core.Meeting{
		ID:        core.NewID(time.Now()),
		Title:     core.FirstNonEmpty(title, i18n.Tr("Созвон по ссылке")),
		MeetURL:   meetURL,
		StartedAt: time.Now(),
		Status:    "recording",
		ReplyTo:   reply,
	}
	// Именно ctx сервиса, а не r.Context(): контекст запроса умирает
	// вместе с ответом, а запись созвона живёт ещё час.
	var msg string
	code := http.StatusOK
	switch s.D.Start(ctx, adHocKey(meetURL, time.Now()), m, "HTTP ("+via+")") {
	case Started:
		msg = i18n.Tr("Иду на ") + meetURL + i18n.Tr(" — follow-up придёт, когда созвон закончится.")
	case Duplicate:
		msg = i18n.Tr("Я уже иду на этот созвон.")
	case NoCapacity:
		msg = i18n.Tr("Сейчас пишу максимум созвонов сразу — на этот не пойду.")
		code = http.StatusServiceUnavailable
	default:
		msg = i18n.Tr("Не смог записать заявку, посмотри лог сервиса.")
		code = http.StatusInternalServerError
	}
	// Slack показывает response_type/text; всем остальным этот JSON тоже
	// читается нормально.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"response_type": "in_channel",
		"text":          msg,
		"meeting_id":    m.ID,
	})
}

// joinExample — рабочая строка curl с адресом, на который человек только что
// постучался. Отказ, который не показывает, как надо, заставляет идти читать
// README ровно в тот момент, когда созвон уже начался.
func joinExample(r *http.Request) string {
	host := r.Host
	if host == "" {
		host = "localhost:8787"
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return "  curl -X POST " + scheme + "://" + host + "/join \\\n" +
		i18n.Tr("       -H \"Authorization: Bearer <общий секрет>\" \\\n") +
		`       -d '{"url":"https://meet.google.com/abc-defg-hij"}'`
}

// newSecret — готовый общий секрет, который остаётся вписать в .env. Человеку,
// у которого секрета нет, «придумай секрет» помогает куда меньше, чем строка,
// которую можно скопировать целиком.
func newSecret() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		// Сюда не доходит: crypto/rand на Linux и macOS не отказывает. Но
		// подсунуть вместо секрета предсказуемую строку было бы хуже, чем
		// сказать, что придумать его придётся самому.
		return i18n.Tr("<придумай-длинную-случайную-строку>")
	}
	return hex.EncodeToString(b)
}

// denyLog не даёт отказам залить журнал. Порт, открытый в интернет, находят
// сканеры за часы, и без ограничения лог сервиса за сутки превращается в их
// список — а строка про настоящий отказ тонет в нём.
type denyLog struct {
	mu      sync.Mutex
	last    time.Time
	skipped int
}

func (d *denyLog) note(lg *log.Logger, format string, a ...any) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.last.IsZero() && time.Since(d.last) < 30*time.Second {
		d.skipped++
		return
	}
	msg := fmt.Sprintf(format, a...)
	if d.skipped > 0 {
		msg += fmt.Sprintf(i18n.Tr(" (и ещё %d таких же за последние полминуты)"), d.skipped)
		d.skipped = 0
	}
	d.last = time.Now()
	lg.Print(msg)
}

// denyReason — что именно не так с подтверждением. Три разных случая раньше
// отвечали одним «неверный токен», и человек, забывший заголовок, искал
// опечатку в секрете.
//
// Имя переменной окружения в ответе не называется намеренно: его видит каждый,
// кто постучался в порт, а починить по нему он всё равно ничего не может —
// в отличие от того, кто держит сервис и читает лог.
func denyReason(r *http.Request) string {
	if r.Header.Get("X-Slack-Signature") != "" {
		return i18n.Tr("подпись Slack не сошлась: не задан подписывающий секрет или запрос не от Slack")
	}
	h := r.Header.Get("Authorization")
	switch {
	case h == "":
		return i18n.Tr("нужен заголовок Authorization: Bearer <общий секрет>")
	case !strings.HasPrefix(h, "Bearer "):
		return i18n.Tr("заголовок Authorization есть, но он не Bearer")
	}
	return i18n.Tr("неверный секрет")
}

// authenticate возвращает, чем подтверждён запрос: "токен" — общий секрет в
// заголовке, "slack" — подпись Slack. Пустая строка означает отказ.
//
// Токена в query-параметре больше нет: URL целиком оседает в логах любого
// обратного прокси, а этот токен даёт право завести бота в произвольный
// созвон.
func authenticate(r *http.Request, body []byte, token, slackSecret string) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		got := strings.TrimPrefix(h, "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1 {
			return i18n.Tr("токен")
		}
	}
	if verifySlackSignature(r, body, slackSecret) {
		return "slack"
	}
	return ""
}

// verifySlackSignature проверяет, что запрос действительно от Slack, по схеме
// из их документации: HMAC-SHA256 от "v0:<таймстемп>:<тело>". Без этого поле
// channel_id — обычные данные из формы, которые может прислать кто угодно, а
// именно по нему follow-up уходит в канал.
func verifySlackSignature(r *http.Request, body []byte, secret string) bool {
	if secret == "" {
		return false
	}
	ts := r.Header.Get("X-Slack-Request-Timestamp")
	unix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	// Защита от повтора старого перехваченного запроса.
	if d := time.Since(time.Unix(unix, 0)); d > 5*time.Minute || d < -5*time.Minute {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "v0:%s:%s", ts, body)
	want := "v0=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(want), []byte(r.Header.Get("X-Slack-Signature")))
}

// parseJoinRequest понимает и JSON, и form-encoded: Slack шлёт слэш-команды
// формой, где ссылка лежит в поле text вперемешку с остальным текстом, а рядом
// приезжает channel_id — канал, из которого позвали. Туда же вернётся ответ,
// но только если запрос подтверждён подписью Slack: иначе обратный адрес стал
// бы способом попросить бота опубликовать чужой follow-up себе в личку.
//
// Четвёртое значение — почему ссылки не вышло. Раньше на всё был один ответ
// «не нашёл ссылку на созвон», и опечатка в JSON выглядела как незнакомая
// площадка.
func parseJoinRequest(r *http.Request, body []byte, via string) (meetURL, title string, reply core.ReplyTo, why error) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var b struct {
			URL   string `json:"url"`
			Title string `json:"title"`
		}
		if err := json.Unmarshal(body, &b); err != nil {
			return "", "", core.ReplyTo{}, fmt.Errorf(i18n.Tr("это не JSON: %w"), err)
		}
		if u := bot.FindMeetURL(b.URL); u != "" {
			return u, b.Title, core.ReplyTo{}, nil
		}
		if strings.TrimSpace(b.URL) == "" {
			return "", "", core.ReplyTo{}, errors.New(i18n.Tr("в JSON нет поля url со ссылкой на созвон"))
		}
		return "", "", core.ReplyTo{}, bot.MeetingLinkError(b.URL)
	}
	form, err := url.ParseQuery(string(body))
	if err != nil {
		return "", "", core.ReplyTo{}, fmt.Errorf(i18n.Tr("тело не разобралось ни как JSON, ни как форма: %w"), err)
	}
	text := strings.Join([]string{
		form.Get("text"), form.Get("url"), form.Get("link"),
	}, " ")
	if ch := form.Get("channel_id"); ch != "" && via == "slack" {
		reply = core.ReplyTo{Kind: "slack", Addr: ch}
	}
	if u := bot.FindMeetURL(text); u != "" {
		return u, form.Get("title"), reply, nil
	}
	// curl без -H "Content-Type: application/json" шлёт JSON как форму, и
	// разбор превращает его в один пустой ключ. Ссылку в теле всё равно видно,
	// а требовать заголовок ради этого — лишний повод для «у меня 400».
	if u := bot.FindMeetURL(string(body)); u != "" {
		return u, form.Get("title"), reply, nil
	}
	// Ссылка в форме приезжает закодированной, поэтому подсказку про
	// незнакомую площадку ищем и в раскодированном теле.
	hint := string(body)
	if un, err := url.QueryUnescape(hint); err == nil {
		hint += " " + un
	}
	return "", "", core.ReplyTo{}, bot.MeetingLinkError(hint)
}
