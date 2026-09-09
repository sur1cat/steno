package main

import (
	"context"
	"fmt"
	"os"

	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
)

// Один и тот же service-account открывает календари, почту бота и Drive —
// каждый раз с другой областью и от имени другого человека. Раньше эта
// раскладка была переписана в трёх местах и уже начала расходиться: где-то
// клиент кешировался, где-то нет, где-то ключ брался из соседней секции
// конфига, а где-то только из своей.
//
// Ключ файла — не секрет в конфиге, а путь к нему: кто получил этот файл,
// получил календари всей компании.
func googleClient(ctx context.Context, credFile, subject string, scopes ...string) (option.ClientOption, error) {
	if credFile == "" {
		return nil, fmt.Errorf("не указан файл ключа service-account")
	}
	raw, err := os.ReadFile(credFile)
	if err != nil {
		return nil, fmt.Errorf("ключ service-account: %w", err)
	}
	cfg, err := google.JWTConfigFromJSON(raw, scopes...)
	if err != nil {
		return nil, fmt.Errorf("разбор ключа %s: %w", credFile, err)
	}
	cfg.Subject = subject
	// Контекст без отмены: клиент живёт дольше запроса, которым его создали,
	// и сам обновляет токен по истечении.
	return option.WithHTTPClient(cfg.Client(context.WithoutCancel(ctx))), nil
}

// credentialsFile — свой файл секции, иначе общий из google_docs.
func credentialsFile(own, fallback string) string {
	if own != "" {
		return own
	}
	return fallback
}
