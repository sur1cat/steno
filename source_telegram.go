package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"
)

// Самый дешёвый способ завести бота на неожиданный звонок: кинуть ему ссылку
// в Telegram. Тот же бот, что рассылает итоги, слушает входящие — новой
// инфраструктуры не нужно, и это работает с телефона за две секунды.

type telegramSource struct {
	cfg *Config
	d   *Dispatcher
	log *log.Logger
}

func (s *telegramSource) Name() string { return "telegram" }

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

func (s *telegramSource) Run(ctx context.Context) error {
	token, err := secret(s.cfg.Telegram.TokenEnv, "Telegram")
	if err != nil {
		return err
	}
	allowed := map[string]bool{}
	for _, c := range s.cfg.Telegram.AllowedChats {
		if c != "" {
			allowed[c] = true
		}
	}
	if s.cfg.Telegram.ChatID != "" {
		allowed[s.cfg.Telegram.ChatID] = true
	}
	// Отказ, а не «принимаем от всех». Иначе любой, кто нашёл @-имя бота,
	// прислал бы ему ссылку — и бот пошёл бы на чужой звонок под корпоративным
	// Google-аккаунтом. Соседние источники отказывают так же: почта по
	// умолчанию сужена до своего домена, http не стартует без токена.
	if len(allowed) == 0 {
		return fmt.Errorf("telegram.listen включён, но не задан ни chat_id, ни allowed_chats — " +
			"принимать ссылки от кого угодно нельзя")
	}
	s.log.Printf("telegram: слушаю ссылки на созвоны (%d разрешённых чатов)", len(allowed))

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
			s.log.Printf("telegram: %v", redact(err, token))
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
			text := firstNonEmpty(u.Message.Text, u.Message.Caption)
			meetURL := findMeetURL(text)
			if meetURL == "" {
				continue
			}
			who := firstNonEmpty(u.Message.From.FirstName, u.Message.From.Username, "кто-то")
			m := &Meeting{
				ID:        newID(time.Now()),
				Title:     firstNonEmpty(u.Message.Chat.Title, "Созвон по ссылке из Telegram"),
				MeetURL:   meetURL,
				StartedAt: time.Now(),
				Status:    "recording",
				ReplyTo:   ReplyTo{Kind: "telegram", Addr: chat},
			}
			switch s.d.Start(ctx, adHocKey(meetURL, time.Now()), m, "Telegram, "+who) {
			case Started:
				s.reply(ctx, client, token, chat, "Иду на "+meetURL+" — пришлю follow-up сюда, когда закончится.")
			case Duplicate:
				s.reply(ctx, client, token, chat, "Я уже иду на этот созвон.")
			case NoCapacity:
				s.reply(ctx, client, token, chat, "Сейчас пишу максимум созвонов сразу — на этот не пойду. Попробуй ещё раз, когда освободится.")
			default:
				s.reply(ctx, client, token, chat, "Не смог записать заявку, посмотри лог сервиса.")
			}
		}
	}
}

func (s *telegramSource) getUpdates(ctx context.Context, c *http.Client, token string, offset int64) ([]tgUpdate, error) {
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
		return nil, redact(err, token)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out struct {
		OK          bool       `json:"ok"`
		Description string     `json:"description"`
		Result      []tgUpdate `json:"result"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("getUpdates: %s", tail(string(raw), 200))
	}
	if !out.OK {
		return nil, fmt.Errorf("getUpdates: %s", out.Description)
	}
	return out.Result, nil
}

func (s *telegramSource) reply(ctx context.Context, c *http.Client, token, chatID, text string) {
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
