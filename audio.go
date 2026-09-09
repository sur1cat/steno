package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Recorder пишет монитор PulseAudio-синка, в который Chromium отдаёт звук
// созвона. 16 кГц моно — ровно то, что хочет whisper; opus 32k даёт около
// 14 МБ на час речи.
type Recorder struct {
	cmd     *exec.Cmd
	log     io.WriteCloser
	Path    string
	Started time.Time
}

func startRecording(path, source string) (*Recorder, error) {
	logFile, err := os.Create(path + ".ffmpeg.log")
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("ffmpeg",
		"-nostdin",
		"-f", "pulse", "-i", source,
		"-ac", "1", "-ar", "16000",
		"-c:a", "libopus", "-b:a", "32k",
		"-y", path,
	)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return nil, fmt.Errorf("ffmpeg: %w (установлен ли ffmpeg и поднят ли pulseaudio?)", err)
	}
	return &Recorder{cmd: cmd, log: logFile, Path: path, Started: time.Now()}, nil
}

// Stop просит ffmpeg закрыть контейнер по-хорошему: SIGINT заставляет его
// дописать заголовки. SIGKILL оставил бы битый файл.
func (r *Recorder) Stop() error {
	if r == nil || r.cmd == nil || r.cmd.Process == nil {
		return nil
	}
	defer r.log.Close()
	_ = r.cmd.Process.Signal(syscall.SIGINT)

	done := make(chan error, 1)
	go func() { done <- r.cmd.Wait() }()
	var waitErr error
	select {
	case waitErr = <-done:
	case <-time.After(15 * time.Second):
		_ = r.cmd.Process.Kill()
		<-done
		waitErr = fmt.Errorf("не завершился за 15 с")
	}
	// Код возврата ffmpeg — единственный признак того, что запись оборвалась
	// на середине. Без него убитый по OOM ffmpeg выглядел как нормально
	// закончившийся созвон: файл на месте, размер приличный, а второй половины
	// разговора в нём нет. SIGINT мы посылаем сами, он не ошибка.
	if waitErr != nil && !isInterrupted(waitErr) {
		return fmt.Errorf("ffmpeg оборвался (%w) — запись неполная, подробности в %s.ffmpeg.log",
			waitErr, r.Path)
	}
	st, err := os.Stat(r.Path)
	if err != nil {
		return fmt.Errorf("запись не создана: %w", err)
	}
	if st.Size() < 1024 {
		return fmt.Errorf("запись пустая (%d байт) — проверь PULSE_SINK и что Chromium играет в него", st.Size())
	}
	// Признак обрыва — не код возврата, а длительность: файл, оборванный на
	// середине, короче того, сколько шла запись. Код возврата обманчив в обе
	// стороны, длительность — нет.
	if got := oggDuration(r.Path); got > 0 {
		if want := r.Elapsed(); want > time.Minute && got < want/2 {
			return fmt.Errorf("в записи %s, а шла она %s — запись оборвалась, подробности в %s.ffmpeg.log",
				got.Round(time.Second), want.Round(time.Second), r.Path)
		}
	}
	return nil
}

// oggDuration — сколько звука на самом деле в файле. Ноль, если спросить не у
// кого: отсутствие ffprobe не повод объявлять хорошую запись испорченной.
func oggDuration(path string) time.Duration {
	out, err := exec.Command("ffprobe", "-v", "error",
		"-show_entries", "format=duration", "-of", "csv=p=0", path).Output()
	if err != nil {
		return 0
	}
	sec, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil || sec <= 0 {
		return 0
	}
	return time.Duration(sec * float64(time.Second))
}

// isInterrupted отличает наш собственный SIGINT от настоящей поломки.
// isInterrupted — это мы его остановили, а не он упал.
//
// Проверять только Signaled() было недостаточно: ffmpeg SIGINT перехватывает,
// дописывает заголовки и выходит штатно с кодом 255 — то есть по этому условию
// не проходил. На живом созвоне это стоило целой записи: 232 секунды разговора,
// файл валидный, а созвон помечен как failed и выброшен.
//
// Убитый по OOM ffmpeg сюда по-прежнему не попадёт: он умирает от SIGKILL, а
// это Signaled() с сигналом 9.
func isInterrupted(err error) bool {
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return false
	}
	ws, ok := ee.Sys().(syscall.WaitStatus)
	if !ok {
		return false
	}
	if ws.Signaled() {
		return ws.Signal() == syscall.SIGINT || ws.Signal() == syscall.SIGTERM
	}
	return ws.ExitStatus() == 255
}

func (r *Recorder) Elapsed() time.Duration { return time.Since(r.Started) }
