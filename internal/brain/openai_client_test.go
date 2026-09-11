package brain

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/sur1cat/steno/internal/core"
)

// Совместимый с OpenAI путь проверяется целиком на поддельном сервере.
//
// Ключей на машине, где это писалось, нет ни одного — ни OpenAI, ни Groq, ни
// Anthropic, — и живьём ни один из этих провайдеров не опрошен. Зато проверить
// здесь можно всё, что вообще зависит от нас: что именно мы посылаем, как
// понимаем ответ, и что говорим человеку, когда на том конце отказ, пятисотый,
// оборванное соединение или мусор вместо JSON. Разница между «собралось» и
// «работает» — ровно в этом файле.

// oaRecorder — поддельный сервер: складывает разобранные запросы и отвечает
// заготовленным. Ответов может быть несколько: лесенка отступления делает
// второй и третий запрос, и проверять надо именно их.
type oaRecorder struct {
	mu   sync.Mutex
	got  []map[string]any
	body []string
	// reply вызывается на каждый запрос по счёту.
	reply func(n int, w http.ResponseWriter)
}

func (r *oaRecorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// Считаем только запросы к самой модели. Проверка доступности спрашивает
	// список моделей — она бесплатна и в счёт не идёт; смешав их, тест перестал
	// бы отличать «сходили дважды» от «сходили один раз и проверились».
	if req.Method != http.MethodPost || !strings.HasSuffix(req.URL.Path, "/chat/completions") {
		http.Error(w, "не тот запрос: "+req.Method+" "+req.URL.Path, http.StatusNotFound)
		return
	}
	var raw map[string]any
	_ = json.NewDecoder(req.Body).Decode(&raw)
	r.mu.Lock()
	n := len(r.got)
	r.got = append(r.got, raw)
	r.body = append(r.body, req.Header.Get("Authorization"))
	r.mu.Unlock()
	r.reply(n, w)
}

func (r *oaRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.got)
}

func (r *oaRecorder) req(t *testing.T, n int) map[string]any {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if n >= len(r.got) {
		t.Fatalf("запроса №%d не было, всего запросов %d", n+1, len(r.got))
	}
	return r.got[n]
}

// okBody — обычный успешный ответ совместимого сервера.
func okBody(content string) string {
	b, _ := json.Marshal(map[string]any{
		"choices": []any{map[string]any{
			"message":       map[string]any{"content": content},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{
			"prompt_tokens": 1200, "completion_tokens": 300,
			"prompt_tokens_details": map[string]any{"cached_tokens": 200},
		},
	})
	return string(b)
}

func errBody(msg string) string {
	b, _ := json.Marshal(map[string]any{"error": map[string]any{"message": msg, "type": "invalid_request_error"}})
	return string(b)
}

// oaConfig — конфиг, целиком нацеленный на поддельный сервер.
func oaConfig(url string) *core.Config {
	cfg := core.DefaultConfig()
	cfg.Brain.Provider = core.ProviderOpenAI
	cfg.Brain.OpenAI.BaseURL = url + "/v1"
	cfg.Brain.OpenAI.Model = "test-model"
	// Ключа нет намеренно: у местных моделей его и не бывает, а тесты не должны
	// зависеть от переменных окружения той машины, где их запустили.
	cfg.Brain.OpenAI.APIKeyEnv = ""
	return cfg
}

func oaServe(t *testing.T, reply func(n int, w http.ResponseWriter)) (*oaRecorder, *core.Config) {
	t.Helper()
	rec := &oaRecorder{reply: reply}
	srv := httptest.NewServer(rec)
	t.Cleanup(srv.Close)
	return rec, oaConfig(srv.URL)
}

// newStatusServer — сервер, отвечающий одним и тем же на что угодно.
func newStatusServer(t *testing.T, status int, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func testSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title": map[string]any{"type": "string"},
		},
		"required":             []string{"title"},
		"additionalProperties": false,
	}
}

