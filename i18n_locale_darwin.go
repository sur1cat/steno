//go:build darwin

package main

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// systemLang — язык, выбранный в системных настройках macOS.
//
// Спрашиваем только когда в окружении пусто, а пусто там на маке почти всегда:
// Terminal.app не выставляет LANG, если в настройках не включить это отдельно.
// Плата — один запуск `defaults` на команду, и только в этом случае.
func systemLang() string {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "defaults", "read", "-g", "AppleLocale").Output()
	if err != nil {
		// Нет `defaults`, нет ключа, не дождались — язык остаётся английским.
		// Ронять из-за этого запуск нельзя: язык интерфейса не то, ради чего
		// стоит отказываться работать.
		return ""
	}
	return strings.TrimSpace(string(out))
}
