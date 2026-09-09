package main

// Бот: заходит в созвон, пишет звук, снимает субтитры, уходит.
//
// Площадку он не различает. Всё, что от неё зависит, лежит за интерфейсом
// Platform (platform.go): опознание ссылки, скрипт страницы, включение
// субтитров, выбор языка распознавания. Со страницей бот разговаривает через
// window.__steno — договор один и тот же у meet.js и у jitsi.js.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
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
	MuteState    string        `json:"muteState"`
	Participants []string      `json:"participants"`
	Lines        []CaptionLine `json:"lines"`
}

// Часть методов есть не у всех площадок — спрашиваем их через opt(), иначе
// один отсутствующий метод ронял бы весь опрос и бот выходил бы из звонка с
// «страница не отвечает».
const pollJS = `(() => {
  const s = window.__steno;
  if (!s) return {inCall:false,left:false,captionsOn:false,captionsUnavailable:false,
                  muteState:"unknown",participants:[],lines:[]};
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
	lg.Printf("площадка: %s (субтитры: %s)", plat.Title(), plat.Captions())

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
		return nil, fmt.Errorf("открыть %s: %w", openURL, err)
	}
	var scriptErr string
	_ = chromedp.Run(browserCtx, chromedp.Evaluate(
		"window.__stenoError || \"\"", &scriptErr))
	if scriptErr != "" {
		return nil, fmt.Errorf("скрипт страницы не запустился: %s (проверь selectors.json)", scriptErr)
	}

	if err := enterGreenRoom(browserCtx, o, plat, lg); err != nil {
		return nil, err
	}

	lg.Printf("жду, пока впустят (до %s)", o.AdmissionTimeout)
	if err := waitAdmission(browserCtx, o.AdmissionTimeout, lg); err != nil {
		return nil, err
	}
	lg.Printf("в звонке")

	// Запись стартует первой. Включение субтитров занимает до нескольких
	// секунд, и если делать его раньше, начало разговора не попадёт в аудио.
	// Заодно это правильно для сшивки: таймкоды реплик считаются от старта
	// записи, а значит субтитры должны появляться уже внутри неё.
	audioPath := filepath.Join(o.OutDir, "audio.ogg")
	rec, err := startRecording(audioPath, o.AudioSource)
	if err != nil {
		return nil, err
	}
	lg.Printf("пишу звук в %s", audioPath)

	// Субтитры — единственный источник имён говорящих. Включение у всех
	// площадок это тумблер, а не выключатель, поэтому жмём только когда
	// области нет, и проверяем результат.
	enableCaptions(browserCtx, plat, lg)
	if o.CaptionLanguage != "" {
		plat.SetCaptionLanguage(browserCtx, o.Selectors, o.CaptionLanguage, lg)
	}

	res, err := recordLoop(browserCtx, o, plat, rec, lg)
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
			lg.Printf("ВНИМАНИЕ: не нашёл кнопок микрофона и камеры — бот может быть не заглушён")
			return nil
		}
		if !named {
			var ok bool
			js := fmt.Sprintf("window.__steno ? window.__steno.setName(%s) : false",
				mustJSON(o.DisplayName))
			if err := chromedp.Run(ctx, chromedp.Evaluate(js, &ok)); err == nil && ok {
				lg.Printf("представился как %q", o.DisplayName)
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
				lg.Printf("ВНИМАНИЕ: вхожу, не подтвердив выключение микрофона и камеры")
			}
			lg.Printf("нажал кнопку входа")
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("не нашёл кнопку входа за 90 с — скорее всего изменилась вёрстка %s, "+
		"проверь selectors.json", plat.Title())
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

// enableCaptions добивается того, чтобы область субтитров появилась. Если
// аккаунт бота уже включал их раньше, площадка помнит настройку — и одно
// слепое переключение их бы выключило.
func enableCaptions(ctx context.Context, plat Platform, lg *log.Logger) {
	for i := 0; i < 5; i++ {
		var st pollState
		if err := chromedp.Run(ctx, chromedp.Evaluate(pollJS, &st)); err == nil {
			if st.CaptionsOn {
				if i > 0 {
					lg.Printf("субтитры включены")
				}
				return
			}
			// Страница знает, что субтитров не будет: на публичном meet.jit.si
			// они выключены на сервере. Жать там нечего, и пять слепых
			// нажатий подряд — это пять случайных кнопок в чужом созвоне.
			if st.CaptionsUnavailable {
				lg.Printf("субтитры на этом сервере выключены — расшифровка будет по звуку, без имён")
				return
			}
		}
		if err := plat.ToggleCaptions(ctx); err != nil {
			lg.Printf("не удалось переключить субтитры: %v", err)
			return
		}
		time.Sleep(1500 * time.Millisecond)
	}
	lg.Printf("субтитры включить не удалось — расшифровка будет без имён")
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
		lg.Printf("выключил микрофон и камеру (кнопок: %d)", muted)
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
				return fmt.Errorf("бота не впустили в звонок")
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
			lg.Printf("попросился в звонок из комнаты ожидания")
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("хост не впустил бота за %s", timeout)
}

func recordLoop(ctx context.Context, o BotOptions, plat Platform, rec *Recorder, lg *log.Logger) (*BotResult, error) {
	capPath := filepath.Join(o.OutDir, "captions.jsonl")
	capFile, err := os.Create(capPath)
	if err != nil {
		return nil, err
	}
	defer capFile.Close()
	enc := json.NewEncoder(capFile)

	var tracker CaptionTracker
	seen := map[string]bool{}
	aloneSince := time.Time{}
	warnedNoCaptions := false
	lastCaptionTry := time.Time{}
	lastDebug := time.Time{}
	reason := "звонок закончился"
	// Что вышло с субтитрами. Ответ собирается по ходу записи: «ни разу не
	// видели» и «страница сказала, что их нет» — разные новости, и человеку
	// нужна вторая, а не «реплик 0».
	sawCaptions := false
	pageSaysNone := plat.Captions() == CaptionsNever
	toldAboutNone := false
	// Плитки участников на один такт пропадают при внутреннем переходе Meet и
	// при переподключении WebRTC. Выход по одному замеру обрезал бы час
	// разговора на двенадцатой минуте, и снаружи это выглядело бы как
	// нормально завершённая запись.
	notInCall := 0
	const notInCallLimit = 4 // ~6 секунд подряд

	ticker := time.NewTicker(1500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			reason = "отмена"
		case <-ticker.C:
		}
		if ctx.Err() != nil {
			reason = "отмена"
			break
		}

		now := rec.Elapsed().Seconds()

		var st pollState
		if err := chromedp.Run(ctx, chromedp.Evaluate(pollJS, &st)); err != nil {
			lg.Printf("страница не отвечает (%v) — заканчиваю", err)
			reason = "страница закрылась"
			break
		}

		for _, u := range tracker.Update(st.Lines, now) {
			if err := enc.Encode(u); err != nil {
				lg.Printf("не записал реплику: %v", err)
			}
		}
		for _, p := range st.Participants {
			if p != o.DisplayName {
				seen[p] = true
			}
		}
		// Субтитры — единственный источник имён, поэтому возвращаем их столько
		// раз, сколько понадобится. Раньше защёлка срабатывала однажды: одна
		// перерисовка Meet на тридцатой секунде — и весь четырёхчасовой созвон
		// расшифровывался без единого имени.
		if o.DebugCaptions && time.Since(lastDebug) > 8*time.Second {
			lastDebug = time.Now()
			dumpCaptionDOM(ctx, lg)
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
		case pageSaysNone:
			// Возвращать нечего. Один раз сказать — и больше не дёргать
			// страницу: иначе бот всю запись жмёт кнопки, которых нет.
			if !toldAboutNone {
				lg.Printf("субтитров на этой площадке нет — расшифровка будет по звуку, без имён")
				toldAboutNone = true
			}
		case rec.Elapsed() > 30*time.Second && time.Since(lastCaptionTry) > time.Minute:
			if !warnedNoCaptions {
				lg.Printf("субтитры пропали — пробую вернуть")
				warnedNoCaptions = true
			}
			lastCaptionTry = time.Now()
			enableCaptions(ctx, plat, lg)
		}

		if st.Left || !st.InCall {
			notInCall++
			if notInCall >= notInCallLimit {
				reason = "бота вывели из звонка"
				break
			}
		} else {
			notInCall = 0
		}
		if len(st.Participants) <= 1 {
			if aloneSince.IsZero() {
				aloneSince = time.Now()
			} else if time.Since(aloneSince) > o.EmptyFor {
				reason = "остался один"
				break
			}
		} else {
			aloneSince = time.Time{}
		}
		if rec.Elapsed() > o.MaxDuration {
			reason = "превышен потолок длительности"
			break
		}
	}

	for _, u := range tracker.Flush(rec.Elapsed().Seconds()) {
		_ = enc.Encode(u)
	}

	people := make([]string, 0, len(seen))
	for p := range seen {
		people = append(people, p)
	}
	sort.Strings(people)

	capState := captionOutcome(sawCaptions, pageSaysNone)
	lg.Printf("запись окончена: %s, %s, участников %d",
		reason, rec.Elapsed().Round(time.Second), len(people))
	lg.Printf("субтитры: %s", capState.Explain(plat))

	return &BotResult{
		CaptionsPath: capPath,
		Participants: people,
		Started:      rec.Started,
		Ended:        time.Now(),
		LeftReason:   reason,
		Platform:     plat.ID(),
		Captions:     capState,
	}, nil
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
		lg.Printf("[дамп] не вышло: %v", err)
		return
	}
	lg.Printf("[дамп] участников %d; кнопки субтитров: %v", out.Participants, out.CaptionButtons)
	for _, r := range out.Regions {
		lg.Printf("[дамп] role=region label=%q подходит=%v детей=%d текст=%q",
			r.Label, r.Matches, r.Children, r.Sample)
	}
	for _, l := range out.Labelled {
		lg.Printf("[дамп] <%s role=%q> label=%q детей=%d текст=%q",
			l.Tag, l.Role, l.Label, l.Children, l.Sample)
	}
	for _, j := range out.Jsnames {
		if j.Found {
			lg.Printf("[дамп] jsname=%s текст=%q", j.Jsname, j.Sample)
		}
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
