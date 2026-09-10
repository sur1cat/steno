//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Windows steno не выпускает (goreleaser собирает linux и darwin), но пакет
// должен собираться и под него. Заглушки честные: замка нет, фонового режима
// нет, а не «сделали вид, что получилось».

func lockFile(f *os.File) error { return nil }

func unlockFile(f *os.File) {}

// known=false — «не знаю»: без замка решение принимают проверки процесса.
func lockHeldFile(path string) (held, known bool) { return false, false }

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = p.Release()
	return true
}

func processName(pid int) string { return "" }

func processLooksLike(pid int, exe string) bool {
	if strings.TrimSpace(exe) == "" {
		return true
	}
	name := processName(pid)
	if name == "" {
		return true
	}
	return filepath.Base(name) == filepath.Base(exe)
}

func detachChild(cmd *exec.Cmd) {}

func signalStop(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}

func resetLogOffset() {}

func daemonSupported() bool { return false }
