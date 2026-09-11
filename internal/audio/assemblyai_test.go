package audio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sur1cat/steno/internal/core"
)

// Настоящий adapters/assemblyai.sh — на поддельном AssemblyAI.
//
// Это не проверка AssemblyAI, а проверка того самого файла, который лежит в
// репозитории и с которого люди спишут свой адаптер. Ключа на машине нет и не
// будет: договор с сервисом — обычный HTTP, и подделать его дешевле, чем
// заводить платный аккаунт.
//
// Чего эта проверка не заменяет и заменить не может: живого прогона. Что
// AssemblyAI действительно принимает keyterms_prompt в этом виде, что
// speaker_labels даёт utterances с метками «A» и «B», что времена приходят в
// миллисекундах — всё это здесь взято из его документации и подтвердить может
// только настоящий ключ. Проверено тут другое, и оно тоже стоит проверки: что
// адаптер шлёт то, что собирался, и разбирает ответ той формы, о которой
// договорились, — включая отказ, зависание, мусор вместо JSON и пустую
// расшифровку.

func assemblyAdapter(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("скрипты на bash — не для Windows")
	}
	for _, bin := range []string{"bash", "jq", "curl", "ffmpeg"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("для адаптера нужен %s", bin)
		}
	}
	path, err := filepath.Abs(filepath.Join("..", "..", "adapters", "assemblyai.sh"))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// testAudio — секунда тишины в том виде, в каком её умеет прочитать ffmpeg.
// Заодно проверяем, что ffmpeg на этой машине собран с libopus: без него
// адаптер падает на первой же строке, и падение это не про адаптер.
func testAudio(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	wav := filepath.Join(dir, "a.wav")
	if err := os.WriteFile(wav, SilentWAV(time.Second), 0o644); err != nil {
		t.Fatal(err)
	}
	probe := exec.Command("ffmpeg", "-nostdin", "-loglevel", "error", "-i", wav,
		"-ac", "1", "-ar", "16000", "-c:a", "libopus", "-b:a", "24k",
		filepath.Join(dir, "probe.ogg"))
	if err := probe.Run(); err != nil {
		t.Skip("ffmpeg на этой машине без libopus")
	}
	return wav
}

// fakeAssembly — поддельный AssemblyAI. Возвращает адрес, тело запроса на
// расшифровку и число опросов готовности.
type fakeAssembly struct {
	srv    *httptest.Server
	mu     sync.Mutex
	body   map[string]any
	polls  int
	authOK bool
	// orders — сколько раз заказали расшифровку: по этому видно, сколько раз
	// сервис ходил за одной записью.
	orders int
}

