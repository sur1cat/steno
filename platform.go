package main

// Площадки созвонов.
//
// Бот начинался как бот Google Meet, и знание об этом было размазано по
// проекту: регулярка ссылки в dispatch.go, скрипт страницы в meet.js, горячая
// клавиша субтитров в meet_bot.go, подписи кнопок в selectors.json. Здесь всё
// это сведено за один интерфейс, а опознание ссылки — в одну функцию, через
// которую ходят все источники: календарь, почта, Telegram, HTTP и панель.
//
// Договор со страницей общий для всех площадок: скрипт кладёт в неё
// window.__steno с одними и теми же методами (см. meet.js и jitsi.js).
// Поэтому цикл записи в meet_bot.go площадку не различает — различает только
// то, что из страницы сделать нельзя: Meet включает субтитры настоящим
// нажатием клавиши, а Jitsi принимает часть настроек во фрагменте ссылки.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
)

// CaptionSupport — чего ждать от субтитров ещё до того, как бот куда-то пошёл.
//
// Субтитры площадки — единственный источник имён говорящих: whisper слышит
// речь, но не знает, кто говорит. Поэтому «субтитров не будет» — это не
// поломка, но и не мелочь: follow-up выйдет без имён, и человек должен узнать
// об этом заранее, а не по пустой колонке «кто сказал».
type CaptionSupport int

const (
	// CaptionsBuiltIn — площадка распознаёт речь сама и подписывает реплики
	// именами. Так у Google Meet.
	CaptionsBuiltIn CaptionSupport = iota
	// CaptionsOptional — субтитры бывают, но только если их включили на
	// сервере. Так у Jitsi: публичный meet.jit.si отдаёт в config.js
	// transcription.enabled=false и disableClosedCaptions=true, а
	// самостоятельно поднятый сервер с Jigasi — наоборот.
	CaptionsOptional
	// CaptionsNever — площадка их не умеет вовсе.
	CaptionsNever
)

func (c CaptionSupport) String() string {
	switch c {
	case CaptionsBuiltIn:
		return "есть"
	case CaptionsOptional:
		return "зависит от сервера"
	default:
		return "нет"
	}
}

// CaptionState — что вышло с субтитрами на самом деле. Отдельное поле в
// результате записи, а не «реплик получилось ноль»: ноль реплик бывает и у
// молчаливого созвона, а разница между «имён не будет» и «съём субтитров
// сломался» — это разница между «так и задумано» и «чини вёрстку».
type CaptionState string

const (
	// CaptionsWorked — область субтитров нашлась, реплики шли.
	CaptionsWorked CaptionState = "ok"
	// CaptionsNone — субтитров на этой площадке нет и не могло быть.
	// Расшифровка идёт по звуку, имён говорящих в ней не будет.
	CaptionsNone CaptionState = "none"
	// CaptionsFailed — субтитры должны были быть, но включить их не вышло.
	// Это уже повод чинить: скорее всего переехала вёрстка.
	CaptionsFailed CaptionState = "failed"
)

// Explain — строка для лога и для отчёта человеку.
func (c CaptionState) Explain(p Platform) string {
	name := "площадка"
	if p != nil {
		name = p.Title()
	}
	switch c {
	case CaptionsWorked:
		return "субтитры сняты, имена говорящих есть"
	case CaptionsNone:
		return "субтитров нет (" + name + " их не отдаёт) — расшифровка по звуку, без имён говорящих"
	default:
		return "субтитры не включились, хотя должны были — расшифровка по звуку, без имён; " +
			"похоже на смену вёрстки, смотри selectors.json"
	}
}

