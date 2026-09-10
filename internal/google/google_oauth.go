package google

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
)

// Второй способ пустить steno в Google — кнопкой, а не файлом.
//
// Первый (service-account с domain-wide delegation) остаётся и нужен компании:
// только он умеет читать календари сорока человек, ни разу никого не спросив.
// Но для одного человека со своими созвонами он бессмысленно тяжёл: чтобы
// получить этот файл, надо завести проект в Google Cloud, включить три API,
// создать сервисный аккаунт, скачать JSON и прописать делегирование в админке
// домена — которой у частного человека попросту нет.
//
// Здесь человек нажимает кнопку, Google спрашивает «пустить steno в календарь?»,
// он отвечает да. Дальше steno работает от его имени и видит ровно то, что
// видит он сам. Для своих созвонов этого достаточно: встречи, на которые тебя
// позвали, лежат в твоём календаре.
//
// Client ID заводит один раз тот, кто разворачивает steno, — через `steno
// setup`. Человеку в панели про это знать не нужно, он видит одну кнопку.

// GoogleScopes — то, что запрашивается у человека одним разом. Просить по
// одному на каждый канал значит показать три экрана согласия вместо одного, а
// отказ на втором оставит установку наполовину рабочей.
var GoogleScopes = []string{
	"https://www.googleapis.com/auth/calendar.readonly",
	"https://www.googleapis.com/auth/gmail.readonly",
	"https://www.googleapis.com/auth/drive",
	"https://www.googleapis.com/auth/userinfo.email",
}

// GoogleToken — то, что лежит на диске после согласия.
type GoogleToken struct {
	Account string        `json:"account"`
	Scopes  []string      `json:"scopes"`
	Token   *oauth2.Token `json:"token"`
}

func GoogleTokenPath(cfg *core.Config) string {
	return filepath.Join(cfg.DataDir, "google-token.json")
}

// Токен лежит файлом, а не в базе. База — это все расшифровки всех разговоров,
// и класть туда ключ от почты и Drive того же человека значит удваивать цену
// одной утечки. Права 0600 по той же причине, что и у .env.
func SaveGoogleToken(cfg *core.Config, t *GoogleToken) error {
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(GoogleTokenPath(cfg), b, 0o600)
}

func LoadGoogleToken(cfg *core.Config) (*GoogleToken, error) {
	b, err := os.ReadFile(GoogleTokenPath(cfg))
	if err != nil {
		return nil, err
	}
	var t GoogleToken
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, fmt.Errorf(i18n.Tr("разбор %s: %w"), GoogleTokenPath(cfg), err)
	}
	if t.Token == nil {
		return nil, fmt.Errorf(i18n.Tr("в %s нет токена"), GoogleTokenPath(cfg))
	}
	return &t, nil
}

func ForgetGoogleToken(cfg *core.Config) error {
	err := os.Remove(GoogleTokenPath(cfg))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// OauthConfig собирает настройки OAuth. Секрет для приложения этого типа
// («Desktop app») Google секретом не считает: он уезжает на машину человека
// вместе с программой. Хранится он всё равно переменной окружения — не потому
// что тайна, а чтобы конфиг можно было положить в репозиторий целиком.
func OauthConfig(cfg *core.Config, redirect string) (*oauth2.Config, error) {
	id := strings.TrimSpace(cfg.Google.ClientID)
	if id == "" {
		return nil, errors.New(i18n.Tr("не задан google.client_id — заводится один раз через `steno setup`"))
	}
	sec, err := core.Secret(cfg.Google.ClientSecretEnv, "")
	if err != nil {
		return nil, fmt.Errorf(i18n.Tr("не задан %s"), cfg.Google.ClientSecretEnv)
	}
	return &oauth2.Config{
		ClientID:     id,
		ClientSecret: sec,
		Endpoint:     google.Endpoint,
		RedirectURL:  redirect,
		Scopes:       GoogleScopes,
	}, nil
}

// GoogleAccountEmail спрашивает у Google, чей ящик подключили. Знать это надо:
// от чужого имени по кнопке steno работать не может, и сверять, тот ли аккаунт
// подключён, больше не с чем. Заодно это единственная проверка, что выданный
// доступ вообще работает, — а узнать об обратном лучше здесь, чем на созвоне.
func GoogleAccountEmail(ctx context.Context, oc *oauth2.Config, tok *oauth2.Token) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://www.googleapis.com/oauth2/v3/userinfo", nil)
	if err != nil {
		return "", err
	}
	resp, err := oc.Client(ctx, tok).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf(i18n.Tr("Google ответил %s"), resp.Status)
	}
	var body struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&body); err != nil {
		return "", err
	}
	if body.Email == "" {
		return "", errors.New(i18n.Tr("Google не сказал, чей это ящик"))
	}
	return body.Email, nil
}

