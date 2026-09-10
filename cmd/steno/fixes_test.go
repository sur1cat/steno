package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sur1cat/steno/internal/core"
)

// Настройка ищется не только в текущем каталоге: программу ставят один раз, а
// команды набирают откуда придётся. Запущенный из домашнего каталога serve брал
// умолчания — с выключенной панелью и базой в ./data.
func TestConfigFoundFromAnotherDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := filepath.Join(home, "гдето", "steno.json")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	core.RememberConfigPath(cfg)

	if got := core.ResolveConfigPath(core.DefaultConfigPath); got != cfg {
		t.Fatalf("настройка не нашлась по указателю: %q", got)
	}
	// Названный явно путь не подменяем: иначе опечатка в -c тихо уводила бы на
	// чужую настройку вместо честной ошибки.
	if got := core.ResolveConfigPath("свой.json"); got != "свой.json" {
		t.Fatalf("явно названный путь подменён на %q", got)
	}
}

// Путь к адаптеру в конфиге ведёт внутрь каталога с номером версии, и brew
// upgrade его удаляет. Расшифровка отваливалась у всех, кто обновился.
func TestAdapterFoundAfterUpgrade(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "adapters")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(live, "whisper-cpp.sh")
	if err := os.WriteFile(want, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	gone := filepath.Join(dir, "Cellar", "steno", "0.0.1", "share", "steno", "adapters", "whisper-cpp.sh")
	if got := core.AdapterPath(gone); got != want {
		t.Fatalf("адаптер не нашёлся после обновления: %q", got)
	}
	// Чужую команду не трогаем: подменять человеку его собственный скрипт мы не
	// вправе, даже если он сейчас не на месте.
	own := filepath.Join(dir, "мой-скрипт.sh")
	if got := core.AdapterPath(own); got != own {
		t.Fatalf("подменили чужую команду на %q", got)
	}
}
