package brain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
)

// Один клиент на всех, кто говорит на диалекте OpenAI.
//
// Это не «поддержка OpenAI». Формат POST {base_url}/chat/completions повторили
// все: сама OpenAI, Groq, OpenRouter, Together, DeepSeek, а из тех, что крутятся
// на своей машине, — Ollama, LM Studio и llama.cpp. Поэтому здесь нет списка
// провайдеров: есть адрес, модель и имя переменной с ключом. Новый провайдер
// заводится строкой в конфиге, а не правкой этого файла.
//
// Схему steno просит параметром — response_format с json_schema и strict. Наша
// схема (FollowupSchema) написана так, что строгий режим принимает её без
// перевода: во всех объектах перечислены все свойства, везде
// additionalProperties=false, из типов только строки, числа, массивы и один
// enum. За этим следит TestFollowupSchemaFitsStrictMode — переводить схему
// на лету не нужно, но и разойтись с этим требованием молча уже нельзя.
//
// Дальше начинается главное неудобство диалекта: за одним и тем же адресом
// живут модели, умеющие разное. Одна принимает json_schema, соседняя знает
// только json_object, третья не знает ничего и просто говорит текстом.
// Поэтому запрос спускается по лесенке, и каждая ступенька слабее предыдущей.

// oaMode — чем просим JSON. Порядок — от сильного к слабому.
type oaMode string

const (
	// Схема параметром, модель обязана её соблюсти.
	oaSchema oaMode = "schema"
	// Только «ответь объектом JSON»; сама схема уходит в промпт словами.
	oaObject oaMode = "object"
	// Не просим ничего параметром — только словами. Последняя ступенька:
	// разбор всё равно проверит, что вышло.
	oaPrompt oaMode = "prompt"
)

// Имя поля с потолком ответа. OpenAI у рассуждающих моделей принимает только
// max_completion_tokens и отвечает на max_tokens четырёхсоткой; Ollama,
// llama.cpp и половина совместимых серверов знают ровно наоборот. Угадывать
// нечем, поэтому пробуем и второе, если первое не приняли.
const (
	oaMaxCompletion = "max_completion_tokens"
	oaMaxTokens     = "max_tokens"
)

// schemaName уходит в json_schema.name. Ограничение формата: буквы, цифры,
// подчёркивания и дефисы.
const oaSchemaName = "steno_answer"

// oaDefaultTimeout — потолок на один запрос. Рассуждающая модель думает
// минутами, а повисший местный сервер не отвечает никогда: без потолка steno
// стоит на этом до конца света, и созвон остаётся нерасшифрованным.
const oaDefaultTimeout = 15 * time.Minute

type oaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type oaRequest struct {
	Model          string         `json:"model"`
	Messages       []oaMessage    `json:"messages"`
	ResponseFormat map[string]any `json:"response_format,omitempty"`
	// Оба поля потолка объявлены, но в запрос уходит ровно одно: второе
	// вычёркивается omitempty. См. oaMaxCompletion.
	MaxCompletionTokens int64 `json:"max_completion_tokens,omitempty"`
	MaxTokens           int64 `json:"max_tokens,omitempty"`
	Stream              bool  `json:"stream"`
}

type oaUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	PromptDetails    struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

type oaResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
			Refusal string `json:"refusal"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *oaUsage `json:"usage"`
	// Часть серверов отдаёт отказ двухсоткой с этим полем вместо кода ошибки.
	Error *oaError `json:"error"`
}

type oaError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    any    `json:"code"`
	Param   string `json:"param"`
}

// oaHTTP — клиент для всех запросов. Один на процесс: соединения к тому же
// адресу переиспользуются, а у местной модели их за созвон несколько.
var oaHTTP = &http.Client{}

// OpenAIReady — можно ли работать выбранным адресом. Проверяем то, что можно
// проверить не тратя денег и не ходя в сеть: назван ли адрес, названа ли
// модель, лежит ли ключ в переменной.
func OpenAIReady(cfg *core.Config) (bool, string) {
	endpoint := cfg.OpenAIEndpoint()
	if endpoint == "" {
		return false, i18n.Tr("не задан адрес: brain.openai.base_url или brain.openai.preset")
	}
	if u, err := url.Parse(endpoint); err != nil || u.Host == "" ||
		(u.Scheme != "http" && u.Scheme != "https") {
		return false, i18n.Tr("адрес не похож на адрес: ") + cfg.OpenAIBaseURL()
	}
	if strings.TrimSpace(cfg.BrainModel()) == "" {
		return false, i18n.Tr("не задана модель: brain.openai.model")
	}
	// Дальше — то, что видно человеку в doctor. Имени модели здесь нет
	// намеренно: её называет тот, кто зовёт эту функцию, и повторить её вторым
	// разом значило бы напечатать в строке doctor одно и то же дважды.
	env := cfg.OpenAIKeyEnv()
	if env == "" {
		// Ключа нет и не надо — так у местных моделей. Молчать об этом нельзя:
		// у чужого адреса это чаще всего забытая настройка, а не задумка.
		if cfg.OpenAILocal() {
			return true, cfg.OpenAIBaseURL() + i18n.Tr(" · без ключа, расход нулевой")
		}
		return true, cfg.OpenAIBaseURL() + i18n.Tr(" · ключ не задан")
	}
	if _, err := core.Secret(env, "OpenAI"); err != nil {
		return false, i18n.Tr("переменная ") + env + i18n.Tr(" пуста")
	}
	return true, cfg.OpenAIBaseURL() + i18n.Tr(" · ключ ") + env
}

// OpenAIReach — а отвечает ли вообще тот, кого назвали. Настройка может быть
// безупречной, а Ollama на этой же машине — не запущена; сказать об этом надо в
// doctor, а не на первом созвоне, когда час разговора уже записан.
//
// Спрашиваем список моделей: это самый безобидный запрос диалекта, он ничего не
// стоит и заодно проверяет ключ. Отсутствие такого метода поломкой не считаем —
// его поддерживают не все, а нам довольно того, что до сервера дошли.
//
// Из ResolveVia это не зовётся намеренно: там оно стало бы лишним запросом
// перед каждым follow-up.
func OpenAIReach(ctx context.Context, cfg *core.Config) (bool, string) {
	base := cfg.OpenAIBaseURL()
	if base == "" {
		return false, i18n.Tr("не задан адрес: brain.openai.base_url или brain.openai.preset")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		return false, err.Error()
	}
	if env := cfg.OpenAIKeyEnv(); env != "" {
		if key, err := core.Secret(env, "OpenAI"); err == nil {
			req.Header.Set("Authorization", "Bearer "+key)
		}
	}
	resp, err := oaHTTP.Do(req)
	if err != nil {
		return false, i18n.Tail(oaReachError(cfg, err).Error(), 200)
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return false, i18n.Tail(oaHTTPError(cfg, resp.StatusCode, payload).Error(), 200)
	}
	return true, i18n.Tr("отвечает")
}

// askViaOpenAI — один запрос, со всеми ступеньками отступления.
func askViaOpenAI(ctx context.Context, cfg *core.Config, system, user string,
	schema map[string]any, maxTokens int64) (string, core.Spend, error) {

	endpoint := cfg.OpenAIEndpoint()
	if endpoint == "" {
		return "", core.Spend{}, errors.New(i18n.Tr("не задан адрес: brain.openai.base_url или brain.openai.preset"))
	}
	model := strings.TrimSpace(cfg.BrainModel())
	if model == "" {
		return "", core.Spend{}, errors.New(i18n.Tr("не задана модель: brain.openai.model"))
	}
	key := ""
	if env := cfg.OpenAIKeyEnv(); env != "" {
		v, err := core.Secret(env, "OpenAI")
		if err != nil {
			return "", core.Spend{}, err
		}
		key = v
	}
	if maxTokens <= 0 {
		maxTokens = 16000
	}

	timeout := cfg.Brain.OpenAI.Timeout.D()
	if timeout <= 0 {
		timeout = oaDefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	mode := oaStartMode(cfg.Brain.OpenAI.JSONMode, schema)
	// Схема, не годящаяся для строгого режима, не должна превращаться в
	// четырёхсотку у человека посреди созвона: просим тем же словами и идём
	// дальше. Сегодня сюда не попасть — обе наши схемы требованиям отвечают, и
	// за этим следит тест, — но правка схемы дешевле проверки провайдером,
	// которого у нас нет.
	if mode == oaSchema && len(core.StrictSchemaProblems(schema)) > 0 {
		mode = oaObject
	}
	// Отступать ниже можно только на "auto" (и на пустом, что то же самое).
	// Человек, написавший json_mode="schema", просил именно схему: тихо уйти
	// с неё на «попроси словами» значит подсунуть ему разбор похуже и не
	// сказать об этом.
	auto := oaAutoMode(cfg.Brain.OpenAI.JSONMode)
	tokenField := oaMaxCompletion

	// Ступенек три, полей потолка два — четырёх попыток хватает на любую дорогу
	// вниз, и лесенка до этого счётчика не доходит: с последней ступеньки она
	// сама уходит в default и возвращает отказ. Счётчик стоит на случай, когда
	// её однажды удлинят и забудут про низ; сторожить его тестом нечем, и это
	// честнее написать, чем сделать вид, что он проверен.
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		body, spend, err := oaOnce(ctx, endpoint, key, model, system, user, schema,
			mode, tokenField, maxTokens, cfg)
		if err == nil {
			return body, spend, nil
		}
		lastErr = err

		var bad *oaBadRequest
		if !errors.As(err, &bad) {
			return "", core.Spend{}, err
		}
		switch {
		case auto && mode != oaPrompt && bad.blamesFormat():
			mode = oaWeaker(mode)
		case tokenField == oaMaxCompletion && bad.blamesField(oaMaxCompletion):
			tokenField = oaMaxTokens
		default:
			return "", core.Spend{}, err
		}
	}
	return "", core.Spend{}, lastErr
}

// oaAutoMode — разрешено ли отступать на ступеньку ниже.
func oaAutoMode(configured string) bool {
	s := strings.ToLower(strings.TrimSpace(configured))
	return s == "" || s == "auto"
}

// oaStartMode — с какой ступеньки начинать. Без схемы просить нечего: справка о
// проекте — обычный текст, и требовать от неё JSON значит получить его.
func oaStartMode(configured string, schema map[string]any) oaMode {
	if schema == nil {
		return oaPrompt
	}
	switch strings.ToLower(strings.TrimSpace(configured)) {
	case string(oaObject):
		return oaObject
	case string(oaPrompt):
		return oaPrompt
	}
	// И "schema", и "auto", и пустое — начинаем сверху. Разница между ними
	// в том, отступать ли ниже: см. askViaOpenAI.
	return oaSchema
}

func oaWeaker(m oaMode) oaMode {
	if m == oaSchema {
		return oaObject
	}
	return oaPrompt
}

// oaBadRequest — отказ, из которого ещё может выйти толк: сервер назвал поле,
// которое ему не понравилось, и мы умеем послать запрос без него.
type oaBadRequest struct {
	status int
	msg    string
	body   string
}

func (e *oaBadRequest) Error() string { return e.msg }

// blamesFormat — жалоба на то, каким способом мы просили JSON.
func (e *oaBadRequest) blamesFormat() bool {
	s := strings.ToLower(e.msg + " " + e.body)
	for _, w := range []string{"response_format", "json_schema", "response format",
		"structured output", "json mode"} {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// blamesField — сервер назвал поле, которое мы послали.
//
// Спрашиваем всегда про max_completion_tokens и только в одну сторону, и это
// не лень. Имя max_tokens целиком лежит внутри max_completion_tokens, а
// сообщение OpenAI про потолок поминает оба сразу («'max_tokens' is not
// supported… Use 'max_completion_tokens'»), — проверка «встретилось в тексте»
// в обратную сторону переключала бы нас туда-обратно без конца.
func (e *oaBadRequest) blamesField(field string) bool {
	return strings.Contains(strings.ToLower(e.msg+" "+e.body), field)
}

// oaOnce — ровно один поход на сервер.
func oaOnce(ctx context.Context, endpoint, key, model, system, user string,
	schema map[string]any, mode oaMode, tokenField string, maxTokens int64,
	cfg *core.Config) (string, core.Spend, error) {

	if mode != oaSchema && schema != nil {
		system = schemaInWords(system, schema)
	}
	req := oaRequest{
		Model: model,
		Messages: []oaMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
	}
	if tokenField == oaMaxCompletion {
		req.MaxCompletionTokens = maxTokens
	} else {
		req.MaxTokens = maxTokens
	}
	switch {
	case mode == oaSchema && schema != nil:
		req.ResponseFormat = map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   oaSchemaName,
				"schema": schema,
				"strict": true,
			},
		}
	case mode == oaObject && schema != nil:
		req.ResponseFormat = map[string]any{"type": "json_object"}
	}

	raw, err := json.Marshal(req)
	if err != nil {
		return "", core.Spend{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return "", core.Spend{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if key != "" {
		httpReq.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := oaHTTP.Do(httpReq)
	if err != nil {
		return "", core.Spend{}, oaReachError(cfg, err)
	}
	defer resp.Body.Close()
	// Потолок на тело: повисший или чужой сервер может лить в ответ гигабайты,
	// а у нас на другом конце созвон, который надо разобрать.
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return "", core.Spend{}, oaReachError(cfg, err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", core.Spend{}, oaHTTPError(cfg, resp.StatusCode, payload)
	}

	var out oaResponse
	if err := json.Unmarshal(payload, &out); err != nil {
		return "", core.Spend{}, fmt.Errorf(
			i18n.Tr("%s ответил не JSON: %w\n%s"), oaHost(cfg), err, i18n.Tail(string(payload), 300))
	}
	// Двухсотка с полем error — так отвечает часть совместимых серверов.
	if out.Error != nil && strings.TrimSpace(out.Error.Message) != "" {
		return "", core.Spend{}, oaFail(cfg, http.StatusOK, out.Error.Message, string(payload))
	}
	if len(out.Choices) == 0 {
		return "", core.Spend{}, fmt.Errorf(i18n.Tr("%s вернул ответ без единого варианта"), oaHost(cfg))
	}
	choice := out.Choices[0]
	if r := strings.TrimSpace(choice.Message.Refusal); r != "" {
		return "", core.Spend{}, fmt.Errorf(i18n.Tr("модель отказалась: %s"), r)
	}
	if choice.FinishReason == "length" {
		return "", core.Spend{}, fmt.Errorf(
			i18n.Tr("ответ не поместился в claude.max_tokens (%d) — подними его"), maxTokens)
	}
	text := choice.Message.Content
	if strings.TrimSpace(text) == "" {
		return "", core.Spend{}, fmt.Errorf(
			i18n.Tr("%s вернул пустой ответ (finish_reason=%s)"), oaHost(cfg), core.OrDash(choice.FinishReason))
	}
	if schema != nil {
		// Даже в строгом режиме: обёртка в блок кода встречается у совместимых
		// серверов, которые strict принимают, но выполняют его как просьбу.
		text = extractJSON(text)
	} else {
		text = stripPreamble(text)
	}

	var in, outTok, cached int64
	if out.Usage != nil {
		in, outTok, cached = out.Usage.PromptTokens, out.Usage.CompletionTokens,
			out.Usage.PromptDetails.CachedTokens
		// prompt_tokens у OpenAI включает прочитанное из кеша, а Spend
		// показывает их рядом — не вычесть значит показать вход вдвое.
		if cached > 0 && cached <= in {
			in -= cached
		}
	}
	spend := core.SpendWith(cfg.Brain.OpenAI.Prices, cfg.OpenAILocal(), model, in, outTok, cached, 0)
	return text, spend, nil
}

// schemaInWords дописывает схему в системную часть. Нужно там, где параметра
// формата нет или он не понят: у `claude -p` и на нижних ступеньках диалекта
// OpenAI.
//
// Через i18n.Tr это не идёт намеренно: текст читает модель, а не человек. На
// переведённой половине промпта мы уже обжигались — правила расходились с
// подписями, и разбор молча портился.
func schemaInWords(system string, schema map[string]any) string {
	if schema == nil {
		return system
	}
	raw, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return system
	}
	return system + "\n\nОтветь одним объектом JSON строго по этой схеме, без пояснений " +
		"и без обрамления в блок кода:\n\n" + string(raw)
}

// oaHost — как называть сервер в сообщении человеку. Хост, а не весь адрес:
// «api.groq.com не отвечает» читается, «https://api.groq.com/openai/v1/chat/completions
// не отвечает» — нет.
func oaHost(cfg *core.Config) string {
	u, err := url.Parse(cfg.OpenAIBaseURL())
	if err != nil || u.Host == "" {
		return core.OrDash(cfg.OpenAIBaseURL())
	}
	return u.Host
}

// oaReachError — до сервера не дошли вовсе. Самая частая причина у местной
// модели — она просто не запущена, и сказать об этом надо здесь, а не оставить
// человека с «connection refused» посреди русского вывода.
func oaReachError(cfg *core.Config, err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf(i18n.Tr("%s не ответил за отведённое время (brain.openai.timeout)"), oaHost(cfg))
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	if cfg.OpenAILocal() {
		return fmt.Errorf(i18n.Tr("%s не отвечает — модель на этой машине запущена? (%w)"), oaHost(cfg), err)
	}
	return fmt.Errorf(i18n.Tr("не достучались до %s: %w"), oaHost(cfg), err)
}

// oaHTTPError переводит код ответа в то, что человек может починить. Пятисотый
// от чужого сервера сам по себе не говорит ничего: чинить по нему нечего, и
// показывать его как есть — значит переложить разбор на того, кто просто хотел
// разбор созвона.
func oaHTTPError(cfg *core.Config, status int, payload []byte) error {
	msg := ""
	var wrapped struct {
		Error   *oaError `json:"error"`
		Message string   `json:"message"`
		Detail  string   `json:"detail"`
	}
	if err := json.Unmarshal(payload, &wrapped); err == nil {
		switch {
		case wrapped.Error != nil && wrapped.Error.Message != "":
			msg = wrapped.Error.Message
		case wrapped.Message != "":
			msg = wrapped.Message
		case wrapped.Detail != "":
			msg = wrapped.Detail
		}
	}
	if msg == "" {
		msg = i18n.Tail(strings.TrimSpace(string(payload)), 300)
	}
	return oaFail(cfg, status, msg, string(payload))
}

func oaFail(cfg *core.Config, status int, msg, body string) error {
	host := oaHost(cfg)
	env := cfg.OpenAIKeyEnv()
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		if env == "" {
			return fmt.Errorf(i18n.Tr("%s требует ключ, а brain.openai.api_key_env не задан: %s"), host, msg)
		}
		return fmt.Errorf(i18n.Tr("%s не принял ключ из %s: %s"), host, env, msg)
	case http.StatusNotFound:
		return fmt.Errorf(
			i18n.Tr("%s не знает ни такой модели, ни такого адреса: %s\n")+
				i18n.Tr("  → модель сейчас %q, адрес %s\n")+
				i18n.Tr("  → у большинства адрес кончается на /v1"),
			host, msg, cfg.BrainModel(), cfg.OpenAIBaseURL())
	case http.StatusTooManyRequests:
		return fmt.Errorf(i18n.Tr("%s не пустил по лимиту запросов: %s"), host, msg)
	case http.StatusPaymentRequired:
		return fmt.Errorf(i18n.Tr("%s отказал по деньгам: %s"), host, msg)
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		// Единственный случай, из которого ещё может выйти толк: сервер назвал
		// поле, без которого мы умеем обойтись. Разбирает askViaOpenAI.
		return &oaBadRequest{status: status,
			msg:  fmt.Sprintf(i18n.Tr("%s отклонил запрос: %s"), host, msg),
			body: body}
	}
	if status >= 500 {
		return fmt.Errorf(i18n.Tr("%s ответил %d — это его сторона, не наша: %s"), host, status, msg)
	}
	return fmt.Errorf(i18n.Tr("%s ответил %d: %s"), host, status, msg)
}
