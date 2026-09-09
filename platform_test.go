package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Настоящий браузер здесь не поднимается ни разу: всё, что зависит от вёрстки,
// проверяется на фикстурах в jitsi_test.mjs, а всё остальное — обычными
// таблицами. Один запуск headful Chromium на `go test ./...` этот проект уже
// переживал, и повторять не будем.

func TestJitsiCanonicalURL(t *testing.T) {
	var p jitsiPlatform
	cases := []struct{ in, want string }{
		{"https://meet.jit.si/PlanerkaKomandy", "https://meet.jit.si/PlanerkaKomandy"},
		{"зайди на meet.jit.si/Planerka сейчас", "https://meet.jit.si/Planerka"},
		// Ссылку собираем заново, поэтому хвосты отваливаются сами.
		{"https://meet.jit.si/Planerka?jwt=xxx", "https://meet.jit.si/Planerka"},
		{"https://meet.jit.si/Planerka#config.startWithAudioMuted=true", "https://meet.jit.si/Planerka"},
		// Slack оборачивает ссылки в угловые скобки.
		{"<https://meet.jit.si/Standup> запиши", "https://meet.jit.si/Standup"},
		// Точка в конце предложения — не часть имени комнаты.
		{"Планёрка в meet.jit.si/Standup.", "https://meet.jit.si/Standup"},
		// 8x8 кладёт перед комнатой идентификатор арендатора.
		{"https://8x8.vc/vpaas-magic-cookie-abc123/DailyRoom",
			"https://8x8.vc/vpaas-magic-cookie-abc123/DailyRoom"},

		// Комнаты нет — идти некуда.
		{"https://meet.jit.si/", ""},
		{"https://meet.jit.si", ""},
		// Служебные пути и файлы: по ним бот честно ждал бы, пока его впустят.
		{"https://meet.jit.si/static/close3.html", ""},
		{"https://meet.jit.si/about", ""},
		{"https://meet.jit.si/config.js", ""},
		// Похожий хост — не наш хост.
		{"https://notmeet.jit.si/Planerka", ""},
		{"https://zoom.us/j/123456", ""},
		{"позвони мне", ""},
	}
	for _, c := range cases {
		if got := p.CanonicalURL(c.in); got != c.want {
			t.Errorf("CanonicalURL(%q) = %q, ожидали %q", c.in, got, c.want)
		}
	}
}

func TestJitsiExtraHosts(t *testing.T) {
	t.Cleanup(func() { registerJitsiHosts(nil) })
	var p jitsiPlatform

	if got := p.CanonicalURL("https://video.example.com/Weekly"); got != "" {
		t.Fatalf("незнакомый хост опознали как Jitsi: %q", got)
	}
	registerJitsiHosts([]string{"video.example.com"})
	if got := p.CanonicalURL("https://video.example.com/Weekly"); got != "https://video.example.com/Weekly" {
		t.Errorf("свой сервер не опознали: %q", got)
	}

	// Пустая строка в списке дала бы регулярку, совпадающую со всем подряд, —
	// то есть бот пошёл бы по любой ссылке из чата.
	registerJitsiHosts([]string{"", "   ", "notahost"})
	for _, bad := range []string{"https://evil.example/anything", "просто текст", "http://x/y"} {
		if got := p.CanonicalURL(bad); got != "" {
			t.Errorf("мусор в списке хостов сделал %q ссылкой на Jitsi: %q", bad, got)
		}
	}
	// А встроенные хосты от мусора в списке не должны пострадать.
	if got := p.CanonicalURL("https://meet.jit.si/Room"); got != "https://meet.jit.si/Room" {
		t.Errorf("meet.jit.si перестал опознаваться: %q", got)
	}
}

func TestFindMeetingURLPicksPlatform(t *testing.T) {
	cases := []struct {
		in       string
		wantURL  string
		wantPlat string
	}{
		{"https://meet.google.com/abc-defg-hij", "https://meet.google.com/abc-defg-hij", "meet"},
		{"https://meet.jit.si/Planerka", "https://meet.jit.si/Planerka", "jitsi"},
		{"https://zoom.us/j/1", "", ""},
	}
	for _, c := range cases {
		u, p := findMeetingURL(c.in)
		if u != c.wantURL {
			t.Errorf("findMeetingURL(%q) дал ссылку %q, ожидали %q", c.in, u, c.wantURL)
		}
		id := ""
		if p != nil {
			id = p.ID()
		}
		if id != c.wantPlat {
			t.Errorf("findMeetingURL(%q) дал площадку %q, ожидали %q", c.in, id, c.wantPlat)
		}
	}
}

