package core

import (
	"regexp"
	"runtime/debug"
	"strings"
)

// version подставляется на сборке релиза через -ldflags -X main.version=…
//
// Переменной раньше не было вовсе: goreleaser передавал этот флаг, а Go молча
// игнорирует -X для несуществующего символа. Версия никуда не попадала, и
// узнать, какой бинарник у тебя стоит, было нельзя — как и подобрать образ бота
// той же версии.
var version = ""

// semverTag — то, что бывает тегом образа: X.Y.Z, без псевдоверсий и +dirty.
var semverTag = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// StenoVersion — версия сборки. Если её не вшили (собрано из исходников), берём
// то, что записал в бинарник сам Go: для `go build` в репозитории это будет
// (devel), а для установки через go install — настоящий тег модуля.
func StenoVersion() string {
	if v := strings.TrimSpace(version); v != "" {
		return strings.TrimPrefix(v, "v")
	}
	// Только настоящий тег. Go для сборки из репозитория подставляет сюда
	// псевдоверсию вида 0.1.3-0.20260909110036-488f5757+dirty — по ней steno
	// пошёл бы искать образ бота с таким тегом, которого нет и быть не может.
	if bi, ok := debug.ReadBuildInfo(); ok {
		if v := strings.TrimPrefix(bi.Main.Version, "v"); semverTag.MatchString(v) {
			return v
		}
	}
	return "dev"
}

// DefaultBotImage — образ той же версии, что и сам бинарник.
//
// Раньше умолчанием был «steno-bot:latest», который человек собирал у себя
// руками. Такой тег ничего не говорит о том, от какой сборки образ: обновив
// steno, легко остаться со вчерашним ботом и разбираться, почему он ведёт себя
// не так. Собранный из исходников остаётся «latest» — там версии просто нет.
func DefaultBotImage() string {
	v := StenoVersion()
	if v == "dev" {
		return "steno-bot:latest"
	}
	return "ghcr.io/sur1cat/steno-bot:" + v
}
