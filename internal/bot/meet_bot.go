package bot

// Бот: заходит в созвон, пишет звук, снимает субтитры, уходит.
//
// Площадку он не различает. Всё, что от неё зависит, лежит за интерфейсом
// Platform (platform.go): опознание ссылки, скрипт страницы, включение
// субтитров, выбор языка распознавания. Со страницей бот разговаривает через
// window.__steno — договор один и тот же у meet.js и у jitsi.js.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/sur1cat/steno/internal/audio"
	"github.com/sur1cat/steno/internal/i18n"
)

type BotOptions struct {
	MeetURL     string
	DisplayName string
	OutDir      string
	AudioSource string // монитор PulseAudio-синка, куда играет Chromium
	Selectors   *Selectors

	// Площадка. Пусто — определяется по ссылке; заполняется в тестах и там,
	// где она уже известна.
	Platform Platform

	AdmissionTimeout time.Duration
	EmptyFor         time.Duration
	MaxDuration      time.Duration

	// Профиль Chromium с залогиненным аккаунтом бота. Без него бот заходит
	// гостем и хосту придётся впускать его вручную.
	UserDataDir string
	Headless    bool
	// Язык субтитров площадки — он же язык распознавания.
	CaptionLanguage string
	// Печатать в лог, что на странице похоже на область субтитров. Чинить их
	// съём вслепую, тратя на попытку по заходу в живой звонок, нельзя.
	DebugCaptions bool
	Log           *log.Logger
}

type BotResult struct {
	AudioPath    string
	CaptionsPath string
	// SpeakersPath — лента подсветки говорящего рядом с субтитрами. Пишется
	// всегда, когда площадка умеет её отдавать: имена нужны и тогда, когда
	// субтитры оказались пустыми.
	SpeakersPath string
	Participants []string
	Started      time.Time
	Ended        time.Time
	LeftReason   string
	// Площадка, на которой шла запись, и что вышло с субтитрами. Второе —
	// отдельное поле, а не «реплик получилось ноль»: ноль бывает и у
	// молчаливого созвона, а «имён не будет» и «съём субтитров сломался» —
	// это разные новости.
	Platform string       `json:"platform,omitempty"`
	Captions CaptionState `json:"captions,omitempty"`
}

// pollState — то, что страница отдаёт на каждом опросе.
type pollState struct {
	InCall     bool `json:"inCall"`
	Left       bool `json:"left"`
	CaptionsOn bool `json:"captionsOn"`
	// CaptionsUnavailable — страница сама знает, что субтитров не будет.
	// У Jitsi это видно по config.js сервера, у Meet такого признака нет.
	CaptionsUnavailable bool `json:"captionsUnavailable"`
	// MuteState — "muted" | "live" | "unknown". Отдельно от muteSelf(),
	// потому что «ничего не нажал» и «уже выключено» — разные вещи, а на
	// разнице держится единственная защита от бота с живым микрофоном.
	MuteState    string              `json:"muteState"`
	Participants []string            `json:"participants"`
	Lines        []audio.CaptionLine `json:"lines"`
	// Speaking — кого площадка подсвечивает как говорящего прямо сейчас.
	// Второй, независимый от распознавания источник имён: он не зависит ни от
	// языка субтитров, ни от того, включены ли они вообще.
	Speaking []string `json:"speaking"`
}

// Часть методов есть не у всех площадок — спрашиваем их через opt(), иначе
// один отсутствующий метод ронял бы весь опрос и бот выходил бы из звонка с
// «страница не отвечает».
const pollJS = `(() => {
  const s = window.__steno;
  if (!s) return {inCall:false,left:false,captionsOn:false,captionsUnavailable:false,
                  muteState:"unknown",participants:[],lines:[],speaking:[]};
  const opt = (name, fallback) =>
    typeof s[name] === "function" ? s[name]() : fallback;
  const c = s.captions();
  return {
    inCall: s.inCall(),
    left: s.left(),
    captionsOn: c.ok || opt("captionsOn", false),
    captionsUnavailable: opt("captionsUnavailable", false),
    muteState: opt("muteState", "unknown"),
    participants: s.participants(),
    lines: c.lines,
    speaking: opt("speaking", []),
  };
})()`

