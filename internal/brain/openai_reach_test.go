package brain

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// «Провайдер настроен» и «провайдер отвечает» — разные вещи, и путать их
// дороже всего у модели на своей машине: настройка безупречна, Ollama не
// запущена, и узнаётся это на первом созвоне, когда час разговора уже записан.
func TestOpenAIReach(t *testing.T) {
	t.Run("отвечает", func(t *testing.T) {
		var seen []string
		var auth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = append(seen, r.Method+" "+r.URL.Path)
			auth = r.Header.Get("Authorization")
			w.Write([]byte(`{"data":[]}`))
		}))
		defer srv.Close()
		cfg := oaConfig(srv.URL)
		t.Setenv("STENO_TEST_REACH_OK_KEY", "sk-тайна")
		cfg.Brain.OpenAI.APIKeyEnv = "STENO_TEST_REACH_OK_KEY"

		if ok, why := OpenAIReach(context.Background(), cfg); !ok {
			t.Errorf("живой сервер объявлен мёртвым: %s", why)
		}
		// Ключ обязан уйти и в этот запрос: без него провайдер ответит 401, и
		// doctor объявит рабочую установку сломанной.
		if auth != "Bearer sk-тайна" {
			t.Errorf("ключ не уехал в проверку: %q", auth)
		}
		// Проверка обязана быть бесплатной: запрос к самой модели стоит денег и
		// у платного провайдера, и минуты у местной.
		if len(seen) != 1 || seen[0] != "GET /v1/models" {
			t.Errorf("проверка доступности сходила не туда: %v", seen)
		}
	})

	t.Run("не запущен", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := ln.Addr().String()
		ln.Close()
		cfg := oaConfig("http://" + addr)
		ok, why := OpenAIReach(context.Background(), cfg)
		if ok {
			t.Fatal("до несуществующего сервера «достучались»")
		}
		if !strings.Contains(why, "запущена") {
			t.Errorf("не подсказали, что модель не поднята: %s", why)
		}
	})

	t.Run("ключ не принят", func(t *testing.T) {
		_, cfg := oaServe(t, func(n int, w http.ResponseWriter) {})
		t.Setenv("STENO_TEST_REACH_KEY", "плохой")
		cfg.Brain.OpenAI.APIKeyEnv = "STENO_TEST_REACH_KEY"
		// Отвечаем 401 на любой запрос.
		srv := newStatusServer(t, http.StatusUnauthorized, `{"error":{"message":"Invalid API Key"}}`)
		cfg.Brain.OpenAI.BaseURL = srv + "/v1"
		ok, why := OpenAIReach(context.Background(), cfg)
		if ok {
			t.Fatal("сервер, не принявший ключ, объявлен рабочим")
		}
		if !strings.Contains(why, "STENO_TEST_REACH_KEY") {
			t.Errorf("не сказали, какую переменную чинить: %s", why)
		}
	})

	// Метод «список моделей» есть не у всех, и его отсутствие — не поломка:
	// нам довольно того, что до сервера дошли.
	t.Run("нет списка моделей — всё равно доступен", func(t *testing.T) {
		srv := newStatusServer(t, http.StatusNotFound, "not found")
		cfg := oaConfig(srv)
		cfg.Brain.OpenAI.BaseURL = srv + "/v1"
		if ok, why := OpenAIReach(context.Background(), cfg); !ok {
			t.Errorf("сервер без /models объявлен недоступным: %s", why)
		}
	})
}
