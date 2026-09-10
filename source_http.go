package main

import (
	"context"
	"crypto/hmac"
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
	"time"
)

// Третий способ — тонкий эндпоинт под всё остальное: слэш-команда Slack,
// ярлык на телефоне, curl из скрипта. Ничего специфичного для Slack здесь нет,
// поэтому он же годится под что угодно ещё.
//
//	curl -X POST http://steno:8787/join \
//	     -H "Authorization: Bearer $STENO_HTTP_TOKEN" \
//	     -d '{"url":"https://meet.google.com/abc-defg-hij"}'

type httpSource struct {
	cfg *Config
	d   *Dispatcher
	log *log.Logger

	slackSecret string
}

func (s *httpSource) Name() string { return "http" }

func (s *httpSource) Run(ctx context.Context) error {
	token, err := secret(s.cfg.HTTP.TokenEnv, "HTTP")
	if err != nil {
		return err
	}
	addr := s.cfg.HTTP.Addr
	if addr == "" {
		addr = ":8787"
	}
	// Без подписывающего секрета слэш-команды Slack не принимаются: поле
	// channel_id в них ничем не подтверждено.
	s.slackSecret = os.Getenv(s.cfg.Slack.SigningSecretEnv)
	if s.slackSecret == "" {
		s.log.Printf(tr("http: %s не задан — слэш-команды Slack приниматься не будут"),
			s.cfg.Slack.SigningSecretEnv)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/join", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, tr("нужен POST"), http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, tr("не смог прочитать тело"), http.StatusBadRequest)
			return
		}
		via := authenticate(r, body, token, s.slackSecret)
		if via == "" {
			http.Error(w, tr("неверный токен"), http.StatusUnauthorized)
			return
		}
		meetURL, title, reply := parseJoinRequest(r, body, via)
		if meetURL == "" {
			// Отказ должен называть причину: «не умею Zoom» и «ссылки в
			// запросе вообще нет» чинятся по-разному. Ссылка в форме приезжает
			// закодированной, поэтому подсказку ищем и в раскодированном теле.
			hint := string(body)
			if un, err := url.QueryUnescape(hint); err == nil {
				hint += " " + un
			}
			http.Error(w, meetingLinkError(hint).Error(), http.StatusBadRequest)
			return
		}
		m := &Meeting{
			ID:        newID(time.Now()),
			Title:     firstNonEmpty(title, tr("Созвон по ссылке")),
			MeetURL:   meetURL,
			StartedAt: time.Now(),
			Status:    "recording",
			ReplyTo:   reply,
		}
		// Именно ctx сервиса, а не r.Context(): контекст запроса умирает
		// вместе с ответом, а запись созвона живёт ещё час.
		var msg string
		code := http.StatusOK
		switch s.d.Start(ctx, adHocKey(meetURL, time.Now()), m, "HTTP ("+via+")") {
		case Started:
			msg = tr("Иду на ") + meetURL + tr(" — follow-up придёт, когда созвон закончится.")
		case Duplicate:
			msg = tr("Я уже иду на этот созвон.")
		case NoCapacity:
			msg = tr("Сейчас пишу максимум созвонов сразу — на этот не пойду.")
			code = http.StatusServiceUnavailable
		default:
			msg = tr("Не смог записать заявку, посмотри лог сервиса.")
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
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
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

	s.log.Printf(tr("http: слушаю %s (POST /join)"), addr)
	err = srv.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	<-stopped
	return nil
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
			return tr("токен")
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
func parseJoinRequest(r *http.Request, body []byte, via string) (meetURL, title string, reply ReplyTo) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var b struct {
			URL   string `json:"url"`
			Title string `json:"title"`
		}
		if err := json.Unmarshal(body, &b); err != nil {
			return "", "", ReplyTo{}
		}
		return findMeetURL(b.URL), b.Title, ReplyTo{}
	}
	form, err := url.ParseQuery(string(body))
	if err != nil {
		return "", "", ReplyTo{}
	}
	text := strings.Join([]string{
		form.Get("text"), form.Get("url"), form.Get("link"),
	}, " ")
	if ch := form.Get("channel_id"); ch != "" && via == "slack" {
		reply = ReplyTo{Kind: "slack", Addr: ch}
	}
	if u := findMeetURL(text); u != "" {
		return u, form.Get("title"), reply
	}
	// curl без -H "Content-Type: application/json" шлёт JSON как форму, и
	// разбор превращает его в один пустой ключ. Ссылку в теле всё равно видно,
	// а требовать заголовок ради этого — лишний повод для «у меня 400».
	return findMeetURL(string(body)), form.Get("title"), reply
}
