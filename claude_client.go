package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// Как steno получает доступ к Claude.
//
// Обычный путь — ключ из переменной окружения. Но SDK умеет находить учётные
// данные и сам: сначала ANTHROPIC_API_KEY, потом ANTHROPIC_AUTH_TOKEN, потом
// профиль, оставленный командой `ant auth login`. Требовать ключ, когда рабочий
// профиль уже лежит на машине, — лишний шаг на ровном месте.
//
// Для сервера всё-таки нужен именно ключ: профиль живёт у пользователя, а
// сервис работает без него и не может пройти вход в браузере.
func claudeClient(cfg *Config) (anthropic.Client, string, error) {
	key, err := secret(cfg.Claude.APIKeyEnv, "Claude")
	if err == nil {
		return anthropic.NewClient(option.WithAPIKey(key)), "ключ " + cfg.Claude.APIKeyEnv, nil
	}
	// Переменной нет — отдаём разбор SDK: он посмотрит остальные источники,
	// включая профиль ant. Если и там пусто, ошибка придёт от первого запроса,
	// поэтому объясняем заранее, что искали.
	if !anthropicCredentialsAvailable() {
		return anthropic.Client{}, "", fmt.Errorf(
			"нет доступа к Claude: переменная %s пуста и профиль `ant auth login` не найден.\n"+
				"  → ключ создаётся на console.anthropic.com → API keys\n"+
				"  → export %s=sk-ant-…", cfg.Claude.APIKeyEnv, cfg.Claude.APIKeyEnv)
	}
	return anthropic.NewClient(), "профиль ant", nil
}

// anthropicCredentialsAvailable проверяет, есть ли хоть что-то, чем SDK может
// воспользоваться, — чтобы не отправлять запрос заведомо без учётных данных и
// не получать в ответ невнятную четырёхсотку.
func anthropicCredentialsAvailable() bool {
	for _, env := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"} {
		if v, err := secret(env, ""); err == nil && v != "" {
			return true
		}
	}
	return antProfileExists()
}

// antProfileExists ищет профиль, который оставляет `ant auth login`.
func antProfileExists() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	for _, p := range []string{
		filepath.Join(home, ".config", "anthropic"),
		filepath.Join(home, ".anthropic"),
	} {
		if entries, err := os.ReadDir(p); err == nil && len(entries) > 0 {
			return true
		}
	}
	return false
}
