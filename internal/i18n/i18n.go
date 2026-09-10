package i18n

import (
	"fmt"
	"os"
	"strings"
)

// Язык интерфейса.
//
// По умолчанию английский. steno лежит на GitHub, и человек, который запускает
// его в первый раз, английского не выбирает — он его ждёт. Русский включается
// одним из четырёх способов, в этом порядке:
//
//	STENO_LANG=ru steno ui       // разово
//	"lang": "ru"  в steno.json   // насовсем, это же пишет steno setup
//	LANG / LC_ALL / LC_MESSAGES  // как у всякой программы в терминале
//	язык системы                 // иначе — английский
//
// Последняя ступенька нужна из-за macOS: там окружение сплошь и рядом пустое
// (LANG не выставлен ни в одном), а система при этом стоит на ru_KZ. Без неё
// человек, у которого steno месяц говорил по-русски, после обновления получал
// бы английский экран и не понимал, что он сделал не так.
//
// Каталог устроен как gettext: ключ перевода — сама русская строка из кода.
// Из этого следует главное свойство — незнакомая строка возвращается как
// есть, по-русски. Пропущенный перевод виден на экране и чинится одной
// строкой в i18n_en.go, а не роняет вывод и не оставляет пустое место.
const (
	LangEN = "en"
	LangRU = "ru"
)

var UILang = resolveLang(envLang())

// envLang — язык, который просит окружение. STENO_LANG для steno отдельно,
// дальше общепринятые переменные, и в конце язык системы.
func envLang() string {
	for _, k := range []string{"STENO_LANG", "LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return systemLang()
}

// resolveLang понимает и код языка, и локаль целиком: STENO_LANG=ru_RU.UTF-8
// приходит из окружения ничуть не реже, чем ru.
func resolveLang(s string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(s)), "ru") {
		return LangRU
	}
	return LangEN
}

// SetLang применяет язык из конфига. Пустое значение ничего не меняет:
// STENO_LANG уже мог выставить язык, и конфиг без поля не должен его сбивать.
func SetLang(s string) {
	if strings.TrimSpace(s) != "" {
		UILang = resolveLang(s)
	}
}

// Tr переводит строку исходника на текущий язык интерфейса.
func Tr(s string) string {
	if UILang == LangRU {
		return s
	}
	if v := TrEN[s]; v != "" {
		return v
	}
	return s
}

// Trf — Tr для строки формата. Отдельная функция, а не fmt.Sprintf(Tr(f), a…)
// на месте: так go vet видит обёртку над Printf и продолжает проверять
// аргументы против формата.
func Trf(format string, a ...any) string {
	return fmt.Sprintf(Tr(format), a...)
}

// ScriptName переводит название письменности. Сами значения (см. Script())
// сравниваются с таблицей langScript и потому остаются русскими — на экран
// они выходят только через эту функцию.
func ScriptName(s string) string {
	switch s {
	case "кириллица":
		return Tr("кириллица")
	case "латиница":
		return Tr("латиница")
	case "вперемешку":
		return Tr("вперемешку")
	}
	return s
}
