package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Фоновый режим.
//
// `steno serve` занимал терминал целиком, и человеку приходилось держать
// открытым второе окно — а закрытое по ошибке окно уносило с собой сервис
// вместе с календарём и панелью. Отсюда `steno serve -d`, `steno stop` и
// `steno status`.
//
// Главная опасность фонового режима — два запущенных экземпляра. База одна, и
// два процесса, оба слушающие календарь, приведут на встречу двух ботов:
// удвоенная запись, удвоенный follow-up, удвоенный счёт за Claude. Поэтому
// serve в любом виде — и в фоне, и в терминале — сначала берёт замок на
// pid-файле и только потом поднимает источники.
//
// Замок именно flock, а не «есть файл — значит занято»: файл переживает падение
// процесса, а замок отпускает ядро, что бы с процессом ни случилось. Несвежий
// pid-файл после kill -9 не должен запирать сервис навсегда.

const (
	pidFileName = "steno.pid"
	logFileName = "steno.log"

	// Лог режем по размеру: созвоны идут каждый день, а никто не чистит файл,
	// пока не кончится место. Два файла по 8 МБ — это примерно месяц работы и
	// потолок в 16 МБ на диске.
	logSizeLimit   = 8 << 20
	logRotateEvery = time.Minute
)

// errAlreadyRunning — единственная причина, по которой serve отказывается
// стартовать молча. Проверяется через errors.Is в тестах и в daemonize.
type alreadyRunningError struct{}

// Error переводится в момент показа, а не при инициализации пакета:
// пакетные переменные считаются до того, как язык прочитан из конфига.
func (alreadyRunningError) Error() string { return tr("steno уже работает") }

var errAlreadyRunning error = alreadyRunningError{}

// daemonInfo — то, что лежит в pid-файле. Одного pid мало: номера
// переиспользуются, и через сутки после падения steno тот же номер может
// принадлежать чужому процессу. Exe нужен, чтобы это заметить и не отправить
// SIGTERM в чужой процесс.
type daemonInfo struct {
	PID        int       `json:"pid"`
	Started    time.Time `json:"started"`
	Exe        string    `json:"exe"`
	Log        string    `json:"log"`    // пусто — сервис пишет в терминал
	Config     string    `json:"config"` // с какой настройкой запущен
	Background bool      `json:"background"`
	Version    string    `json:"version"`
}

// daemonPaths — куда класть pid-файл и лог. Оба ложатся рядом с настройкой:
// человек и так знает этот каталог (там steno.json, .env и data/), и искать
// лог в /var/log или ~/Library/Logs ему негде.
type daemonPaths struct {
	Dir    string
	PID    string
	Log    string
	Config string // абсолютный путь к настройке; пусто — настройки нет
}

// daemonPathsFor решает, где живёт фоновый экземпляр для этой настройки.
// mkdir не делает: status и stop не должны заводить каталоги.
func daemonPathsFor(cfgPath string) daemonPaths {
	p := resolveConfigPath(cfgPath)
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if st, err := os.Stat(p); err == nil && !st.IsDir() {
		dir := filepath.Dir(p)
		return daemonPaths{Dir: dir, PID: filepath.Join(dir, pidFileName),
			Log: filepath.Join(dir, logFileName), Config: p}
	}
	// Настройки нет — а serve с одними умолчаниями всё равно запускается
	// (панель, HTTP). Складываем служебные файлы туда же, где лежит указатель
	// на настройку, чтобы stop нашёл их из любого каталога.
	dir := stenoStateDir()
	return daemonPaths{Dir: dir, PID: filepath.Join(dir, pidFileName),
		Log: filepath.Join(dir, logFileName)}
}

func stenoStateDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".config", "steno")
	}
	return filepath.Join(os.TempDir(), "steno")
}

// daemonPointerPath — где запущенный экземпляр оставляет след.
//
// stop и status ищут pid-файл рядом с настройкой, а настройку — по тем же
// правилам, что и все команды: сначала в текущем каталоге, потом по указателю
// от setup. У человека, который завёл steno.json руками и запустил serve из
// того каталога, указателя нет, и `steno stop` из домашнего каталога отвечал
// бы «не запущен» при живом сервисе. Поэтому сам serve отмечается: вот мой
// pid-файл, ищите здесь.
func daemonPointerPath() string { return filepath.Join(stenoStateDir(), "daemon") }

// В указателе не один путь, а несколько: последний запуск сверху.
//
// Одной строки не хватило. Человек с двумя установками (рабочая и та, на
// которой пробовал) запускает вторую, останавливает её — и `steno status` без
// -c отвечает «не запущен», хотя первая всё это время работает. Поэтому
// помним последние несколько мест и показываем то, где кто-то живой.
const rememberedDaemonsMax = 5

