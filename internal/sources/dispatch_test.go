package sources

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/sur1cat/steno/internal/bot"
	"github.com/sur1cat/steno/internal/core"
)

func TestFindMeetURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"пойдём https://meet.google.com/abc-defg-hij сейчас", "https://meet.google.com/abc-defg-hij"},
		{"meet.google.com/qwe-rtyu-iop", "https://meet.google.com/qwe-rtyu-iop"},
		{"https://meet.google.com/abc-defg-hij?authuser=0", "https://meet.google.com/abc-defg-hij"},
		// Не пойдём по ссылке, которая только выглядит похоже.
		{"https://meet.google.com/lookup/abcdef", ""},
		{"https://zoom.us/j/123456", ""},
		{"позвони мне", ""},
	}
	for _, c := range cases {
		if got := bot.FindMeetURL(c.in); got != c.want {
			t.Errorf("findMeetURL(%q) = %q, ожидали %q", c.in, got, c.want)
		}
	}
}

// Повторная ссылка на тот же созвон не должна приводить второго бота, а та же
// комната завтра — должна.
func TestAdHocKey(t *testing.T) {
	u := "https://meet.google.com/abc-defg-hij"
	base := time.Date(2026, 9, 8, 11, 3, 0, 0, time.UTC)
	if adHocKey(u, base) != adHocKey(u, base.Add(9*time.Minute)) {
		t.Error("две ссылки в пределах получаса дали разные ключи")
	}
	if adHocKey(u, base) == adHocKey(u, base.Add(24*time.Hour)) {
		t.Error("завтрашняя встреча в той же комнате получила тот же ключ")
	}
	if adHocKey(u, base) == adHocKey("https://meet.google.com/qwe-rtyu-iop", base) {
		t.Error("разные комнаты дали один ключ")
	}
}