// Platform — одна площадка созвонов.
type Platform interface {
	// ID — машинный код: "meet", "jitsi". Он же уходит в логи и в result.json.
	ID() string
	// Title — как называть площадку человеку: «Google Meet».
	Title() string

	// CanonicalURL достаёт из произвольного текста ссылку на созвон этой
	// площадки и приводит её к канонической форме. Пустая строка — «это не моя
	// ссылка».
	//
	// Возвращается всегда собранная заново ссылка, а не кусок входного текста.
	// На этом держится защита от подмены: приглашение вида
	// https://evil.example/?x=meet.google.com/abc-defg-hij уводит бота не на
	// evil.example, а в настоящую комнату Meet — потому что host мы не берём
	// из текста, а подставляем свой.
	CanonicalURL(text string) string

	// NavigateURL — что на самом деле открыть в браузере. Обычно то же самое,
	// но Jitsi принимает настройки во фрагменте, и бот пользуется этим, чтобы
	// войти уже с выключенным микрофоном, а не выключать его потом.
	NavigateURL(canonical string) string

	// BootstrapJS — скрипт, который кладёт в страницу window.__steno.
	BootstrapJS(sel *Selectors) (string, error)

	// Captions — чего ждать от субтитров.
	Captions() CaptionSupport

	// ToggleCaptions — одна попытка переключить субтитры. Именно переключить:
	// цикл и проверка результата общие и живут в боте, потому что у всех
	// площадок это тумблер, и слепое второе нажатие их бы выключило.
	ToggleCaptions(ctx context.Context) error

	// SetCaptionLanguage выбирает язык распознавания, если площадка его
	// выбирает. Пустая реализация — законно.
	SetCaptionLanguage(ctx context.Context, sel *Selectors, lang string, lg *log.Logger)
}

// allPlatforms — реестр. Порядок важен только для сообщений человеку.
func allPlatforms() []Platform { return []Platform{meetPlatform{}, jitsiPlatform{}} }

// platformByID — площадка по её коду. nil, если такой нет.
func platformByID(id string) Platform {
	for _, p := range allPlatforms() {
		if p.ID() == id {
			return p
		}
	}
	return nil
}

// findMeetingURL — единственное место, где решается, ссылка это на созвон или
// нет. Сюда ходят все источники; своих регулярок у них больше нет.
func findMeetingURL(text string) (string, Platform) {
	for _, p := range allPlatforms() {
		if u := p.CanonicalURL(text); u != "" {
			return u, p
		}
	}
	return "", nil
}

// platformOf — площадка уже канонической ссылки. Нужна боту: он получает URL
// снаружи (флагом --url) и должен понять, чей скрипт класть в страницу.
func platformOf(rawURL string) (Platform, error) {
	_, p := findMeetingURL(rawURL)
	if p == nil {
		return nil, meetingLinkError(rawURL)
	}
	return p, nil
}

// Площадки, которые мы узнаём в лицо, но не умеем.
//
// Список нужен ровно за одним: чтобы брошенная в чат ссылка на Zoom получила
// внятный отказ, а не тишину. Тишина неотличима от «бот сломался», и человек
// выясняет это на живом созвоне, когда бот не пришёл.
//
// Хост привязан к границе слова слева ([^\w.-]), иначе notmeet.jit.si и
// evilzoom.us тоже считались бы своими.
var knownUnsupported = []struct {
	re    *regexp.Regexp
	title string
}{
	{regexp.MustCompile(`(?i)(?:^|[^\w.-])zoom\.(?:us|com)/`), "Zoom"},
	{regexp.MustCompile(`(?i)(?:^|[^\w.-])teams\.(?:microsoft|live)\.com/`), "Microsoft Teams"},
	{regexp.MustCompile(`(?i)(?:^|[^\w.-])teams\.cloud\.microsoft/`), "Microsoft Teams"},
	{regexp.MustCompile(`(?i)(?:^|[^\w.-])[\w-]*\.?webex\.com/`), "Webex"},
	{regexp.MustCompile(`(?i)(?:^|[^\w.-])whereby\.com/`), "Whereby"},
	{regexp.MustCompile(`(?i)(?:^|[^\w.-])telemost\.yandex\.[a-z]+/`), "Яндекс Телемост"},
	{regexp.MustCompile(`(?i)(?:^|[^\w.-])ktalk\.ru/`), "Контур.Толк"},
	{regexp.MustCompile(`(?i)(?:^|[^\w.-])dion\.vc/`), "Dion"},
	{regexp.MustCompile(`(?i)(?:^|[^\w.-])discord\.(?:gg|com)/`), "Discord"},
}

// Ссылка-приглашение Meet без кода комнаты. Формально это Meet, но идти по
// ней некуда, и отдельная подсказка тут стоит дороже общего «не умею».
var meetLookupRe = regexp.MustCompile(`(?i)meet\.google\.com/lookup/`)