// Удачный ответ: проверяем и то, что послали, и то, что поняли.
func TestOpenAISendsStrictSchemaAndReadsAnswer(t *testing.T) {
	rec, cfg := oaServe(t, func(n int, w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, okBody(`{"title":"планёрка"}`))
	})
	cfg.Brain.OpenAI.Prices = map[string]core.Price{
		"test-model": {Input: 10, Output: 20, CacheRead: 1},
	}

	out, spend, err := askViaOpenAI(context.Background(), cfg, "правила", "расшифровка", testSchema(), 4321)
	if err != nil {
		t.Fatalf("удачный ответ не прошёл: %v", err)
	}
	if out != `{"title":"планёрка"}` {
		t.Errorf("вернули не тело ответа: %q", out)
	}
	if rec.count() != 1 {
		t.Errorf("запросов %d, ждали один", rec.count())
	}

	got := rec.req(t, 0)
	if got["model"] != "test-model" {
		t.Errorf("модель уехала не та: %v", got["model"])
	}
	// Потолок ответа — полем max_completion_tokens: рассуждающие модели OpenAI
	// на max_tokens отвечают четырёхсоткой.
	if got["max_completion_tokens"] != float64(4321) {
		t.Errorf("потолок ответа: %v", got["max_completion_tokens"])
	}
	if _, ok := got["max_tokens"]; ok {
		t.Error("послали оба поля потолка разом")
	}
	msgs, _ := got["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("сообщений %d, ждали два", len(msgs))
	}
	first, _ := msgs[0].(map[string]any)
	second, _ := msgs[1].(map[string]any)
	if first["role"] != "system" || first["content"] != "правила" {
		t.Errorf("системная часть: %v", first)
	}
	if second["role"] != "user" || second["content"] != "расшифровка" {
		t.Errorf("пользовательская часть: %v", second)
	}

	rf, _ := got["response_format"].(map[string]any)
	if rf["type"] != "json_schema" {
		t.Fatalf("схему не попросили: %v", rf)
	}
	js, _ := rf["json_schema"].(map[string]any)
	if js["strict"] != true {
		t.Errorf("strict не выставлен: %v", js["strict"])
	}
	if s, _ := js["name"].(string); s == "" {
		t.Error("у схемы нет имени, а формат его требует")
	}
	sent, _ := json.Marshal(js["schema"])
	want, _ := json.Marshal(testSchema())
	if string(sent) != string(want) {
		t.Errorf("схема доехала не та:\n  послали %s\n  ждали  %s", sent, want)
	}

	// Учёт: prompt_tokens у OpenAI включает прочитанное из кеша, и показывать
	// вход дважды нельзя.
	if spend.Input != 1000 || spend.Output != 300 || spend.CacheRead != 200 {
		t.Errorf("токены посчитаны не так: %+v", spend)
	}
	if !spend.PriceKnown {
		t.Error("цена задана в конфиге, а расход помечен неизвестным")
	}
	// 1000/1e6*10 + 300/1e6*20 + 200/1e6*1 = 0.0162
	if diff := spend.USD - 0.0162; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("цена посчитана неверно: %v", spend.USD)
	}
}

// Ключ уходит заголовком, и только когда он есть.
func TestOpenAISendsKeyWhenGiven(t *testing.T) {
	rec, cfg := oaServe(t, func(n int, w http.ResponseWriter) {
		fmt.Fprint(w, okBody(`{"title":"с ключом"}`))
	})
	t.Setenv("STENO_TEST_OPENAI_KEY", "sk-тайна")
	cfg.Brain.OpenAI.APIKeyEnv = "STENO_TEST_OPENAI_KEY"

	if _, _, err := askViaOpenAI(context.Background(), cfg, "s", "u", testSchema(), 100); err != nil {
		t.Fatalf("запрос с ключом сорвался: %v", err)
	}
	rec.mu.Lock()
	auth := rec.body[0]
	rec.mu.Unlock()
	if auth != "Bearer sk-тайна" {
		t.Errorf("ключ уехал не так: %q", auth)
	}
}

