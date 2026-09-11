package publish

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sur1cat/steno/internal/brain"
	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/google"
	"github.com/sur1cat/steno/internal/i18n"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
)

// PublishAll рассылает follow-up по всем включённым адресатам. Отказ одного не
// отменяет остальных: лучше опубликовать в двух местах из трёх, чем нигде.
// Но молчать о провале нельзя — вернувшийся список ошибок решает, считать ли
// созвон опубликованным.
func PublishAll(ctx context.Context, cfg *core.Config, st *core.Store, m *core.Meeting, f *core.Followup, segs []core.Segment, lg *log.Logger) []error {
	if cfg.NoPublish {
		return nil
	}
	// Настройки адресатов перечитываются перед каждой рассылкой: включил Slack
	// в панели — следующий созвон уедет туда, без перезапуска сервиса.
	cfg = core.ActiveChannels(st, cfg)
	var errs []error
	fail := func(target string, err error) {
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", target, err))
		}
	}

	docURL, slackChannelID := "", ""
	if cfg.GoogleDocs.Enabled {
		u, err := publishGoogleDoc(ctx, cfg, m, f, segs)
		record(st, lg, m.ID, "google_docs", u, err)
		fail("google_docs", err)
		docURL = u
	}
	if cfg.Slack.Enabled {
		u, ch, err := publishSlack(ctx, cfg, m, f, segs, docURL, lg)
		record(st, lg, m.ID, "slack", u, err)
		fail("slack", err)
		slackChannelID = ch
	}
	if cfg.Telegram.Enabled {
		err := publishTelegram(ctx, cfg, m, f, cfg.Telegram.ChatID, docURL)
		record(st, lg, m.ID, "telegram", "", err)
		fail("telegram", err)
	}
	// Адаптеры, которые человек принёс сам. Идут после встроенных адресатов:
	// ссылка на документ к этому моменту уже есть, и она видна им в links.
	if cmds := Commands(cfg); len(cmds) > 0 {
		errs = append(errs, PublishCommands(ctx, st, lg, cmds, NewPayload(m, f, segs, docURL))...)
	}
	errs = append(errs, publishToOrigin(ctx, cfg, st, m, f, docURL, slackChannelID, lg)...)
	return errs
}

// publishToOrigin отвечает туда, откуда попросили записать созвон. Это не
// дубль настроенных адресатов: человек, который кинул ссылку боту в личку,
// ждёт follow-up там же — даже если команда публикует его в Google Docs.
// Если он спросил в том же чате, куда сервис и так пишет, второй раз не шлём.
func publishToOrigin(ctx context.Context, cfg *core.Config, st *core.Store, m *core.Meeting, f *core.Followup, docURL, slackChannelID string, lg *log.Logger) []error {
	if m.ReplyTo.Empty() {
		return nil
	}
	var err error
	target := ""
	switch m.ReplyTo.Kind {
	case "telegram":
		if cfg.Telegram.Enabled && m.ReplyTo.Addr == cfg.Telegram.ChatID {
			return nil
		}
		target = i18n.Tr("telegram:откуда просили")
		err = publishTelegram(ctx, cfg, m, f, m.ReplyTo.Addr, docURL)
	case "slack":
		// Сравниваем с id, который вернул сам Slack: в конфиге канал задан
		// именем, а слэш-команда присылает id.
		if slackChannelID != "" && m.ReplyTo.Addr == slackChannelID {
			return nil
		}
		target = i18n.Tr("slack:откуда просили")
		var token string
		if token, err = core.Secret(cfg.Slack.TokenEnv, "Slack"); err == nil {
			sc := &slackClient{token: token}
			_, err = sc.post(ctx, m.ReplyTo.Addr, RenderSlack(m, f, docURL), "")
		}
	default:
		return nil
	}
	record(st, lg, m.ID, target, "", err)
	if err != nil {
		return []error{fmt.Errorf("%s: %w", target, err)}
	}
	return nil
}

// Свой клиент с таймаутом: у http.DefaultClient его нет, и повисшее
// соединение к Slack или Telegram держало бы слот записи до конца света.
var pubClient = &http.Client{Timeout: 45 * time.Second}

