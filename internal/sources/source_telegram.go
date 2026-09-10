package sources

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/sur1cat/steno/internal/bot"
	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
	"github.com/sur1cat/steno/internal/publish"
)

// Самый дешёвый способ завести бота на неожиданный звонок: кинуть ему ссылку
// в Telegram. Тот же бот, что рассылает итоги, слушает входящие — новой
// инфраструктуры не нужно, и это работает с телефона за две секунды.

type TelegramSource struct {
	Cfg *core.Config
	D   *Dispatcher
	Log *log.Logger
}

func (s *TelegramSource) Name() string { return "telegram" }

type tgUpdate struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		MessageID int64  `json:"message_id"`
		Text      string `json:"text"`
		Caption   string `json:"caption"`
		Chat      struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
		} `json:"chat"`
		From struct {
			FirstName string `json:"first_name"`
			Username  string `json:"username"`
		} `json:"from"`
	} `json:"message"`
}

func (s *TelegramSource) Run(ctx context.Context) error {
	token, err := core.Secret(s.Cfg.Telegram.TokenEnv, "Telegram")
	if err != nil {
		return err
	}
	allowed := map[string]bool{}
	for _, c := range s.Cfg.Telegram.AllowedChats {
		if c != "" {
			allowed[c] = true
		}
	}
	if s.Cfg.Telegram.ChatID != "" {
		allowed[s.Cfg.Telegram.ChatID] = true
	}
	// Отказ, а не «принимаем от всех». Иначе любой, кто нашёл @-имя бота,
	// прислал бы ему ссылку — и бот пошёл бы на чужой звонок под корпоративным
	// Google-аккаунтом. Соседние источники отказывают так же: почта по
	// умолчанию сужена до своего домена, http не стартует без токена.
	if len(allowed) == 0 {
		return errors.New(i18n.Tr("telegram.listen включён, но не задан ни chat_id, ни allowed_chats — ") +
			i18n.Tr("принимать ссылки от кого угодно нельзя"))
	}
	s.Log.Printf(i18n.Tr("telegram: слушаю ссылки на созвоны (%d разрешённых чатов)"), len(allowed))

	// Long polling: держим соединение 30 секунд, поэтому клиенту нужен запас.
	client := &http.Client{Timeout: 60 * time.Second}
	var offset int64

	for {
		if ctx.Err() != nil {
			return nil
		}
		updates, err := s.getUpdates(ctx, client, token, offset)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			// Токен лежит в пути URL, а *url.Error печатает URL целиком.
			s.Log.Printf("telegram: %v", publish.Redact(err, token))
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(10 * time.Second):
			}
			continue
		}
		for _, u := range updates {
			offset = u.UpdateID + 1
			if u.Message == nil {
				continue
			}
			chat := strconv.FormatInt(u.Message.Chat.ID, 10)
			if !allowed[chat] {
				continue
			}
			text := core.FirstNonEmpty(u.Message.Text, u.Message.Caption)
			meetURL := bot.FindMeetURL(text)
			if meetURL == "" {
				// Просто болтовня в чате — молчим. А вот ссылка на площадку,
				// которую мы не умеем, молчания не заслуживает: тишина
				// неотличима от «бот сломался», и человек выясняет это на
				// живом созвоне, когда бот не пришёл.
				if hint := bot.LinkHint(text); hint != "" {
					s.reply(ctx, client, token, chat, hint+".")
				}
				continue
			}
			who := core.FirstNonEmpty(u.Message.From.FirstName, u.Message.From.Username, i18n.Tr("кто-то"))
			m := &core.Meeting{
				ID:        core.NewID(time.Now()),
				Title:     core.FirstNonEmpty(u.Message.Chat.Title, i18n.Tr("Созвон по ссылке из Telegram")),
				MeetURL:   meetURL,
				StartedAt: time.Now(),
				Status:    "recording",
				ReplyTo:   core.ReplyTo{Kind: "telegram", Addr: chat},
			}
			switch s.D.Start(ctx, adHocKey(meetURL, time.Now()), m, "Telegram, "+who) {
			case Started:
				s.reply(ctx, client, token, chat, i18n.Tr("Иду на ")+meetURL+i18n.Tr(" — пришлю follow-up сюда, когда закончится."))
			case Duplicate:
				s.reply(ctx, client, token, chat, i18n.Tr("Я уже иду на этот созвон."))
			case NoCapacity:
				s.reply(ctx, client, token, chat, i18n.Tr("Сейчас пишу максимум созвонов сразу — на этот не пойду. Попробуй ещё раз, когда освободится."))
			default:
				s.reply(ctx, client, token, chat, i18n.Tr("Не смог записать заявку, посмотри лог сервиса."))
			}
		}
	}
}

func (s *TelegramSource) getUpdates(ctx context.Context, c *http.Client, token string, offset int64) ([]tgUpdate, error) {
	body, _ := json.Marshal(map[string]any{
		"offset":          offset,
		"timeout":         30,
		"allowed_updates": []string{"message"},
	})
	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://api.telegram.org/bot"+token+"/getUpdates", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return nil, publish.Redact(err, token)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out struct {
		OK          bool       `json:"ok"`
		Description string     `json:"description"`
		Result      []tgUpdate `json:"result"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("getUpdates: %s", i18n.Tail(string(raw), 200))
	}
	if !out.OK {
		return nil, fmt.Errorf("getUpdates: %s", out.Description)
	}
	return out.Result, nil
}

func (s *TelegramSource) reply(ctx context.Context, c *http.Client, token, chatID, text string) {
	body, _ := json.Marshal(map[string]any{
		"chat_id":              chatID,
		"text":                 text,
		"link_preview_options": map[string]any{"is_disabled": true},
	})
	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://api.telegram.org/bot"+token+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if resp, err := c.Do(req); err == nil {
		resp.Body.Close()
	}
}
