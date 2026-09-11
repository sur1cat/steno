package publish

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sur1cat/steno/internal/core"
)

// Адаптер Discord проверяется на поддельном Discord: настоящий вебхук в тестах
// не тронешь, а весь договор — это то, что уходит в сеть и что возвращается в
// stdout.

func discordAdapter(t *testing.T) string {
	t.Helper()
	for _, bin := range []string{"bash", "curl", "jq", "awk"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("для проверки адаптера нужен %s", bin)
		}
	}
	p, err := filepath.Abs(filepath.Join("..", "..", "adapters", "discord.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("адаптера нет: %v", err)
	}
	return p
}

type discordPost struct {
	Content         string `json:"content"`
	AllowedMentions *struct {
		Parse []string `json:"parse"`
	} `json:"allowed_mentions"`
}

// mentionsMuted — поле есть и список разрешённых упоминаний в нём пуст. Именно
// пуст, а не отсутствует: без поля Discord разбирает @everyone как обычно.
func mentionsMuted(p discordPost) bool {
	return p.AllowedMentions != nil && p.AllowedMentions.Parse != nil && len(p.AllowedMentions.Parse) == 0
}

type fakeDiscord struct {
	*httptest.Server
	mu       sync.Mutex
	posts    []discordPost
	files    map[string]string
	status   int
	answer   string
	hangFor  time.Duration
	gotQuery string
}

