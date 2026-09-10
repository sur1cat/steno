package audio

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// --- микрофон ----------------------------------------------------------------

// Вывод настоящего ffmpeg 9.0.1 на этом маке. Камеры в нём идут первыми и тем
// же форматом, что микрофоны, — на этом и ловится разбор без разделов: первым
// «микрофоном» оказывается FaceTime-камера, и запись уходит в никуда.
const avfoundationOut = `[AVFoundation indev @ 0x145a045f0] AVFoundation video devices:
[AVFoundation indev @ 0x145a045f0] [0] HD-камера FaceTime
[AVFoundation indev @ 0x145a045f0] [1] Камера Обзора стола (iPhone (Rustem))
[AVFoundation indev @ 0x145a045f0] [2] Камера (iPhone (Rustem))
[AVFoundation indev @ 0x145a045f0] [3] Capture screen 0
[AVFoundation indev @ 0x145a045f0] AVFoundation audio devices:
[AVFoundation indev @ 0x145a045f0] [0] Микрофон (iPhone (Rustem))
[AVFoundation indev @ 0x145a045f0] [1] Микрофон MacBook Air
[AVFoundation indev @ 0x145a045f0] [2] AirPods Pro #2
[in#0 @ 0x145904280] Error opening input: Input/output error
Error opening input file .
`

func TestParseAVFoundationMicsБерётТолькоЗвук(t *testing.T) {
	got := parseAVFoundationMics(avfoundationOut)
	want := []MicDevice{
		{Index: "0", Name: "Микрофон (iPhone (Rustem))"},
		{Index: "1", Name: "Микрофон MacBook Air"},
		{Index: "2", Name: "AirPods Pro #2"},
	}
	if len(got) != len(want) {
		t.Fatalf("устройств %d, ждали %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("устройство %d: %+v, ждали %+v", i, got[i], want[i])
		}
	}
	for _, d := range got {
		if strings.Contains(d.Name, "амера") || strings.Contains(d.Name, "Capture screen") {
			t.Errorf("в микрофоны попала картинка: %+v", d)
		}
	}
}

func TestMicInputArgs(t *testing.T) {
	args, err := micInputArgs("")
	if err != nil {
		if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
			t.Fatalf("устройство по умолчанию не собралось: %v", err)
		}
		t.Skipf("на %s микрофон по умолчанию не назвать", runtime.GOOS)
	}
	joined := strings.Join(args, " ")
	switch runtime.GOOS {
	case "darwin":
		// Двоеточие впереди — это «видео пусто, звук такой»: без него
		// avfoundation откроет камеру и запишет картинку вместо речи.
		if joined != "-f avfoundation -i :default" {
			t.Errorf("вход по умолчанию: %q", joined)
		}
		named, _ := micInputArgs("1")
		if strings.Join(named, " ") != "-f avfoundation -i :1" {
			t.Errorf("выбранный микрофон: %q", named)
		}
		// Человек скопирует номер вместе с двоеточием — «::1» ffmpeg не поймёт.
		colon, _ := micInputArgs(":1")
		if strings.Join(colon, " ") != "-f avfoundation -i :1" {
			t.Errorf("номер с двоеточием: %q", colon)
		}
	case "linux":
		if joined != "-f pulse -i default" {
			t.Errorf("вход по умолчанию: %q", joined)
		}
	}
}

// Запись, начавшаяся и тут же оборвавшаяся, обязана сказать об этом сразу.
// Ждать конца заметки нельзя: человек нажал кнопку и ушёл говорить.
func TestStartMicRecordingЗамечаетСразуУпавшийFfmpeg(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("нужен /bin/sh")
	}
	path := filepath.Join(t.TempDir(), "audio.ogg")
	// Устройства с таким номером нет ни на одной машине: ffmpeg упадёт сразу.
	if runtime.GOOS != "darwin" {
		t.Skip("проверка про avfoundation")
	}
	// На занятой машине ffmpeg заводится дольше боевых полутора секунд.
	// Проверяем разбор отказа, а не скорость соседей по процессору.
	was := micStartCheck
	micStartCheck = 10 * time.Second
	t.Cleanup(func() { micStartCheck = was })
	rec, err := StartMicRecording(path, "99", time.Minute)
	if err == nil {
		_ = rec.Stop()
		t.Fatal("несуществующее устройство приняли за рабочий микрофон")
	}
	if !strings.Contains(err.Error(), "Микрофон") && !strings.Contains(err.Error(), "запись не пошла") {
		t.Errorf("сообщение не объясняет, что случилось: %v", err)
	}
}

// Формат записи — договор с whisper: 16 кГц моно opus, ровно как у созвона.
// Плюс выравнивание громкости, без которого тихая речь теряется целыми
// абзацами (см. micFilter).
func TestMicRecordArgs(t *testing.T) {
	args := strings.Join(micRecordArgs("/tmp/a.ogg",
		[]string{"-f", "avfoundation", "-i", ":default"}, 10*time.Minute), " ")
	for _, want := range []string{
		"-f avfoundation -i :default",
		"-af highpass=f=70,speechnorm=",
		"-ac 1 -ar 16000",
		"-c:a libopus -b:a 32k",
		"-y /tmp/a.ogg",
		// Страховка от сироты: ffmpeg переживает смерть сервиса, и без -t он
		// пишет микрофон, пока не кончится место. Запас над потолком нужен,
		// чтобы обычную остановку успевал сделать сторож.
		"-t 900",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("в команде записи нет %q:\n%s", want, args)
		}
	}
	// -nostdin обязателен: без него ffmpeg читает наш терминал и съедает Enter,
	// которым его же и останавливают.
	if !strings.HasPrefix(args, "-nostdin") {
		t.Errorf("команда начинается не с -nostdin: %s", args)
	}
}
