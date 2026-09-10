//go:build !windows

package core

import (
	"os/exec"
	"syscall"
)

// Адаптер расшифровки — это shell-обёртка, которая запускает whisper и чистит
// за собой временный WAV на выходе. Убивать её SIGKILL'ом нельзя: обработчик
// не отработает, файл на несколько сотен мегабайт останется, а сам whisper
// осиротеет и продолжит жечь процессор.
func SetProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func TerminateGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
}