// Модель за этим адресом не умеет json_schema. Отступаем на json_object и
// кладём схему в промпт словами — и это должно случиться само.
func TestOpenAIFallsBackToJSONObject(t *testing.T) {
	rec, cfg := oaServe(t, func(n int, w http.ResponseWriter) {
		if n == 0 {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, errBody("response_format json_schema is not supported for this model"))
			return
		}
		fmt.Fprint(w, okBody(`{"title":"мягче"}`))
	})

	out, _, err := askViaOpenAI(context.Background(), cfg, "правила", "текст", testSchema(), 100)
	if err != nil {
		t.Fatalf("откат не сработал: %v", err)
	}
	if out != `{"title":"мягче"}` {
		t.Errorf("ответ: %q", out)
	}
	if rec.count() != 2 {
		t.Fatalf("запросов %d, ждали два", rec.count())
	}
	second := rec.req(t, 1)
	rf, _ := second["response_format"].(map[string]any)
	if rf["type"] != "json_object" {
		t.Errorf("вторая ступенька просит не json_object: %v", rf)
	}
	msgs, _ := second["messages"].([]any)
	first, _ := msgs[0].(map[string]any)
	sys, _ := first["content"].(string)
	if !strings.Contains(sys, "JSON") || !strings.Contains(sys, `"additionalProperties"`) {
		t.Errorf("схема не уехала в промпт словами:\n%s", sys)
	}
	// Слово JSON в промпте — не украшение: без него OpenAI отклоняет
	// json_object как таковой.
	if !strings.Contains(strings.ToUpper(sys), "JSON") {
		t.Error("в промпте нет слова JSON, а json_object его требует")
	}
}

// Сервер не знает и про json_object. Последняя ступенька — просто просьба
// словами, без единого параметра формата.
func TestOpenAIFallsBackToWordsOnly(t *testing.T) {
	rec, cfg := oaServe(t, func(n int, w http.ResponseWriter) {
		if n < 2 {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, errBody("unknown parameter: response_format"))
			return
		}
		fmt.Fprint(w, okBody("```json\n{\"title\":\"словами\"}\n```"))
	})

	out, _, err := askViaOpenAI(context.Background(), cfg, "правила", "текст", testSchema(), 100)
	if err != nil {
		t.Fatalf("вторая ступенька отката не сработала: %v", err)
	}
	if rec.count() != 3 {
		t.Fatalf("запросов %d, ждали три", rec.count())
	}
	third := rec.req(t, 2)
	if _, ok := third["response_format"]; ok {
		t.Errorf("на последней ступеньке всё ещё просим формат: %v", third["response_format"])
	}
	// Модель обернула ответ в блок кода — разбор не должен на этом падать.
	if out != `{"title":"словами"}` {
		t.Errorf("JSON не вынули из блока кода: %q", out)
	}
}

// json_mode задан руками — отступать нельзя. Человек просил именно схему, и
// тихо подсунуть ему разбор похуже значит соврать.
func TestOpenAIPinnedSchemaModeDoesNotDowngrade(t *testing.T) {
	rec, cfg := oaServe(t, func(n int, w http.ResponseWriter) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, errBody("response_format is not supported"))
	})
	cfg.Brain.OpenAI.JSONMode = "schema"

	_, _, err := askViaOpenAI(context.Background(), cfg, "s", "u", testSchema(), 100)
	if err == nil {
		t.Fatal("отказ проглочен")
	}
	if rec.count() != 1 {
		t.Errorf("запросов %d — при заданном json_mode должен быть ровно один", rec.count())
	}
	if !strings.Contains(err.Error(), "отклонил запрос") {
		t.Errorf("невнятный отказ: %v", err)
	}
}

// json_mode=object — начинаем сразу со второй ступеньки, лишнего запроса нет.
func TestOpenAIPinnedObjectModeStartsLower(t *testing.T) {
	rec, cfg := oaServe(t, func(n int, w http.ResponseWriter) {
		fmt.Fprint(w, okBody(`{"title":"сразу"}`))
	})
	cfg.Brain.OpenAI.JSONMode = "object"

	if _, _, err := askViaOpenAI(context.Background(), cfg, "s", "u", testSchema(), 100); err != nil {
		t.Fatalf("запрос сорвался: %v", err)
	}
	if rec.count() != 1 {
		t.Fatalf("запросов %d, ждали один", rec.count())
	}
	rf, _ := rec.req(t, 0)["response_format"].(map[string]any)
	if rf["type"] != "json_object" {
		t.Errorf("начали не со второй ступеньки: %v", rf)
	}
}

