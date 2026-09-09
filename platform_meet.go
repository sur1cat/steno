package main

// Google Meet — первая площадка и та, на которой всё проверялось живьём.
// Здесь только то, что от неё зависит; сам цикл записи площадку не различает.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"time"

	"github.com/chromedp/chromedp"
)

type meetPlatform struct{}

func (meetPlatform) ID() string    { return "meet" }
func (meetPlatform) Title() string { return "Google Meet" }

// Код созвона в Meet — три-четыре-три латинские буквы. Регулярка нарочно
// строгая: иначе бот пойдёт по любой ссылке, которую кто-нибудь кинет.
var meetCodeRe = regexp.MustCompile(`meet\.google\.com/([a-z]{3}-[a-z]{4}-[a-z]{3})`)

func (meetPlatform) CanonicalURL(text string) string {
	if m := meetCodeRe.FindStringSubmatch(text); m != nil {
		return "https://meet.google.com/" + m[1]
	}
	return ""
}

func (meetPlatform) NavigateURL(canonical string) string { return canonical }

func (meetPlatform) BootstrapJS(sel *Selectors) (string, error) {
	b, err := json.Marshal(sel)
	if err != nil {
		return "", err
	}
	return "window.__stenoSelectors = " + string(b) + ";\n" + meetJS, nil
}

// У Meet субтитры встроенные и с именами говорящих — ради них он и выбран
// первым: whisper слышит речь, но не знает, кто говорит.
func (meetPlatform) Captions() CaptionSupport { return CaptionsBuiltIn }

// ToggleCaptions жмёт «c». Именно настоящим событием клавиатуры через CDP:
// синтетический KeyboardEvent из страницы Meet игнорирует.
func (meetPlatform) ToggleCaptions(ctx context.Context) error {
	return chromedp.Run(ctx, chromedp.KeyEvent("c"))
}

// SetCaptionLanguage переключает язык субтитров. Без этого Meet распознаёт
// речь языком по умолчанию — обычно английским, — и русский разговор приходит
// набором похоже звучащих английских слов. Имена говорящих при этом остаются
// верными, поэтому в связке с whisper это не смертельно; смертельно, когда
// текст берётся прямо из субтитров.
func (meetPlatform) SetCaptionLanguage(ctx context.Context, sel *Selectors, lang string, lg *log.Logger) {
	names := sel.CaptionLanguages[lang]
	if len(names) == 0 {
		names = []string{lang} // код не из таблицы — пробуем как есть
	}

	var opened bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(
		"window.__steno ? window.__steno.openCaptionSettings() : false", &opened)); err != nil || !opened {
		lg.Printf("не нашёл настройки субтитров — язык остаётся тем, что стоит в Meet")
		return
	}
	time.Sleep(1500 * time.Millisecond) // диалог рисуется не мгновенно

	var res struct {
		OK      bool     `json:"ok"`
		Picked  string   `json:"picked"`
		How     string   `json:"how"`
		Combos  []string `json:"combos"`
		Options []string `json:"options"`
	}
	js := fmt.Sprintf("window.__steno ? window.__steno.pickCaptionLanguage(%s) : {ok:false}",
		mustJSON(names))
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &res)); err != nil {
		lg.Printf("выбор языка субтитров: %v", err)
	} else if res.OK {
		lg.Printf("язык субтитров: %s", res.Picked)
	} else {
		lg.Printf("не нашёл %v среди языков субтитров; выпадашки: %v; варианты: %v",
			names, res.Combos, res.Options)
	}

	var closed bool
	_ = chromedp.Run(ctx, chromedp.Evaluate(
		"window.__steno ? window.__steno.closeDialog() : false", &closed))
	if !closed {
		_ = chromedp.Run(ctx, chromedp.KeyEvent("\u001b")) // Escape
	}
	time.Sleep(700 * time.Millisecond)
}
