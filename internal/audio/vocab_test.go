package audio

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sur1cat/steno/internal/core"
)

// vocabRepo — проект в двух шагах от настоящего: репозиторий с коммитами трёх
// человек, docker-compose с сервисами и каталоги модулей. Ровно то, из чего
// словарь и собирается.
func vocabRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	commit := func(name, subject string) {
		t.Helper()
		cmd := exec.Command("git", "commit", "-q", "--allow-empty", "-m", subject)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME="+name, "GIT_AUTHOR_EMAIL=x@x",
			"GIT_COMMITTER_NAME="+name, "GIT_COMMITTER_EMAIL=x@x")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git commit: %s", out)
		}
	}
	init := exec.Command("git", "init", "-q")
	init.Dir = repo
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s", out)
	}
	// Орынгали коммитит чаще всех — значит и в словаре он первый.
	commit("Oryngali Karimzhan", "сапар: разбор повторной доставки")
	commit("Oryngali Karimzhan", "сапар: таймауты")
	commit("Topatayev Anuar", "биометрия: снимок документа")
	commit("dependabot[bot]", "bump lodash")

	mustWriteFile(t, filepath.Join(repo, "docker-compose.yml"), `services:
  db:
    image: postgres
  worker_sapar:
    build: .
  worker_avr:
    build: .
`)
	for _, mod := range []string{"biometric", "lotteries", "migrations", "node_modules"} {
		if err := os.MkdirAll(filepath.Join(repo, mod), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

// Главный случай, из-за которого всё это и делается: на созвоне звучат имя
// человека из коммитов и имя сервиса из docker-compose, а whisper их не знает.
// Оба обязаны оказаться в подсказке, и имя человека — раньше сервиса.
func TestVocabularyCollectsPeopleAndServices(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Projects = []core.Project{{
		Name:    "taxi-kolesa",
		Aliases: []string{"такси"},
		People:  []string{"Орынгали"},
		// SaveProject держит имена людей и внутри общего словаря — здесь то же
		// самое руками, чтобы проверять на том, что придёт из базы.
		Vocabulary: []string{"аквайринг", "Орынгали"},
		Sources:    []core.Source{{Kind: "path", Value: vocabRepo(t)}},
	}}
	m := &core.Meeting{Participants: []string{"Rustem Turgeldin"}}

	got := MeetingVocabulary(context.Background(), cfg, nil, m)
	t.Logf("подсказка при живом бюджете: %s", got)

	// Просторный бюджет — чтобы видеть весь собранный словарь, а не только то,
	// что влезло. Собирается и режется это в разных местах, и проверять их
	// вместе значит не понять, что именно сломалось.
	wide := &Vocab{}
	wide.source()
	wide.addPeople(m.Participants...)
	wide.source()
	wide.addProject(context.Background(), cfg.DataDir, cfg.Projects[0])
	all := wide.Prompt(400)
	t.Logf("весь словарь: %s", all)

	for _, want := range []string{
		"Rustem Turgeldin",   // участник созвона
		"Орынгали",           // имя, вписанное человеком
		"Oryngali Karimzhan", // автор коммитов
		"Topatayev Anuar",
		"worker_sapar", // сервис из docker-compose — тот самый «Сапар»
		"taxi-kolesa",  // название проекта
		"такси",        // и псевдоним
		"аквайринг",    // ручной словарь проекта
		"biometric",    // модуль верхнего уровня
	} {
		if !strings.Contains(all, want) {
			t.Errorf("в словаре нет %q", want)
		}
	}
	// Роботы на созвоне не говорят, а место занимают.
	if strings.Contains(all, "dependabot") {
		t.Error("в словарь попал бот из авторов коммитов")
	}
	// Каталоги, которые есть у всех, ничего не говорят о проекте.
	for _, bad := range []string{"node_modules", "migrations"} {
		if strings.Contains(all, bad) {
			t.Errorf("в словарь попал служебный каталог %q", bad)
		}
	}

	// А это уже про очередь на отсечение. Имена людей идут раньше названий:
	// незнакомое имя whisper заменяет другим словом целиком, и вместе с ним
	// пропадает, на ком задача.
	order := func(a, b, why string) {
		t.Helper()
		i, j := strings.Index(all, a), strings.Index(all, b)
		if i < 0 || j < 0 {
			t.Errorf("нет %q или %q в словаре:\n%s", a, b, all)
			return
		}
		if i > j {
			t.Errorf("%q оказалось позже %q — %s:\n%s", a, b, why, all)
		}
	}
	// Имя, вписанное человеком, — раньше подписи из git: «Орынгали» это то, как
	// человека зовут вслух, «Oryngali Karimzhan» — то, чем подписан аккаунт.
	// В общем словаре проекта это имя лежит вперемешку с сервисами, и без
	// отдельного разбора уехало бы в хвост, к тому, что отсекается первым.
	order("Орынгали", "Oryngali Karimzhan", "имя вслух важнее подписи в git")
	order("Орынгали", "аквайринг", "имена людей важнее слов")
	order("аквайринг", "worker_sapar", "слово от человека важнее добытого из репозитория")
	order("Oryngali Karimzhan", "worker_sapar", "имена людей важнее названий")
	order("worker_sapar", "biometric", "сервис важнее каталога модуля")

	// При живом бюджете первым делом обязаны уцелеть люди и название проекта.
	for _, want := range []string{"Rustem Turgeldin", "Орынгали", "taxi-kolesa"} {
		if !strings.Contains(got, want) {
			t.Errorf("при живом бюджете потерялось %q:\n%s", want, got)
		}
	}
	// Подсказка — список через запятую, а не фраза: whisper продолжает её как
	// текст, и с подводкой вроде «В разговоре встречаются…» он сливает
	// расшифровку в один абзац.
	if strings.Contains(got, ":") || !strings.HasSuffix(got, ".") {
		t.Errorf("подсказка перестала быть списком через запятую: %q", got)
	}
}

// Словарь одного проекта не должен выбирать бюджет целиком: на живой паре
// проектов места хватало только первому по алфавиту, и taxi-kolesa, о котором
// и шёл разговор, не попадал в подсказку ни одним сервисом.
func TestVocabularySharesBudgetBetweenProjects(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.DataDir = t.TempDir()
	many := core.Project{Name: "aaa"}
	for i := 0; i < 40; i++ {
		many.Vocabulary = append(many.Vocabulary, "перваяслово"+strings.Repeat("х", i%5+1))
	}
	cfg.Projects = []core.Project{many, {Name: "zzz", Vocabulary: []string{"каспи", "лотереи"}}}

	got := MeetingVocabulary(context.Background(), cfg, nil, &core.Meeting{})
	for _, want := range []string{"zzz", "каспи"} {
		if !strings.Contains(got, want) {
			t.Errorf("второй проект не попал в подсказку (%q):\n%s", want, got)
		}
	}
}

// Участники созвона — самые надёжные имена, какие есть: они из аккаунтов
// Google, а не из распознавания. Поэтому они первые в списке.
func TestVocabularyPutsMeetingParticipantsFirst(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Projects = []core.Project{{
		Name: "taxi-kolesa",
		// Хоть одно слово, вписанное руками, обязательно: без него подсказка
		// не отправляется вовсе (см. anyHandWritten).
		Vocabulary: []string{"Сапар"},
		Sources:    []core.Source{{Kind: "path", Value: vocabRepo(t)}},
	}}
	got := MeetingVocabulary(context.Background(), cfg, nil,
		&core.Meeting{Participants: []string{"Айгерим Сатпаева"}})
	if !strings.HasPrefix(got, "Айгерим Сатпаева") {
		t.Errorf("участник созвона не первый в подсказке: %q", got)
	}
}

// Словарь, собранный только из репозитория, замерами не исправил ни одного
// слова и при этом слепил 13 реплик в 2. Пока человек не вписал ничего сам,
// подсказки быть не должно — даже при известных участниках созвона.
func TestVocabularyStaysSilentWithoutHandWrittenWords(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Projects = []core.Project{{
		Name:    "taxi-kolesa",
		Aliases: []string{"такси", "колёса"},
		Sources: []core.Source{{Kind: "path", Value: vocabRepo(t)}},
	}}
	m := &core.Meeting{Participants: []string{"Айгерим Сатпаева"}}
	if got := MeetingVocabulary(context.Background(), cfg, nil, m); got != "" {
		t.Errorf("подсказка собралась без единого слова от человека: %q", got)
	}
	// Одно слово руками — и подсказка появляется целиком, вместе с тем,
	// что добыто из репозитория.
	cfg.Projects[0].People = []string{"Орынгали"}
	got := MeetingVocabulary(context.Background(), cfg, nil, m)
	if got == "" {
		t.Fatal("вписали человека — подсказка всё равно пустая")
	}
	if !strings.Contains(got, "Орынгали") {
		t.Errorf("вписанного человека нет в подсказке: %q", got)
	}
}

// Выключатель обязан выключать: подсказка меняет не только слова, но и
// разбивку на реплики, и человек должен уметь вернуть прежнее поведение.
func TestVocabularyCanBeTurnedOff(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Transcribe.Vocabulary = false
	cfg.Projects = []core.Project{{Name: "taxi-kolesa", Aliases: []string{"такси"}}}
	if got := MeetingVocabulary(context.Background(), cfg, nil,
		&core.Meeting{Participants: []string{"Айгерим"}}); got != "" {
		t.Errorf("словарь выключен, а подсказка собралась: %q", got)
	}
}

// Потолок подсказки у whisper — половина текстового контекста модели, около
// 224 токенов. Проект с полусотней сервисов туда не влезает, и обрезать надо с
// конца: имена людей важнее названий.
func TestVocabularyPromptFitsBudget(t *testing.T) {
	v := &Vocab{}
	v.addPeople("Орынгали Каримжан", "Ануар Топатаев", "Рустем Тургельдин")
	for _, s := range []string{
		"worker_sapar", "worker_realtime", "worker_drivers_sync", "worker_parser",
		"worker_features", "worker_others", "worker_freezer", "worker_route_check",
		"worker_reports", "worker_avr", "kafka_orders_consumer", "binotel_ws",
		"telegram_bot", "unfraud_bot", "manager_bot", "avr_report_bot",
		"kpi_bonus_bot", "acquirers", "biometric", "lotteries", "fleet_registry",
		"mobile_clients", "documents", "callcenter", "exceptions", "kaspi",
	} {
		v.addTerms(s)
	}

	full := v.Prompt(promptBudget)
	if n := wordTokens(full); n > promptBudget {
		t.Fatalf("подсказка на %d токенов при бюджете %d:\n%s", n, promptBudget, full)
	}
	// Сервисов больше, чем влезает, — значит хвост обязан быть отрезан.
	if strings.Contains(full, "kaspi") {
		t.Errorf("влезло всё — бюджет ничего не ограничил, проверять нечего:\n%s", full)
	}

	// Тесный бюджет: людей три, места хватает ровно на них.
	tight := v.Prompt(40)
	for _, want := range []string{"Орынгали Каримжан", "Ануар Топатаев", "Рустем Тургельдин"} {
		if !strings.Contains(tight, want) {
			t.Errorf("при тесном бюджете потерялось имя %q:\n%s", want, tight)
		}
	}
	if n := wordTokens(tight); n > 40 {
		t.Errorf("бюджет в 40 токенов не соблюдён: %d токенов в %q", n, tight)
	}
	// Названия сервисов уступают именам людей: их отсекает первыми.
	if strings.Contains(tight, "worker_sapar") {
		t.Errorf("при тесном бюджете уцелело название, а отсекать надо его:\n%s", tight)
	}
	if len(tight) >= len(full) {
		t.Errorf("тесный бюджет ничего не обрезал: %q", tight)
	}
}

// Оценка длины должна ошибаться в одну сторону — в большую. Недооценка
// означала бы, что подсказку обрежет сам whisper, а он режет её с начала, то
// есть по именам людей. Числа справа измерены самим whisper.cpp: он печатает
// точное число токенов, когда подсказка не влезает в -mc.
func TestWordTokensNeverUndercounts(t *testing.T) {
	for _, c := range []struct {
		text string
		real int
	}{
		{"Орынгали, Рустем Тургельдин, Сапар, worker_sapar, МДС, VLive.", 33},
		{"Орынгали, Айгерим, Данияр, Жанибек, Мадина, Рустем.", 29},
		{"worker_sapar, billing, webhook, migrations, onboarding.", 17},
	} {
		got := wordTokens(c.text)
		if got < c.real {
			t.Errorf("оценка %d меньше настоящих %d токенов: %q", got, c.real, c.text)
		}
		// И не втрое больше — иначе бюджет уйдёт в пустоту.
		if got > 2*c.real {
			t.Errorf("оценка %d вдвое с лишним больше настоящих %d: %q", got, c.real, c.text)
		}
	}
}

// Подсказка уходит адаптеру окружением, а не четвёртым аргументом: форма
// команды расшифровки записана в конфигах у людей, и менять её нельзя.
func TestTranscriberPassesVocabularyInEnvironment(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "adapter.sh")
	seen := filepath.Join(dir, "seen")
	mustWriteFile(t, script, "#!/bin/sh\n"+
		"printf 'args=%s\\n' \"$*\" > "+seen+"\n"+
		"printf 'prompt=%s\\n' \"$STENO_PROMPT\" >> "+seen+"\n"+
		"echo '{\"segments\":[{\"start\":0,\"end\":1,\"text\":\"тест\"}]}'\n")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := core.DefaultConfig()
	cfg.Transcribe.Cmd = []string{script, "{{audio}}", "{{language}}"}
	cfg.Transcribe.Nice = false

	if _, _, err := RunTranscriber(context.Background(), cfg, "a.ogg",
		"Орынгали, worker_sapar."); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(seen)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	if !strings.Contains(got, "prompt=Орынгали, worker_sapar.") {
		t.Errorf("словарь не дошёл до адаптера через STENO_PROMPT:\n%s", got)
	}
	// Аргументов по-прежнему два: путь к аудио и язык.
	if !strings.Contains(got, "args=a.ogg \n") && !strings.Contains(got, "args=a.ogg\n") {
		t.Errorf("форма команды расшифровки изменилась — чужие конфиги сломаются:\n%s", got)
	}
}

// Без словаря переменной быть не должно вовсе: адаптер по её отсутствию
// отличает «словаря нет» от «словарь пустой» и зовёт whisper с -mc 0, как
// звал всегда.
func TestTranscriberOmitsEmptyVocabulary(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "adapter.sh")
	seen := filepath.Join(dir, "seen")
	mustWriteFile(t, script, "#!/bin/sh\n"+
		"printf 'set=%s\\n' \"${STENO_PROMPT+да}\" > "+seen+"\n"+
		"echo '{\"segments\":[{\"start\":0,\"end\":1,\"text\":\"тест\"}]}'\n")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := core.DefaultConfig()
	cfg.Transcribe.Cmd = []string{script, "{{audio}}"}
	cfg.Transcribe.Nice = false

	if _, _, err := RunTranscriber(context.Background(), cfg, "a.ogg", "  "); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(seen)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(raw)) != "set=" {
		t.Errorf("пустой словарь всё равно выставил STENO_PROMPT: %q", raw)
	}
}

// Словарь может прийти и не от Vocab.Prompt — из чужого кода, из теста. Потолок
// whisper он обязан соблюдать всё равно: подсказку сверх потолка whisper
// обрежет сам, начиная с первых слов, то есть с имён людей.
func TestVocabularyHintTrimsOverlongInput(t *testing.T) {
	var parts []string
	for i := 0; i < 400; i++ {
		parts = append(parts, "worker_sapar")
	}
	got := vocabularyHint([]string{strings.Join(parts, ", ") + "."})
	if got == "" {
		t.Fatal("длинный словарь выброшен целиком")
	}
	if n := wordTokens(got); n > promptTokenLimit {
		t.Errorf("подсказка на %d токенов ушла бы в whisper как есть", n)
	}
	// А то, что и так влезает, трогать не надо.
	if got := vocabularyHint([]string{"Орынгали, worker_sapar."}); got != "Орынгали, worker_sapar." {
		t.Errorf("короткий словарь изменился: %q", got)
	}
}
