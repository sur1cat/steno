package core

import (
	"sort"
	"strings"
)

// Мелкие помощники по строкам, нужные всем сразу.
//
// Лежали по одному в setup.go, doctor.go и publish.go, а звали их из панели,
// терминального интерфейса и заметок. При разборе по пакетам каждая тянула бы
// за собой свой файл целиком, поэтому собраны здесь.

func SortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// CommaList — свой разбор списка через запятую: одноимённая функция живёт в
// файлах панели, которые сейчас переписываются, и завязываться на неё незачем.
func CommaList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// LastLines берёт последние непустые строки: у адаптера самое полезное — в
// конце, там он пишет, чего не хватает и как это поставить.
func LastLines(s string, n int) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

func FirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
