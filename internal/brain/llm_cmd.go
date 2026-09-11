package brain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
)

// Модель через внешний скрипт.
//
// Это тот же приём, которым в steno уже подключается распознавание: конфиг
// называет команду, steno её запускает и разговаривает с ней JSON'ом. Ради
// него всё и затевалось — чтобы «а у нас своя обёртка» перестало означать
// «steno вам не подойдёт». llamafile, самописный питон, внутренний эндпоинт за
// корпоративным VPN, чужой протокол — всё это подключается скриптом и без
// единой строки Go.
//
// # Договор
//
// Он описан здесь и продублирован словами в adapters/ollama.sh, потому что
// читать его будут именно там. Менять его после публикации нельзя: чужие
// скрипты сломаются молча.
//
//	steno → скрипту, одним объектом JSON в stdin:
//	  {
//	    "system":     "правила разбора",
//	    "user":       "шапка и расшифровка",
//	    "schema":     {…} | null,   // JSON Schema ответа; null — свободный текст
//	    "model":      "llama3.1",   // llm.model; пусто — на усмотрение скрипта
//	    "max_tokens": 16000         // 0 — на усмотрение скрипта
//	  }
//
//	скрипт → steno, одним объектом JSON в stdout:
//	  {
//	    "text":  "ответ модели",     // обязателен; со схемой — объект JSON строкой
//	    "usage": {"input": 0, "output": 0, "cache_read": 0, "cache_write": 0},
//	    "usd":   0.0,                // необязательно, см. ниже
//	    "model": "llama3.1:8b"       // необязательно: чем на самом деле считали
//	  }
//
//	диагностика — в stderr, отказ — ненулевым кодом возврата.
//
// Почему промпт идёт в stdin, а не аргументом, как путь к аудио у
// расшифровки: расшифровка часового созвона — это десятки килобайт, и в
// аргументы командной строки она не помещается. Схема едет там же, а не
// отдельным файлом: скрипту, которому нужен файл, записать его — одна строка,
// а обещать два входа сразу значит потом всю жизнь держать их согласованными.
//
// Отсутствие usd и присутствие "usd": 0 — разные вещи, и это главное в учёте.
// Ноль означает «бесплатно, и я это знаю» — так отвечает адаптер к модели на
// своей машине. Отсутствие означает «не знаю», и steno так и напишет: соврать
// нулём тут дороже, чем промолчать.

const cmdDefaultTimeout = 15 * time.Minute

// cmdRequest — то, что steno кладёт скрипту в stdin.
type cmdRequest struct {
	System    string         `json:"system"`
	User      string         `json:"user"`
	Schema    map[string]any `json:"schema"`
	Model     string         `json:"model,omitempty"`
	MaxTokens int64          `json:"max_tokens,omitempty"`
}

type cmdUsage struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
}

// cmdResponse — то, что steno читает у скрипта из stdout. USD — указатель
// намеренно: см. шапку файла.
type cmdResponse struct {
	Text  string    `json:"text"`
	Usage *cmdUsage `json:"usage"`
	USD   *float64  `json:"usd"`
	Model string    `json:"model"`
}

// LLMCmdAvailable — настроен ли скрипт и запускается ли он.
func LLMCmdAvailable(cfg *core.Config) (bool, string) {
	if len(cfg.LLM.Cmd) == 0 {
		return false, i18n.Tr("не задан llm.cmd — путь к скрипту модели")
	}
	bin := core.AdapterPath(cfg.LLM.Cmd[0])
	if _, err := exec.LookPath(bin); err != nil {
		return false, i18n.Tr("не запускается ") + bin + ": " + err.Error()
	}
	// Слово «скрипт» здесь не повторяем: его уже сказал ProviderTitle, и в
	// строке doctor оно оказывалось дважды подряд.
	return true, strings.Join(cfg.LLM.Cmd, " ")
}

func askViaCmd(ctx context.Context, cfg *core.Config, system, user string,
	schema map[string]any, maxTokens int64) (string, core.Spend, error) {

	if len(cfg.LLM.Cmd) == 0 {
		return "", core.Spend{}, errors.New(i18n.Tr("не задан llm.cmd — путь к скрипту модели"))
	}
	args := append([]string(nil), cfg.LLM.Cmd...)
	// Через AdapterPath: путь в конфиге мог указывать в каталог версии, которую
	// снёс brew upgrade, а адаптер при этом лежит рядом с новой. Ровно так же
	// это делает расшифровка.
	args[0] = core.AdapterPath(args[0])

	req, err := json.Marshal(cmdRequest{
		System: system, User: user, Schema: schema,
		Model: strings.TrimSpace(cfg.LLM.Model), MaxTokens: maxTokens,
	})
	if err != nil {
		return "", core.Spend{}, err
	}

	timeout := cfg.LLM.Timeout.D()
	if timeout <= 0 {
		timeout = cmdDefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdin = strings.NewReader(string(req))
	// Скрипт печатает диагностику человеку и потому говорит на языке steno —
	// как и адаптеры расшифровки. Передаём всегда: язык мог прийти из конфига,
	// а не из окружения, и сам по себе до скрипта тогда не доедет.
	cmd.Env = append(os.Environ(), "STENO_LANG="+i18n.UILang)
	// По таймауту гасим всю группу мягко: у скрипта может быть свой curl или
	// свой питон, и оставлять их сиротами нельзя.
	core.SetProcessGroup(cmd)
	cmd.Cancel = func() error { return core.TerminateGroup(cmd) }
	cmd.WaitDelay = 15 * time.Second

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", core.Spend{}, fmt.Errorf(
				i18n.Tr("скрипт модели не уложился в отведённое время (llm.timeout): %w"), ctx.Err())
		}
		// Хвост stderr, а не начало: скрипт пишет туда, чего ему не хватило, —
		// а начало занято служебным «exit status 1».
		return "", core.Spend{}, fmt.Errorf(i18n.Tr("скрипт модели %v: %w\n%s"),
			args, err, i18n.Tail(strings.TrimSpace(stderr.String()), 800))
	}

	var res cmdResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &res); err != nil {
		return "", core.Spend{}, fmt.Errorf(i18n.Tr("скрипт модели вернул не тот JSON: %w\n%s"),
			err, i18n.Tail(stdout.String(), 400))
	}
	if strings.TrimSpace(res.Text) == "" {
		return "", core.Spend{}, errors.New(i18n.Tr("скрипт модели вернул пустой ответ"))
	}
	text := res.Text
	if schema != nil {
		text = extractJSON(text)
	} else {
		text = stripPreamble(text)
	}

	// Имя модели берём то, которое назвал скрипт: он один знает, чем на самом
	// деле считал. Не назвал — остаётся то, что просили.
	model := core.FirstNonEmpty(strings.TrimSpace(res.Model), strings.TrimSpace(cfg.LLM.Model))
	var u cmdUsage
	if res.Usage != nil {
		u = *res.Usage
	}
	spend := core.SpendWith(cfg.LLM.Prices, false, model,
		u.Input, u.Output, u.CacheRead, u.CacheWrite)
	if res.USD != nil {
		// Сказанное скриптом главнее таблицы: он ходил к модели, а мы нет.
		spend.USD = *res.USD
		spend.PriceKnown = true
	}
	return text, spend, nil
}
