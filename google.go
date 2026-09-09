package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
)

// Единственная дверь в Google: календари, почта бота и Drive ходят сюда, каждый
// со своей областью доступа. Раньше эта раскладка была переписана в трёх местах
// и уже начала расходиться: где-то клиент кешировался, где-то нет, где-то ключ
// брался из соседней секции конфига, а где-то только из своей.
//
// Дверей на самом деле две, и выбирает не настройка, а то, что есть.
//
// Подключённый кнопкой аккаунт главнее файла: файл прописывает тот, кто
// разворачивает сервис, а кнопку нажимает человек — и нажимает он её именно
// тогда, когда хочет, чтобы steno работал от его имени. Обратный порядок
// означал бы, что кнопка иногда молча ничего не меняет.
//
// Ключ организации — не секрет в конфиге, а путь к файлу: кто получил этот
// файл, получил календари всей компании.
func googleClient(ctx context.Context, cfg *Config, credFile, subject string, scopes ...string) (option.ClientOption, error) {
	t, err := loadGoogleToken(cfg)
	switch {
	case err == nil:
		// От чужого имени по кнопке работать нельзя: Google выдал доступ к
		// одному ящику, а не к чужим. Молча взять свой вместо запрошенного
		// значило бы читать не тот календарь и не заметить этого.
		if subject != "" && !strings.EqualFold(subject, t.Account) {
			return nil, fmt.Errorf(
				"Google подключён как %s, а тут нужен доступ от имени %s — "+
					"по кнопке steno работает только от того, кто её нажал; "+
					"чужой календарь и чужую почту так не открыть",
				t.Account, subject)
		}
		return googleOAuthClient(ctx, cfg, scopes)
	case !errors.Is(err, os.ErrNotExist):
		// Файл есть, но не читается или испорчен. Сползти отсюда на ключ
		// организации значило бы отвечать не на тот вопрос.
		return nil, err
	}

	if credFile == "" {
		return nil, fmt.Errorf(
			"нет доступа в Google: либо нажми «Подключить Google» в настройках панели, " +
				"либо укажи ключ организации (google_docs.credentials_file) — " +
				"он нужен, чтобы читать чужие календари")
	}
	raw, err := os.ReadFile(credFile)
	if err != nil {
		return nil, fmt.Errorf("ключ service-account: %w", err)
	}
	jwt, err := google.JWTConfigFromJSON(raw, scopes...)
	if err != nil {
		return nil, fmt.Errorf("разбор ключа %s: %w", credFile, err)
	}
	jwt.Subject = subject
	// Контекст без отмены: клиент живёт дольше запроса, которым его создали,
	// и сам обновляет токен по истечении.
	return option.WithHTTPClient(jwt.Client(context.WithoutCancel(ctx))), nil
}

// credentialsFile — свой файл секции, иначе общий из google_docs.
func credentialsFile(own, fallback string) string {
	if own != "" {
		return own
	}
	return fallback
}
