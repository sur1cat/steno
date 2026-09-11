package main

import (
	"strings"
	"testing"
)

// Совет про отставший образ обязан совпадать с тем, что steno запустит.
// Скачанный из реестра образ не поможет, пока конфиг пинит другое имя, —
// и совет «просто docker pull» вёл в тупик ровно так.
func TestImageFixesMatchWhatWillRun(t *testing.T) {
	const reg = "ghcr.io/sur1cat/steno-bot:9.9.9"

	got := strings.Join(imageFixes(reg, reg), "\n")
	if !strings.Contains(got, "docker pull "+reg) || strings.Contains(got, "make bot-image") {
		t.Errorf("конфиг указывает на реестр — совет должен быть только pull: %q", got)
	}

	got = strings.Join(imageFixes("steno-bot:latest", reg), "\n")
	if !strings.Contains(got, "make bot-image") {
		t.Errorf("локальный latest — нужна пересборка: %q", got)
	}
	if !strings.Contains(got, `"image": "`+reg) {
		t.Errorf("совет про pull без смены имени в конфиге — тупик: %q", got)
	}

	// Сборка без версии: в реестре ничего нет, единственная дорога — собрать.
	got = strings.Join(imageFixes("steno-bot:latest", "steno-bot:latest"), "\n")
	if strings.Contains(got, "docker pull") {
		t.Errorf("dev-сборка — тянуть неоткуда, а совет тянет: %q", got)
	}
}
