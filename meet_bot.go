package main

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

	AdmissionTimeout time.Duration
	EmptyFor         time.Duration
	MaxDuration      time.Duration

	// Профиль Chromium с залогиненным аккаунтом бота. Без него бот заходит
	// гостем и хосту придётся впускать его вручную.
	UserDataDir string
	Headless    bool
	// Язык субтитров Meet — он же язык распознавания.
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
}

// pollState — то, что страница отдаёт на каждом опросе.
type pollState struct {
	InCall       bool          `json:"inCall"`
	Left         bool          `json:"left"`
	CaptionsOn   bool          `json:"captionsOn"`
	Participants []string      `json:"participants"`
	Lines        []CaptionLine `json:"lines"`
}

const pollJS = `(() => {
  if (!window.__steno) return {inCall:false,left:false,captionsOn:false,participants:[],lines:[]};
  const c = window.__steno.captions();
  return {
    inCall: window.__steno.inCall(),
    left: window.__steno.left(),
    captionsOn: c.ok,
    participants: window.__steno.participants(),
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

	boot, err := o.Selectors.bootstrapJS()
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

	// Скрипт ставим до навигации, чтобы он пережил внутренние переходы Meet.
	inject := chromedp.ActionFunc(func(ctx context.Context) error {
		_, err := page.AddScriptToEvaluateOnNewDocument(boot).Do(ctx)
		return err
	})
	if err := chromedp.Run(browserCtx, inject, chromedp.Navigate(o.MeetURL)); err != nil {
		return nil, fmt.Errorf("открыть %s: %w", o.MeetURL, err)
	}
	var scriptErr string
	_ = chromedp.Run(browserCtx, chromedp.Evaluate(
		"window.__stenoError || \"\"", &scriptErr))
	if scriptErr != "" {
		return nil, fmt.Errorf("скрипт страницы не запустился: %s (проверь selectors.json)", scriptErr)
	}

	if err := enterGreenRoom(browserCtx, o, lg); err != nil {
		return nil, err
	}

	lg.Printf("жду, пока впустят (до %s)", o.AdmissionTimeout)
	if err := waitAdmission(browserCtx, o.AdmissionTimeout); err != nil {
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

	// Субтитры — единственный источник имён говорящих. «c» их переключает, а
	// не включает, поэтому жмём только когда области нет, и проверяем результат.
	enableCaptions(browserCtx, lg)
	if o.CaptionLanguage != "" {
		setCaptionLanguage(browserCtx, o.Selectors, o.CaptionLanguage, lg)
	}

	res, err := recordLoop(browserCtx, o, rec, lg)
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
func enterGreenRoom(ctx context.Context, o BotOptions, lg *log.Logger) error {
	deadline := time.Now().Add(90 * time.Second)
	named, muted := false, false
	for time.Now().Before(deadline) {
		var st pollState
		inCall := false
		if err := chromedp.Run(ctx, chromedp.Evaluate(pollJS, &st)); err == nil {
			inCall = st.InCall
		}
		// Если бота впустили сразу (аккаунт в том же Workspace или
		// переподключение), комнаты ожидания не было — но заглушить себя всё
		// равно надо. Раньше выход стоял до этого, и такой бот сидел в звонке
		// с включённым микрофоном.
		if inCall {
			if n := muteSelf(ctx, lg); n > 0 || muted {
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
			if !muted {
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
	return fmt.Errorf("не нашёл кнопку входа за 90 с — скорее всего изменилась вёрстка Meet, проверь joinButtonTexts в selectors.json")
}

// enableCaptions добивается того, чтобы область субтитров появилась. Если
// аккаунт бота уже включал их раньше, Meet помнит настройку — и одно слепое
// нажатие «c» их бы выключило.
func enableCaptions(ctx context.Context, lg *log.Logger) {
	for i := 0; i < 5; i++ {
		var st pollState
		if err := chromedp.Run(ctx, chromedp.Evaluate(pollJS, &st)); err == nil && st.CaptionsOn {
			if i > 0 {
				lg.Printf("субтитры включены")
			}
			return
		}
		if err := chromedp.Run(ctx, chromedp.KeyEvent("c")); err != nil {
			lg.Printf("не удалось нажать «c» для субтитров: %v", err)
			return
		}
		time.Sleep(1500 * time.Millisecond)
	}
	lg.Printf("субтитры включить не удалось — расшифровка будет без имён")
}

// muteSelf возвращает, сколько кнопок удалось выключить. Ноль — это не «всё
// уже выключено», а «не нашёл»: включённость определяется атрибутом
// data-is-muted, и если Meet его переименует, бот войдёт с живым микрофоном.
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

func waitAdmission(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
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
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("хост не впустил бота за %s", timeout)
}

func recordLoop(ctx context.Context, o BotOptions, rec *Recorder, lg *log.Logger) (*BotResult, error) {
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
		if !st.CaptionsOn && rec.Elapsed() > 30*time.Second && time.Since(lastCaptionTry) > time.Minute {
			if !warnedNoCaptions {
				lg.Printf("субтитры пропали — пробую вернуть")
				warnedNoCaptions = true
			}
			lastCaptionTry = time.Now()
			enableCaptions(ctx, lg)
		} else if st.CaptionsOn {
			warnedNoCaptions = false
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

	lg.Printf("запись окончена: %s, %s, участников %d",
		reason, rec.Elapsed().Round(time.Second), len(people))

	return &BotResult{
		CaptionsPath: capPath,
		Participants: people,
		Started:      rec.Started,
		Ended:        time.Now(),
		LeftReason:   reason,
	}, nil
}

// setCaptionLanguage переключает язык субтитров. Без этого Meet распознаёт
// речь языком по умолчанию — обычно английским, — и русский разговор приходит
// набором похоже звучащих английских слов. Имена говорящих при этом остаются
// верными, поэтому в связке с whisper это не смертельно; смертельно, когда
// текст берётся прямо из субтитров.
func setCaptionLanguage(ctx context.Context, sel *Selectors, lang string, lg *log.Logger) {
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
