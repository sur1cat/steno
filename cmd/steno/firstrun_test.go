package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
)

// Первый запуск проверяется с нуля, как его видит чужой человек: пустой HOME,
// пустой каталог моделей, ни ключей, ни входа. Всё, что здесь спрашивается или
// не спрашивается, — ровно то, что решает, попробует человек steno или закроет
// терминал.

// fakeTools кладёт в PATH поддельные whisper-cli, ffmpeg, jq и claude — чтобы
// тест не зависел от того, что стоит на машине, и не запускал настоящие.
// claudeIn — что отвечать на `claude auth status`.
func fakeTools(t *testing.T, claudeIn bool) {
	t.Helper()
	bin := t.TempDir()
	for _, n := range []string{"whisper-cli", "ffmpeg", "jq"} {
		if err := os.WriteFile(filepath.Join(bin, n), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if claudeIn {
		script := "#!/bin/sh\necho '{\"loggedIn\":true,\"authMethod\":\"fake\"}'\n"
		if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
}

// cleanEnv — ни одного ключа и ни одного профиля: HOME свой, переменные пусты.
func cleanEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, k := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "OPENAI_API_KEY",
		"GROQ_API_KEY", "OPENROUTER_API_KEY", "WHISPER_MODEL", "WHISPER_VAD_MODEL", "WHISPER_BIN",
		"STENO_WHISPER_MODEL", "STENO_CONFIG"} {
		t.Setenv(k, "")
	}
	t.Setenv("WHISPER_MODEL_DIR", filepath.Join(home, ".cache", "whisper"))
	return home
}

// noNetwork подменяет клиент скачивания таким, который роняет тест: там, где
// спрашивать нельзя, нельзя и качать.
func noNetwork(t *testing.T) {
	t.Helper()
	old := modelHTTP
	modelHTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Errorf("ушёл в сеть без спроса: %s", r.URL)
		return nil, errors.New("network is off in this test")
	})}
	t.Cleanup(func() { modelHTTP = old })
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func whisperConfig(t *testing.T) *core.Config {
	t.Helper()
	cfg := core.DefaultConfig()
	// Адаптер по названному пути обязан существовать. Без файла doctor честно
	// отвечает «не запускается» ещё до проверки модели — и на маке разработчика
	// этого не видно, потому что AdapterPath находит адаптер из brew-установки
	// в /opt/homebrew/share/steno/adapters. Тест проходил только там, где
	// стоит steno; на чистом Linux в CI он падал.
	adapter := filepath.Join(t.TempDir(), "whisper-cpp.sh")
	if err := os.WriteFile(adapter, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg.Transcribe.Cmd = []string{adapter, "{{audio}}", "{{language}}"}
	return cfg
}

// --- настройка ---------------------------------------------------------------

// Минимальная настройка — та же, что завёл бы мастер, если ответить ему так,
// как отвечает заметка: для себя, ~/steno, whisper, подписка Claude, ничего
// больше. Сравниваем файлы байт в байт: разошлись — значит появилась третья
// форма конфига, и `steno setup` после `steno note` будет чинить то, чего
// человек не ломал.
func TestQuietSetupMatchesWizard(t *testing.T) {
	home := cleanEnv(t)
	fakeTools(t, true)
	t.Chdir(t.TempDir())

	// Мастер: язык по умолчанию · для себя · каталог по умолчанию · верно ·
	// whisper · подписка Claude (по умолчанию, вход есть) · opus · без
	// календаря · без почты · без Telegram · без Telegram · без Docs · без
	// Slack · адрес панели · пароль (сгенерируется).
	input := strings.Join([]string{
		"", "1", "", "", "2", "", "",
		"n", "n", "n",
		"n", "n", "n",
		"", "",
	}, "\n") + "\n"
	withStdin(t, input, func() {
		if err := cmdSetup(context.Background(), nil); err != nil {
			t.Fatalf("мастер сорвался: %v", err)
		}
	})
	dir := filepath.Join(home, "steno")
	wizard, err := os.ReadFile(filepath.Join(dir, "steno.json"))
	if err != nil {
		t.Fatalf("мастер не записал конфиг в ~/steno: %v", err)
	}
	wizardEnv := envKeys(t, filepath.Join(dir, ".env"))
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}

	p, err := quietSetup()
	if err != nil {
		t.Fatalf("тихая настройка сорвалась: %v", err)
	}
	if p != filepath.Join(dir, "steno.json") {
		t.Errorf("конфиг лёг в %s, а мастер кладёт в %s", p, filepath.Join(dir, "steno.json"))
	}
	quiet, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(quiet) != string(wizard) {
		t.Errorf("тихая настройка разошлась с мастером:\n--- мастер ---\n%s\n--- тихая ---\n%s", wizard, quiet)
	}
	if got := envKeys(t, filepath.Join(dir, ".env")); strings.Join(got, ",") != strings.Join(wizardEnv, ",") {
		t.Errorf("секреты в .env: %v, у мастера %v", got, wizardEnv)
	}
	if st, err := os.Stat(filepath.Join(dir, ".env")); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf(".env: %v, права %v", err, st.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); err != nil {
		t.Error("нет .gitignore рядом с .env")
	}
	if ptr, _ := os.ReadFile(filepath.Join(home, ".config", "steno", "path")); strings.TrimSpace(string(ptr)) != p {
		t.Errorf("указатель на настройку: %q, ждали %q", ptr, p)
	}
	if _, err := os.Stat(filepath.Join(dir, "data", "steno.db")); err != nil {
		t.Error("база не заведена — первая команда рапортовала бы о переносе каналов из конфига")
	}

	var cfg core.Config
	if err := json.Unmarshal(quiet, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Transcribe.Source != "command" || !strings.HasSuffix(cfg.Transcribe.Cmd[0], "whisper-cpp.sh") {
		t.Errorf("заметка без whisper: %+v", cfg.Transcribe)
	}
	if cfg.Claude.Via != "cli" {
		t.Errorf("вход в Claude Code есть, а разбор не на подписке: %q", cfg.Claude.Via)
	}
	if cfg.Calendar.Enabled || cfg.Telegram.Listen || cfg.Telegram.Enabled || cfg.Slack.Enabled {
		t.Error("тихая настройка включила канал, о котором никто не просил")
	}
}

func envKeys(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("нет .env: %v", err)
	}
	var keys []string
	for _, l := range strings.Split(string(raw), "\n") {
		if k, _, ok := strings.Cut(l, "="); ok && !strings.HasPrefix(l, "#") {
			keys = append(keys, k)
		}
	}
	return keys
}