func RunBot(ctx context.Context, o BotOptions) (*BotResult, error) {
	lg := o.Log
	if lg == nil {
		lg = log.New(os.Stderr, "", log.LstdFlags)
	}
	if err := os.MkdirAll(o.OutDir, 0o755); err != nil {
		return nil, err
	}

	// Адреса своих серверов Jitsi приезжают в selectors.json — внутри
	// контейнера это единственное, что бот про них знает: конфига сервиса там
	// нет. Регистрируем до опознания ссылки, иначе своя площадка не найдётся.
	if o.Selectors != nil {
		registerJitsiHosts(o.Selectors.Jitsi.Hosts)
	}
	plat := o.Platform
	if plat == nil {
		p, err := platformOf(o.MeetURL)
		if err != nil {
			return nil, err
		}
		plat = p
	}
	lg.Printf(i18n.Tr("площадка: %s (субтитры: %s)"), plat.Title(), plat.Captions())

	boot, err := plat.BootstrapJS(o.Selectors)
	if err != nil {
		return nil, err
	}

	flags := []chromedp.ExecAllocatorOption{
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-gpu", true),
		// Разрешить микрофон и камеру без диалога. Заглушку-пищалку Chrome
		// (--use-fake-device-for-media-stream) намеренно не берём: её слышат
		// все участники. Тишину даёт виртуальный source в PulseAudio.
		chromedp.Flag("use-fake-ui-for-media-stream", true),
		chromedp.Flag("autoplay-policy", "no-user-gesture-required"),
		// --enable-automation намеренно не передаём: он выставляет
		// navigator.webdriver и вешает баннер «браузером управляет ПО».
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("disable-background-timer-throttling", true),
		chromedp.Flag("disable-renderer-backgrounding", true),
		chromedp.Flag("disable-backgrounding-occluded-windows", true),
		chromedp.WindowSize(1280, 720),
	}
	if o.Headless {
		flags = append(flags, chromedp.Headless)
	}
	if o.UserDataDir != "" {
		flags = append(flags, chromedp.UserDataDir(o.UserDataDir))
	}
	if p := os.Getenv("STENO_CHROME_PATH"); p != "" {
		flags = append(flags, chromedp.ExecPath(p))
	}

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, flags...)
	defer cancelAlloc()
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()

	// Скрипт ставим до навигации, чтобы он пережил внутренние переходы страницы.
	inject := chromedp.ActionFunc(func(ctx context.Context) error {
		_, err := page.AddScriptToEvaluateOnNewDocument(boot).Do(ctx)
		return err
	})
	openURL := plat.NavigateURL(o.MeetURL)
	if err := chromedp.Run(browserCtx, inject, chromedp.Navigate(openURL)); err != nil {
		return nil, fmt.Errorf(i18n.Tr("открыть %s: %w"), openURL, err)
	}
	var scriptErr string
	_ = chromedp.Run(browserCtx, chromedp.Evaluate(
		"window.__stenoError || \"\"", &scriptErr))
	if scriptErr != "" {
		return nil, fmt.Errorf(i18n.Tr("скрипт страницы не запустился: %s (проверь selectors.json)"), scriptErr)
	}

	if err := enterGreenRoom(browserCtx, o, plat, lg); err != nil {
		return nil, err
	}

	lg.Printf(i18n.Tr("жду, пока впустят (до %s)"), o.AdmissionTimeout)
	if err := waitAdmission(browserCtx, o.AdmissionTimeout, lg); err != nil {
		return nil, err
	}
	lg.Print(i18n.Tr("в звонке"))

	// Запись стартует первой. Включение субтитров занимает до нескольких
	// секунд, и если делать его раньше, начало разговора не попадёт в аудио.
	// Заодно это правильно для сшивки: таймкоды реплик считаются от старта
	// записи, а значит субтитры должны появляться уже внутри неё.
	audioPath := filepath.Join(o.OutDir, "audio.ogg")
	rec, err := audio.StartRecording(audioPath, o.AudioSource)
	if err != nil {
		return nil, err
	}
	lg.Printf(i18n.Tr("пишу звук в %s"), audioPath)

	// Субтитры — главный источник имён говорящих: имя там привязано к
	// конкретной фразе. Подсветка плитки достраивает их там, где субтитры
	// молчат, но заменить не может. Включение у всех площадок это тумблер, а
	// не выключатель, поэтому жмём только когда области нет, и проверяем
	// результат.
	capOn := enableCaptions(browserCtx, plat, lg)
	if o.CaptionLanguage != "" {
		plat.SetCaptionLanguage(browserCtx, o.Selectors, o.CaptionLanguage, lg)
	}

	res, err := recordLoop(browserCtx, o, plat, rec, capOn, lg)
	stopErr := rec.Stop()
	if err != nil {
		return nil, err
	}
	if stopErr != nil {
		return nil, stopErr
	}
	res.AudioPath = audioPath
	return res, nil
}

