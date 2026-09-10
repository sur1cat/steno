package i18n

import "testing"

// Порядок ступенек важнее самого факта, что язык определяется: человек,
// поставивший STENO_LANG=en на русской системе, должен получить английский,
// а не наоборот.
func TestEnvLangPrefersExplicitOverLocale(t *testing.T) {
	for _, k := range []string{"STENO_LANG", "LC_ALL", "LC_MESSAGES", "LANG"} {
		t.Setenv(k, "")
	}
	t.Setenv("LANG", "ru_RU.UTF-8")
	if got := resolveLang(envLang()); got != LangRU {
		t.Errorf("LANG=ru_RU.UTF-8 дал %q, а не русский", got)
	}
	t.Setenv("LC_ALL", "en_US.UTF-8")
	if got := resolveLang(envLang()); got != LangEN {
		t.Errorf("LC_ALL важнее LANG: получили %q", got)
	}
	t.Setenv("STENO_LANG", "ru")
	if got := resolveLang(envLang()); got != LangRU {
		t.Errorf("STENO_LANG важнее всех: получили %q", got)
	}
}

// Пустая переменная не должна считаться ответом: она сплошь и рядом
// выставлена пустой в окружении и обязана пропускать ход дальше по цепочке.
func TestEnvLangSkipsEmptyVars(t *testing.T) {
	t.Setenv("STENO_LANG", "")
	t.Setenv("LC_ALL", "   ")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LANG", "ru_UA.UTF-8")
	if got := resolveLang(envLang()); got != LangRU {
		t.Errorf("пустые переменные заслонили LANG: получили %q", got)
	}
}
