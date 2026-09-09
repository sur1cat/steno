package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// ffmpeg перехватывает SIGINT, дописывает заголовки и выходит штатно с кодом
// 255 — а проверка искала процесс, убитый сигналом. На живом созвоне это стоило
// целой записи: 232 секунды разговора, файл валидный, созвон помечен failed.
func TestFfmpegInterruptIsNotFailure(t *testing.T) {
	// Код 255 после нашего же SIGINT — это штатный выход ffmpeg.
	cmd := exec.Command("sh", "-c", "exit 255")
	err := cmd.Run()
	if err == nil {
		t.Fatal("ожидали ненулевой код")
	}
	if !isInterrupted(err) {
		t.Fatal("код 255 не признан штатной остановкой — запись будет выброшена")
	}

	// А смерть от SIGKILL — настоящая беда, её проглатывать нельзя: так умирает
	// ffmpeg, убитый по нехватке памяти, и запись обрывается на середине.
	killed := exec.Command("sh", "-c", "kill -9 $$")
	if err := killed.Run(); err == nil {
		t.Fatal("ожидали смерть от сигнала")
	} else if isInterrupted(err) {
		t.Fatal("SIGKILL признан штатной остановкой — обрыв записи пройдёт незамеченным")
	}
}

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
	rememberConfigPath(cfg)

	if got := resolveConfigPath(defaultConfigPath); got != cfg {
		t.Fatalf("настройка не нашлась по указателю: %q", got)
	}
	// Названный явно путь не подменяем: иначе опечатка в -c тихо уводила бы на
	// чужую настройку вместо честной ошибки.
	if got := resolveConfigPath("свой.json"); got != "свой.json" {
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
	if got := adapterPath(gone); got != want {
		t.Fatalf("адаптер не нашёлся после обновления: %q", got)
	}
	// Чужую команду не трогаем: подменять человеку его собственный скрипт мы не
	// вправе, даже если он сейчас не на месте.
	own := filepath.Join(dir, "мой-скрипт.sh")
	if got := adapterPath(own); got != own {
		t.Fatalf("подменили чужую команду на %q", got)
	}
}