// Ссылка неизвестной площадки обязана давать понятный отказ. Раньше она просто
// игнорировалась, и человек узнавал об этом на созвоне, куда бот не пришёл.
func TestLinkHint(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://zoom.us/j/123456", "Zoom"},
		{"https://teams.microsoft.com/l/meetup-join/19%3ameeting_x", "Microsoft Teams"},
		{"https://acme.webex.com/meet/team", "Webex"},
		{"https://telemost.yandex.ru/j/123", "Телемост"},
		{"https://meet.google.com/lookup/abcdef", "кода комнаты"},
		// Поддержанная ссылка подсказки не требует.
		{"https://meet.google.com/abc-defg-hij", ""},
		{"https://meet.jit.si/Planerka", ""},
		// Обычная переписка — говорить нечего, иначе бот засорит чат.
		{"когда созвонимся?", ""},
		// Похожий хост не должен считаться Zoom'ом.
		{"https://evilzoom.us/j/1", ""},
	}
	for _, c := range cases {
		got := linkHint(c.in)
		if c.want == "" {
			if got != "" {
				t.Errorf("linkHint(%q) = %q, ожидали тишину", c.in, got)
			}
			continue
		}
		if !strings.Contains(got, c.want) {
			t.Errorf("linkHint(%q) = %q, ожидали упоминание %q", c.in, got, c.want)
		}
	}
}

func TestMeetingLinkError(t *testing.T) {
	if err := meetingLinkError("https://zoom.us/j/1"); err == nil ||
		!strings.Contains(err.Error(), "Zoom") {
		t.Errorf("про Zoom не сказано: %v", err)
	}
	err := meetingLinkError("тут вообще нет ссылок")
	if err == nil || !strings.Contains(err.Error(), "Jitsi Meet") ||
		!strings.Contains(err.Error(), "Google Meet") {
		t.Errorf("отказ должен перечислять, что бот умеет: %v", err)
	}
}

// Бот входит в Jitsi уже немым. Под --local микрофон настоящий, и один
// незаглушённый бот означает чужой созвон с живого микрофона оператора.
func TestJitsiNavigateURLEntersMuted(t *testing.T) {
	got := jitsiPlatform{}.NavigateURL("https://meet.jit.si/Planerka")
	if !strings.HasPrefix(got, "https://meet.jit.si/Planerka#") {
		t.Fatalf("ссылка потерялась: %q", got)
	}
	for _, want := range []string{
		"config.startWithAudioMuted=true",
		"config.startWithVideoMuted=true",
		// Экран перед входом — единственное место, где бот вводит имя.
		"config.prejoinConfig.enabled=true",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("в ссылке нет %q: %q", want, got)
		}
	}
	// У Meet ничего дописывать нельзя: он такие фрагменты не понимает.
	if u := (meetPlatform{}).NavigateURL("https://meet.google.com/abc-defg-hij"); u !=
		"https://meet.google.com/abc-defg-hij" {
		t.Errorf("ссылку Meet изменили: %q", u)
	}
}

// Разница между «субтитров и не могло быть» и «субтитры сломались» решает,
// чинить вёрстку или нет.
func TestCaptionOutcome(t *testing.T) {
	cases := []struct {
		saw, none bool
		want      CaptionState
	}{
		{true, false, CaptionsWorked},
		{true, true, CaptionsWorked}, // видели — значит были
		{false, true, CaptionsNone},
		{false, false, CaptionsFailed},
	}
	for _, c := range cases {
		if got := captionOutcome(c.saw, c.none); got != c.want {
			t.Errorf("captionOutcome(%v,%v) = %q, ожидали %q", c.saw, c.none, got, c.want)
		}
	}
	// Объяснение должно называть площадку: «субтитров нет» без имени площадки
	// читается как поломка.
	if s := CaptionsNone.Explain(jitsiPlatform{}); !strings.Contains(s, "Jitsi Meet") {
		t.Errorf("в объяснении нет площадки: %q", s)
	}
	if s := CaptionsFailed.Explain(meetPlatform{}); !strings.Contains(s, "selectors.json") {
		t.Errorf("сломанные субтитры должны отправлять к selectors.json: %q", s)
	}
}