// reply — что отвечать на опрос готовности. Отдельной функцией: зависание,
// отказ движка и мусор различаются только этим.
func startFakeAssembly(t *testing.T, reply func(w http.ResponseWriter, poll int)) *fakeAssembly {
	t.Helper()
	f := &fakeAssembly{authOK: true}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("authorization") != "test-key" {
			f.mu.Lock()
			f.authOK = false
			f.mu.Unlock()
			http.Error(w, `{"error":"authentication failed"}`, http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/v2/upload":
			// Тело — сам звук; важно, что он вообще доехал непустым.
			b := make([]byte, 1)
			if n, _ := r.Body.Read(b); n == 0 {
				http.Error(w, `{"error":"empty upload"}`, http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"upload_url":"https://cdn.assemblyai.test/u/1"}`))
		case r.URL.Path == "/v2/transcript" && r.Method == http.MethodPost:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			f.body = body
			f.orders++
			f.mu.Unlock()
			_, _ = w.Write([]byte(`{"id":"tr_1","status":"queued"}`))
		case r.URL.Path == "/v2/transcript/tr_1":
			f.mu.Lock()
			f.polls++
			n := f.polls
			f.mu.Unlock()
			reply(w, n)
		default:
			http.Error(w, "не тот путь: "+r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAssembly) sent() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.body
}

func (f *fakeAssembly) ordered() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.orders
}

// runAssembly зовёт адаптер так же, как его зовёт сервис: через RunTranscriber,
// со словарём в STENO_TERMS. Проверять договор в обход RunTranscriber
// бессмысленно — половина договора это то, как сервис читает stdout.
func runAssembly(t *testing.T, f *fakeAssembly, lang string, vocab *Vocab) ([]core.Segment, string, error) {
	t.Helper()
	quiet(t)
	t.Setenv("ASSEMBLYAI_API_KEY", "test-key")
	t.Setenv("ASSEMBLYAI_BASE", f.srv.URL)
	t.Setenv("ASSEMBLYAI_POLL", "1")
	t.Setenv("ASSEMBLYAI_TIMEOUT", "6")

	cfg := core.DefaultConfig()
	cfg.Transcribe.Cmd = []string{assemblyAdapter(t), "{{audio}}", "{{language}}"}
	cfg.Transcribe.Language = lang
	cfg.Transcribe.Nice = false
	cfg.Transcribe.Timeout = core.Duration(60 * time.Second)
	return RunTranscriber(context.Background(), cfg, testAudio(t), vocab)
}

const assemblyDone = `{"status":"completed","language_code":"ru","text":"Анвару нужно закончить сапар. Да, возьму.",
 "utterances":[
   {"speaker":"A","start":21760,"end":23660,"confidence":0.61,
    "text":"Анвару нужно закончить сапар.",
    "words":[{"text":"Анвару","start":21760,"end":22100,"confidence":0.33,"speaker":"A"},
             {"text":"нужно","start":22100,"end":22400,"confidence":0.86,"speaker":"A"},
             {"text":"закончить","start":22400,"end":23000,"confidence":0.99,"speaker":"A"},
             {"text":"сапар.","start":23100,"end":23660,"confidence":0.28,"speaker":"A"}]},
   {"speaker":"B","start":24400,"end":25300,"confidence":0.95,"text":"Да, возьму.",
    "words":[{"text":"Да,","start":24400,"end":24700,"confidence":0.95,"speaker":"B"},
             {"text":"возьму.","start":24700,"end":25300,"confidence":0.95,"speaker":"B"}]}]}`

// Удачный ответ. Разом проверяется и то, что уходит наружу, и то, что доезжает
// обратно: словарь созвона в keyterms_prompt, диаризация, метки говорящих,
// уверенность реплики и каждого слова, миллисекунды в секундах.
func TestAssemblyAIHappyPath(t *testing.T) {
	f := startFakeAssembly(t, func(w http.ResponseWriter, _ int) {
		_, _ = w.Write([]byte(assemblyDone))
	})
	v := &Vocab{Lang: "ru"}
	v.source()
	v.addPeople("Орынгали")
	v.addTerms("Сапар", "VLive")
	segs, notes, err := runAssembly(t, f, "ru", v)
	if err != nil {
		t.Fatalf("адаптер не отработал: %v", err)
	}
	// Словарь ушёл отдельным входом и разбивку не трогал — адаптер говорит об
	// этом полем vocabulary, и сервис не делает второй проход: он только
	// удвоил бы счёт.
	if n := f.ordered(); n != 1 {
		t.Errorf("расшифровку заказали %d раз(а), ожидали один: словарь в keyterms второго прохода не требует", n)
	}

	sent := f.sent()
	if sent["audio_url"] != "https://cdn.assemblyai.test/u/1" {
		t.Errorf("не тот audio_url: %v", sent["audio_url"])
	}
	if sent["speaker_labels"] != true {
		t.Errorf("диаризацию не попросили: %v", sent["speaker_labels"])
	}
	if sent["language_code"] != "ru" {
		t.Errorf("язык: %v", sent["language_code"])
	}
	if _, ok := sent["language_detection"]; ok {
		t.Error("язык задан явно, а адаптер всё равно просит его определять")
	}
	// Словарь созвона уходит отдельным входом, а не подсказкой в тексте: у
	// whisper --prompt ломает разбивку на реплики, здесь такого входа нет вовсе.
	// Берётся список из STENO_TERMS, а не фраза из STENO_PROMPT: фраза здесь
	// стала бы одним «термином» длиной в предложение.
	terms, _ := sent["keyterms_prompt"].([]any)
	if len(terms) != 3 || terms[0] != "Орынгали" || terms[1] != "Сапар" || terms[2] != "VLive" {
		t.Errorf("словарь доехал не так: %v", sent["keyterms_prompt"])
	}
	models, _ := sent["speech_models"].([]any)
	if len(models) != 2 || models[0] != "universal-3-5-pro" {
		t.Errorf("модели: %v", sent["speech_models"])
	}

	if len(segs) != 2 {
		t.Fatalf("реплик %d, ожидали 2: %+v", len(segs), segs)
	}
	if segs[0].Speaker != "A" || segs[1].Speaker != "B" {
		t.Errorf("метки говорящих: %q и %q", segs[0].Speaker, segs[1].Speaker)
	}
	if segs[0].Start != 21.76 || segs[0].End != 23.66 {
		t.Errorf("миллисекунды не перевели в секунды: %+v", segs[0])
	}
	if segs[0].Conf == nil || *segs[0].Conf != 0.61 {
		t.Errorf("уверенность реплики: %v", segs[0].Conf)
	}
	if len(segs[0].Words) != 4 {
		t.Fatalf("слов в первой реплике %d: %+v", len(segs[0].Words), segs[0].Words)
	}
	if w := segs[0].Words[0]; w.Word != "Анвару" || w.Conf == nil || *w.Conf != 0.33 || w.Start != 21.76 {
		t.Errorf("первое слово: %+v", w)
	}
	// То, ради чего договор расширяли: неверно распознанное слово доезжает до
	// модели помеченным, а не выдаётся за факт.
	want := "Анвару" + core.UncertainMark + " нужно закончить сапар." + core.UncertainMark
	if got := core.MarkUncertain(segs[0]); got != want {
		t.Errorf("получили %q, ожидали %q", got, want)
	}
	if !strings.Contains(notes, "словарь на 3") {
		t.Errorf("адаптер промолчал о словаре: %q", notes)
	}
}

// Языка нет — просим определить его сам. На созвоне, где переходят с русского
// на английский, жёстко заданный язык хуже; но подменять явно указанный —
// хуже вдвойне, и это проверено выше.
func TestAssemblyAIAsksToDetectLanguage(t *testing.T) {
	f := startFakeAssembly(t, func(w http.ResponseWriter, _ int) {
		_, _ = w.Write([]byte(assemblyDone))
	})
	if _, _, err := runAssembly(t, f, "", nil); err != nil {
		t.Fatal(err)
	}
	sent := f.sent()
	if sent["language_detection"] != true {
		t.Errorf("язык не задан, определять не просят: %v", sent)
	}
	if _, ok := sent["language_code"]; ok {
		t.Errorf("пустой язык уехал как language_code: %v", sent["language_code"])
	}
	if _, ok := sent["keyterms_prompt"]; ok {
		t.Errorf("словаря не было, а keyterms_prompt уехал: %v", sent["keyterms_prompt"])
	}
}

// Отказ. Ключ неверный — адаптер обязан упасть словами, а не отдать пустую
// расшифровку с нулевым кодом: пустой follow-up выглядит как настоящий.
func TestAssemblyAIRefusal(t *testing.T) {
	f := startFakeAssembly(t, func(w http.ResponseWriter, _ int) {})
	t.Setenv("ASSEMBLYAI_API_KEY", "no-such-key")
	quiet(t)
	cfg := core.DefaultConfig()
	cfg.Transcribe.Cmd = []string{assemblyAdapter(t), "{{audio}}", "{{language}}"}
	cfg.Transcribe.Nice = false
	t.Setenv("ASSEMBLYAI_BASE", f.srv.URL)
	_, _, err := RunTranscriber(context.Background(), cfg, testAudio(t), nil)
	if err == nil {
		t.Fatal("отказ сервера принят за удачную расшифровку")
	}
	if !strings.Contains(err.Error(), "загрузка не прошла") {
		t.Errorf("невнятная ошибка: %v", err)
	}
}

// Движок не справился и сказал об этом. Причину надо донести до человека
// целиком: «не поддерживается язык» и «файл битый» чинятся по-разному.
func TestAssemblyAIEngineError(t *testing.T) {
	f := startFakeAssembly(t, func(w http.ResponseWriter, _ int) {
		_, _ = w.Write([]byte(`{"status":"error","error":"Audio file does not appear to contain audio"}`))
	})
	_, _, err := runAssembly(t, f, "ru", nil)
	if err == nil {
		t.Fatal("ошибка движка принята за расшифровку")
	}
	// Именно своими словами, а не куском тела ответа: «движок не справился» и
	// «ответ не похож на ожидаемый» чинятся по-разному, а тело в обоих случаях
	// одно и то же.
	if !strings.Contains(err.Error(), "движок не справился: Audio file does not appear to contain audio") {
		t.Errorf("причину потеряли: %v", err)
	}
}

// Зависание. Расшифровка часа звука идёт минуты, и всё это время ответ —
// processing; но созвон, застрявший навсегда, обязан кончиться словами, а не
// висеть до конца света.
func TestAssemblyAITimeout(t *testing.T) {
	f := startFakeAssembly(t, func(w http.ResponseWriter, _ int) {
		_, _ = w.Write([]byte(`{"status":"processing"}`))
	})
	start := time.Now()
	_, _, err := runAssembly(t, f, "ru", nil)
	if err == nil {
		t.Fatal("вечное processing принято за расшифровку")
	}
	if !strings.Contains(err.Error(), "не уложился") {
		t.Errorf("невнятная ошибка: %v", err)
	}
	if time.Since(start) > 30*time.Second {
		t.Errorf("ждали дольше своего же срока: %s", time.Since(start))
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.polls < 2 {
		t.Errorf("опросов готовности всего %d — похоже, ждали не тем местом", f.polls)
	}
}

// Мусор вместо JSON. Сервер отдал HTML — страницу прокси, заглушку, что угодно.
// Крутиться на этом час нельзя: без статуса цикл кончился бы «не уложился в
// срок» и увёл разбираться совсем не туда.
func TestAssemblyAIGarbage(t *testing.T) {
	f := startFakeAssembly(t, func(w http.ResponseWriter, _ int) {
		w.Header().Set("content-type", "text/html")
		_, _ = w.Write([]byte(`<html><body>502 Bad Gateway</body></html>`))
	})
	start := time.Now()
	_, _, err := runAssembly(t, f, "ru", nil)
	if err == nil {
		t.Fatal("HTML принят за расшифровку")
	}
	if !strings.Contains(err.Error(), "не похож на ожидаемый") {
		t.Errorf("невнятная ошибка: %v", err)
	}
	if time.Since(start) > 20*time.Second {
		t.Errorf("на мусоре крутились до самого срока: %s", time.Since(start))
	}
}

// Пустая расшифровка. Движок дослушал файл и не нашёл в нём речи — это не
// ошибка адаптера, и врать про неё он не должен. Объясняет её сервис: там же,
// где объясняет её для whisper.
func TestAssemblyAIEmptyTranscript(t *testing.T) {
	f := startFakeAssembly(t, func(w http.ResponseWriter, _ int) {
		_, _ = w.Write([]byte(`{"status":"completed","text":"","utterances":[],"words":[]}`))
	})
	segs, notes, err := runAssembly(t, f, "ru", nil)
	if err != nil {
		t.Fatalf("пустая расшифровка не должна быть падением адаптера: %v", err)
	}
	if len(segs) != 0 {
		t.Fatalf("из пустого ответа родились реплики: %+v", segs)
	}
	if !strings.Contains(notes, "нет речи") {
		t.Errorf("о пустом ответе промолчали: %q", notes)
	}
	if CheckTranscript(segs) == nil {
		t.Error("пустая расшифровка прошла бы в follow-up как настоящая")
	}
}

// Диаризации в ответе нет — так отвечает модель, которая её не умеет. Реплики
// собираются из слов: одна реплика на весь созвон превратила бы таймкоды
// follow-up в ссылку на его начало.
func TestAssemblyAIWithoutUtterances(t *testing.T) {
	f := startFakeAssembly(t, func(w http.ResponseWriter, _ int) {
		_, _ = w.Write([]byte(`{"status":"completed","text":"раз два три",
		 "words":[{"text":"раз","start":1000,"end":1400,"confidence":0.9},
		          {"text":"два","start":1400,"end":1800,"confidence":0.4},
		          {"text":"три","start":9000,"end":9400,"confidence":0.8}]}`))
	})
	segs, _, err := runAssembly(t, f, "ru", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Пауза в семь секунд — граница реплики.
	if len(segs) != 2 {
		t.Fatalf("реплик %d, ожидали 2: %+v", len(segs), segs)
	}
	if segs[0].Text != "раз два" || segs[1].Text != "три" {
		t.Errorf("реплики нарезаны не так: %q и %q", segs[0].Text, segs[1].Text)
	}
	if segs[0].Start != 1 || segs[0].End != 1.8 {
		t.Errorf("времена первой реплики: %+v", segs[0])
	}
	if len(segs[0].Words) != 2 || segs[0].Words[1].Conf == nil || *segs[0].Words[1].Conf != 0.4 {
		t.Errorf("слова первой реплики: %+v", segs[0].Words)
	}
	if got := core.MarkUncertain(segs[0]); got != "раз два"+core.UncertainMark {
		t.Errorf("пометка не встала: %q", got)
	}
}
