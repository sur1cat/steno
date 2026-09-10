//go:build !darwin

package main

// systemLang — на Linux и Windows язык системы приезжает теми же переменными
// окружения, которые уже проверены выше. Спрашивать больше некого.
func systemLang() string { return "" }
