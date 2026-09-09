//go:build !windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Всё, что про процессы и замки, живёт здесь: на Windows этих вызовов нет, а
// steno собирается под linux и darwin.

func lockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func unlockFile(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

// lockHeldFile отвечает, держит ли кто-то замок на файле, не отбирая его.
//
// known=false — «не знаю»: файла нет или ядро ответило чем-то третьим. Врать
// «свободен» в этом случае нельзя: на этом ответе строится решение запускаться,
// а два steno на одной базе приведут двух ботов на один созвон.
func lockHeldFile(path string) (held, known bool) {
	f, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return false, false
	}
	defer f.Close()
	// flock не требует прав на запись — замок висит на открытом файле, а не на
	// содержимом.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return true, true
		}
		return false, false
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false, true
}

// processAlive — есть ли процесс с таким номером. EPERM значит «есть, но
// чужой»: это тоже живой процесс, и запускаться поверх него нельзя.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// processName — имя исполняемого файла процесса.
//
// На Linux читаем /proc, на macOS спрашиваем ps: подпроцесс дороже, но зовём мы
// его только в stop, status и на запуске. Пустая строка значит «не удалось
// узнать», а не «процесса нет».
func processName(pid int) string {
	if b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm"); err == nil {
		return strings.TrimSpace(string(b))
	}
	// -ww, потому что ps обрезает вывод по ширине терминала, а полный путь к
	// бинарнику в неё не влезает.
	out, err := exec.Command("ps", "-ww", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// processLooksLike — тот ли это процесс, что записал pid-файл.
//
// Номера процессов переиспользуются. Через сутки после падения steno тот же
// номер носит чужая программа, и `steno stop` без этой проверки отправил бы
// SIGTERM ей. Сравниваем с тем, что записал в файл сам steno, а не с зашитым
// именем: бинарник может называться как угодно, в тестах он вообще steno.test.
func processLooksLike(pid int, exe string) bool {
	if strings.TrimSpace(exe) == "" {
		return true // старый pid-файл без имени — судим по замку
	}
	name := processName(pid)
	if name == "" {
		return true // не спросили — не выдумываем
	}
	return sameProcessName(name, exe)
}

func sameProcessName(got, want string) bool {
	got, want = filepath.Base(got), filepath.Base(want)
	if got == want {
		return true
	}
	// Linux обрезает /proc/<pid>/comm до 15 символов, и длинное имя доезжает
	// огрызком: «steno-with-long» вместо «steno-with-long-name».
	if len(got) >= 15 && strings.HasPrefix(want, got) {
		return true
	}
	return false
}

// detachChild уводит дочерний процесс в собственный сеанс. Без этого Ctrl+C в
// терминале, из которого запустили `serve -d`, убивал бы и фоновый steno: сигнал
// уходит всей группе процессов терминала.
func detachChild(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// signalStop просит сервис остановиться. Именно SIGTERM: serve ловит его и
// ждёт идущие записи, SIGKILL оборвал бы созвон на середине.
func signalStop(pid int) error {
	return syscall.Kill(pid, syscall.SIGTERM)
}

// resetLogOffset ставит запись в лог обратно в начало после обрезания.
//
// Файл открыт с O_APPEND, и смещение пересчитывается на каждой записи — обычно
// это не нужно. Но лог мог открыть не steno (launchd открывает его сам), и если
// там O_APPEND не выставлен, обрезанный файл дописывался бы со старого
// смещения: мегабайты нулей вместо журнала.
func resetLogOffset() {
	for _, fd := range []int{1, 2} {
		_, _ = syscall.Seek(fd, 0, 0)
	}
}

func daemonSupported() bool { return true }
