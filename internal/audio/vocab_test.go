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

	v := MeetingVocabulary(context.Background(), cfg, nil, m)
	if v == nil {
		t.Fatal("словарь со словами от человека не собрался")
	}
	got := v.Prompt(promptBudget)
	t.Logf("подсказка при живом бюджете: %s", got)
	// Порядок отсечения виден только при тесном бюджете: рабочий (160)
	// вмещает всё, что здесь собрано. Тесный — 60, на котором это мерилось.
	tight := v.Prompt(60)

	// Просторный бюджет — чтобы видеть весь собранный словарь, а не только то,
	// что влезло. Собирается и режется это в разных местах, и проверять их
	// вместе значит не понять, что именно сломалось.
	wide := &Vocab{Lang: "ru"}
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
	order("аквайринг", "taxi-kolesa", "слово, вписанное из-за ошибки распознавания, важнее названия проекта")
	order("taxi-kolesa", "такси", "название проекта важнее псевдонима")
	order("аквайринг", "worker_sapar", "слово от человека важнее добытого из репозитория")
	order("Oryngali Karimzhan", "worker_sapar", "имена людей важнее названий")
	order("worker_sapar", "biometric", "сервис важнее каталога модуля")

	// При живом бюджете первым делом обязаны уцелеть люди и слово, которое
	// человек вписал сам; название проекта во фразу уже не влезает — оно
	// нужно модели, а не whisper, и уступает.
	for _, want := range []string{"Rustem Turgeldin", "Орынгали", "аквайринг"} {
		if !strings.Contains(tight, want) {
			t.Errorf("при тесном бюджете потерялось %q:\n%s", want, tight)
		}
	}
	if strings.Contains(tight, "taxi-kolesa") {
		t.Errorf("название проекта вытеснило бы слово от человека, а влезло вместе с ним:\n%s", tight)
	}
	// Подсказка — фраза с точками, люди в одном предложении, названия в другом.
	// Это измерено на записях (twopass_test.go): список через запятую рушит
	// границы реплик и вставляет лишние слова, перечень через двоеточие внутри
	// фразы ведёт себя как список. Форма закреплена дословно — правка каркаса
	// это новый замер.
	if !strings.HasPrefix(got, "Созвон команды разработки. Участвуют ") ||
		!strings.Contains(got, ". Обсуждают ") || !strings.HasSuffix(got, ".") || strings.Contains(got, ":") {
		t.Errorf("подсказка перестала быть фразой из трёх предложений: %q", got)
	}
	if at := strings.Index(all, ". Обсуждают "); at < 0 {
		t.Errorf("в подсказке нет предложения про названия: %q", all)
	} else if people := all[:at]; !strings.Contains(people, "Орынгали") || strings.Contains(people, "taxi-kolesa") ||
		strings.Contains(all[at:], "Oryngali Karimzhan") {
		t.Errorf("люди и названия перемешались между предложениями: %q", all)
	}
	// Одно предложение про людей — один человек: «Участвует», а не «Участвуют».
	one := &Vocab{Lang: "ru"}
	one.addPeople("Орынгали")
	if p := one.Prompt(promptBudget); p != "Созвон команды разработки. Участвует Орынгали." {
		t.Errorf("фраза про одного человека: %q", p)
	}
}