func record(st *core.Store, lg *log.Logger, id, target, url string, err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
		lg.Printf("%s: %v", target, err)
	} else if url != "" {
		lg.Printf("%s: %s", target, url)
	} else {
		lg.Printf(i18n.Tr("%s: отправлено"), target)
	}
	if e := st.SavePublication(id, target, url, msg); e != nil {
		lg.Printf(i18n.Tr("не записал результат публикации: %v"), e)
	}
}

// --- Google Docs -----------------------------------------------------------

// Документ создаётся загрузкой HTML в Drive с конвертацией в Google Doc.
// Собирать его через docs.batchUpdate — это сотни запросов на стили; Drive
// делает то же самое из разметки одним вызовом.
func publishGoogleDoc(ctx context.Context, cfg *core.Config, m *core.Meeting, f *core.Followup, segs []core.Segment) (string, error) {
	scopes := cfg.GoogleDocs.Scopes
	if len(scopes) == 0 {
		scopes = []string{drive.DriveScope}
	}
	opt, err := google.GoogleClient(ctx, cfg, cfg.GoogleDocs.CredentialsFile, cfg.GoogleDocs.Subject, scopes...)
	if err != nil {
		return "", err
	}
	srv, err := drive.NewService(ctx, opt)
	if err != nil {
		return "", err
	}

	name := fmt.Sprintf("%s — %s", m.StartedAt.Format("2006-01-02"), core.OrDash(core.FirstNonEmpty(f.Title, m.Title)))
	file := &drive.File{
		Name:     name,
		MimeType: "application/vnd.google-apps.document",
	}
	if cfg.GoogleDocs.FolderID != "" {
		file.Parents = []string{cfg.GoogleDocs.FolderID}
	}
	res, err := srv.Files.Create(file).
		Media(strings.NewReader(RenderHTML(m, f, segs)), googleapi.ContentType("text/html")).
		SupportsAllDrives(true).
		Fields("id, webViewLink").
		Context(ctx).Do()
	if err != nil {
		return "", err
	}
	return res.WebViewLink, nil
}

// --- Slack -----------------------------------------------------------------

func publishSlack(ctx context.Context, cfg *core.Config, m *core.Meeting, f *core.Followup, segs []core.Segment, docURL string, lg *log.Logger) (string, string, error) {
	token, err := core.Secret(cfg.Slack.TokenEnv, "Slack")
	if err != nil {
		return "", "", err
	}
	sc := &slackClient{token: token}

	ts, err := sc.post(ctx, cfg.Slack.Channel, RenderSlack(m, f, docURL), "")
	if err != nil {
		return "", "", err
	}
	channelID := sc.lastChannelID
	permalink := sc.permalink(ctx, channelID, ts)

	if cfg.Slack.ThreadFull && len(segs) > 0 {
		parts := chunk(brain.RenderTranscript(segs), 3500)
		for i, part := range parts {
			if _, err := sc.post(ctx, channelID, "```"+part+"```", ts); err != nil {
				// Молчать нельзя: расшифровка ушла кусками, и следующий кусок
				// уже не уйдёт — пусть это видно в publications.error.
				return permalink, channelID, fmt.Errorf(i18n.Tr("расшифровка в тред: кусок %d из %d: %w"), i+1, len(parts), err)
			}
			// У Slack примерно одно сообщение в секунду на канал.
			select {
			case <-ctx.Done():
				return permalink, channelID, ctx.Err()
			case <-time.After(time.Second):
			}
		}
	}

	if cfg.Slack.DMOwners {
		sc.dmOwners(ctx, f, permalink, lg)
	}
	return permalink, channelID, nil
}

type slackClient struct {
	token string
	users map[string]string // нормализованное имя -> user id
	// id канала из ответа chat.postMessage. В конфиге канал задают именем
	// («#созвоны»), а слэш-команда присылает id («C0FEED») — сравнивать их
	// напрямую бессмысленно, поэтому берём id, который вернул сам Slack.
	lastChannelID string
}