func TestBootstrapJSCarriesSelectors(t *testing.T) {
	sel, err := loadSelectors("")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range allPlatforms() {
		js, err := p.BootstrapJS(sel)
		if err != nil {
			t.Fatalf("%s: %v", p.ID(), err)
		}
		if !strings.HasPrefix(js, "window.__stenoSelectors = ") {
			t.Errorf("%s: скрипт не начинается с селекторов", p.ID())
		}
		if !strings.Contains(js, "window.__steno") {
			t.Errorf("%s: в скрипте нет window.__steno", p.ID())
		}
	}
	// Скрипты не должны перепутаться местами: у каждой площадки свой.
	meetJSOut, _ := meetPlatform{}.BootstrapJS(sel)
	jitsiJSOut, _ := jitsiPlatform{}.BootstrapJS(sel)
	if strings.Contains(meetJSOut, "premeeting-name-input") {
		t.Error("в скрипт Meet попал Jitsi")
	}
	if strings.Contains(jitsiJSOut, "data-participant-id") {
		t.Error("в скрипт Jitsi попал Meet")
	}
}

func TestPlatformOf(t *testing.T) {
	p, err := platformOf("https://meet.jit.si/Planerka")
	if err != nil || p == nil || p.ID() != "jitsi" {
		t.Fatalf("получили %v, %v", p, err)
	}
	if _, err := platformOf("https://zoom.us/j/1"); err == nil ||
		!strings.Contains(err.Error(), "Zoom") {
		t.Errorf("бот должен отказываться внятно: %v", err)
	}
	if p := platformByID("jitsi"); p == nil || p.Title() != "Jitsi Meet" {
		t.Errorf("площадка по коду не нашлась: %v", p)
	}
	if p := platformByID("skype"); p != nil {
		t.Errorf("нашлась несуществующая площадка: %v", p)
	}
}

// Дедупликация должна работать одинаково на всех площадках: иначе на созвон
// придут два бота — из календаря и по ссылке из чата.
func TestMeetingKeyAcrossPlatforms(t *testing.T) {
	at := time.Date(2026, 9, 9, 12, 3, 0, 0, time.UTC)
	a := adHocKey("https://meet.jit.si/Planerka?jwt=1", at)
	b := adHocKey("meet.jit.si/Planerka", at.Add(9*time.Minute))
	if a != b {
		t.Errorf("одна комната дала разные ключи: %q и %q", a, b)
	}
	if a == adHocKey("https://meet.jit.si/Drugaya", at) {
		t.Error("разные комнаты дали один ключ")
	}
	if a == adHocKey("https://meet.google.com/abc-defg-hij", at) {
		t.Error("комнаты разных площадок дали один ключ")
	}
}

// --- скрипты страниц на фикстурах -------------------------------------------

// Разбор DOM живёт в meet.js и jitsi.js, и проверяется он на синтетическом
// дереве через node. Браузер для этого не нужен и не запускается.
func TestPageScriptsOnFixtures(t *testing.T) {
	requireNode(t)
	for _, f := range []string{"meet_test.mjs", "jitsi_test.mjs"} {
		out, err := exec.Command("node", f).CombinedOutput()
		if err != nil {
			t.Errorf("%s: %v\n%s", f, err, out)
		}
	}
}

// TestPollJSContract замыкает договор между Go и страницей. Имена полей заданы
// дважды — в pollJS и в тегах pollState, — и разъехаться они могут молча: бот
// получил бы нули и вышел бы из звонка, решив, что его там нет.
func TestPollJSContract(t *testing.T) {
	requireNode(t)
	out, err := exec.Command("node", "jitsi_test.mjs", "--print-poll").Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	var st pollState
	if err := json.Unmarshal(out, &st); err != nil {
		t.Fatalf("разбор опроса: %v (%s)", err, out)
	}
	if !st.InCall {
		t.Error("inCall не доехал до Go")
	}
	if st.Left {
		t.Error("left не доехал до Go")
	}
	if !st.CaptionsOn {
		t.Error("captionsOn не доехал до Go")
	}
	if !st.CaptionsUnavailable {
		t.Error("captionsUnavailable не доехал до Go")
	}
	if st.MuteState != "muted" {
		t.Errorf("muteState = %q", st.MuteState)
	}
	if len(st.Participants) == 0 {
		t.Error("участники не доехали до Go")
	}
	if len(st.Lines) != 1 || st.Lines[0].Speaker != "Участник А" || st.Lines[0].Text != "привет" {
		t.Errorf("реплики не доехали до Go: %+v", st.Lines)
	}
	// Подсветка говорящего — второй источник имён, и без него сегодняшний
	// случай (субтитры пустые, имена всё равно нужны) не работает вовсе.
	// Разъехаться поле может так же молча, как и остальные.
	if len(st.Speaking) != 1 || st.Speaking[0] != "Участник А" {
		t.Errorf("подсветка говорящего не доехала до Go: %+v", st.Speaking)
	}
}

func requireNode(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("нет node — разбор DOM не проверяется (make test-js после установки)")
	}
}