// Фраза строится на языке распознавания: whisper продолжает её как текст, и
// русская подводка перед английским созвоном — это просьба переводить.
func TestVocabularyPromptFollowsLanguage(t *testing.T) {
	v := &Vocab{Lang: "en"}
	v.addPeople("Anuar", "Rustem")
	v.addTerms("Sapar")
	if got, want := v.Prompt(promptBudget), "Development team call. Anuar and Rustem take part. They discuss Sapar."; got != want {
		t.Errorf("английская фраза:\n     %q\nнужно %q", got, want)
	}
	// Язык берётся из настройки распознавания, а при автоопределении — из
	// языка интерфейса (в тестах он русский).
	cfg := core.DefaultConfig()
	cfg.Transcribe.Language = "en"
	if got := promptLang(cfg); got != "en" {
		t.Errorf("язык подсказки при language=en: %q", got)
	}
	for _, auto := range []string{"", "auto"} {
		cfg.Transcribe.Language = auto
		if got := promptLang(cfg); got != "ru" {
			t.Errorf("язык подсказки при language=%q: %q, ожидали язык интерфейса", auto, got)
		}
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

	v := MeetingVocabulary(context.Background(), cfg, nil, &core.Meeting{})
	if v == nil {
		t.Fatal("словарь не собрался")
	}
	// Тесный бюджет: при рабочем сорок слов влезают целиком, и очерёдность не видна.
	got := v.Prompt(60)
	// Слова второго проекта идут вперемешку со словами первого, а не после
	// всех сорока: и первое, и второе его слово обязаны влезть.
	for _, want := range []string{"каспи", "лотереи"} {
		if !strings.Contains(got, want) {
			t.Errorf("второй проект не попал в подсказку (%q):\n%s", want, got)
		}
	}
	if strings.Count(got, "перваяслово") >= 4 {
		t.Errorf("первый проект выбрал бюджет один:\n%s", got)
	}
}

// Участники созвона — самые надёжные имена, какие есть: они из аккаунтов
// Google, а не из распознавания. Поэтому они первые в списке.
func TestVocabularyPutsHandWrittenPeopleBeforeParticipants(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Projects = []core.Project{{
		Name:       "taxi-kolesa",
		People:     []string{"Орынгали"},
		Vocabulary: []string{"Сапар"},
		Sources:    []core.Source{{Kind: "path", Value: vocabRepo(t)}},
	}}
	v := MeetingVocabulary(context.Background(), cfg, nil,
		&core.Meeting{Participants: []string{"Rustem Turgeldin"}})
	if v == nil {
		t.Fatal("словарь не собрался")
	}
	got := v.Prompt(200)
	// Вписанный руками «Орынгали» обязан стоять раньше участника из Google:
	// участник латиницей на русскую речь не действует, а слотов во фразе мало.
	a, b := strings.Index(got, "Орынгали"), strings.Index(got, "Rustem Turgeldin")
	if a < 0 || b < 0 {
		t.Fatalf("в подсказке нет одного из имён: %q", got)
	}
	if a > b {
		t.Errorf("участник из Google встал раньше вписанного руками: %q", got)
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
	if got := MeetingVocabulary(context.Background(), cfg, nil, m); got != nil {
		t.Errorf("подсказка собралась без единого слова от человека: %q", got.Prompt(promptBudget))
	}
	// Одно слово руками — и подсказка появляется целиком, вместе с тем,
	// что добыто из репозитория.
	cfg.Projects[0].People = []string{"Орынгали"}
	v := MeetingVocabulary(context.Background(), cfg, nil, m)
	if v == nil {
		t.Fatal("вписали человека — подсказка всё равно пустая")
	}
	if got := v.Prompt(promptBudget); !strings.Contains(got, "Орынгали") {
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
		&core.Meeting{Participants: []string{"Айгерим"}}); got != nil {
		t.Errorf("словарь выключен, а подсказка собралась: %q", got.Prompt(promptBudget))
	}
}

// Потолок подсказки у whisper — половина текстового контекста модели, около
// 224 токенов. Проект с полусотней сервисов туда не влезает, и обрезать надо с
// конца: имена людей важнее названий. Меряется фраза целиком, с каркасом: у
// whisper бюджет один на всё.
func TestVocabularyPromptFitsBudget(t *testing.T) {
	v := &Vocab{Lang: "ru"}
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

	// Порядок отсечения мерился на 60 — на нём и проверяем; рабочий бюджет
	// выше и здесь ничего бы не отрезал.
	const tight = 60
	full := v.Prompt(tight)
	if n := wordTokens(full); n > tight {
		t.Fatalf("подсказка на %d токенов при бюджете %d:\n%s", n, tight, full)
	}
	// Сервисов больше, чем влезает, — значит хвост обязан быть отрезан.
	if strings.Contains(full, "kaspi") {
		t.Errorf("влезло всё — бюджет ничего не ограничил, проверять нечего:\n%s", full)
	}
	// Живой бюджет с каркасом фразы вмещает два полных имени из трёх; названия
	// сервисов уступают людям и отсекаются первыми — ни одного не влезло.
	for _, want := range []string{"Орынгали Каримжан", "Ануар Топатаев"} {
		if !strings.Contains(full, want) {
			t.Errorf("при живом бюджете потерялось имя %q:\n%s", want, full)
		}
	}
	if strings.Contains(full, "worker_sapar") {
		t.Errorf("при живом бюджете уцелело название, а отсекать надо его раньше имён:\n%s", full)
	}
	if !strings.HasSuffix(full, "Ануар Топатаев.") {
		t.Errorf("фраза без названий должна кончаться на людях: %q", full)
	}

	// Просторнее — влезают третье имя и первые сервисы, но не все.
	wide := v.Prompt(90)
	for _, want := range []string{"Рустем Тургельдин", "worker_sapar"} {
		if !strings.Contains(wide, want) {
			t.Errorf("при бюджете 90 потерялось %q:\n%s", want, wide)
		}
	}
	if strings.Contains(wide, "kaspi") || wordTokens(wide) > 90 {
		t.Errorf("бюджет 90 не соблюдён: %d токенов в %q", wordTokens(wide), wide)
	}
	// Теснее — одно имя, и режется с конца, а не с начала.
	if tight := v.Prompt(45); tight != "Созвон команды разработки. Участвует Орынгали Каримжан." {
		t.Errorf("при тесном бюджете ожидали одно первое имя, получили %q", tight)
	}
	// Первое слово не влезло — подсказки нет вовсе, а не голый каркас.
	if got := v.Prompt(30); got != "" {
		t.Errorf("в бюджет не влезло ни одного слова, а фраза есть: %q", got)
	}
	// Список для движков с отдельным входом бюджету whisper не подчиняется:
	// в нём всё, что собрано, — но не больше сотни.
	if terms := v.Terms(); len(terms) != 29 || terms[0] != "Орынгали Каримжан" || terms[28] != "kaspi" {
		t.Errorf("список слов: %d, %v", len(terms), terms)
	}
	many := &Vocab{}
	for i := 0; i < 300; i++ {
		many.addTerms("слово" + strings.Repeat("х", i%7+1) + strings.Repeat("у", i/7))
	}
	if n := len(many.Terms()); n != termsLimit {
		t.Errorf("список слов не ограничен сотней: %d", n)
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
// команды расшифровки записана в конфигах у людей, и менять её нельзя. И в
// двух видах сразу: фразой в STENO_PROMPT — для whisper, списком в
// STENO_TERMS — для движков с отдельным входом для словаря.
func TestTranscriberPassesVocabularyInEnvironment(t *testing.T) {
	quiet(t)
	dir := t.TempDir()
	script := filepath.Join(dir, "adapter.sh")
	seen := filepath.Join(dir, "seen")
	mustWriteFile(t, script, "#!/bin/sh\n"+
		"printf 'args=%s\\n' \"$*\" >> "+seen+"\n"+
		"printf 'prompt=%s\\n' \"${STENO_PROMPT-нет}\" >> "+seen+"\n"+
		"printf 'terms=%s\\n' \"${STENO_TERMS-нет}\" >> "+seen+"\n"+
		"echo '{\"segments\":[{\"start\":0,\"end\":1,\"text\":\"тест\"}]}'\n")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := core.DefaultConfig()
	cfg.Transcribe.Cmd = []string{script, "{{audio}}", "{{language}}"}
	cfg.Transcribe.Nice = false

	v := &Vocab{Lang: "ru"}
	v.addPeople("Орынгали")
	v.addTerms("worker_sapar")
	if _, _, err := RunTranscriber(context.Background(), cfg, "a.ogg", v); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(seen)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	if !strings.Contains(got, "prompt=Созвон команды разработки. Участвует Орынгали. Обсуждают worker_sapar.\n") {
		t.Errorf("фраза не дошла до адаптера через STENO_PROMPT:\n%s", got)
	}
	if !strings.Contains(got, "terms=Орынгали, worker_sapar\n") {
		t.Errorf("список не дошёл до адаптера через STENO_TERMS:\n%s", got)
	}
	// Аргументов по-прежнему два: путь к аудио и язык. Адаптер без признака
	// vocabulary зовётся дважды, и оба раза с той же командой.
	if n := strings.Count(got, "args=a.ogg \n") + strings.Count(got, "args=a.ogg\n"); n != 2 {
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

	// Словарь без единого полезного слова — то же, что без словаря.
	empty := &Vocab{}
	empty.addTerms("  ", "-", "v")
	if _, _, err := RunTranscriber(context.Background(), cfg, "a.ogg", empty); err != nil {
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

// Рабочий бюджет обязан вмещать всё, что человек вписал руками. При 60 на
// живом созвоне 2026-09-11 шестое имя — «Гульназ» — отрезалось на два токена,
// whisper услышал «для нас», и задача осталась без исполнителя.
func TestVocabularyBudgetHoldsEveryHandWrittenName(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Projects = []core.Project{{
		Name:       "Такси",
		Aliases:    []string{"такси", "парк"},
		People:     []string{"Ануар", "Орынгали", "Азамат", "Рустем", "Гульназ"},
		Vocabulary: []string{"Сапар", "Yandex"},
	}}
	v := MeetingVocabulary(context.Background(), cfg, nil,
		&core.Meeting{Participants: []string{"Rustem Turgeldin"}})
	if v == nil {
		t.Fatal("словарь не собрался")
	}
	got := v.Prompt(promptBudget)
	for _, want := range []string{"Ануар", "Орынгали", "Азамат", "Рустем", "Гульназ", "Сапар", "Yandex"} {
		if !strings.Contains(got, want) {
			t.Errorf("вписанное руками %q не влезло в рабочий бюджет:\n%s", want, got)
		}
	}
	if n := wordTokens(got); n > promptTokenLimit/2 {
		t.Errorf("подсказка на %d токенов по оценке — слишком близко к пределу whisper %d, режущему с начала", n, promptTokenLimit)
	}
}