func (c *slackClient) call(ctx context.Context, method string, body any, out any) error {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://slack.com/api/"+method, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := pubClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	var envelope struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("%s: %s", method, i18n.Tail(string(raw), 200))
	}
	if !envelope.OK {
		return fmt.Errorf("%s: %s", method, envelope.Error)
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func (c *slackClient) post(ctx context.Context, channel, text, threadTS string) (string, error) {
	body := map[string]any{"channel": channel, "text": text, "mrkdwn": true}
	if threadTS != "" {
		body["thread_ts"] = threadTS
	}
	var out struct {
		TS      string `json:"ts"`
		Channel string `json:"channel"`
	}
	if err := c.call(ctx, "chat.postMessage", body, &out); err != nil {
		return "", err
	}
	c.lastChannelID = out.Channel
	return out.TS, nil
}

func (c *slackClient) permalink(ctx context.Context, channel, ts string) string {
	var out struct {
		Permalink string `json:"permalink"`
	}
	q := url.Values{"channel": {channel}, "message_ts": {ts}}
	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://slack.com/api/chat.getPermalink?"+q.Encode(), nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := pubClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	return out.Permalink
}

// dmOwners пишет в личку каждому, на ком осталась задача. Имена из субтитров
// Meet сопоставляются с профилями Slack по нормализованному имени; кого не
// нашли — тихо пропускаем, follow-up в канале всё равно уже опубликован.
func (c *slackClient) dmOwners(ctx context.Context, f *core.Followup, link string, lg *log.Logger) {
	byOwner := map[string][]core.ActionItem{}
	for _, a := range f.ActionItems {
		if a.Owner == "" || a.Owner == "не назначен" {
			continue
		}
		byOwner[a.Owner] = append(byOwner[a.Owner], a)
	}
	if len(byOwner) == 0 {
		return
	}
	if err := c.loadUsers(ctx); err != nil {
		lg.Printf(i18n.Tr("slack: не смог получить список пользователей: %v"), err)
		return
	}
	for owner, items := range byOwner {
		id, ok := c.users[normalizeName(owner)]
		if !ok {
			lg.Printf(i18n.Tr("slack: не нашёл в воркспейсе %q — задачи остались только в канале"), owner)
			continue
		}
		var b strings.Builder
		fmt.Fprint(&b, i18n.Tr("На тебе с созвона:\n"))
		for _, a := range items {
			fmt.Fprintf(&b, "• %s _(%s)_\n", a.What, DueOr(a.Due, i18n.Tr("срок не назван")))
		}
		if link != "" {
			fmt.Fprintf(&b, i18n.Tr("\n<%s|Весь follow-up>"), link)
		}
		if _, err := c.post(ctx, id, b.String(), ""); err != nil {
			lg.Printf(i18n.Tr("slack: личное сообщение %s: %v"), owner, err)
		}
	}
}

func (c *slackClient) loadUsers(ctx context.Context) error {
	if c.users != nil {
		return nil
	}
	c.users = map[string]string{}
	cursor := ""
	for {
		var out struct {
			Members []struct {
				ID      string `json:"id"`
				Deleted bool   `json:"deleted"`
				IsBot   bool   `json:"is_bot"`
				Profile struct {
					RealName    string `json:"real_name"`
					DisplayName string `json:"display_name"`
				} `json:"profile"`
			} `json:"members"`
			Meta struct {
				NextCursor string `json:"next_cursor"`
			} `json:"response_metadata"`
		}
		body := map[string]any{"limit": 200}
		if cursor != "" {
			body["cursor"] = cursor
		}
		if err := c.call(ctx, "users.list", body, &out); err != nil {
			return err
		}
		for _, m := range out.Members {
			if m.Deleted || m.IsBot {
				continue
			}
			for _, n := range []string{m.Profile.RealName, m.Profile.DisplayName} {
				if n = normalizeName(n); n != "" {
					if _, exists := c.users[n]; !exists {
						c.users[n] = m.ID
					}
				}
			}
		}
		cursor = out.Meta.NextCursor
		if cursor == "" {
			return nil
		}
	}
}

func normalizeName(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(s))), " ")
}

