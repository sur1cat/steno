package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
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
	// Закрывается, когда ffmpeg вышел; exitErr — с чем. Ждать начинаем сразу
	// при старте, а не в Stop, и вот зачем: пока Wait никто не вызвал, про
	// упавший ffmpeg ничего не известно — процесс висит зомби, а вопрос
	// «пишется ли вообще» остаётся без ответа до конца записи.
	//
	// Боту это было безразлично: он всё равно сидит в звонке до конца. Заметке
	// нет. Микрофон отказывает в первые доли секунды (нет такого устройства,
	// macOS не дала доступ), а отваливается — посреди записи, когда наушники
	// уходят из зоны. И в том, и в другом случае человек продолжает говорить в
	// никуда, пока ему не скажут.
	//
	// Канал закрывается, а не отдаёт значение: наблюдателей двое — тот, кто
	// проверяет старт, и сторож, — и ни один не должен забрать результат у
	// другого.
	done    chan struct{}
	exitErr error
	// Что советовать, когда файл вышел пустым. У бота и у микрофона причины
	// разные, а совет «проверь PULSE_SINK» человеку с микрофоном не говорит
	// ничего.
	emptyHint string
}

// newRecorder заводит ожидание сразу: Wait вызывается ровно один раз, в
// фоне, а Stop и сторож потом читают готовый результат.
func newRecorder(cmd *exec.Cmd, logFile io.WriteCloser, path string) *Recorder {
	r := &Recorder{cmd: cmd, log: logFile, Path: path, Started: time.Now(),
		done: make(chan struct{})}
	go func() {
		r.exitErr = cmd.Wait()
		close(r.done)
	}()
	return r
}

// Exited закрывается, когда ffmpeg завершился — сам или по нашей просьбе.
// Читать exitErr после этого безопасно: закрытие канала это гарантирует.
func (r *Recorder) Exited() <-chan struct{} {
	if r == nil || r.done == nil {
		return nil // nil-канал в select молчит вечно — это и нужно
	}
	return r.done
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
		return nil, fmt.Errorf(tr("ffmpeg: %w (установлен ли ffmpeg и поднят ли pulseaudio?)"), err)
	}
	return newRecorder(cmd, logFile, path), nil
}

