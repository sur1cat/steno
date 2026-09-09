package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
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
	return nil
}

// isInterrupted отличает наш собственный SIGINT от настоящей поломки.
func isInterrupted(err error) bool {
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return false
	}
	ws, ok := ee.Sys().(syscall.WaitStatus)
	return ok && ws.Signaled() && ws.Signal() == syscall.SIGINT
}

func (r *Recorder) Elapsed() time.Duration { return time.Since(r.Started) }