// Сервер не знает поля max_completion_tokens — так отвечает половина
// совместимых. Меняем имя поля и повторяем.
func TestOpenAISwapsTokenLimitField(t *testing.T) {
	rec, cfg := oaServe(t, func(n int, w http.ResponseWriter) {
		if n == 0 {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, errBody("Unrecognized request argument supplied: max_completion_tokens"))
			return
		}
		fmt.Fprint(w, okBody(`{"title":"по-старому"}`))
	})

	if _, _, err := askViaOpenAI(context.Background(), cfg, "s", "u", testSchema(), 777); err != nil {
		t.Fatalf("подмена поля потолка не сработала: %v", err)
	}
	if rec.count() != 2 {
		t.Fatalf("запросов %d, ждали два", rec.count())
	}
	second := rec.req(t, 1)
	if second["max_tokens"] != float64(777) {
		t.Errorf("во втором запросе нет max_tokens: %v", second)
	}
	if _, ok := second["max_completion_tokens"]; ok {
		t.Error("во втором запросе осталось max_completion_tokens")
	}
	// Формат при этом трогать не должны: жаловались не на него.
	rf, _ := second["response_format"].(map[string]any)
	if rf["type"] != "json_schema" {
		t.Errorf("заодно уронили схему: %v", rf)
	}
}

// Сервер, которому не нравится всё сразу. Лесенка обязана дойти до низа ровно
// один раз и остановиться, а не ходить по кругу.
//
// Проверяем не «сколько-нибудь немного запросов», а всю последовательность
// целиком: сильная ступенька, слабее, ещё слабее, и напоследок другое имя поля
// потолка. Порядок здесь и есть поведение — «не больше четырёх» проходило бы и
// на лесенке, идущей задом наперёд.
func TestOpenAIWalksDownTheLadderExactlyOnce(t *testing.T) {
	rec, cfg := oaServe(t, func(n int, w http.ResponseWriter) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, errBody("response_format and max_completion_tokens are both wrong"))
	})

	if _, _, err := askViaOpenAI(context.Background(), cfg, "s", "u", testSchema(), 100); err == nil {
		t.Fatal("бесконечный отказ проглочен")
	}
	if rec.count() != 4 {
		t.Fatalf("сходили %d раз, а ступенек ровно четыре", rec.count())
	}

	want := []struct {
		format string // "" — формат не просим вовсе
		field  string
	}{
		{"json_schema", "max_completion_tokens"},
		{"json_object", "max_completion_tokens"},
		{"", "max_completion_tokens"},
		{"", "max_tokens"},
	}
	for i, w := range want {
		got := rec.req(t, i)
		format := ""
		if rf, ok := got["response_format"].(map[string]any); ok {
			format, _ = rf["type"].(string)
		}
		if format != w.format {
			t.Errorf("запрос %d просит формат %q, ждали %q", i+1, format, w.format)
		}
		if _, ok := got[w.field]; !ok {
			t.Errorf("в запросе %d нет поля %s: %v", i+1, w.field, got)
		}
	}
}

// Отказы, из которых человек должен понять, что чинить.
func TestOpenAIExplainsFailures(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   []string
		absent []string
	}{
		{
			name: "ключ не принят", status: http.StatusUnauthorized,
			body: errBody("Incorrect API key provided"),
			want: []string{"не принял ключ", "STENO_TEST_KEY_ENV"},
		},
		{
			name: "нет такой модели", status: http.StatusNotFound,
			body: errBody("The model `test-model` does not exist"),
			want: []string{"test-model", "/v1"},
		},
		{
			name: "лимит запросов", status: http.StatusTooManyRequests,
			body: errBody("Rate limit reached"),
			want: []string{"по лимиту запросов"},
		},
		{
			name: "кончились деньги", status: http.StatusPaymentRequired,
			body: errBody("Insufficient credits"),
			want: []string{"по деньгам"},
		},
		{
			name: "пятисотый", status: http.StatusInternalServerError,
			body: "<html>502 Bad Gateway</html>",
			want: []string{"500", "его сторона"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, cfg := oaServe(t, func(n int, w http.ResponseWriter) {
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			})
			t.Setenv("STENO_TEST_KEY_ENV", "sk-тайна")
			cfg.Brain.OpenAI.APIKeyEnv = "STENO_TEST_KEY_ENV"

			_, _, err := askViaOpenAI(context.Background(), cfg, "s", "u", testSchema(), 100)
			if err == nil {
				t.Fatal("отказ проглочен")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("в сообщении нет %q:\n%v", w, err)
				}
			}
			for _, w := range tc.absent {
				if strings.Contains(err.Error(), w) {
					t.Errorf("в сообщении есть лишнее %q:\n%v", w, err)
				}
			}
			// Секрет не должен попасть в текст ошибки ни при каком коде: её
			// пишут в журнал, а журнал читают через плечо.
			if strings.Contains(err.Error(), "sk-тайна") {
				t.Errorf("ключ утёк в сообщение об ошибке: %v", err)
			}
		})
	}
}

