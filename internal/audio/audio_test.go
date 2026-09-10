package audio

import (
	"os/exec"
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
