package brain

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/sur1cat/steno/internal/core"
)

// Настоящий adapters/ollama.sh — на поддельной Ollama.
//
// Это не проверка Ollama, а проверка того самого файла, который лежит в
// репозитории и с которого люди будут списывать свои адаптеры. Сам Ollama на
// машине не установлен и ставить его не нужно: договор с ним — обычный HTTP,
// и подделать его дешевле, чем скачать три гигабайта весов.
//
// Чего эта проверка не заменяет: живого прогона с настоящей моделью. Что
// Ollama действительно соблюдает схему в поле format, здесь не проверено — это
// обещание её документации, и подтвердить его может только запуск.

func ollamaAdapter(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("скрипты на bash — не для Windows")
	}
	for _, bin := range []string{"bash", "jq", "curl"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("для адаптера нужен %s", bin)
		}
	}
	path, err := filepath.Abs(filepath.Join("..", "..", "adapters", "ollama.sh"))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// fakeOllama — поддельный сервер Ollama. Возвращает адрес и то, что он получил.
func fakeOllama(t *testing.T, reply func(w http.ResponseWriter)) (string, func() map[string]any) {
	t.Helper()
	var mu sync.Mutex
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			http.Error(w, "не тот путь: "+r.URL.Path, http.StatusNotFound)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		got = body
		mu.Unlock()
		reply(w)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, func() map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return got
	}
}

func ollamaConfig(t *testing.T, host string) *core.Config {
	t.Helper()
	t.Setenv("OLLAMA_HOST", host)
	cfg := core.DefaultConfig()
	cfg.Brain.Provider = core.ProviderCommand
	cfg.LLM.Cmd = []string{ollamaAdapter(t)}
	cfg.LLM.Model = "llama3.1"
	return cfg
}

// Схема доезжает до Ollama полем format — там она ограничивает генерацию, а не
// просит соблюсти себя словами. Роли при этом раздельны: правила разбора
// должны стоять там, куда модель смотрит как на правила.
func TestOllamaAdapterSendsSchemaAsFormat(t *testing.T) {
	host, got := fakeOllama(t, func(w http.ResponseWriter) {
		json.NewEncoder(w).Encode(map[string]any{
			"model":             "llama3.1:8b",
			"message":           map[string]any{"role": "assistant", "content": `{"title":"планёрка"}`},
			"prompt_eval_count": 1200,
			"eval_count":        300,
		})
	})
	cfg := ollamaConfig(t, host)

	out, spend, err := askViaCmd(context.Background(), cfg, "правила", "расшифровка", testSchema(), 4321)
	if err != nil {
		t.Fatalf("адаптер не отработал: %v", err)
	}
	if out != `{"title":"планёрка"}` {
		t.Errorf("ответ поехал: %q", out)
	}

	req := got()
	if req["model"] != "llama3.1" {
		t.Errorf("модель: %v", req["model"])
	}
	if req["stream"] != false {
		t.Errorf("поток не выключен: %v — адаптер разбирает ответ целиком", req["stream"])
	}
	msgs, _ := req["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("сообщений %d, ждали два", len(msgs))
	}
	first, _ := msgs[0].(map[string]any)
	second, _ := msgs[1].(map[string]any)
	if first["role"] != "system" || first["content"] != "правила" {
		t.Errorf("системная часть склеена или потеряна: %v", first)
	}
	if second["role"] != "user" || second["content"] != "расшифровка" {
		t.Errorf("пользовательская часть: %v", second)
	}
	sent, _ := json.Marshal(req["format"])
	want, _ := json.Marshal(testSchema())
	if string(sent) != string(want) {
		t.Errorf("схема доехала не та:\n  %s\n  %s", sent, want)
	}
	opts, _ := req["options"].(map[string]any)
	if opts["num_predict"] != float64(4321) {
		t.Errorf("потолок ответа не доехал: %v", req["options"])
	}

	// Учёт: токены Ollama отдаёт своими полями, а ноль в деньгах — утверждение,
	// а не заглушка: модель считает на этой же машине.
	if spend.Input != 1200 || spend.Output != 300 {
		t.Errorf("токены: %+v", spend)
	}
	if !spend.PriceKnown || spend.USD != 0 {
		t.Errorf("у местной модели расход — известный ноль: %+v", spend)
	}
	if spend.Model != "llama3.1:8b" {
		t.Errorf("адаптер не назвал, чем считал: %q", spend.Model)
	}
}

