package main

// Jitsi Meet — вторая площадка.
//
// Выбрана первой из «остальных» по трём причинам, и все три проверялись, а не
// предполагались.
//
//  1. Вход по ссылке без аккаунта. В config.js публичного meet.jit.si стоит
//     requireDisplayName:false и заведён anonymousdomain — гость заходит сам,
//     хосту не нужно ничего подтверждать.
//  2. Есть, за что цепляться в вёрстке. У Jitsi собственный набор e2e-тестов,
//     и он опирается на data-testid и постоянные id — см. комментарий в
//     jitsi.js. У Meet ничего подобного нет, там всё держится на ARIA.
//  3. Часть настроек принимается прямо в ссылке. Бот входит уже с выключенным
//     микрофоном и камерой, а не выключает их потом, найдя нужные кнопки.
//
// Чего у Jitsi нет — субтитров. Публичный meet.jit.si отдаёт
// transcription.enabled=false и disableClosedCaptions=true; они появляются
// только на своём сервере с поднятым Jigasi. Поэтому у площадки
// CaptionsOptional, и бот выясняет правду у самой страницы, а не молча
// остаётся без имён говорящих.

import (
	"context"
	"encoding/json"
	"log"
	"regexp"
	"strings"
	"sync"

	"github.com/chromedp/chromedp"
)

type jitsiPlatform struct{}

func (jitsiPlatform) ID() string    { return "jitsi" }
func (jitsiPlatform) Title() string { return "Jitsi Meet" }

// Публичные площадки Jitsi. Свои серверы добавляются в selectors.json
// (jitsi.hosts) — у Jitsi нет одного канонического адреса, его ставят себе.
var defaultJitsiHosts = []string{"meet.jit.si", "8x8.vc"}

// Пути, которые выглядят как комната, но ею не являются. Без этого списка
// ссылка на https://meet.jit.si/static/close3.html увела бы бота на страницу
// закрытия, и он бы честно ждал там, пока его впустят.
var jitsiReservedPaths = map[string]bool{
	"static": true, "libs": true, "css": true, "images": true, "fonts": true,
	"sounds": true, "lang": true, "about": true, "close": true,
	"_load-test": true, "_": true,
}

var (
	jitsiReMu  sync.Mutex
	jitsiRe    *regexp.Regexp
	jitsiReFor string
)

// jitsiURLRe собирает регулярку под текущий список хостов. Хост привязан к
// границе слева ([^\w.-]), иначе notmeet.jit.si тоже считался бы своим.
//
// Имя комнаты у Jitsi почти произвольное, поэтому строгой проверки «похоже на
// код созвона», как у Meet, тут быть не может. Защиту даёт другое: ссылка
// собирается заново из нашего хоста и одного-двух сегментов пути, а не
// берётся из текста целиком.
func jitsiURLRe() *regexp.Regexp {
	hosts := jitsiHosts()
	key := strings.Join(hosts, "|")
	jitsiReMu.Lock()
	defer jitsiReMu.Unlock()
	if jitsiRe != nil && jitsiReFor == key {
		return jitsiRe
	}
	alt := make([]string, 0, len(hosts))
	for _, h := range hosts {
		alt = append(alt, regexp.QuoteMeta(h))
	}
	const seg = `[A-Za-z0-9][\w.~%+-]*`
	jitsiRe = regexp.MustCompile(
		`(?i)(?:^|[^\w.-])(` + strings.Join(alt, "|") + `)/(` + seg + `)(?:/(` + seg + `))?`)
	jitsiReFor = key
	return jitsiRe
}

func (jitsiPlatform) CanonicalURL(text string) string {
	m := jitsiURLRe().FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	host := strings.ToLower(m[1])
	// Точка в имени комнаты законна, но чаще это конец предложения или файл
	// вроде config.js — и то и другое комнатой не является.
	first := strings.TrimRight(m[2], ".-")
	second := strings.TrimRight(m[3], ".-")
	if first == "" || jitsiReservedPaths[strings.ToLower(first)] || looksLikeFile(first) {
		return ""
	}
	path := first
	// Второй сегмент бывает у 8x8: /vpaas-magic-cookie-…/RoomName.
	if second != "" && !looksLikeFile(second) {
		path += "/" + second
	}
	return "https://" + host + "/" + path
}

func looksLikeFile(seg string) bool {
	i := strings.LastIndexByte(seg, '.')
	if i < 0 || i == len(seg)-1 {
		return false
	}
	switch strings.ToLower(seg[i+1:]) {
	case "js", "html", "htm", "json", "css", "ico", "txt", "png", "svg", "jpg", "map":
		return true
	}
	return false
}

// NavigateURL добавляет настройки во фрагмент. Jitsi разбирает его до входа в
// конференцию, поэтому бот входит уже немым — вместо того, чтобы искать и
// нажимать кнопки, надеясь успеть раньше первого слова.
//
// Фрагмент, а не запрос: у Jitsi именно #config.* переопределяет config.js, и
// на сервер он не уходит.
func (jitsiPlatform) NavigateURL(canonical string) string {
	return canonical + "#" + strings.Join([]string{
		// Главное. Микрофон бота молчит и без этого (в контейнере он подключён
		// к пустому источнику PulseAudio), но под --local микрофон настоящий,
		// и один незаглушённый бот означает чужой созвон с живого микрофона.
		"config.startWithAudioMuted=true",
		"config.startWithVideoMuted=true",
		// Экран перед входом нужен: на нём бот вводит имя. Без имени в списке
		// участников висит случайный гость, и никто не знает, что идёт запись.
		"config.prejoinConfig.enabled=true",
		// «Открыть в приложении» — лишний экран, на котором бот застрянет.
		"config.disableDeepLinking=true",
		// Диалог «точно уйти?» перехватывает закрытие вкладки.
		"config.disableBeforeUnloadHandlers=true",
		// Ни аналитики, ни аватарок с чужих доменов из контейнера бота.
		"config.disableThirdPartyRequests=true",
	}, "&")
}

func (jitsiPlatform) BootstrapJS(sel *Selectors) (string, error) {
	b, err := json.Marshal(sel)
	if err != nil {
		return "", err
	}
	return "window.__stenoSelectors = " + string(b) + ";\n" + jitsiJS, nil
}

// Субтитры у Jitsi зависят от сервера: на публичном meet.jit.si их нет вовсе,
// на своём с Jigasi — есть. Что именно вышло, бот выясняет у страницы.
func (jitsiPlatform) Captions() CaptionSupport { return CaptionsOptional }

func (jitsiPlatform) ToggleCaptions(ctx context.Context) error {
	var how string
	return chromedp.Run(ctx, chromedp.Evaluate(
		`window.__steno && window.__steno.toggleCaptions ? window.__steno.toggleCaptions() : ""`,
		&how))
}

// Язык распознавания задаётся на сервере (Jigasi), а не в интерфейсе гостя.
func (jitsiPlatform) SetCaptionLanguage(context.Context, *Selectors, string, *log.Logger) {}