func rememberDaemon(p daemonPaths) {
	ptr := daemonPointerPath()
	if os.MkdirAll(filepath.Dir(ptr), 0o755) != nil {
		return
	}
	list := []string{p.PID}
	for _, old := range readDaemonPointer() {
		if old != p.PID {
			list = append(list, old)
		}
	}
	if len(list) > rememberedDaemonsMax {
		list = list[:rememberedDaemonsMax]
	}
	_ = os.WriteFile(ptr, []byte(strings.Join(list, "\n")+"\n"), 0o644)
}

func readDaemonPointer() []string {
	b, err := os.ReadFile(daemonPointerPath())
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// rememberedDaemons — места, где недавно работал steno, свежие сначала.
func rememberedDaemons() []daemonPaths {
	var out []daemonPaths
	for _, pid := range readDaemonPointer() {
		dir := filepath.Dir(pid)
		out = append(out, daemonPaths{Dir: dir, PID: pid,
			Log: filepath.Join(dir, logFileName), Config: filepath.Join(dir, defaultConfigPath)})
	}
	return out
}

// runState — что мы знаем о процессе из pid-файла.
type runState int

const (
	daemonStopped runState = iota // pid-файла нет
	daemonRunning                 // процесс жив и это точно steno
	daemonStale                   // файл остался от прошлого запуска
)

// inspectDaemon отвечает на единственный важный вопрос: можно ли запускаться.
//
// Проверок три, и каждая ловит свой случай:
//   - процесса с таким pid нет вовсе — падение, kill -9, перезагрузка;
//   - процесс есть, но это не steno — номер переиспользован системой;
//   - процесс есть, похож на steno, но замок на файле свободен — значит steno
//     этот файл не держит (например, файл скопировали из другой установки).
//
// Первые две дешёвые и работают везде; третья — главная, потому что замок
// отпускает ядро, а не программа.
func inspectDaemon(p daemonPaths) (daemonInfo, runState) {
	info, err := readDaemonInfo(p.PID)
	if err != nil {
		return daemonInfo{}, daemonStopped
	}
	if info.PID <= 0 || !processAlive(info.PID) {
		return info, daemonStale
	}
	if !processLooksLike(info.PID, info.Exe) {
		return info, daemonStale
	}
	if held, known := lockHeld(p.PID); known && !held {
		return info, daemonStale
	}
	return info, daemonRunning
}

// lockHeld — переменная, а не функция, ради тестов: на сетевой файловой системе
// flock может не работать вовсе, и проверить, что тогда решает опознание
// процесса, иначе нечем.
var lockHeld = lockHeldFile

// sameFile проверяет, что заперли тот самый файл, который лежит по пути. Между
// открытием и flock прошлый хозяин мог файл удалить — тогда замок висит на
// inode, до которого никому уже не добраться.
func sameFile(f *os.File, path string) (bool, error) {
	a, err := f.Stat()
	if err != nil {
		return false, err
	}
	b, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return os.SameFile(a, b), nil
}

func readDaemonInfo(path string) (daemonInfo, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return daemonInfo{}, err
	}
	var info daemonInfo
	if err := json.Unmarshal(b, &info); err != nil {
		return daemonInfo{}, fmt.Errorf("%s: %w", path, err)
	}
	return info, nil
}

// pidLock — взятый замок. Держать его нужно до конца работы процесса: закроется
// файл — отпустится замок, и второй steno спокойно запустится поверх первого.
type pidLock struct {
	path string
	f    *os.File
}

// heldLock не даёт сборщику мусора закрыть файл замка. Замок живёт столько же,
// сколько процесс, и отдельного места для него нет.
var heldLock *pidLock

// acquireDaemonLock берёт замок и записывает в pid-файл, кто его держит.
//
// Если замок занят — возвращает errAlreadyRunning с pid хозяина, чтобы человек
// увидел не «что-то не так», а «работает pid 431, останови его steno stop».
func acquireDaemonLock(p daemonPaths, info daemonInfo) (*pidLock, error) {
	if err := os.MkdirAll(filepath.Dir(p.PID), 0o755); err != nil {
		return nil, err
	}
	// Три попытки — на гонку «взяли замок ровно тогда, когда прошлый хозяин
	// удалил файл»: замок оказался бы на inode, которого нет по этому пути, и
	// следующий steno завёл бы новый файл и новый замок. Проверяем, что
	// заперли именно тот файл, который лежит по пути.
	for attempt := 0; attempt < 3; attempt++ {
		f, err := os.OpenFile(p.PID, os.O_RDWR|os.O_CREATE, 0o644)
		if err != nil {
			return nil, err
		}
		if err := lockFile(f); err != nil {
			other, rerr := readDaemonInfo(p.PID)
			f.Close()
			if rerr == nil && other.PID > 0 {
				return nil, fmt.Errorf(tr("%w: pid %d, запущен %s"),
					errAlreadyRunning, other.PID, other.Started.Local().Format("02.01 15:04"))
			}
			return nil, errAlreadyRunning
		}
		if same, err := sameFile(f, p.PID); err == nil && !same {
			unlockFile(f)
			f.Close()
			continue
		}
		if err := writeDaemonInfo(f, info); err != nil {
			unlockFile(f)
			f.Close()
			return nil, err
		}
		return &pidLock{path: p.PID, f: f}, nil
	}
	return nil, fmt.Errorf(tr("не удалось взять %s: файл подменяют на ходу"), p.PID)
}

