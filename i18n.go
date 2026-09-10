package main

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
	langEN = "en"
	langRU = "ru"
)

var uiLang = resolveLang(envLang())

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
		return langRU
	}
	return langEN
}

// setLang применяет язык из конфига. Пустое значение ничего не меняет:
// STENO_LANG уже мог выставить язык, и конфиг без поля не должен его сбивать.
func setLang(s string) {
	if strings.TrimSpace(s) != "" {
		uiLang = resolveLang(s)
	}
}

// tr переводит строку исходника на текущий язык интерфейса.
func tr(s string) string {
	if uiLang == langRU {
		return s
	}
	if v := trEN[s]; v != "" {
		return v
	}
	return s
}

// trf — tr для строки формата. Отдельная функция, а не fmt.Sprintf(tr(f), a…)
// на месте: так go vet видит обёртку над Printf и продолжает проверять
// аргументы против формата.
func trf(format string, a ...any) string {
	return fmt.Sprintf(tr(format), a...)
}

// scriptName переводит название письменности. Сами значения (см. Script())
// сравниваются с таблицей langScript и потому остаются русскими — на экран
// они выходят только через эту функцию.
func scriptName(s string) string {
	switch s {
	case "кириллица":
		return tr("кириллица")
	case "латиница":
		return tr("латиница")
	case "вперемешку":
		return tr("вперемешку")
	}
	return s
}