// enterGreenRoom проходит экран перед входом: имя (если зашли гостем),
// выключение микрофона и камеры, кнопка входа.
func enterGreenRoom(ctx context.Context, o BotOptions, plat Platform, lg *log.Logger) error {
	deadline := time.Now().Add(90 * time.Second)
	named, muted := false, false
	for time.Now().Before(deadline) {
		var st pollState
		inCall := false
		if err := chromedp.Run(ctx, chromedp.Evaluate(pollJS, &st)); err == nil {
			inCall = st.InCall
			// Страница умеет ответить прямо: «микрофон и камера выключены».
			// Meet так не умеет, там признак только один — нажатая кнопка.
			if st.MuteState == "muted" {
				muted = true
			} else if st.MuteState == "live" {
				muted = false
			}
		}
		// Если бота впустили сразу (аккаунт в том же Workspace или
		// переподключение), комнаты ожидания не было — но заглушить себя всё
		// равно надо. Раньше выход стоял до этого, и такой бот сидел в звонке
		// с включённым микрофоном.
		if inCall {
			if n := muteSelf(ctx, lg); n > 0 || muted {
				return nil
			}
			if confirmMuted(ctx) {
				return nil
			}
			lg.Print(i18n.Tr("ВНИМАНИЕ: не нашёл кнопок микрофона и камеры — бот может быть не заглушён"))
			return nil
		}
		if !named {
			var ok bool
			js := fmt.Sprintf("window.__steno ? window.__steno.setName(%s) : false",
				mustJSON(o.DisplayName))
			if err := chromedp.Run(ctx, chromedp.Evaluate(js, &ok)); err == nil && ok {
				lg.Printf(i18n.Tr("представился как %q"), o.DisplayName)
				named = true
			}
		}
		if n := muteSelf(ctx, lg); n > 0 {
			muted = true
		}
		var joined bool
		if err := chromedp.Run(ctx, chromedp.Evaluate(
			"window.__steno ? window.__steno.clickJoin() : false", &joined)); err == nil && joined {
			if !muted && !confirmMuted(ctx) {
				// Под --local микрофон и камера настоящие, а не виртуальные:
				// молча войти незаглушённым означает вести чужой созвон с
				// живого микрофона оператора.
				lg.Print(i18n.Tr("ВНИМАНИЕ: вхожу, не подтвердив выключение микрофона и камеры"))
			}
			lg.Print(i18n.Tr("нажал кнопку входа"))
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf(i18n.Tr("не нашёл кнопку входа за 90 с — скорее всего изменилась вёрстка %s, ")+
		i18n.Tr("проверь selectors.json"), plat.Title())
}

// confirmMuted спрашивает страницу напрямую. Отдельно от muteSelf(), потому
// что «кнопок не нашлось» — это не «микрофон включён»: у Jitsi бот входит уже
// немым по настройке в ссылке, и нажимать там нечего.
func confirmMuted(ctx context.Context) bool {
	var state string
	if err := chromedp.Run(ctx, chromedp.Evaluate(
		`window.__steno && window.__steno.muteState ? window.__steno.muteState() : "unknown"`,
		&state)); err != nil {
		return false
	}
	return state == "muted"
}

const (
	// captionToggleTries — сколько раз пробовать переключатель. Прежние пять
	// слепых нажатий подряд — это ещё и пять шансов выключить субтитры,
	// которые уже работали: у всех площадок это тумблер, а не выключатель.
	// Два нажатия — чётное число: если мы сдались, площадка осталась ровно в
	// том состоянии, в каком была до нас.
	captionToggleTries = 2
	// captionSettleWait — сколько ждать появления области после нажатия. На
	// живом созвоне Meet показал её через три секунды после последнего
	// нажатия; полутора секунд, которые ждал прежний код, не хватало.
	captionSettleWait = 5 * time.Second
	captionSettleStep = 500 * time.Millisecond
)

// captionPage — то немногое, что enableCaptions делает со страницей.
// Вынесено за функции ради теста: сама логика «нажать и убедиться» и есть то,
// что соврало на живом созвоне, а поднимать браузер на каждый go test нельзя.
type captionPage struct {
	// state: включены ли субтитры; знает ли страница, что их не будет;
	// удалось ли вообще спросить.
	state  func() (on, unavailable, ok bool)
	toggle func() error
	wait   func(time.Duration)
}

// enableCaptions добивается того, чтобы область субтитров появилась. Если
// аккаунт бота уже включал их раньше, площадка помнит настройку — и одно
// слепое переключение их бы выключило.
func enableCaptions(ctx context.Context, plat Platform, lg *log.Logger) bool {
	return enableCaptionsOn(captionPage{
		state: func() (bool, bool, bool) {
			var st pollState
			if err := chromedp.Run(ctx, chromedp.Evaluate(pollJS, &st)); err != nil {
				return false, false, false
			}
			return st.CaptionsOn, st.CaptionsUnavailable, true
		},
		toggle: func() error { return plat.ToggleCaptions(ctx) },
		wait:   time.Sleep,
	}, lg)
}

// enableCaptionsOn — сам цикл. Возвращает, появилась ли область субтитров.
//
// Прежний код печатал «субтитры включить не удалось» сразу после последнего
// нажатия, ни разу больше не заглянув на страницу. На живом созвоне субтитры
// появились через три секунды после этой строчки, и в логе остались оба
// сообщения разом: «включить не удалось» в начале и «субтитры сняты, имена
// говорящих есть» в конце. Человек, который такое читает, ищет поломку,
// которой нет, — и проходит мимо настоящей.
func enableCaptionsOn(p captionPage, lg *log.Logger) bool {
	pressed := false
	for i := 0; i < captionToggleTries; i++ {
		on, unavailable, ok := p.state()
		if ok && on {
			if pressed {
				lg.Print(i18n.Tr("субтитры включены"))
			}
			return true
		}
		// Страница знает, что субтитров не будет: на публичном meet.jit.si
		// они выключены на сервере. Жать там нечего, и слепые нажатия — это
		// случайные кнопки в чужом созвоне.
		if ok && unavailable {
			lg.Print(i18n.Tr("субтитры на этом сервере выключены — расшифровка будет по звуку, без имён"))
			return false
		}
		if err := p.toggle(); err != nil {
			lg.Printf(i18n.Tr("не удалось переключить субтитры: %v"), err)
			return false
		}
		pressed = true
		// Ждём именно появления области, а не фиксированную паузу: пока её
		// нет, следующее нажатие выключит только что включённые субтитры.
		for waited := time.Duration(0); waited < captionSettleWait; waited += captionSettleStep {
			p.wait(captionSettleStep)
			if on, _, ok := p.state(); ok && on {
				lg.Print(i18n.Tr("субтитры включены"))
				return true
			}
		}
	}
	lg.Print(i18n.Tr("субтитры включить не удалось — расшифровка будет без имён"))
	return false
}

// muteSelf возвращает, сколько кнопок удалось выключить. Ноль — это не «всё
// уже выключено», а «не нашёл»: у Meet включённость определяется атрибутом
// data-is-muted, и если его переименуют, бот войдёт с живым микрофоном.
func muteSelf(ctx context.Context, lg *log.Logger) int {
	var muted int
	if err := chromedp.Run(ctx, chromedp.Evaluate(
		"window.__steno ? window.__steno.muteSelf() : 0", &muted)); err != nil {
		return 0
	}
	if muted > 0 {
		lg.Printf(i18n.Tr("выключил микрофон и камеру (кнопок: %d)"), muted)
	}
	return muted
}

func waitAdmission(ctx context.Context, timeout time.Duration, lg *log.Logger) error {
	deadline := time.Now().Add(timeout)
	knocked := false
	for time.Now().Before(deadline) {
		var st pollState
		if err := chromedp.Run(ctx, chromedp.Evaluate(pollJS, &st)); err == nil {
			if st.InCall {
				return nil
			}
			if st.Left {
				return errors.New(i18n.Tr("бота не впустили в звонок"))
			}
		}
		// У некоторых площадок между экраном перед входом и звонком есть ещё
		// одна комната ожидания со своей кнопкой «попроситься». Пока её не
		// нажать, хост заявки не увидит, и бот простоит здесь весь таймаут, а
		// потом уйдёт с «хост не впустил» — хотя его никто и не звал.
		// Метода может не быть: у Meet такого экрана нет.
		var asked bool
		_ = chromedp.Run(ctx, chromedp.Evaluate(
			`window.__steno && window.__steno.reknock ? window.__steno.reknock() : false`,
			&asked))
		if asked && !knocked {
			knocked = true
			lg.Print(i18n.Tr("попросился в звонок из комнаты ожидания"))
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf(i18n.Tr("хост не впустил бота за %s"), timeout)
}

// capOn — что вышло у enableCaptions до начала цикла. Нужно, чтобы не
// оставлять в логе одинокое «включить не удалось», когда субтитры всё-таки
// появились: два взаимоисключающих сообщения про один созвон — это дороже,
// чем оба по отдельности.
func recordLoop(ctx context.Context, o BotOptions, plat Platform, rec *audio.Recorder, capOn bool, lg *log.Logger) (*BotResult, error) {
	capPath := filepath.Join(o.OutDir, "captions.jsonl")
	capFile, err := os.Create(capPath)
	if err != nil {
		return nil, err
	}
	defer capFile.Close()
	enc := json.NewEncoder(capFile)

	// Лента подсветки говорящего — отдельным файлом. Она не реплики: у неё нет
	// текста, другая задержка и другая достоверность, и складывать её в тот же
	// файл значило бы потом гадать, что было субтитрами, а что рамкой вокруг
	// плитки. Ошибку создания глотать нельзя молча, но и запись из-за неё
	// ронять незачем: звук и субтитры дороже.
	spkPath := filepath.Join(o.OutDir, audio.SpeakersFileName)
	spkEnc := (*json.Encoder)(nil)
	if f, err := os.Create(spkPath); err != nil {
		lg.Printf(i18n.Tr("лента говорящих не пишется (%v) — имена будут только из субтитров"), err)
		spkPath = ""
	} else {
		defer f.Close()
		spkEnc = json.NewEncoder(f)
	}

	var tracker audio.CaptionTracker
	var speakers audio.SpeakerTracker
	var coverage audio.SpeakerCoverage
	// Подсветка либо есть на площадке, либо её признак переехал. Разница видна
	// только по времени: за полминуты разговора вдвоём кто-нибудь
	// подсвечивается обязательно.
	//
	// Считаем именно «видели хоть раз», а не закрытые отрезки: отрезок
	// закрывается, только когда человек замолчал, и на монологе счётчик
	// отрезков стоял бы на нуле всю первую минуту — бот пожаловался бы на
	// исправно работающую подсветку.
	sawSpeaking := false
	toldNoSpeaking := false
	openedPeople := false
	seen := map[string]bool{}
	aloneSince := time.Time{}
	warnedNoCaptions := false
	lastCaptionTry := time.Time{}
	lastDebug := time.Time{}
	reason := i18n.Tr("звонок закончился")
	// Что вышло с субтитрами. Ответ собирается по ходу записи: «ни разу не
	// видели» и «страница сказала, что их нет» — разные новости, и человеку
	// нужна вторая, а не «реплик 0».
	sawCaptions := false
	pageSaysNone := plat.Captions() == CaptionsNever
	toldAboutNone := false
	// Плитки участников на один такт пропадают при внутреннем переходе Meet и
	// при переподключении WebRTC. Выход по одному замеру обрезал бы час
	// разговора на двенадцатой минуте, и снаружи это выглядело бы как
	// нормально завершённая запись. Считаем временем, а не тактами: частота
	// опроса — настройка съёма субтитров, и менять из-за неё то, через сколько
	// бот считает себя выведенным из звонка, нельзя.
	notInCallSince := time.Time{}
	const notInCallLimit = 6 * time.Second
	// Сколько текста дали субтитры. Считается по ходу: «реплик ноль» бывает и
	// у молчаливого созвона, а вот «за четыре минуты речи тридцать букв» —
	// это уже сломанное распознавание, и человек должен увидеть это в логе,
	// а не в follow-up с чужими исполнителями.
	var yield audio.CaptionYield
	// Про неудачу с субтитрами уже сказано — значит, про их появление тоже
	// надо сказать, иначе в логе останутся два противоположных сообщения.
	toldCaptionsFailed := !capOn

	ticker := time.NewTicker(audio.CaptionPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			reason = i18n.Tr("отмена")
		case <-ticker.C:
		}
		if ctx.Err() != nil {
			reason = i18n.Tr("отмена")
			break
		}

		now := rec.Elapsed().Seconds()

		var st pollState
		if err := chromedp.Run(ctx, chromedp.Evaluate(pollJS, &st)); err != nil {
			lg.Printf(i18n.Tr("страница не отвечает (%v) — заканчиваю"), err)
			reason = i18n.Tr("страница закрылась")
			break
		}

		for _, u := range tracker.Update(st.Lines, now) {
			yield.Add(u)
			if err := enc.Encode(u); err != nil {
				lg.Printf(i18n.Tr("не записал реплику: %v"), err)
			}
		}
		// Молчащий бот тоже участник, и площадка иногда подсвечивает
		// собственную плитку. Себя из ленты убираем и здесь, а не только в
		// странице: у страницы для этого один признак, а имя бота мы знаем
		// наверняка.
		speakingNow := audio.WithoutSelf(st.Speaking, o.DisplayName)
		if len(speakingNow) > 0 {
			sawSpeaking = true
		}
		for _, s := range speakers.Update(speakingNow, now) {
			coverage.Add(s)
			if spkEnc != nil {
				if err := spkEnc.Encode(s); err != nil {
					lg.Printf(i18n.Tr("не записал отрезок говорящего: %v"), err)
				}
			}
		}
		for _, p := range st.Participants {
			if p != o.DisplayName {
				seen[p] = true
			}
		}
		// Полоски микрофона, по которым видно говорящего, Meet рисует в
		// строках панели «Участники», а плитки в сетке виртуализируются:
		// говорящего может не быть в сетке вовсе. Панель открываем не сразу и
		// только когда подсветка молчит — если она и так работает, лишний
		// клик в чужом созвоне ни к чему.
		if !openedPeople && !sawSpeaking && len(seen) > 0 &&
			rec.Elapsed() > 20*time.Second {
			openedPeople = true
			openPeoplePanel(ctx, lg)
		}
		// Сказать о неработающей подсветке надо один раз и по делу: пока в
		// звонке никого нет, молчание — это не поломка.
		if !toldNoSpeaking && !sawSpeaking && len(seen) > 0 &&
			rec.Elapsed() > 45*time.Second {
			toldNoSpeaking = true
			lg.Printf(i18n.Tr("подсветка говорящего ни разу не сработала за %s — либо в звонке ")+
				i18n.Tr("молчат, либо её признак переехал. Устойчивого признака у Meet нет, ")+
				i18n.Tr("чинится это по дампу: перезапусти с --debug-captions и положи ")+
				i18n.Tr("увиденное в speakingJsnames или speakingSelectors в selectors.json"),
				rec.Elapsed().Round(time.Second))
		}
		// Субтитры — главный источник имён, поэтому возвращаем их столько
		// раз, сколько понадобится. Раньше защёлка срабатывала однажды: одна
		// перерисовка Meet на тридцатой секунде — и весь четырёхчасовой созвон
		// расшифровывался без единого имени.
		if o.DebugCaptions && time.Since(lastDebug) > 8*time.Second {
			lastDebug = time.Now()
			dumpCaptionDOM(ctx, lg)
			dumpSpeakingDOM(ctx, lg)
		}
		if st.CaptionsUnavailable {
			pageSaysNone = true
		}
		if st.CaptionsOn {
			sawCaptions = true
		}
		switch {
		case st.CaptionsOn:
			warnedNoCaptions = false
			if toldCaptionsFailed {
				// Прежде чем это появилось, лог утверждал обратное: сначала
				// «включить не удалось», а в конце записи — «субтитры сняты».
				lg.Print(i18n.Tr("субтитры всё-таки появились — имена говорящих будут"))
				toldCaptionsFailed = false
			}
		case pageSaysNone:
			// Возвращать нечего. Один раз сказать — и больше не дёргать
			// страницу: иначе бот всю запись жмёт кнопки, которых нет.
			if !toldAboutNone {
				lg.Print(i18n.Tr("субтитров на этой площадке нет — расшифровка будет по звуку, без имён"))
				toldAboutNone = true
			}
		case rec.Elapsed() > 30*time.Second && time.Since(lastCaptionTry) > time.Minute:
			if !warnedNoCaptions {
				lg.Print(i18n.Tr("субтитры пропали — пробую вернуть"))
				warnedNoCaptions = true
			}
			lastCaptionTry = time.Now()
			toldCaptionsFailed = !enableCaptions(ctx, plat, lg)
		}

		if st.Left || !st.InCall {
			if notInCallSince.IsZero() {
				notInCallSince = time.Now()
			} else if time.Since(notInCallSince) >= notInCallLimit {
				reason = i18n.Tr("бота вывели из звонка")
				break
			}
		} else {
			notInCallSince = time.Time{}
		}
		if len(st.Participants) <= 1 {
			if aloneSince.IsZero() {
				aloneSince = time.Now()
			} else if time.Since(aloneSince) > o.EmptyFor {
				reason = i18n.Tr("остался один")
				break
			}
		} else {
			aloneSince = time.Time{}
		}
		if rec.Elapsed() > o.MaxDuration {
			reason = i18n.Tr("превышен потолок длительности")
			break
		}
	}

	for _, u := range tracker.Flush(rec.Elapsed().Seconds()) {
		yield.Add(u)
		_ = enc.Encode(u)
	}
	for _, s := range speakers.Flush(rec.Elapsed().Seconds()) {
		coverage.Add(s)
		if spkEnc != nil {
			_ = spkEnc.Encode(s)
		}
	}

	people := make([]string, 0, len(seen))
	for p := range seen {
		people = append(people, p)
	}
	sort.Strings(people)

	capState := captionOutcome(sawCaptions, pageSaysNone)
	lg.Printf(i18n.Tr("запись окончена: %s, %s, участников %d"),
		reason, rec.Elapsed().Round(time.Second), len(people))
	lg.Printf(i18n.Tr("субтитры: %s"), capState.Explain(plat))
	if r := yield.Report(rec.Elapsed(), o.CaptionLanguage); r != "" {
		lg.Printf("%s", r)
	}
	// Про подсветку говорим всегда: именно она отвечает на вопрос «будут ли
	// имена», когда субтитры оказались пустыми.
	if r := coverage.Report(rec.Elapsed().Seconds()); r != "" {
		lg.Printf("%s", r)
	}

	return &BotResult{
		CaptionsPath: capPath,
		SpeakersPath: spkPath,
		Participants: people,
		Started:      rec.Started,
		Ended:        time.Now(),
		LeftReason:   reason,
		Platform:     plat.ID(),
		Captions:     capState,
	}, nil
}

// openPeoplePanel открывает панель участников — там, где площадка её вообще
// имеет. Панель локальная, другие участники её не видят; нужна она потому, что
// подсветку говорящего Meet рисует в строках этой панели, а сетка плиток
// показывает не всех.
//
// «Метода нет» и «кнопки не нашёл» здесь разные вещи, и путать их нельзя: у
// Jitsi панель не нужна, и жаловаться там не на что. Поэтому площадка, не
// умеющая этого, отвечает "no-method" и молчит — цикл записи остаётся общим и
// про площадки по-прежнему ничего не знает.
func openPeoplePanel(ctx context.Context, lg *log.Logger) {
	var res string
	if err := chromedp.Run(ctx, chromedp.Evaluate(
		`window.__steno && window.__steno.openPeoplePanel
			? window.__steno.openPeoplePanel() : "no-method"`, &res)); err != nil {
		return
	}
	switch res {
	case "clicked":
		lg.Print(i18n.Tr("открыл панель участников: подсветку говорящего площадка рисует в ней"))
	case "":
		lg.Print(i18n.Tr("не нашёл кнопку панели участников — подсветка говорящего может не сработать; ") +
			i18n.Tr("смотри peoplePanelLabels в selectors.json"))
	}
}

// captionOutcome — итог по субтитрам. Отдельной функцией, чтобы у него был
// тест: разница между «их и не могло быть» и «сломались» решает, чинить
// вёрстку или нет.
func captionOutcome(sawCaptions, pageSaysNone bool) CaptionState {
	switch {
	case sawCaptions:
		return CaptionsWorked
	case pageSaysNone:
		return CaptionsNone
	default:
		return CaptionsFailed
	}
}

// dumpCaptionDOM печатает всё, что на странице похоже на субтитры: подписи
// областей, что в них лежит, какие кнопки называются «субтитры». По одному
// такому дампу видно, переехала вёрстка или субтитры просто не включились.
func dumpCaptionDOM(ctx context.Context, lg *log.Logger) {
	var out struct {
		Regions []struct {
			Label    string `json:"label"`
			Matches  bool   `json:"matches"`
			Children int    `json:"children"`
			Sample   string `json:"sample"`
		} `json:"regions"`
		Labelled []struct {
			Tag      string `json:"tag"`
			Role     string `json:"role"`
			Label    string `json:"label"`
			Children int    `json:"children"`
			Sample   string `json:"sample"`
		} `json:"labelled"`
		Jsnames []struct {
			Jsname string `json:"jsname"`
			Found  bool   `json:"found"`
			Sample string `json:"sample"`
		} `json:"jsnames"`
		Participants   int      `json:"participants"`
		CaptionButtons []string `json:"captionButtons"`
	}
	js := `(() => (window.__steno && window.__steno.debugCaptions) ? window.__steno.debugCaptions() : null)()`
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &out)); err != nil {
		lg.Printf(i18n.Tr("[дамп] не вышло: %v"), err)
		return
	}
	lg.Printf(i18n.Tr("[дамп] участников %d; кнопки субтитров: %v"), out.Participants, out.CaptionButtons)
	for _, r := range out.Regions {
		lg.Printf(i18n.Tr("[дамп] role=region label=%q подходит=%v детей=%d текст=%q"),
			r.Label, r.Matches, r.Children, r.Sample)
	}
	for _, l := range out.Labelled {
		lg.Printf(i18n.Tr("[дамп] <%s role=%q> label=%q детей=%d текст=%q"),
			l.Tag, l.Role, l.Label, l.Children, l.Sample)
	}
	for _, j := range out.Jsnames {
		if j.Found {
			lg.Printf(i18n.Tr("[дамп] jsname=%s текст=%q"), j.Jsname, j.Sample)
		}
	}
}

// dumpSpeakingDOM печатает всё, чем плитки участников могли бы помечать
// говорящего: сырые атрибуты и классы. Устойчивого признака у Meet нет, и когда
// нынешний переедет, чинить его будут по этому дампу — одним заходом в живой
// звонок, а не десятью.
//
// Форма ответа у площадок разная (у Jitsi это состояние стора, а не плитки),
// поэтому разбираем в свободную структуру и печатаем что нашлось.
func dumpSpeakingDOM(ctx context.Context, lg *log.Logger) {
	var out struct {
		How        string   `json:"how"`
		Store      bool     `json:"store"`
		DominantID string   `json:"dominantId"`
		Speaking   []string `json:"speaking"`
		Tiles      []struct {
			ID      string   `json:"id"`
			Name    string   `json:"name"`
			Self    bool     `json:"self"`
			How     string   `json:"how"`
			Classes []string `json:"classes"`
			Attrs   []string `json:"attrs"`
		} `json:"tiles"`
	}
	js := `(() => (window.__steno && window.__steno.debugSpeaking) ? window.__steno.debugSpeaking() : null)()`
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &out)); err != nil {
		lg.Printf(i18n.Tr("[дамп] подсветка: не вышло: %v"), err)
		return
	}
	lg.Printf(i18n.Tr("[дамп] говорят сейчас: %v"), out.Speaking)
	if out.How != "" {
		lg.Printf(i18n.Tr("[дамп] источник подсветки: %s (стор %v, id %q)"), out.How, out.Store, out.DominantID)
	}
	for _, t := range out.Tiles {
		lg.Printf(i18n.Tr("[дамп] плитка %q свой=%v признак=%q классы=%v"), t.Name, t.Self, t.How, t.Classes)
		if len(t.Attrs) > 0 {
			lg.Printf(i18n.Tr("[дамп]   атрибуты: %v"), t.Attrs)
		}
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