// unsupportedPlatform возвращает название площадки, которую мы узнали, но не
// умеем. Пустая строка — ссылки на созвон в тексте нет вовсе.
func unsupportedPlatform(text string) string {
	for _, u := range knownUnsupported {
		if u.re.MatchString(text) {
			return u.title
		}
	}
	return ""
}

// supportedPlatforms — «Google Meet, Jitsi Meet» для сообщений человеку.
func supportedPlatforms() string {
	names := make([]string, 0, 2)
	for _, p := range allPlatforms() {
		names = append(names, p.Title())
	}
	return strings.Join(names, ", ")
}

// linkHint — готовая фраза человеку о том, почему по этому тексту бот никуда
// не пойдёт. Пустая строка означает «ссылки на созвон тут вообще не было» —
// на такое отвечать нечего, это обычная переписка.
func linkHint(text string) string {
	if meetLookupRe.MatchString(text) {
		return "это ссылка вида meet.google.com/lookup — кода комнаты в ней нет; " +
			"нужна ссылка с кодом вида abc-defg-hij"
	}
	if name := unsupportedPlatform(text); name != "" {
		return name + " я пока не умею — работаю только с " + supportedPlatforms()
	}
	return ""
}

// meetingLinkError — один и тот же понятный отказ во всех источниках. Раньше
// ссылка неизвестной площадки просто игнорировалась: в Telegram молча, в почте
// молча, в календаре с пометкой «нет ссылки на Meet».
func meetingLinkError(text string) error {
	if h := linkHint(text); h != "" {
		return errors.New(h)
	}
	return fmt.Errorf("не нашёл ссылку на созвон — умею %s", supportedPlatforms())
}

// --- свои серверы Jitsi -----------------------------------------------------

// Jitsi чаще всего ставят себе сами, и хост у каждого свой. Список живёт в
// selectors.json (jitsi.hosts) — там же, где остальная вёрстка, и он же едет
// внутрь контейнера бота.
//
// Почему глобально, а не параметром: опознать ссылку нужно там, куда конфиг не
// дотягивается, — в ключе дедупликации и в разборе события календаря. Тащить
// туда конфиг через пять слоёв ради списка из двух строк — хуже, чем одна
// переменная, выставляемая один раз на старте.
var (
	extraHostsMu sync.RWMutex
	extraHosts   []string
)

// registerJitsiHosts запоминает адреса своих серверов Jitsi. Вызывается один
// раз на старте — из newDispatcher на стороне сервиса и из RunBot на стороне
// бота, потому что в контейнере конфига нет, а selectors.json есть.
func registerJitsiHosts(hosts []string) {
	clean := make([]string, 0, len(hosts))
	for _, h := range hosts {
		h = strings.ToLower(strings.TrimSpace(h))
		h = strings.TrimPrefix(strings.TrimPrefix(h, "https://"), "http://")
		h = strings.TrimSuffix(h, "/")
		// Пустая строка в списке превратилась бы в регулярку, совпадающую со
		// всем подряд: бот пошёл бы по любой ссылке из чата.
		if h == "" || !strings.Contains(h, ".") {
			continue
		}
		clean = append(clean, h)
	}
	extraHostsMu.Lock()
	extraHosts = clean
	extraHostsMu.Unlock()
}

// applyPlatformConfig — то, что надо сделать один раз на старте, до того как
// кто-нибудь начнёт разбирать ссылки: иначе ссылка на свой сервер Jitsi
// сначала считалась бы чужой.
func applyPlatformConfig(cfg *Config, lg *log.Logger) {
	sel, err := loadSelectors(cfg.Bot.Selectors)
	if err != nil {
		lg.Printf("selectors: %v — беру встроенные", err)
		return
	}
	registerJitsiHosts(sel.Jitsi.Hosts)
}

func jitsiHosts() []string {
	extraHostsMu.RLock()
	defer extraHostsMu.RUnlock()
	out := make([]string, 0, len(defaultJitsiHosts)+len(extraHosts))
	out = append(out, defaultJitsiHosts...)
	out = append(out, extraHosts...)
	return out
}