func writeDaemonInfo(f *os.File, info daemonInfo) error {
	b, err := json.Marshal(info)
	if err != nil {
		return err
	}
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.WriteAt(append(b, '\n'), 0); err != nil {
		return err
	}
	return f.Sync()
}

// release отпускает замок. Файл остаётся на месте: по нему status покажет, чем
// кончился прошлый запуск, а inspectDaemon всё равно отличит его от живого по
// свободному замку. Удалять файл — значит открыть гонку с тем, кто в этот
// момент его открывает.
func (l *pidLock) release() {
	if l == nil || l.f == nil {
		return
	}
	unlockFile(l.f)
	l.f.Close()
	l.f = nil
}

// --- лог ---------------------------------------------------------------------

// openLog открывает лог на дозапись. O_APPEND обязателен: без него обрезание
// файла при ротации оставило бы дырку из нулей размером со старый лог, потому
// что смещение в открытом файле осталось бы прежним.
func openLog(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
}

// rotateLog режет лог, когда он перерос предел.
//
// Копия и обрезание, а не переименование: лог открыт у работающего процесса, и
// переименованный файл он продолжил бы писать под старым именем — размер
// перестал бы уменьшаться вовсе. Копия теряет разве что строку, записанную
// между копированием и обрезанием; для журнала это приемлемо, для остановки
// сервиса ради ротации — нет.
func rotateLog(path string, limit int64) (bool, error) {
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if st.Size() <= limit {
		return false, nil
	}
	src, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer src.Close()
	old := path + ".1"
	dst, err := os.OpenFile(old, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return false, err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return false, err
	}
	if err := dst.Close(); err != nil {
		return false, err
	}
	if err := os.Truncate(path, 0); err != nil {
		return false, err
	}
	return true, nil
}

// watchLogSize сторожит размер лога, пока сервис работает. Проверка по таймеру,
// а не на каждой строке: лог пишется через unix-fd, унаследованный от родителя
// (или от launchd), и перехватить каждую запись мы не можем.
func watchLogSize(done <-chan struct{}, path string, limit int64, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			if cut, _ := rotateLog(path, limit); cut {
				resetLogOffset()
			}
		}
	}
}

// --- разбор аргументов -------------------------------------------------------

// cutDaemonFlag вынимает из аргументов serve флаг фонового режима. Дальше
// аргументы уезжают в дочерний процесс уже без него — иначе тот снова ушёл бы
// в фон, и так до бесконечности.
func cutDaemonFlag(args []string) ([]string, bool) {
	out := make([]string, 0, len(args))
	found := false
	for _, a := range args {
		switch a {
		case "-d", "--d", "-daemon", "--daemon", "-b", "--background":
			found = true
		default:
			out = append(out, a)
		}
	}
	return out, found
}

// configArg достаёт -c из аргументов до того, как их разберёт flag. Знать путь
// к настройке нужно раньше: от него зависит, где лежат pid-файл и лог.
func configArg(args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		for _, pre := range []string{"-c=", "--c=", "-config=", "--config="} {
			if v, ok := strings.CutPrefix(a, pre); ok {
				return v
			}
		}
		if a == "-c" || a == "--c" || a == "-config" || a == "--config" {
			if i+1 < len(args) {
				return args[i+1]
			}
		}
	}
	return envOr("STENO_CONFIG", defaultConfigPath)
}

// --- вывод -------------------------------------------------------------------

func humanSize(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf(tr("%d Б"), n)
	case n < 1024*1024:
		return fmt.Sprintf(tr("%.0f КБ"), float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf(tr("%.1f МБ"), float64(n)/(1024*1024))
	}
	return fmt.Sprintf(tr("%.1f ГБ"), float64(n)/(1024*1024*1024))
}

// sinceText — «3 ч 12 мин назад» вместо «3h12m14.7s».
func sinceText(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf(tr("%d сек"), int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf(tr("%d мин"), int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf(tr("%d ч %d мин"), int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf(tr("%d дн %d ч"), int(d.Hours())/24, int(d.Hours())%24)
}

func fileSize(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return st.Size()
}