// Мусор вместо JSON. Сюда попадают все, кто поставил прокси и получил его
// страницу с ошибкой вместо ответа модели.
func TestOpenAIRejectsGarbage(t *testing.T) {
	_, cfg := oaServe(t, func(n int, w http.ResponseWriter) {
		fmt.Fprint(w, "<html>привет</html>")
	})
	_, _, err := askViaOpenAI(context.Background(), cfg, "s", "u", testSchema(), 100)
	if err == nil {
		t.Fatal("мусор принят за ответ")
	}
	if !strings.Contains(err.Error(), "не JSON") {
		t.Errorf("невнятная жалоба на мусор: %v", err)
	}
}

// Двухсотка с полем error — так отвечает часть совместимых серверов.
func TestOpenAIReadsErrorInsideOKResponse(t *testing.T) {
	_, cfg := oaServe(t, func(n int, w http.ResponseWriter) {
		fmt.Fprint(w, errBody("model is loading"))
	})
	_, _, err := askViaOpenAI(context.Background(), cfg, "s", "u", nil, 100)
	if err == nil {
		t.Fatal("отказ, приехавший двухсоткой, проглочен")
	}
	if !strings.Contains(err.Error(), "model is loading") {
		t.Errorf("не показали, что сказал сервер: %v", err)
	}
}

// Оборванное соединение: сервер принял запрос и замолчал.
func TestOpenAIBrokenConnection(t *testing.T) {
	rec := &oaRecorder{reply: func(n int, w http.ResponseWriter) {}}
	srv := httptest.NewServer(rec)
	cfg := oaConfig(srv.URL)
	// Рвём соединение изнутри обработчика: клиент уже отправил запрос и ждёт.
	rec.reply = func(n int, w http.ResponseWriter) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("не смогли перехватить соединение")
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		conn.Close()
	}
	defer srv.Close()

	_, _, err := askViaOpenAI(context.Background(), cfg, "s", "u", testSchema(), 100)
	if err == nil {
		t.Fatal("оборванное соединение принято за ответ")
	}
	// Местный адрес — значит подсказка про «а модель запущена?».
	if !strings.Contains(err.Error(), "не отвечает") && !strings.Contains(err.Error(), "не достучались") {
		t.Errorf("невнятная жалоба на обрыв: %v", err)
	}
}

// Сервера нет вовсе. Самый частый первый запуск с Ollama: адрес правильный,
// модель не запущена.
func TestOpenAIServerNotRunning(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // порт заведомо свободен и заведомо никем не слушается

	cfg := oaConfig("http://" + addr)
	_, _, err = askViaOpenAI(context.Background(), cfg, "s", "u", testSchema(), 100)
	if err == nil {
		t.Fatal("запрос в никуда прошёл")
	}
	if !strings.Contains(err.Error(), "запущена") {
		t.Errorf("не подсказали, что модель на этой машине не поднята: %v", err)
	}
}

// Ответ, который не поместился в потолок, — это не «модель ничего не сказала».
func TestOpenAITruncatedAnswer(t *testing.T) {
	_, cfg := oaServe(t, func(n int, w http.ResponseWriter) {
		b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
			"message": map[string]any{"content": `{"title":"обрыв`}, "finish_reason": "length"}}})
		w.Write(b)
	})
	_, _, err := askViaOpenAI(context.Background(), cfg, "s", "u", testSchema(), 100)
	if err == nil {
		t.Fatal("обрезанный ответ принят за целый")
	}
	if !strings.Contains(err.Error(), "max_tokens") {
		t.Errorf("не сказали, куда смотреть: %v", err)
	}
}