func TestParseJoinRequestJSON(t *testing.T) {
	body := []byte(`{"url":"https://meet.google.com/abc-defg-hij","title":"Планёрка"}`)
	r := httptest.NewRequest("POST", "/join", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	u, title, reply, why := parseJoinRequest(r, body, "токен")
	if u != "https://meet.google.com/abc-defg-hij" || title != "Планёрка" || why != nil {
		t.Fatalf("получили %q / %q / %v", u, title, why)
	}
	// Обратный адрес из тела не принимается: иначе владелец общего токена
	// заставил бы бота опубликовать чужой follow-up куда захочет.
	if !reply.Empty() {
		t.Fatalf("приняли обратный адрес из запроса: %+v", reply)
	}
}

// Slack шлёт слэш-команду формой, ссылка приезжает в угловых скобках
// автолинковки, а рядом — channel_id канала, из которого позвали.
func TestParseJoinRequestSlackForm(t *testing.T) {
	body := []byte("text=%3Chttps%3A%2F%2Fmeet.google.com%2Fabc-defg-hij%3E+запиши&channel_id=C0FEED")
	r := httptest.NewRequest("POST", "/join", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	u, _, reply, _ := parseJoinRequest(r, body, "slack")
	if u != "https://meet.google.com/abc-defg-hij" {
		t.Fatalf("получили %q", u)
	}
	if reply.Kind != "slack" || reply.Addr != "C0FEED" {
		t.Fatalf("обратный адрес не распознан: %+v", reply)
	}

	// Тот же запрос, но подтверждённый только общим токеном: channel_id никем
	// не подписан, значит обратному адресу верить нельзя.
	if _, _, r2, _ := parseJoinRequest(r, body, "токен"); !r2.Empty() {
		t.Fatalf("взяли неподтверждённый channel_id: %+v", r2)
	}
}

func TestAuthenticate(t *testing.T) {
	body := []byte("text=x")
	mk := func() *http.Request { return httptest.NewRequest("POST", "/join", bytes.NewReader(body)) }

	if authenticate(mk(), body, "s3cret", "") != "" {
		t.Error("запрос без заголовка прошёл")
	}
	r := mk()
	r.Header.Set("Authorization", "Bearer s3cret")
	if authenticate(r, body, "s3cret", "") != "токен" {
		t.Error("верный токен не прошёл")
	}
	r = mk()
	r.Header.Set("Authorization", "Bearer wrong")
	if authenticate(r, body, "s3cret", "") != "" {
		t.Error("неверный токен прошёл")
	}
	// Токен в query больше не принимается: URL целиком попадает в логи прокси.
	r = httptest.NewRequest("POST", "/join?token=s3cret", bytes.NewReader(body))
	if authenticate(r, body, "s3cret", "") != "" {
		t.Error("токен из query всё ещё принимается")
	}
}

func TestVerifySlackSignature(t *testing.T) {
	const secret = "8f742231b10e8888abcd99yyyzzz85a5"
	body := []byte("text=hello&channel_id=C0FEED")

	sign := func(ts string) string {
		mac := hmac.New(sha256.New, []byte(secret))
		fmt.Fprintf(mac, "v0:%s:%s", ts, body)
		return "v0=" + hex.EncodeToString(mac.Sum(nil))
	}
	now := strconv.FormatInt(time.Now().Unix(), 10)

	r := httptest.NewRequest("POST", "/join", bytes.NewReader(body))
	r.Header.Set("X-Slack-Request-Timestamp", now)
	r.Header.Set("X-Slack-Signature", sign(now))
	if !verifySlackSignature(r, body, secret) {
		t.Error("верная подпись не прошла")
	}
	if verifySlackSignature(r, body, "другой-секрет") {
		t.Error("подпись прошла с чужим секретом")
	}
	if verifySlackSignature(r, []byte("text=подменили"), secret) {
		t.Error("подпись прошла для подменённого тела")
	}
	// Перехваченный вчера запрос не должен приниматься сегодня.
	old := strconv.FormatInt(time.Now().Add(-24*time.Hour).Unix(), 10)
	r2 := httptest.NewRequest("POST", "/join", bytes.NewReader(body))
	r2.Header.Set("X-Slack-Request-Timestamp", old)
	r2.Header.Set("X-Slack-Signature", sign(old))
	if verifySlackSignature(r2, body, secret) {
		t.Error("прошёл просроченный запрос")
	}
	// Без секрета слэш-команды не принимаются вовсе.
	if verifySlackSignature(r, body, "") {
		t.Error("прошли без настроенного секрета")
	}
}

// Кто угодно, узнав почту бота, не должен уметь позвать его в свой звонок.
func TestGmailSenderAllowed(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Gmail.Account = "steno@company.com"
	s := &GmailSource{Cfg: cfg}

	ok := []string{"Участник А <user@company.com>", "user@company.com", "Участник А <USER@Company.COM>"}
	for _, from := range ok {
		if !s.senderAllowed(from) {
			t.Errorf("свой домен не прошёл: %q", from)
		}
	}
	bad := []string{
		// Домен-двойник: подстрока "@company.com" в нём есть, владеет им чужой.
		"Evil <attacker@company.com.evil.net>",
		// Адрес свой только на вид — в имени отправителя.
		`"steno@company.com" <attacker@evil.net>`,
		"attacker@notcompany.com",
		"мусор без адреса",
		"",
	}
	for _, from := range bad {
		if s.senderAllowed(from) {
			t.Errorf("чужой отправитель прошёл: %q", from)
		}
	}

	cfg.Gmail.AllowedDomains = []string{"company.com", "partner.com"}
	if !s.senderAllowed("user@partner.com") {
		t.Error("явно разрешённый домен не прошёл")
	}
	if s.senderAllowed("user@partner.com.evil.net") {
		t.Error("двойник разрешённого домена прошёл")
	}
}

// Созвон, которому не хватило слота, должен вернуться на следующем опросе,
// а не оказаться забаненным навсегда.
func TestCapacityRefusalDoesNotBurnKey(t *testing.T) {
	dir := t.TempDir()
	st, err := core.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	cfg := core.DefaultConfig()
	cfg.Calendar.MaxConcurrent = 1
	d := NewDispatcher(cfg, st, log.New(io.Discard, "", 0))
	d.sem <- struct{}{} // единственный слот занят

	key := "https://meet.google.com/abc-defg-hij@2026-09-08T11:00:00Z"
	m := &core.Meeting{ID: "cap-1", MeetURL: "https://meet.google.com/abc-defg-hij",
		StartedAt: time.Now(), Status: "recording"}
	if got := d.Start(context.Background(), key, m, "тест"); got != NoCapacity {
		t.Fatalf("при занятом слоте получили %v, ожидали NoCapacity", got)
	}
	seen, err := st.EventSeen(key)
	if err != nil {
		t.Fatal(err)
	}
	if seen {
		t.Fatal("созвон помечен виденным, хотя не поехал в работу — больше он не вернётся")
	}
}

// Два источника могут узнать про один и тот же созвон одновременно: ссылку
// бросили в Telegram и одновременно прислали боту на почту. Проверка «видели
// ли» и отметка должны быть одним шагом, иначе оба проходят.
func TestStartIsAtomicUnderRace(t *testing.T) {
	dir := t.TempDir()
	st, err := core.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	cfg := core.DefaultConfig()
	cfg.Calendar.MaxConcurrent = 8
	d := NewDispatcher(cfg, st, log.New(io.Discard, "", 0))
	// Проверяем дедупликацию, а не запись: настоящая запись подняла бы docker
	// или, при bot.local, Chrome прямо на машине разработчика.
	d.Run = func(context.Context, *core.Config, *core.Store, *core.Meeting) error { return nil }

	const key = "https://meet.google.com/abc-defg-hij@2026-09-08T11:00Z"
	results := make(chan StartResult, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results <- d.Start(context.Background(), key, &core.Meeting{
				ID: fmt.Sprintf("race-%d", i), MeetURL: "https://meet.google.com/abc-defg-hij",
				StartedAt: time.Now(), Status: "recording",
			}, "гонка")
		}(i)
	}
	wg.Wait()
	close(results)

	started := 0
	for r := range results {
		if r == Started {
			started++
		}
	}
	if started != 1 {
		t.Fatalf("на один созвон завели %d ботов", started)
	}
}

// Встреча из календаря и та же ссылка, брошенная в Telegram, должны давать
// один ключ. Раньше форматы расходились — с секундами против округления, — и
// совпасть не могли в принципе: на созвон приходили два бота.
func TestPlannedAndAdHocKeysAgree(t *testing.T) {
	start := time.Date(2026, 9, 8, 11, 3, 0, 0, time.UTC)
	planned := plannedKey("https://meet.google.com/abc-defg-hij?hs=1", start)
	adhoc := adHocKey("meet.google.com/abc-defg-hij", start)
	if planned == adhoc {
		t.Fatalf("ключи совпали случайно — проверь тест: %s", planned)
	}
	// Совпасть они должны на границе получаса: именно так ад-хок ключ и
	// округляется.
	onBucket := time.Date(2026, 9, 8, 11, 0, 0, 0, time.UTC)
	if plannedKey("https://meet.google.com/abc-defg-hij?hs=1", onBucket) !=
		adHocKey("meet.google.com/abc-defg-hij", onBucket) {
		t.Errorf("календарь и ад-хок дали разные ключи на одну встречу:\n  %s\n  %s",
			plannedKey("https://meet.google.com/abc-defg-hij", onBucket),
			adHocKey("meet.google.com/abc-defg-hij", onBucket))
	}
	// Разные комнаты не должны схлопываться.
	if adHocKey("https://meet.google.com/abc-defg-hij", onBucket) ==
		adHocKey("https://meet.google.com/qwe-rtyu-iop", onBucket) {
		t.Error("разные комнаты дали один ключ")
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

// Календарь зовёт сам и по кругу, поэтому событию положена одна попытка:
// бот вышел «остался один» — и следующий опрос не должен отправить его в ту
// же пустую комнату. Через Telegram (Start) повторный зов остаётся возможен:
// там зовёт человек, осознанно.
func TestStartOnceDoesNotRejoinFinishedEvent(t *testing.T) {
	st, err := core.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	d := NewDispatcher(core.DefaultConfig(), st, log.New(io.Discard, "", 0))
	// Вместо бота: заводим строку и тут же заканчиваем её как «остался один».
	d.Run = func(_ context.Context, _ *core.Config, st *core.Store, m *core.Meeting) error {
		if err := st.CreateMeeting(m); err != nil {
			return err
		}
		return st.FinishMeeting(m.ID, m.StartedAt, time.Now(), []string{"Rustem"}, "published", "", "остался один")
	}

	key := "https://meet.google.com/abc-defg-hij@2026-09-11T12:00:00Z"
	first := &core.Meeting{ID: "cal-1", MeetURL: "https://meet.google.com/abc-defg-hij",
		StartedAt: time.Now(), Status: "recording"}
	if got := d.StartOnce(context.Background(), key, first, "календарь"); got != Started {
		t.Fatalf("первый заход: %v", got)
	}
	// Дождаться, пока «бот» вернётся и созвон закроется.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if m, err := st.Meeting("cal-1"); err == nil && m.Status == "published" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("созвон так и не закрылся")
		}
		time.Sleep(10 * time.Millisecond)
	}

	again := &core.Meeting{ID: "cal-2", MeetURL: first.MeetURL, StartedAt: time.Now(), Status: "recording"}
	if got := d.StartOnce(context.Background(), key, again, "календарь"); got != Duplicate {
		t.Errorf("календарь позвал на то же событие второй раз: %v", got)
	}
	// А зов человеком — по-прежнему можно: созвон кончился, путь свободен.
	if got := d.Start(context.Background(), key, again, "Telegram"); got != Started {
		t.Errorf("осознанный повторный зов после конца созвона должен пройти: %v", got)
	}
}
