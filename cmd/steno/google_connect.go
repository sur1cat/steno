package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/google"
	"github.com/sur1cat/steno/internal/i18n"
)

// googleConnect — согласие Google из терминала, без панели.
//
// Панель необязательна, и путь к Google не вправе через неё лежать: человек,
// который панель не поднимал, всё равно должен мочь нажать «войти». Устроено
// как у gcloud: слушаем петлю на случайном порту, открываем браузер, ждём,
// пока Google вернёт код, меняем его на токен. Настольным OAuth-клиентам
// Google разрешает любой порт на 127.0.0.1, регистрировать адрес не надо.
//
// Если браузера на этой машине нет (сервер по ssh), ссылку можно открыть где
// угодно, а адрес, на который перекинуло, — вставить сюда: код в нём.
func googleConnect(ctx context.Context, path string, in *bufio.Reader) error {
	cfg, err := core.LoadConfig(path)
	if err != nil {
		return err
	}
	_ = loadDotEnv(dotEnvBeside(path))
	if !google.GoogleOAuthReady(cfg) {
		return errors.New(i18n.Tr("сначала Client ID и секрет:  steno google set"))
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer ln.Close()
	redirect := "http://" + ln.Addr().String() + "/callback"
	oc, err := google.OauthConfig(cfg, redirect)
	if err != nil {
		return err
	}
	state := google.NewGoogleStates().Issue()
	authURL := oc.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)

	// Код приходит либо на петлю из браузера, либо строкой от человека.
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("state") != state {
			http.Error(w, i18n.Tr("это не тот вход, который просили — начни заново"), http.StatusBadRequest)
			return
		}
		if e := q.Get("error"); e != "" {
			http.Error(w, i18n.Tr("Google отказал: ")+e, http.StatusBadRequest)
			errCh <- errors.New(i18n.Tr("Google отказал: ") + e)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "<meta charset=utf-8><body style=\"font:16px system-ui;padding:40px\">"+
			i18n.Tr("Готово — эту вкладку можно закрыть, дальше в терминале.")+"</body>")
		codeCh <- q.Get("code")
	})}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	fmt.Println(i18n.Tr("Открываю браузер — там выбери аккаунт и дай согласие."))
	fmt.Println(dim(i18n.Tr("  Google предупредит, что приложение не проверено: это твой же клиент.")))
	fmt.Println(dim(i18n.Tr("  Advanced → Go to steno → отметить всё → Continue.")))
	fmt.Println()
	fmt.Println(dim(i18n.Tr("  Если браузер не открылся или он на другой машине — ссылка:")))
	fmt.Println("  " + authURL)
	fmt.Println(dim(i18n.Tr("  Открой её где угодно и вставь сюда адрес, на который перекинуло:")))
	openBrowser(authURL)

	// Ввод руками читаем параллельно: на сервере петля недостижима из чужого
	// браузера, и код доедет только строкой.
	go func() {
		line, _ := in.ReadString('\n')
		if code := codeFromPasted(strings.TrimSpace(line), state); code != "" {
			codeCh <- code
		} else if strings.TrimSpace(line) != "" {
			errCh <- errors.New(i18n.Tr("в вставленной строке нет кода — нужен адрес целиком, начиная с http://127.0.0.1"))
		}
	}()

	var code string
	select {
	case code = <-codeCh:
	case err := <-errCh:
		return err
	case <-time.After(10 * time.Minute):
		return errors.New(i18n.Tr("не дождался согласия за 10 минут — запусти ещё раз"))
	case <-ctx.Done():
		return ctx.Err()
	}

	tok, err := oc.Exchange(ctx, code)
	if err != nil {
		return fmt.Errorf(i18n.Tr("Google не обменял код на доступ: %w"), err)
	}
	account, err := google.GoogleAccountEmail(ctx, oc, tok)
	if err != nil {
		return err
	}
	if err := google.SaveGoogleToken(cfg, &google.GoogleToken{
		Account: account, Scopes: google.GoogleScopes, Token: tok,
	}); err != nil {
		return err
	}
	fmt.Println(ok(i18n.Tr("вошёл как ") + account))
	fmt.Println(dim(i18n.Tr("  календарь и почта включаются в панели или в steno ui — когда захочешь")))
	return nil
}

// codeFromPasted достаёт код из адреса, который человек вставил руками.
// Принимаем адрес целиком: вырезать из него ?code=… самому — работа не для
// человека, только что прошедшего три экрана Google.
func codeFromPasted(s, state string) string {
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	q := u.Query()
	if q.Get("state") != state {
		return ""
	}
	return q.Get("code")
}

func dotEnvBeside(configPath string) string {
	if i := strings.LastIndex(configPath, string(os.PathSeparator)); i >= 0 {
		return configPath[:i+1] + ".env"
	}
	return ".env"
}