// Без входа в Claude Code разбор остаётся на auto — не на «ключ», которого нет.
func TestQuietSetupLeavesAutoWithoutClaude(t *testing.T) {
	cleanEnv(t)
	fakeTools(t, false)
	p, err := quietSetup()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := core.LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Claude.Via != "auto" {
		t.Errorf("входа нет, а разбор закреплён на %q", cfg.Claude.Via)
	}
}

// Лежащий в ~/steno конфиг — чужой, и заводить поверх него нельзя, даже если
// указатель на него пропал.
func TestEnsureConfigKeepsExisting(t *testing.T) {
	home := cleanEnv(t)
	fakeTools(t, false)
	t.Chdir(t.TempDir())
	dir := filepath.Join(home, "steno")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mine := `{"data_dir": "./data", "lang": "en"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "steno.json"), []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}

	p, err := ensureConfig(core.DefaultConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join(dir, "steno.json") {
		t.Errorf("нашёл %q, а конфиг лежит в %q", p, dir)
	}
	if raw, _ := os.ReadFile(p); string(raw) != mine {
		t.Errorf("чужой конфиг переписан:\n%s", raw)
	}
	if ptr, _ := os.ReadFile(filepath.Join(home, ".config", "steno", "path")); strings.TrimSpace(string(ptr)) != p {
		t.Errorf("указатель не восстановлен: %q", ptr)
	}

	// Названный явно файл не подменяется и не заводится.
	explicit := filepath.Join(t.TempDir(), "нет.json")
	if p, err := ensureConfig(explicit); err != nil || p != explicit {
		t.Errorf("явный путь: %q, %v", p, err)
	}
	if _, err := os.Stat(explicit); err == nil {
		t.Error("по явному пути завёлся конфиг — опечатка в -c стала установкой")
	}
}

// --- модель ------------------------------------------------------------------

// Оборванная скачка не оставляет ни модели, ни обрывка: следующий запуск
// нашёл бы огрызок с именем модели и упал внутри whisper с невнятным «failed
// to load».
func TestDownloadAbortLeavesNothing(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000000")
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(200)
		w.Write([]byte(ggmlMagic))
		w.Write(make([]byte, 100000))
		w.(http.Flusher).Flush()
		<-release // дальше не отдаём: клиент должен уйти сам
	}))
	defer srv.Close()
	defer close(release)
	old := modelHTTP
	modelHTTP = srv.Client()
	t.Cleanup(func() { modelHTTP = old })

	dst := filepath.Join(t.TempDir(), "ggml-test.bin")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()
	err := downloadFile(ctx, srv.URL+"/model.bin", dst, func(int64, int64) {})
	if err == nil {
		t.Fatal("оборванная скачка сошла за удачную")
	}
	if _, err := os.Stat(dst); err == nil {
		t.Error("после обрыва лежит файл с именем модели")
	}
	if _, err := os.Stat(dst + ".part"); err == nil {
		t.Error("после обрыва остался обрывок .part")
	}
}

// Сервер отдал не то: страницу, обрезанный файл, не ggml. Ни один из них не
// должен лечь под именем модели.
func TestDownloadRejectsWrongContent(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"страница": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte("<html>rate limited</html>"))
		},
		"обрезан": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "100")
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write([]byte(ggmlMagic + "half"))
		},
		"не ggml": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write([]byte("PK\x03\x04 это архив"))
		},
		"404": func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		},
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(h)
			defer srv.Close()
			old := modelHTTP
			modelHTTP = srv.Client()
			t.Cleanup(func() { modelHTTP = old })
			dst := filepath.Join(t.TempDir(), "ggml-test.bin")
			if err := downloadFile(context.Background(), srv.URL+"/m.bin", dst, func(int64, int64) {}); err == nil {
				t.Fatal("плохой ответ сошёл за модель")
			}
			for _, p := range []string{dst, dst + ".part"} {
				if _, err := os.Stat(p); err == nil {
					t.Errorf("остался %s", filepath.Base(p))
				}
			}
		})
	}
}

// Целая скачка кладёт файл под именем модели, и обрывка рядом не остаётся.
func TestDownloadWritesWholeFile(t *testing.T) {
	payload := ggmlMagic + strings.Repeat("x", 300000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		w.Write([]byte(payload))
	}))
	defer srv.Close()
	old := modelHTTP
	modelHTTP = srv.Client()
	t.Cleanup(func() { modelHTTP = old })

	dst := filepath.Join(t.TempDir(), "sub", "ggml-test.bin")
	var last, total int64
	if err := downloadFile(context.Background(), srv.URL+"/m.bin", dst, func(d, tot int64) { last, total = d, tot }); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(dst); err != nil || string(raw) != payload {
		t.Errorf("файл не тот: %v, %d байт", err, len(raw))
	}
	if _, err := os.Stat(dst + ".part"); err == nil {
		t.Error("обрывок .part остался рядом с готовой моделью")
	}
	if last != int64(len(payload)) || total != int64(len(payload)) {
		t.Errorf("ход скачивания: %d из %d, ждали %d", last, total, len(payload))
	}
}

// Без терминала модель не спрашивается и не качается — говорится, что скачать.
func TestNoteWithoutTerminalDoesNotAskForModel(t *testing.T) {
	cleanEnv(t)
	fakeTools(t, false)
	noNetwork(t)
	cfg := whisperConfig(t)

	var err error
	// stdin — труба с готовым «да»: если бы вопрос всё-таки задали, ответ бы
	// нашёлся, и скачка пошла бы в сеть — noNetwork это поймает.
	withStdin(t, "y\n", func() { err = ensureTranscriber(context.Background(), cfg) })
	if err == nil {
		t.Fatal("без модели и без терминала заметка пошла дальше")
	}
	if !strings.Contains(err.Error(), "curl -L -o") || !strings.Contains(err.Error(), whisperModelDefault) {
		t.Errorf("в отказе не сказано, что скачать: %v", err)
	}
	if w := inspectWhisper(cfg); w.model != "" {
		t.Errorf("откуда-то взялась модель: %s", w.model)
	}
}

// Модель на месте — вопросов нет, VAD тоже не спрашивается (без терминала и
// не качается). И чужой распознаватель (Groq) whisper не касается вовсе.
func TestNoteWithModelAsksNothing(t *testing.T) {
	home := cleanEnv(t)
	fakeTools(t, false)
	noNetwork(t)
	dir := filepath.Join(home, ".cache", "whisper")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ggml-large-v3-q5_0.bin"), []byte(ggmlMagic), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureTranscriber(context.Background(), whisperConfig(t)); err != nil {
		t.Errorf("модель есть, а заметка не пошла: %v", err)
	}

	groq := core.DefaultConfig()
	groq.Transcribe.Cmd = []string{"/nowhere/groq.sh", "{{audio}}", "{{language}}"}
	t.Setenv("WHISPER_MODEL_DIR", t.TempDir())
	if err := ensureTranscriber(context.Background(), groq); err != nil {
		t.Errorf("расшифровка через Groq, а заметка требует whisper: %v", err)
	}
}

// Нет whisper-cli — не поломка, а выбор: две дороги и ни слова паники.
func TestNoteWithoutWhisperNamesBothRoads(t *testing.T) {
	cleanEnv(t)
	t.Setenv("PATH", t.TempDir())
	err := ensureTranscriber(context.Background(), whisperConfig(t))
	if err == nil {
		t.Fatal("без whisper-cli заметка пошла дальше")
	}
	for _, want := range []string{"whisper-cli", "steno setup", "Groq"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("в отказе нет %q:\n%v", want, err)
		}
	}
}

// --- разбор ------------------------------------------------------------------

// Ключ провайдера в окружении подхватывается без единого вопроса.
func TestNoteTakesKeyFromEnvironmentSilently(t *testing.T) {
	home := cleanEnv(t)
	fakeTools(t, false)
	t.Setenv("GROQ_API_KEY", "gsk_тест")
	cfg := core.DefaultConfig()
	cfgPath := filepath.Join(home, "steno.json")

	var err error
	withStdin(t, "sk-ant-подсунутый\n", func() { err = ensureBrain(cfg, cfgPath) })
	if err != nil {
		t.Fatalf("ключ Groq в окружении, а разбор не нашёлся: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".env")); err == nil {
		t.Error("вопрос всё-таки задан: ключ из stdin записан в .env")
	}
}

// Нет ничего и нет терминала — дороги перечислены, вопрос не задан.
func TestNoteWithoutTerminalDoesNotAskForKey(t *testing.T) {
	home := cleanEnv(t)
	fakeTools(t, false)
	cfg := core.DefaultConfig()
	cfgPath := filepath.Join(home, "steno.json")

	var err error
	withStdin(t, "sk-ant-подсунутый\n", func() { err = ensureBrain(cfg, cfgPath) })
	if err == nil {
		t.Fatal("разбирать нечем, а заметка пошла дальше")
	}
	for _, want := range []string{"claude", "console.anthropic.com", "console.groq.com", ".env"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("в отказе нет %q:\n%v", want, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".env")); err == nil {
		t.Error("без терминала ключ прочитан из stdin и записан в .env")
	}
}

// Ключ узнаётся по началу, кладётся в .env рядом с конфигом и работает сразу.
func TestKeyEnvByPrefixAndEnvFile(t *testing.T) {
	for key, want := range map[string]string{
		"sk-ant-api03-x": "ANTHROPIC_API_KEY",
		"gsk_x":          "GROQ_API_KEY",
		"sk-or-v1-x":     "OPENROUTER_API_KEY",
		"sk-proj-x":      "OPENAI_API_KEY",
		"sk-x":           "OPENAI_API_KEY",
		"hf_x":           "",
	} {
		if got := keyEnvByPrefix(key); got != want {
			t.Errorf("%s → %q, ждали %q", key, got, want)
		}
	}

	path := filepath.Join(t.TempDir(), ".env")
	if err := writeEnvVar(path, "GROQ_API_KEY", "один"); err != nil {
		t.Fatal(err)
	}
	if err := writeEnvVar(path, "OPENAI_API_KEY", "два"); err != nil {
		t.Fatal(err)
	}
	if err := writeEnvVar(path, "GROQ_API_KEY", "три"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "GROQ_API_KEY=три\n") || strings.Contains(string(raw), "один") {
		t.Errorf("переменная не заменилась:\n%s", raw)
	}
	if !strings.Contains(string(raw), "OPENAI_API_KEY=два\n") {
		t.Errorf("вторая переменная не дописалась:\n%s", raw)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Errorf("права на .env %o", st.Mode().Perm())
	}
	// Файл должен читаться тем же loadDotEnv, что и у мастера.
	t.Setenv("OPENAI_API_KEY", "")
	os.Unsetenv("OPENAI_API_KEY")
	if err := loadDotEnv(path); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("OPENAI_API_KEY"); got != "два" {
		t.Errorf("loadDotEnv прочитал %q", got)
	}
}

func TestSizeText(t *testing.T) {
	i18n.UILang = i18n.LangRU
	for n, want := range map[int64]string{
		1081140203: "1.03 ГБ",
		574041195:  "547 МБ",
		885098:     "0.8 МБ",
		0:          "?",
	} {
		if got := sizeText(n); got != want {
			t.Errorf("%d → %q, ждали %q", n, got, want)
		}
	}
}

// --- doctor ------------------------------------------------------------------

// doctor видит те же четыре шага, что и `steno note`, теми же словами — и
// у того, что чинится на месте, есть чем починить (offer), а не только curl.
func TestDoctorSeesFirstRunSteps(t *testing.T) {
	home := cleanEnv(t)
	fakeTools(t, false)
	noNetwork(t)
	cfg := whisperConfig(t)

	c := checkTranscribe(cfg)
	if c.state != "fail" || !strings.Contains(c.note, "модели нет") {
		t.Fatalf("без модели расшифровка объявлена: %+v", c)
	}
	if c.offer == nil {
		t.Error("модели нет, а скачать doctor не предлагает")
	}

	// Без whisper-cli — две дороги, и предлагать нечего: ставить пакеты за
	// человека doctor не берётся.
	t.Setenv("PATH", t.TempDir())
	c = checkTranscribe(cfg)
	if c.state != "fail" || !strings.Contains(strings.Join(c.fix, "\n"), "steno setup") {
		t.Errorf("без whisper-cli дороги не названы: %+v", c)
	}
	if c.offer != nil {
		t.Error("doctor вызвался ставить whisper сам")
	}

	// Разбор: нет ничего — есть что предложить; ключ в окружении — всё готово
	// и назван тот, кто ответит.
	fakeTools(t, false)
	brainless := core.DefaultConfig()
	c = checkBrain(brainless, filepath.Join(home, "steno.json"))
	if c.state != "fail" || c.offer == nil {
		t.Errorf("разбирать нечем, а doctor не предлагает ключ: %+v", c)
	}
	t.Setenv("GROQ_API_KEY", "gsk_тест")
	c = checkBrain(core.DefaultConfig(), "")
	if c.state != "ok" || !strings.HasPrefix(c.note, "Groq") {
		t.Errorf("ключ Groq в окружении, а doctor говорит: %+v", c)
	}
}