func newFakeDiscord(t *testing.T) *fakeDiscord {
	t.Helper()
	d := &fakeDiscord{status: http.StatusOK, files: map[string]string{},
		answer: `{"id":"M1","channel_id":"C1","guild_id":"G1"}`}
	d.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// Так адаптер узнаёт сервер, если его не было в ответе на вебхук.
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"id":"W1","guild_id":"G1","channel_id":"C1"}`)
			return
		}
		d.mu.Lock()
		d.gotQuery = r.URL.RawQuery
		hang := d.hangFor
		status, answer := d.status, d.answer
		d.mu.Unlock()
		if hang > 0 {
			// Ждём отменяемо: когда адаптер убьют по сроку, соединение
			// закроется, и тест не будет стоять эти секунды впустую.
			select {
			case <-time.After(hang):
			case <-r.Context().Done():
				return
			}
		}
		ct, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		var body []byte
		if strings.HasPrefix(ct, "multipart/") {
			mr := multipart.NewReader(r.Body, params["boundary"])
			for {
				part, err := mr.NextPart()
				if err != nil {
					break
				}
				raw, _ := io.ReadAll(part)
				if part.FormName() == "payload_json" {
					body = raw
				} else {
					d.mu.Lock()
					d.files[part.FileName()] = string(raw)
					d.mu.Unlock()
				}
			}
		} else {
			body, _ = io.ReadAll(r.Body)
		}
		var p discordPost
		_ = json.Unmarshal(body, &p)
		d.mu.Lock()
		d.posts = append(d.posts, p)
		d.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, answer)
	}))
	t.Cleanup(d.Close)
	return d
}

func (d *fakeDiscord) last() discordPost {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.posts) == 0 {
		return discordPost{}
	}
	return d.posts[len(d.posts)-1]
}

func runDiscord(t *testing.T, d *fakeDiscord, p *Payload, timeout time.Duration) (string, string, error) {
	t.Helper()
	t.Setenv("DISCORD_WEBHOOK_URL", d.URL+"/api/webhooks/1/секрет")
	return RunCommand(context.Background(),
		Command{Cmd: []string{discordAdapter(t)}, Timeout: timeout}, p)
}

func TestDiscordAdapterPublishesAndReturnsLink(t *testing.T) {
	d := newFakeDiscord(t)
	m, f, segs := testMeeting()
	link, notes, err := runDiscord(t, d, NewPayload(m, f, segs, ""), 30*time.Second)
	if err != nil {
		t.Fatalf("адаптер не отработал: %v\n%s", err, notes)
	}
	if link != "https://discord.com/channels/G1/C1/M1" {
		t.Errorf("ссылка на сообщение: %q", link)
	}
	post := d.last()
	if !strings.Contains(post.Content, "Про биллинг и радио") {
		t.Errorf("в сообщение не попал follow-up: %q", post.Content)
	}
	if !strings.Contains(post.Content, "доделать вебхуки") {
		t.Errorf("в сообщение не попали задачи: %q", post.Content)
	}
	// Без wait=true Discord возвращает пустой 204, и ссылке взяться неоткуда.
	if !strings.Contains(d.gotQuery, "wait=true") {
		t.Errorf("адаптер не попросил ответ с сообщением: %q", d.gotQuery)
	}
	// Пустой parse — то, что не даёт follow-up поднять пингом весь сервер.
	if !mentionsMuted(post) {
		t.Errorf("упоминания не заглушены: %+v", post.AllowedMentions)
	}
}

// На созвоне сказали «@everyone» — в follow-up это едет дословно, и без
// заглушённых упоминаний бот поднял бы весь сервер.
func TestDiscordAdapterDoesNotPingEveryone(t *testing.T) {
	d := newFakeDiscord(t)
	m, f, segs := testMeeting()
	f.TLDR = []string{"@everyone смотрим отчёт до пятницы"}
	if _, notes, err := runDiscord(t, d, NewPayload(m, f, segs, ""), 30*time.Second); err != nil {
		t.Fatalf("%v\n%s", err, notes)
	}
	post := d.last()
	if !strings.Contains(post.Content, "@everyone") {
		t.Fatalf("текст созвона потерян: %q", post.Content)
	}
	if !mentionsMuted(post) {
		t.Errorf("упоминания не заглушены: %+v", post.AllowedMentions)
	}
}

// Ответ вебхука без сервера — обычное дело; сервер тогда спрашивают у самого
// вебхука.
func TestDiscordAdapterAsksWebhookForGuild(t *testing.T) {
	d := newFakeDiscord(t)
	d.answer = `{"id":"M2","channel_id":"C2"}`
	m, f, segs := testMeeting()
	link, notes, err := runDiscord(t, d, NewPayload(m, f, segs, ""), 30*time.Second)
	if err != nil {
		t.Fatalf("%v\n%s", err, notes)
	}
	if link != "https://discord.com/channels/G1/C2/M2" {
		t.Errorf("ссылка собралась не так: %q", link)
	}
}

// У Discord потолок 2000 символов. Длинный follow-up режется по строкам, а
// целиком уходит вложением.
func TestDiscordAdapterAttachesLongFollowup(t *testing.T) {
	d := newFakeDiscord(t)
	m, f, segs := testMeeting()
	for i := 0; i < 200; i++ {
		f.ActionItems = append(f.ActionItems, core.ActionItem{
			Owner: "Рустем", What: "очень длинная задача про вебхуки, биллинг и радио", Due: "2026-09-12"})
	}
	p := NewPayload(m, f, segs, "")
	if len([]rune(p.Text.Markdown)) < 4000 {
		t.Fatalf("follow-up вышел коротким (%d) — проверка ничего не проверяет", len([]rune(p.Text.Markdown)))
	}
	if _, notes, err := runDiscord(t, d, p, 30*time.Second); err != nil {
		t.Fatalf("%v\n%s", err, notes)
	}
	post := d.last()
	if n := len([]rune(post.Content)); n > 2000 {
		t.Errorf("в сообщении %d символов — Discord такое не примет", n)
	}
	if n := len([]rune(post.Content)); n < 100 {
		t.Errorf("в сообщении почти ничего нет: %q", post.Content)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.files) != 1 {
		t.Fatalf("follow-up целиком не доложен файлом: %v", d.files)
	}
	if got := d.files["followup.md"]; !strings.Contains(got, "очень длинная задача") ||
		len([]rune(got)) < len([]rune(p.Text.Markdown)) {
		t.Errorf("во вложении не весь follow-up: %d символов из %d",
			len([]rune(got)), len([]rune(p.Text.Markdown)))
	}
}

func TestDiscordAdapterRefusalIsReadable(t *testing.T) {
	m, f, segs := testMeeting()
	cases := []struct {
		name   string
		status int
		answer string
		want   string
	}{
		{"вебхук удалён", http.StatusNotFound, `{"message":"Unknown Webhook"}`, "404"},
		{"слишком часто", http.StatusTooManyRequests, `{"retry_after":3}`, "429"},
		{"сервер сломался", http.StatusInternalServerError, `внутренняя ошибка`, "500"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := newFakeDiscord(t)
			d.status, d.answer = c.status, c.answer
			link, notes, err := runDiscord(t, d, NewPayload(m, f, segs, ""), 30*time.Second)
			if err == nil {
				t.Fatalf("отказ %d принят за успех", c.status)
			}
			if !strings.Contains(err.Error(), c.want) && !strings.Contains(notes, c.want) {
				t.Errorf("отказ не называет код %s: %v\n%s", c.want, err, notes)
			}
			if link != "" {
				t.Errorf("при отказе вернулась ссылка %q", link)
			}
		})
	}
}

// Мусор вместо JSON: сообщение ушло (код 200), но ссылки из такого не собрать.
// Это не отказ — и падать тут нельзя.
func TestDiscordAdapterSurvivesJunkAnswer(t *testing.T) {
	d := newFakeDiscord(t)
	d.answer = "<html>что-то пошло не так</html>"
	m, f, segs := testMeeting()
	link, notes, err := runDiscord(t, d, NewPayload(m, f, segs, ""), 30*time.Second)
	if err != nil {
		t.Fatalf("мусор в ответе принят за отказ: %v\n%s", err, notes)
	}
	if link != "" {
		t.Errorf("из мусора собралась ссылка %q", link)
	}
}

// Молчащий Discord не должен держать публикацию дольше срока.
func TestDiscordAdapterTimesOut(t *testing.T) {
	d := newFakeDiscord(t)
	d.hangFor = 5 * time.Second
	m, f, segs := testMeeting()
	started := time.Now()
	_, _, err := runDiscord(t, d, NewPayload(m, f, segs, ""), 500*time.Millisecond)
	if err == nil {
		t.Fatal("зависший вебхук отработал")
	}
	if !strings.Contains(err.Error(), "не уложился") {
		t.Errorf("ошибка не про срок: %v", err)
	}
	// Срок должен работать заметно раньше, чем ответит вебхук: иначе проверка
	// проходит и на адаптере, у которого срока нет вовсе.
	if d := time.Since(started); d > 3*time.Second {
		t.Errorf("зависший вебхук держал публикацию %s", d)
	}
}

func TestDiscordAdapterNeedsWebhookURL(t *testing.T) {
	m, f, segs := testMeeting()
	t.Setenv("DISCORD_WEBHOOK_URL", "")
	_, notes, err := RunCommand(context.Background(),
		Command{Cmd: []string{discordAdapter(t)}}, NewPayload(m, f, segs, ""))
	if err == nil {
		t.Fatal("адаптер без вебхука сделал вид, что опубликовал")
	}
	if !strings.Contains(notes, "DISCORD_WEBHOOK_URL") {
		t.Errorf("не сказано, чего не хватает: %q", notes)
	}
	if !strings.Contains(notes, "Webhook") && !strings.Contains(notes, "ебхук") {
		t.Errorf("не сказано, где её взять: %q", notes)
	}
}