// Отказ модели по существу — отдельно от отказа сервера.
func TestOpenAIRefusal(t *testing.T) {
	_, cfg := oaServe(t, func(n int, w http.ResponseWriter) {
		b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
			"message": map[string]any{"refusal": "не буду"}, "finish_reason": "stop"}}})
		w.Write(b)
	})
	_, _, err := askViaOpenAI(context.Background(), cfg, "s", "u", testSchema(), 100)
	if err == nil || !strings.Contains(err.Error(), "не буду") {
		t.Errorf("отказ модели потерялся: %v", err)
	}
}

// Пустой список вариантов.
func TestOpenAIEmptyChoices(t *testing.T) {
	_, cfg := oaServe(t, func(n int, w http.ResponseWriter) {
		fmt.Fprint(w, `{"choices":[]}`)
	})
	_, _, err := askViaOpenAI(context.Background(), cfg, "s", "u", testSchema(), 100)
	if err == nil || !strings.Contains(err.Error(), "без единого варианта") {
		t.Errorf("пустой ответ принят за ответ: %v", err)
	}
}

// Модель на этой же машине: ноль — правда, и его надо помечать известным.
// Модель на чужом адресе без цены — незнание, и его надо помечать незнанием;
// это проверяется в core (TestSpendWithoutPriceStaysUnknown), потому что
// httptest живёт только на петле.
func TestOpenAILocalModelCostsZeroForReal(t *testing.T) {
	_, cfg := oaServe(t, func(n int, w http.ResponseWriter) {
		fmt.Fprint(w, okBody(`{"title":"своя"}`))
	})
	if !cfg.OpenAILocal() {
		t.Fatal("адрес поддельного сервера не считается местным — тест сторожит пустоту")
	}
	_, spend, err := askViaOpenAI(context.Background(), cfg, "s", "u", testSchema(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if !spend.PriceKnown || spend.USD != 0 {
		t.Errorf("у местной модели расход должен быть известным нулём: %+v", spend)
	}
}

// Без схемы формат не просим вовсе: справка о проекте — обычный текст.
func TestOpenAIWithoutSchemaAsksForNoFormat(t *testing.T) {
	rec, cfg := oaServe(t, func(n int, w http.ResponseWriter) {
		fmt.Fprint(w, okBody("Проект про платежи."))
	})
	out, _, err := askViaOpenAI(context.Background(), cfg, "s", "u", nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rec.req(t, 0)["response_format"]; ok {
		t.Error("у запроса без схемы просим формат")
	}
	if out != "Проект про платежи." {
		t.Errorf("текст без схемы поехал: %q", out)
	}
}

// Настройка без модели, без адреса или с пустой переменной не должна уходить в
// сеть: оттуда придёт невнятица про чужой сервер вместо «допиши настройку».
//
// Поэтому проверяем не только текст ошибки, но и то, что запроса не было вовсе.
// Без этого тест проходил на сломанном коде: пустая модель уезжала на сервер,
// тот отвечал чем угодно, и «ошибка есть» подтверждалось не тем, чем надо.
func TestOpenAIRefusesIncompleteConfig(t *testing.T) {
	cases := []struct {
		name  string
		spoil func(*core.Config, *testing.T)
		want  string
	}{
		{"нет адреса", func(c *core.Config, t *testing.T) { c.Brain.OpenAI.BaseURL = "" }, "адрес"},
		{"нет модели", func(c *core.Config, t *testing.T) { c.Brain.OpenAI.Model = "" }, "модел"},
		{"переменная с ключом пуста", func(c *core.Config, t *testing.T) {
			t.Setenv("STENO_TEST_MISSING_KEY", "")
			c.Brain.OpenAI.APIKeyEnv = "STENO_TEST_MISSING_KEY"
		}, "STENO_TEST_MISSING_KEY"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, cfg := oaServe(t, func(n int, w http.ResponseWriter) {
				fmt.Fprint(w, okBody(`{"title":"этого не должно случиться"}`))
			})
			tc.spoil(cfg, t)
			_, _, err := askViaOpenAI(context.Background(), cfg, "s", "u", nil, 10)
			if err == nil {
				t.Fatal("недописанная настройка принята")
			}
			if rec.count() != 0 {
				t.Errorf("сходили на сервер %d раз, хотя чинить надо настройку", rec.count())
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("в сообщении нет %q: %v", tc.want, err)
			}
		})
	}
}
