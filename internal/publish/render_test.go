package publish

import (
	"strings"
	"testing"
)

// Длинный follow-up не должен упереться в лимит Telegram на 4096 символов.
func TestSplitForTelegramKeepsEverything(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 600; i++ {
		b.WriteString("• строка про задачу и её владельца\n")
	}
	parts := splitForTelegram(b.String())
	if len(parts) < 2 {
		t.Fatalf("не порезали: %d частей", len(parts))
	}
	total := 0
	for _, p := range parts {
		if n := len([]rune(p)); n > 3900 {
			t.Fatalf("часть длиной %d символов", n)
		}
		total += strings.Count(p, "•")
	}
	if total != 600 {
		t.Fatalf("потеряли строки: %d из 600", total)
	}
}
