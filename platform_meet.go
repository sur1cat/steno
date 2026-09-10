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

// Язык субтитров. Meet распознаёт речь тем языком, который стоит в его
// настройках, а не тем, на котором говорят, — и при чужом языке отдаёт не
// мусор, а почти пустоту: на живом созвоне 232 секунды русской речи дали 31
// букву английских слов. Имена говорящих остаются верными, но их почти не на
// что вешать, и расшифровка теряет самое ценное — кто что сказал.
//
// Путь к языку в нынешнем Meet — три нажатия: ⋮ («Ещё») → «Настройки» →
// вкладка «Субтитры». Между ними Meet рисует меню и диалог, поэтому шаги
// разнесены и каждому дано время появиться.
const (
	captionSettingsTries = 6
	captionSettingsPause = 900 * time.Millisecond
)

// manualLanguageHint — что делать человеку, когда автоматика не дошла. Язык
// субтитров запоминается за аккаунтом Meet, поэтому выставленный один раз
// руками он останется и на следующих созвонах: это надёжнее любого клика по
// чужой вёрстке.
const manualLanguageHint = "поставь язык вручную в аккаунте бота: " +
	"⋮ → Настройки → Субтитры → язык встречи. Meet его запоминает, хватит одного раза"

func (meetPlatform) SetCaptionLanguage(ctx context.Context, sel *Selectors, lang string, lg *log.Logger) {
	names := sel.CaptionLanguages[lang]
	if len(names) == 0 {
		names = []string{lang} // код не из таблицы — пробуем как есть
	}

	var step struct {
		State   string   `json:"state"`
		Done    bool     `json:"done"`
		Tabs    []string `json:"tabs"`
		Buttons []string `json:"buttons"`
	}
	opened := false
	for i := 0; i < captionSettingsTries && !opened; i++ {
		if err := chromedp.Run(ctx, chromedp.Evaluate(
			tr(`window.__steno && window.__steno.captionSettingsStep
				? window.__steno.captionSettingsStep() : {state:"нет скрипта страницы"}`),
			&step)); err != nil {
			lg.Printf(tr("настройки субтитров: %v"), err)
			return
		}
		if step.Done {
			opened = true
			break
		}
		time.Sleep(captionSettingsPause)
	}

	if !opened {
		// Именно так и выглядела поломка на живом созвоне: искали кнопку
		// «настройки субтитров», которой в нынешнем Meet уже нет.
		lg.Printf(tr("не дошёл до настроек субтитров (остановился на %q%s) — Meet будет ")+
			tr("слушать тем языком, который стоит у него сейчас; %s"),
			step.State, describeStep(step.Tabs, step.Buttons), tr(manualLanguageHint))
		closeCaptionDialog(ctx)
		return
	}

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
		lg.Printf(tr("выбор языка субтитров: %v"), err)
	} else if res.OK {
		lg.Printf(tr("язык субтитров: %s (%s)"), res.Picked, res.How)
	} else {
		// Всегда говорим, чем кончилось: молчание здесь означало бы, что про
		// чужой язык человек узнает только по пустой расшифровке.
		lg.Printf(tr("не нашёл %v среди языков субтитров; выпадашки: %v; варианты: %v; %s"),
			names, res.Combos, res.Options, tr(manualLanguageHint))
	}

	var now string
	_ = chromedp.Run(ctx, chromedp.Evaluate(
		`window.__steno && window.__steno.captionLanguage ? window.__steno.captionLanguage() : ""`,
		&now))
	if now != "" {
		lg.Printf(tr("Meet слушает языком: %s"), now)
	}

	closeCaptionDialog(ctx)
}

// describeStep — хвост сообщения с тем, что скрипт видел на странице. Без него
// «не дошёл до настроек» чинится только заходом в живой звонок руками.
func describeStep(tabs, buttons []string) string {
	switch {
	case len(tabs) > 0:
		return fmt.Sprintf(tr("; вкладки: %v"), tabs)
	case len(buttons) > 0:
		return fmt.Sprintf(tr("; кнопки: %v"), buttons)
	}
	return ""
}

// closeCaptionDialog убирает всё, что мы открыли. Оставленный диалог
// перекрывает область субтитров — и тогда мы чиним язык ценой самих субтитров.
func closeCaptionDialog(ctx context.Context) {
	var closed bool
	_ = chromedp.Run(ctx, chromedp.Evaluate(
		"window.__steno ? window.__steno.closeDialog() : false", &closed))
	if !closed {
		_ = chromedp.Run(ctx, chromedp.KeyEvent("\u001b")) // Escape
	}
	// Второй Escape закрывает меню ⋮, если до диалога дело не дошло.
	_ = chromedp.Run(ctx, chromedp.KeyEvent("\u001b"))
	time.Sleep(700 * time.Millisecond)
}