// Stop просит ffmpeg закрыть контейнер по-хорошему: SIGINT заставляет его
// дописать заголовки. SIGKILL оставил бы битый файл.
func (r *Recorder) Stop() error {
	if r == nil || r.cmd == nil || r.cmd.Process == nil {
		return nil
	}
	defer r.log.Close()
	_ = r.cmd.Process.Signal(syscall.SIGINT)

	done := r.done
	if done == nil { // запись, заведённая не через newRecorder (тесты)
		done = make(chan struct{})
		go func() { r.exitErr = r.cmd.Wait(); close(done) }()
	}
	var waitErr error
	select {
	case <-done:
		waitErr = r.exitErr
	case <-time.After(15 * time.Second):
		_ = r.cmd.Process.Kill()
		<-done
		waitErr = errors.New(tr("не завершился за 15 с"))
	}
	// Код возврата ffmpeg — единственный признак того, что запись оборвалась
	// на середине. Без него убитый по OOM ffmpeg выглядел как нормально
	// закончившийся созвон: файл на месте, размер приличный, а второй половины
	// разговора в нём нет. SIGINT мы посылаем сами, он не ошибка.
	if waitErr != nil && !isInterrupted(waitErr) {
		return fmt.Errorf(tr("ffmpeg оборвался (%w) — запись неполная, подробности в %s.ffmpeg.log"),
			waitErr, r.Path)
	}
	st, err := os.Stat(r.Path)
	if err != nil {
		return fmt.Errorf(tr("запись не создана: %w"), err)
	}
	if st.Size() < 1024 {
		hint := r.emptyHint
		if hint == "" {
			hint = tr("проверь PULSE_SINK и что Chromium играет в него")
		}
		return fmt.Errorf(tr("запись пустая (%d байт) — %s"), st.Size(), hint)
	}
	// Признак обрыва — не код возврата, а длительность: файл, оборванный на
	// середине, короче того, сколько шла запись. Код возврата обманчив в обе
	// стороны, длительность — нет.
	if got := oggDuration(r.Path); got > 0 {
		if want := r.Elapsed(); want > time.Minute && got < want/2 {
			return fmt.Errorf(tr("в записи %s, а шла она %s — запись оборвалась, подробности в %s.ffmpeg.log"),
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

// --- микрофон ---------------------------------------------------------------

// Второй источник звука: не монитор синка внутри контейнера, а микрофон той
// машины, за которой сидит человек. Всё остальное — формат файла, закрытие
// контейнера по SIGINT, проверки на обрыв — общее с записью созвона, потому
// что дальше по конвейеру заметку и созвон никто не различает.

// micStartCheck — сколько ждать, прежде чем поверить, что запись пошла.
// Отказ приходит быстро: нет такого устройства, macOS не дала доступ к
// микрофону, ffmpeg собран без avfoundation — на свободной машине это доли
// секунды. Полторы секунды означают «нажал и сразу увидел, что не пишется»
// вместо часа молчания, обнаруженного на пустой расшифровке.
//
// Переменная, а не константа, ровно ради теста: на занятой машине ffmpeg
// заводится дольше полутора секунд, и тест про «упавший старт замечен»
// начинает мигать. Проверить он должен разбор отказа, а не скорость соседей.
// Промах здесь ничего не теряет: не замеченный тут упавший ffmpeg ловит
// сторож записи (noteHub.watch) в ту же миллисекунду, только сообщение о нём
// доедет до человека следующим опросом, а не ответом на нажатие.
var micStartCheck = 1500 * time.Millisecond

// startMicRecording пишет микрофон в тот же формат, что и запись созвона:
// 16 кГц моно opus, потому что этого ждёт whisper и потому что заметка обязана
// проходить через тот же адаптер расшифровки.
//
// device — пусто (устройство по умолчанию системы), индекс или имя из
// micDevices. max — потолок записи, он же страховка от осиротевшего ffmpeg.
func startMicRecording(path, device string, max time.Duration) (*Recorder, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, fmt.Errorf(tr("нужен ffmpeg, чтобы писать с микрофона: %w"), err)
	}
	input, err := micInputArgs(device)
	if err != nil {
		return nil, err
	}
	logFile, err := os.Create(path + ".ffmpeg.log")
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("ffmpeg", micRecordArgs(path, input, max)...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return nil, fmt.Errorf(tr("ffmpeg не запустился: %w"), err)
	}
	r := newRecorder(cmd, logFile, path)
	r.emptyHint = tr("похоже, микрофон молчал: проверь, то ли устройство выбрано и не выключен ли звук")
	// Ждём чуть-чуть и смотрим, жив ли он. Живой ffmpeg за это время ничего не
	// сообщает — его молчание и есть признак того, что запись пошла.
	if err := r.startedOK(micStartCheck); err != nil {
		_ = r.log.Close()
		return nil, fmt.Errorf("%w%s", err, micTrouble(path+".ffmpeg.log"))
	}
	return r, nil
}

// micFilter — то, чем запись с микрофона отличается от записи созвона, кроме
// источника.
//
// У созвона звук приходит с готовой громкостью: Chromium сводит участников
// сам. У микрофона громкость зависит от того, как далеко человек сидит и как
// тихо говорит, и разброс огромный. На живой проверке речь легла на −45 дБ —
// вдвадцатеро тише обычной, — и whisper потерял первый абзац целиком: тот
// самый, который начинался словами «что нужно не забыть». В записи он есть,
// его слышно; в расшифровке его не было. Тот же файл, пропущенный через
// speechnorm, распознался целиком, да ещё и «докер» перестал быть «дочей».
//
// speechnorm, а не dynaudnorm: он поднимает речь до порога и ограничен
// лимитером, то есть громкому голосу почти ничего не делает, а тихий вытягивает
// — e задаёт потолок усиления, а не само усиление. Тишину между фразами он
// поднимает тоже, но её отрезает VAD в адаптере расшифровки, и на проверке
// выдуманных строк не появилось.
//
// highpass снимает гул кулера и стола, на котором стоит ноутбук.
const micFilter = "highpass=f=70,speechnorm=e=12.5:r=0.0001:l=1"

// micRecordArgs — вся команда записи одним куском, чтобы её можно было
// прочитать глазами и проверить тестом. Формат обязан совпадать с записью
// созвона: 16 кГц моно opus — это то, что ждёт whisper.
//
// -t — не дубликат потолка, а страховка от сироты. ffmpeg живёт своим
// процессом, и убитый сервис его не уносит: на живой проверке `steno stop`
// посреди заметки оставил ffmpeg писать микрофон дальше, уже без всякого
// сторожа — а сторож умер вместе с сервисом. С -t осиротевшая запись
// заканчивается сама, дописав контейнер, а не растёт до конца места на диске.
// Запас над потолком нужен, чтобы обычную остановку делал сторож: он ещё и
// строку в базе закрывает, а ffmpeg про базу ничего не знает.
func micRecordArgs(path string, input []string, max time.Duration) []string {
	args := append([]string{"-nostdin", "-nostats"}, input...)
	if max <= 0 {
		max = noteMaxDefault
	}
	args = append(args, "-t", fmt.Sprintf("%.0f", (max+5*time.Minute).Seconds()))
	return append(args,
		"-af", micFilter,
		"-ac", "1", "-ar", "16000",
		"-c:a", "libopus", "-b:a", "32k",
		"-y", path)
}

// startedOK возвращает ошибку, если ffmpeg успел умереть за отпущенное время.
func (r *Recorder) startedOK(within time.Duration) error {
	select {
	case <-r.Exited():
		if r.exitErr == nil {
			return errors.New(tr("ffmpeg сразу закончил запись"))
		}
		return fmt.Errorf(tr("запись не пошла: %w"), r.exitErr)
	case <-time.After(within):
		return nil
	}
}

// micTrouble — хвост лога ffmpeg плюс подсказка про разрешение macOS. Само
// сообщение ffmpeg человеку мало что говорит («Input/output error»), а самая
// частая причина именно эта: система не дала доступ к микрофону тому
// приложению, из которого запущен steno.
func micTrouble(logPath string) string {
	var b strings.Builder
	if out, err := os.ReadFile(logPath); err == nil {
		if t := tail(strings.TrimSpace(string(out)), 400); t != "" {
			b.WriteString("\n")
			b.WriteString(t)
		}
	}
	if runtime.GOOS == "darwin" {
		b.WriteString(tr("\nЕсли микрофона тут быть не должно: macOS спрашивает разрешение у того ") +
			tr("приложения, из которого запущен steno. Системные настройки → ") +
			tr("Конфиденциальность и безопасность → Микрофон."))
	}
	return b.String()
}

// micInputArgs — как на этой системе попросить у ffmpeg микрофон.
func micInputArgs(device string) ([]string, error) {
	d := strings.TrimSpace(device)
	switch runtime.GOOS {
	case "darwin":
		// avfoundation берёт вход как "видео:звук". Пусто до двоеточия — значит
		// только звук, и картинка с камеры не пишется вовсе. "default" — то
		// устройство, которое выбрано в системных настройках звука; индекс из
		// micDevices тоже годится.
		d = strings.TrimPrefix(d, ":")
		if d == "" {
			d = "default"
		}
		return []string{"-f", "avfoundation", "-i", ":" + d}, nil
	case "linux":
		if d == "" {
			d = "default"
		}
		return []string{"-f", "pulse", "-i", d}, nil
	case "windows":
		if d == "" {
			return nil, errors.New(tr("на Windows нужно назвать микрофон: ") +
				tr("ffmpeg -list_devices true -f dshow -i dummy покажет имена"))
		}
		return []string{"-f", "dshow", "-i", "audio=" + d}, nil
	}
	return nil, fmt.Errorf(tr("не знаю, как писать микрофон на %s"), runtime.GOOS)
}

// MicDevice — микрофон, каким его видит ffmpeg. Index — то, что уходит в
// -i ":N"; имя показывается человеку.
type MicDevice struct {
	Index string `json:"index"`
	Name  string `json:"name"`
}

// micDevices — список микрофонов. На macOS спрашиваем сам ffmpeg: список
// AVFoundation он печатает в stderr и завершается с ошибкой (входа-то ему не
// дали), поэтому код возврата здесь ничего не значит и не проверяется.
func micDevices() ([]MicDevice, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, fmt.Errorf(tr("нужен ffmpeg: %w"), err)
	}
	if runtime.GOOS != "darwin" {
		// На Linux устройства называет pulseaudio, и имена у него свои
		// («alsa_input.pci-0000_00_1f.3.analog-stereo»). Гадать за него не
		// будем: pactl list short sources печатает их точнее.
		return nil, fmt.Errorf(tr("список микрофонов steno умеет спрашивать только на macOS; ")+
			tr("на %s назови устройство сам"), runtime.GOOS)
	}
	cmd := exec.Command("ffmpeg", "-hide_banner", "-f", "avfoundation",
		"-list_devices", "true", "-i", "")
	var buf strings.Builder
	cmd.Stderr = &buf
	_ = cmd.Run()
	devs := parseAVFoundationMics(buf.String())
	if len(devs) == 0 {
		return nil, errors.New(tr("ffmpeg не нашёл ни одного микрофона"))
	}
	return devs, nil
}

// parseAVFoundationMics вытаскивает из болтовни ffmpeg только звуковые
// устройства. Камеры он печатает тем же форматом и выше по тексту, поэтому без
// разделения на разделы первым «микрофоном» оказывается FaceTime-камера — и
// запись уходит в никуда.
func parseAVFoundationMics(out string) []MicDevice {
	var devs []MicDevice
	audio := false
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		// Строки идут с префиксом вида «[AVFoundation indev @ 0x145a045f0] ».
		if i := strings.Index(line, "] "); i > 0 && strings.Contains(line[:i], "@") {
			line = strings.TrimSpace(line[i+2:])
		}
		switch {
		case strings.HasSuffix(line, "audio devices:"):
			audio = true
			continue
		case strings.HasSuffix(line, "devices:"):
			audio = false
			continue
		}
		if !audio || !strings.HasPrefix(line, "[") {
			continue
		}
		end := strings.Index(line, "]")
		if end < 2 {
			continue
		}
		idx, name := line[1:end], strings.TrimSpace(line[end+1:])
		if name == "" || strings.TrimLeft(idx, "0123456789") != "" {
			continue
		}
		devs = append(devs, MicDevice{Index: idx, Name: name})
	}
	return devs
}