// --- Telegram --------------------------------------------------------------

func publishTelegram(ctx context.Context, cfg *core.Config, m *core.Meeting, f *core.Followup, chatID, docURL string) error {
	if chatID == "" {
		return errors.New(i18n.Tr("telegram: не указан чат"))
	}
	token, err := core.Secret(cfg.Telegram.TokenEnv, "Telegram")
	if err != nil {
		return err
	}
	for _, part := range splitForTelegram(RenderTelegram(m, f, docURL)) {
		body := map[string]any{
			"chat_id":              chatID,
			"text":                 part,
			"parse_mode":           "HTML",
			"link_preview_options": map[string]any{"is_disabled": true},
		}
		b, _ := json.Marshal(body)
		req, err := http.NewRequestWithContext(ctx, "POST",
			"https://api.telegram.org/bot"+token+"/sendMessage", bytes.NewReader(b))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := pubClient.Do(req)
		if err != nil {
			return Redact(err, token)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var out struct {
			OK          bool   `json:"ok"`
			Description string `json:"description"`
		}
		_ = json.Unmarshal(raw, &out)
		if !out.OK {
			return fmt.Errorf("telegram: %s", core.FirstNonEmpty(out.Description, i18n.Tail(string(raw), 200)))
		}
		time.Sleep(300 * time.Millisecond) // лимит Telegram на сообщения подряд
	}
	return nil
}

// Redact вырезает токен из текста ошибки. Токен Telegram лежит в пути URL, а
// *url.Error печатает URL целиком — иначе он оседает в логах и в колонке
// publications.error, то есть в незашифрованном файле базы.
func Redact(err error, secrets ...string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	for _, s := range secrets {
		if s != "" {
			msg = strings.ReplaceAll(msg, s, "***")
		}
	}
	return errors.New(msg)
}

// chunk режет по символам, а не по байтам: в кириллице символ — два байта, и
// байтовый разрез разваливает букву пополам.
// SendTelegramText и SendSlackText — простая отправка текста. Публикация
// follow-up для сводок не годится: там разметка, ссылки и деление на части под
// конкретный созвон.
func SendTelegramText(ctx context.Context, cfg *core.Config, chatID, text string) error {
	token, err := core.Secret(cfg.Telegram.TokenEnv, "Telegram")
	if err != nil {
		return err
	}
	for _, part := range chunk(text, 3900) {
		body, _ := json.Marshal(map[string]any{
			"chat_id": chatID, "text": part,
			"link_preview_options": map[string]any{"is_disabled": true},
		})
		req, err := http.NewRequestWithContext(ctx, "POST",
			"https://api.telegram.org/bot"+token+"/sendMessage", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := pubClient.Do(req)
		if err != nil {
			return Redact(err, token)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var out struct {
			OK          bool   `json:"ok"`
			Description string `json:"description"`
		}
		_ = json.Unmarshal(raw, &out)
		if !out.OK {
			return fmt.Errorf("telegram: %s", core.FirstNonEmpty(out.Description, i18n.Tail(string(raw), 200)))
		}
	}
	return nil
}

func SendSlackText(ctx context.Context, cfg *core.Config, channel, text string) error {
	token, err := core.Secret(cfg.Slack.TokenEnv, "Slack")
	if err != nil {
		return err
	}
	sc := &slackClient{token: token}
	_, err = sc.post(ctx, channel, slackEsc(text), "")
	return err
}

func chunk(s string, n int) []string {
	var out []string
	for {
		r := []rune(s)
		if len(r) <= n {
			break
		}
		cut := n
		if i := strings.LastIndex(string(r[:n]), "\n"); i > 0 {
			if p := len([]rune(string(r[:n])[:i])); p > n/2 {
				cut = p
			}
		}
		out = append(out, string(r[:cut]))
		s = strings.TrimLeft(string(r[cut:]), "\n")
	}
	if s != "" {
		out = append(out, s)
	}
	return out
}