// GoogleOAuthReady — можно ли вообще показывать кнопку. Отдельно от «подключён
// ли»: кнопка без client_id ведёт в ошибку, и честнее сказать об этом сразу.
func GoogleOAuthReady(cfg *core.Config) bool {
	if strings.TrimSpace(cfg.Google.ClientID) == "" {
		return false
	}
	_, err := core.Secret(cfg.Google.ClientSecretEnv, "")
	return err == nil
}

// googleOAuthClient — клиент от имени подключившегося человека.
func googleOAuthClient(ctx context.Context, cfg *core.Config, scopes []string) (option.ClientOption, error) {
	t, err := LoadGoogleToken(cfg)
	if err != nil {
		return nil, err
	}
	if missing := MissingScopes(t.Scopes, scopes); len(missing) > 0 {
		return nil, fmt.Errorf(
			i18n.Tr("Google подключён, но без доступа к %s — нажми «Подключить Google» ещё раз"),
			strings.Join(HumanScopes(missing), i18n.Tr(" и ")))
	}
	oc, err := OauthConfig(cfg, "")
	if err != nil {
		return nil, err
	}
	// Контекст без отмены: клиент живёт дольше запроса, которым его создали, и
	// сам обновляет токен по истечении.
	src := oc.TokenSource(context.WithoutCancel(ctx), t.Token)
	return option.WithHTTPClient(oauth2.NewClient(context.WithoutCancel(ctx), &savingTokenSource{
		cfg: cfg, stored: t, inner: src,
	})), nil
}

// savingTokenSource дописывает обновлённый refresh-токен обратно в файл. Без
// этого через неделю-другую (или после отзыва) steno продолжал бы ходить со
// старым токеном и падать, а человек видел бы «подключено».
type savingTokenSource struct {
	cfg    *core.Config
	stored *GoogleToken
	inner  oauth2.TokenSource
	mu     sync.Mutex
}

func (s *savingTokenSource) Token() (*oauth2.Token, error) {
	t, err := s.inner.Token()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stored.Token == nil || t.AccessToken != s.stored.Token.AccessToken {
		// RefreshToken Google присылает только при первом согласии — при
		// обновлении поле пустое, и записать его поверх значит потерять доступ.
		if t.RefreshToken == "" {
			t.RefreshToken = s.stored.Token.RefreshToken
		}
		s.stored.Token = t
		_ = SaveGoogleToken(s.cfg, s.stored)
	}
	return t, nil
}

func MissingScopes(have, want []string) []string {
	set := map[string]bool{}
	for _, s := range have {
		set[s] = true
	}
	var out []string
	for _, s := range want {
		if !set[s] {
			out = append(out, s)
		}
	}
	return out
}

// HumanScopes — область доступа словами. «https://www.googleapis.com/auth/
// calendar.readonly» человеку не говорит ничего.
func HumanScopes(scopes []string) []string {
	names := map[string]string{
		"https://www.googleapis.com/auth/calendar.readonly": i18n.Tr("календарю"),
		"https://www.googleapis.com/auth/gmail.readonly":    i18n.Tr("почте"),
		"https://www.googleapis.com/auth/drive":             i18n.Tr("документам"),
		"https://www.googleapis.com/auth/drive.file":        i18n.Tr("документам"),
		"https://www.googleapis.com/auth/userinfo.email":    i18n.Tr("адресу почты"),
	}
	var out []string
	for _, s := range scopes {
		if n, ok := names[s]; ok {
			out = append(out, n)
		} else {
			out = append(out, s)
		}
	}
	return out
}

// googleState — одноразовая метка обмена. Без неё чужая страница могла бы
// подсунуть свой code и подключить к steno чужой ящик.
type GoogleStates struct {
	mu sync.Mutex
	m  map[string]time.Time
}

func NewGoogleStates() *GoogleStates { return &GoogleStates{m: map[string]time.Time{}} }

func (g *GoogleStates) Issue() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	s := base64.RawURLEncoding.EncodeToString(b)
	g.mu.Lock()
	defer g.mu.Unlock()
	// Заодно чистим просроченные: карта иначе растёт от каждой нажатой и
	// брошенной кнопки.
	for k, t := range g.m {
		if time.Since(t) > 15*time.Minute {
			delete(g.m, k)
		}
	}
	g.m[s] = time.Now()
	return s
}

func (g *GoogleStates) Take(s string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	t, ok := g.m[s]
	delete(g.m, s)
	return ok && time.Since(t) <= 15*time.Minute
}