// Без схемы поля format быть не должно вовсе: с ним Ollama потребует JSON и
// там, где нужен обычный текст, — например, в справке о проекте.
func TestOllamaAdapterOmitsFormatWithoutSchema(t *testing.T) {
	host, got := fakeOllama(t, func(w http.ResponseWriter) {
		json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{"content": "Проект про приём платежей."},
		})
	})
	cfg := ollamaConfig(t, host)

	out, _, err := askViaCmd(context.Background(), cfg, "s", "u", nil, 0)
	if err != nil {
		t.Fatalf("адаптер не отработал: %v", err)
	}
	if out != "Проект про приём платежей." {
		t.Errorf("текст поехал: %q", out)
	}
	req := got()
	if _, ok := req["format"]; ok {
		t.Errorf("format послан без схемы: %v", req["format"])
	}
	// Потолок не задан — options тоже не нужны: пустой num_predict Ollama
	// поняла бы как «ноль токенов».
	if _, ok := req["options"]; ok {
		t.Errorf("options послан без потолка: %v", req["options"])
	}
}

// Отказы Ollama должны доходить до человека словами, а не превращаться в
// «модель ответила странно».
func TestOllamaAdapterExplainsFailures(t *testing.T) {
	t.Run("модель не скачана", func(t *testing.T) {
		host, _ := fakeOllama(t, func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]any{"error": `model "llama3.1" not found`})
		})
		cfg := ollamaConfig(t, host)
		_, _, err := askViaCmd(context.Background(), cfg, "s", "u", nil, 0)
		if err == nil {
			t.Fatal("отказ Ollama проглочен")
		}
		if !strings.Contains(err.Error(), "ollama pull llama3.1") {
			t.Errorf("не подсказали, чем чинить:\n%v", err)
		}
	})

	t.Run("пустой ответ", func(t *testing.T) {
		host, _ := fakeOllama(t, func(w http.ResponseWriter) {
			json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"content": ""}})
		})
		cfg := ollamaConfig(t, host)
		_, _, err := askViaCmd(context.Background(), cfg, "s", "u", nil, 0)
		if err == nil {
			t.Fatal("пустой ответ принят за ответ")
		}
		// Жалуется именно адаптер, а не steno следом за ним: steno скажет
		// «скрипт вернул пустой ответ» и оставит человека гадать, чей это
		// пустой ответ — скрипта или модели за ним.
		if !strings.Contains(err.Error(), "ollama вернул пустой ответ") {
			t.Errorf("адаптер промолчал о пустом ответе Ollama:\n%v", err)
		}
	})

	t.Run("сервер не поднят", func(t *testing.T) {
		cfg := ollamaConfig(t, "http://127.0.0.1:1")
		_, _, err := askViaCmd(context.Background(), cfg, "s", "u", nil, 0)
		if err == nil {
			t.Fatal("запрос в никуда прошёл")
		}
		if !strings.Contains(err.Error(), "ollama serve") {
			t.Errorf("не подсказали, что сервер не запущен:\n%v", err)
		}
	})

	t.Run("модель не выбрана", func(t *testing.T) {
		host, _ := fakeOllama(t, func(w http.ResponseWriter) {})
		cfg := ollamaConfig(t, host)
		cfg.LLM.Model = ""
		t.Setenv("OLLAMA_MODEL", "")
		_, _, err := askViaCmd(context.Background(), cfg, "s", "u", nil, 0)
		if err == nil || !strings.Contains(err.Error(), "llm.model") {
			t.Errorf("адаптер не сказал, что модель не выбрана:\n%v", err)
		}
	})
}

// Адаптер говорит с человеком на языке steno — как и адаптеры расшифровки.
func TestOllamaAdapterSpeaksStenoLanguage(t *testing.T) {
	cfg := ollamaConfig(t, "http://127.0.0.1:1")
	_, _, err := askViaCmd(context.Background(), cfg, "s", "u", nil, 0)
	if err == nil {
		t.Fatal("запрос в никуда прошёл")
	}
	// Тесты этого пакета идут на русском (см. lang_test.go), значит и адаптер
	// обязан ответить по-русски: STENO_LANG до него доезжает.
	if !strings.Contains(err.Error(), "не ответил") {
		t.Errorf("адаптер ответил не на языке steno:\n%v", err)
	}
}
