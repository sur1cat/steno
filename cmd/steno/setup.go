package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/sur1cat/steno/internal/brain"
	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
	"github.com/sur1cat/steno/internal/spec"
	"golang.org/x/term"
)

// Мастер установки.
//
// Развернуть steno должно быть можно и на ноутбуке одного человека, и на
// сервере команды с шестью параллельными созвонами. Разница между этими двумя
// установками — десяток решений, каждое из которых по отдельности неочевидно:
// сколько расшифровок держать одновременно, какой моделью распознавать, куда
// публиковать. Заставлять человека собирать это из документации значит не
// получить ни одной установки, кроме своей.
//
// Поэтому мастер спрашивает по одному и сразу проверяет ответ: ключ, который
// не работает, лучше узнать здесь, а не на первом созвоне.

type setupProfile struct {
	Key   string
	Name  string
	About string

	MaxConcurrentMeetings int
	MaxConcurrentWhisper  int
	Effort                string
}

// См. uiTabTitles: таблица собирается лениво, уже на известном языке.
var setupProfiles = sync.OnceValue(func() []setupProfile {
	return []setupProfile{
		{
			Key: "personal", Name: i18n.Tr("Для себя"),
			About:                 i18n.Tr("Один человек, свои созвоны. Записи и расшифровки на своей машине."),
			MaxConcurrentMeetings: 1, MaxConcurrentWhisper: 1, Effort: "low",
		},
		{
			Key: "team", Name: i18n.Tr("Небольшая команда"),
			About:                 i18n.Tr("Несколько созвонов в неделю, редко больше одного разом."),
			MaxConcurrentMeetings: 2, MaxConcurrentWhisper: 1, Effort: "low",
		},
		{
			Key: "big", Name: i18n.Tr("Большая команда"),
			About:                 i18n.Tr("Пять-шесть созвонов параллельно, отдельный сервер, GPU или Groq."),
			MaxConcurrentMeetings: 6, MaxConcurrentWhisper: 2, Effort: "low",
		},
	}
})

type setupState struct {
	dir string
	// -o назван человеком: тогда каталог не переспрашиваем и не уводим в ~/steno.
	dirChosen bool
	profile   setupProfile
	cfg       *core.Config
	env       map[string]string
	in        *bufio.Reader
}

func cmdSetup(ctx context.Context, args []string) error {
	fs := newFlagSet("setup")
	out := fs.String("o", "", i18n.Tr("куда записать конфиг"))
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	chosen := *out != ""
	if !chosen {
		*out = "steno.json"
	}

	s := &setupState{
		cfg: core.DefaultConfig(),
		env: map[string]string{},
		in:  bufio.NewReader(os.Stdin),
	}
	abs, err := filepath.Abs(*out)
	if err != nil {
		return err
	}
	s.dir = filepath.Dir(abs)
	s.dirChosen = chosen

	// Ctrl+C обязан прерывать. Сам по себе он этого не делал: main перехватывает
	// SIGINT ради мягкой остановки сервиса, а мастер висит на чтении stdin и про
	// отмену контекста не знает — сигнал ловился и пропадал. Хуже того, в строке
	// ниже было написано, что прервать можно в любой момент.
	//
	// Состояние терминала снимаем заранее: прерывание на вводе пароля приходится
	// на сырой режим, и без восстановления человек остаётся с неработающей
	// оболочкой.
	fd := int(os.Stdin.Fd())
	tty, _ := term.GetState(fd)
	// done закрывается при выходе: без него обработчик срабатывал и на обычном
	// завершении — main отменяет контекст, когда команда вернулась, и мастер
	// дописывал «прервано» под успешно записанным конфигом.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-done:
			return
		case <-ctx.Done():
		}
		if tty != nil {
			_ = term.Restore(fd, tty)
		}
		fmt.Println()
		fmt.Println(dim(i18n.Tr("прервано — ничего не записано")))
		os.Exit(130)
	}()

	title(i18n.Tr("steno — настройка"))
	fmt.Println(dim(i18n.Tr("Спрошу по одному и сразу проверю. Пустой ответ берёт значение в скобках.")))
	fmt.Println(dim(i18n.Tr("Ctrl+C прерывает в любой момент — до самого конца ничего не записывается.")))
	fmt.Println()

	if _, err := os.Stat(abs); err == nil {
		if !s.confirm(abs+i18n.Tr(" уже есть. Перезаписать?"), false) {
			return errors.New(i18n.Tr("отменено"))
		}
	}

	steps := []func(context.Context) error{
		s.askLang,
		s.askProfile,
		s.askData,
		s.askTranscribe,
		s.askClaude,
		s.askSources,
		s.askTargets,
		s.askPanel,
		s.askAgent,
	}
	// +1 — «Готово» тоже раздел и тоже печатает заголовок. Без этого последним
	// показывалось «шаг 8 из 7».
	stepNo, stepTotal = 0, len(steps)+1
	for _, step := range steps {
		if err := step(ctx); err != nil {
			return err
		}
	}
	return s.write(abs)
}

// quietSetup — та же настройка, что делает мастер, но без единого вопроса и
// без единой строки на экран. Заводит её `steno note`, когда настройки нет
// вовсе: заметке не нужны ни бот, ни Telegram, ни календарь, и спрашивать о
// них человека, который хочет наговорить мысль, значит его потерять.
//
// Ответы — умолчания мастера, кроме двух мест, где умолчание рассчитано на
// того, кто отвечает: профиль «для себя», а не «команда» (пределы на
// параллельные созвоны заметке ни к чему), и whisper на этой машине, а не
// Groq (Groq просит ключ, а спросить его некому). Разбор — подписка Claude
// Code, если вход выполнен, как выбрал бы и мастер; иначе остаётся auto,
// который подхватит ключ из окружения, а `steno note` спросит, если нет и
// его (ensureBrain). Панель — тот же адрес и такой же придуманный пароль.
// Всё остальное — теми же функциями, что у мастера, и лежит там же: ~/steno,
// указатель в ~/.config/steno/path, секреты в .env с правами 0600.
func quietSetup() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	s := &setupState{cfg: core.DefaultConfig(), env: map[string]string{}}
	s.setLang(i18n.UILang)
	s.setProfile(setupProfiles()[0])
	if err := s.setDir(filepath.Join(home, "steno")); err != nil {
		return "", err
	}
	s.useWhisper()
	if ok, _ := brain.ClaudeCLIAvailable(); ok {
		s.useClaudeCLI()
	}
	s.setPanel(defaultPanelAddr, randomPassword())
	w, err := s.save(filepath.Join(s.dir, core.DefaultConfigPath))
	if err != nil {
		return "", err
	}
	// База — тут же, с каналами из этой настройки. Иначе первая же команда
	// рапортовала бы «перенёс 6 каналов из конфига в базу — дальше правь их в
	// панели» человеку, который ни конфига не писал, ни панели не видел.
	if st, err := core.OpenStore(s.cfg.DataDir); err == nil {
		_, _ = core.ImportChannels(st, s.cfg)
		st.Close()
	}
	return w.config, nil
}

// --- шаги --------------------------------------------------------------------

// askLang — первым вопросом и до всего остального: дальше мастер говорит на
// выбранном языке, и спрашивать об этом в конце было бы поздно. Заголовок и
// подписи здесь двуязычные — на этом шаге ещё неизвестно, чей это экран.
//
// Умолчание — тот язык, на котором steno запущен: STENO_LANG=ru steno setup
// не должен переспрашивать очевидное.
func (s *setupState) askLang(context.Context) error {
	section("Language · Язык")
	def := 0
	if i18n.UILang == i18n.LangRU {
		def = 1
	}
	i := s.choose("Interface language · Язык интерфейса", []string{
		"English " + dim("CLI, terminal interface, panel"),
		"Русский " + dim("CLI, терминальный интерфейс, панель"),
	}, def)
	s.setLang([]string{i18n.LangEN, i18n.LangRU}[i])
	return nil
}

// Ниже у каждого шага мастера есть пара «спросить» и «применить»: askLang
// спрашивает, setLang применяет. Разделены они ради quietSetup — настройки без
// единого вопроса, которую `steno note` заводит сама. Она собирается из тех
// же «применить», что и мастер, и потому не может стать третьей формой
// конфига: другой каталог, другой путь к адаптеру, забытый указатель или
// .gitignore — всё это здесь общее.

func (s *setupState) setLang(lang string) {
	s.cfg.Lang = lang
	i18n.SetLang(s.cfg.Lang)
	// Часть умолчаний зависит от языка, а конфиг собран до этого вопроса:
	// имя бота, язык субтитров, язык follow-up и маркер календаря.
	core.ApplyLangDefaults(s.cfg)
}

func (s *setupState) askProfile(context.Context) error {
	section(i18n.Tr("Масштаб"))
	var opts []string
	for _, p := range setupProfiles() {
		opts = append(opts, p.Name+" — "+dim(p.About))
	}
	s.setProfile(setupProfiles()[s.choose(i18n.Tr("Как будете пользоваться?"), opts, 1)])
	return nil
}

func (s *setupState) setProfile(p setupProfile) {
	s.profile = p
	s.cfg.Calendar.MaxConcurrent = p.MaxConcurrentMeetings
	s.cfg.Transcribe.MaxConcurrent = p.MaxConcurrentWhisper
	s.cfg.Claude.Effort = p.Effort
}

// askData выбирает каталог и заводит его сам. Раньше мастер молча писал в
// текущий, и человеку приходилось перед запуском делать mkdir и cd — шаг, о
// котором он узнавал только из инструкции.
func (s *setupState) askData(context.Context) error {
	section(i18n.Tr("Где хранить"))
	fmt.Println(dim(i18n.Tr("  Сюда лягут настройки, записи созвонов и база. Каталог заведу сам.")))
	fmt.Println()

	home, _ := os.UserHomeDir()
	def := filepath.Join(home, "steno")
	if s.dirChosen {
		def = s.dir // человек сам назвал файл через -o, не спорим
	}
	// Ответ на «Каталог» — свободная строка, и промахнуться в ней легко:
	// заготовленный список ответов съезжает на один, цифра от нумерованного
	// вопроса попадает сюда, и steno молча заводит ./1 рядом с собой. Ровно так
	// в корне репозитория и появились каталоги 1/ и 3/ — месяц никто не замечал,
	// потому что мастер ничего об этом не сказал.
	//
	// Запрещать подозрительные ответы бесполезно: их не угадаешь. Поэтому
	// показываем то, что получится, до того как создадим, — увидев в вопросе
	// путь с /1 на конце, человек скажет «нет».
	var abs string
	for {
		dir := brain.ExpandHome(s.ask(i18n.Tr("Каталог"), def))
		a, err := filepath.Abs(dir)
		if err != nil {
			return err
		}
		if s.confirm(i18n.Trf("заведу %s — верно?", a), true) {
			abs = a
			break
		}
	}
	if err := s.setDir(abs); err != nil {
		return err
	}
	fmt.Println(ok(abs))
	return nil
}

// setDir заводит каталог настройки и данных. Конфиг ляжет в него же — см. write.
func (s *setupState) setDir(abs string) error {
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return fmt.Errorf(i18n.Tr("не создался каталог: %w"), err)
	}
	s.dir = abs
	s.cfg.DataDir = filepath.Join(abs, "data")
	if err := os.MkdirAll(s.cfg.DataDir, 0o755); err != nil {
		return fmt.Errorf(i18n.Tr("не создался каталог: %w"), err)
	}
	return nil
}

func (s *setupState) askTranscribe(ctx context.Context) error {
	section(i18n.Tr("Чем распознавать речь"))
	fmt.Println(dim(i18n.Tr("  От этого зависит и качество, и во что обойдётся железо.")))
	fmt.Println()

	i := s.choose(i18n.Tr("Выбери"), []string{
		i18n.Tr("Groq — та же whisper-large-v3, но на их железе. ") +
			dim(i18n.Tr("$0.04 за час звука, ничего ставить не надо, аудио уходит наружу")),
		i18n.Tr("whisper на своей машине. ") +
			dim(i18n.Tr("ничего не уходит наружу; нужна модель на 0.5–3 ГБ, а без GPU медленно")),
		i18n.Tr("Субтитры Google Meet. ") +
			dim(i18n.Tr("бесплатно и мгновенно, качество ниже, смешанную речь не тянет")),
	}, 0)

	switch i {
	case 0:
		s.cfg.Transcribe.Source = "command"
		s.cfg.Transcribe.Cmd = []string{core.FindAdapter("groq.sh"), "{{audio}}", "{{language}}"}
		key := s.askSecret(i18n.Tr("Ключ Groq"), "console.groq.com/keys")
		if key != "" {
			s.env["GROQ_API_KEY"] = key
			fmt.Println(ok(i18n.Tr("ключ записан")))
		}
	case 1:
		s.useWhisper()
		s.checkWhisper()
	case 2:
		s.cfg.Transcribe.Source = "captions"
		fmt.Println(dim(i18n.Tr("  Текст возьмётся из субтитров Meet. Имена говорящих в нём уже есть.")))
	}
	return nil
}

func (s *setupState) useWhisper() {
	s.cfg.Transcribe.Source = "command"
	s.cfg.Transcribe.Cmd = []string{core.FindAdapter("whisper-cpp.sh"), "{{audio}}", "{{language}}"}
}

// checkWhisper смотрит, чего не хватает, и говорит, чем это ставится. Узнать об
// отсутствующей модели на первом созвоне — худший момент из возможных.
func (s *setupState) checkWhisper() {
	missing := []string{}
	for _, bin := range []string{"whisper-cli", "ffmpeg", "jq"} {
		if _, err := exec.LookPath(bin); err != nil {
			missing = append(missing, bin)
		}
	}
	if len(missing) > 0 {
		fmt.Println(warn(i18n.Tr("не хватает: ") + strings.Join(missing, ", ")))
		fmt.Println(dim("  brew install whisper-cpp ffmpeg jq"))
	} else {
		fmt.Println(ok(i18n.Tr("whisper-cli, ffmpeg и jq на месте")))
	}

	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".cache", "whisper")
	found := ""
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "ggml-") && strings.HasSuffix(e.Name(), ".bin") &&
				!strings.Contains(e.Name(), "silero") {
				found = e.Name()
				break
			}
		}
	}
	if found == "" {
		fmt.Println(warn(i18n.Tr("модели нет — без неё расшифровка не заработает")))
		fmt.Println(dim("  mkdir -p " + dir))
		fmt.Println(dim("  curl -L -o " + dir + "/ggml-large-v3-q5_0.bin \\"))
		fmt.Println(dim("    https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-large-v3-q5_0.bin"))
		fmt.Println(dim(i18n.Tr("  и отдельно VAD — 868 КБ, но ускоряет в восемь раз:")))
		fmt.Println(dim("  curl -L -o " + dir + "/ggml-silero-v5.1.2.bin \\"))
		fmt.Println(dim("    https://huggingface.co/ggml-org/whisper-vad/resolve/main/ggml-silero-v5.1.2.bin"))
	} else if strings.Contains(found, "turbo") {
		// turbo не просто хуже — она подменяет незнакомые слова похожими
		// знакомыми и зацикливается. Ошибка получается связной и правдоподобной,
		// в follow-up её уже не отличить от сказанного.
		fmt.Println(warn(i18n.Tr("модель ") + found + i18n.Tr(" — она путает названия и зацикливается")))
		fmt.Println(dim(i18n.Tr("  на записи созвона turbo превратила Plaud в Cloud AI и повторила")))
		fmt.Println(dim(i18n.Tr("  его семнадцать раз подряд. Возьми large-v3-q5_0 — тот же размер:")))
		fmt.Println(dim("  curl -L -o " + dir + "/ggml-large-v3-q5_0.bin \\"))
		fmt.Println(dim("    https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-large-v3-q5_0.bin"))
	} else {
		fmt.Println(ok(i18n.Tr("модель ") + found))
	}
}

func (s *setupState) askClaude(ctx context.Context) error {
	section(i18n.Tr("Кто собирает follow-up"))

	// Четыре пути, и выбор между ними не про вендора, а про то, чем человек
	// платит и что у него уже стоит. Две подписки (Claude Code и ChatGPT) не
	// требуют отдельного счёта, но требуют выполненного входа, который делает
	// человек: на сервере, куда никто не заходит, это не работает. Поэтому
	// подписку предлагаем первой только если её CLI уже готов.
	//
	// Порядок первых двух не менялся с тех пор, как их было всего два: у людей
	// записаны установки «выбери 1» и «выбери 2», и переставлять их значит
	// молча переключить чью-то установку на другой способ платить.
	cliOK, cliNote := brain.ClaudeCLIAvailable()
	codexOK, codexNote := brain.CodexCLIAvailable()
	opts := []string{
		i18n.Tr("Подписка Claude ") + dim(i18n.Tr("через claude -p, отдельный ключ не нужен")),
		i18n.Tr("Ключ Anthropic ") + dim(i18n.Tr("нужен для сервера: работает без входа человеком")),
		i18n.Tr("Совместимый с OpenAI ") +
			dim(i18n.Tr("OpenAI, Groq, OpenRouter, DeepSeek — или своя модель на этой машине")),
		i18n.Tr("Подписка ChatGPT ") + dim(i18n.Tr("через codex exec, схема соблюдается точнее всех")),
		i18n.Tr("Свой скрипт ") + dim(i18n.Tr("всё остальное: llamafile, своя обёртка, эндпоинт за VPN")),
	}
	if cliOK {
		opts[0] += "\n     " + ok(cliNote)
	} else {
		opts[0] += "\n     " + warn(cliNote)
	}
	if codexOK {
		opts[3] += "\n     " + ok(codexNote)
	} else {
		opts[3] += "\n     " + warn(codexNote)
	}
	def := 1
	if cliOK {
		def = 0
	}
	switch s.choose(i18n.Tr("Чем платить за follow-up"), opts, def) {
	case 0:
		s.useClaudeCLI()
		if !cliOK {
			fmt.Println(warn(i18n.Tr("CLI пока не готов — steno скажет об этом при первом созвоне")))
			fmt.Println(dim(i18n.Tr("  поставь Claude Code и войди: claude auth login")))
		}
		fmt.Println(dim(i18n.Tr("  Потолок на один follow-up: $") +
			strconv.FormatFloat(s.cfg.Claude.MaxUSDPerCall, 'f', -1, 64) +
			i18n.Tr(" — меняется в claude.max_usd_per_call")))
	case 1:
		s.useClaudeKey()
		key := s.askSecret(i18n.Tr("Ключ Anthropic"), "console.anthropic.com → API keys")
		if key != "" {
			s.env["ANTHROPIC_API_KEY"] = key
		}
	case 2:
		s.askOpenAI()
		return nil
	case 3:
		s.cfg.Brain.Provider = core.ProviderCodex
		if !codexOK {
			fmt.Println(warn(i18n.Tr("codex пока не готов — steno скажет об этом при первом созвоне")))
			fmt.Println(dim(i18n.Tr("  поставь его и войди: npm i -g @openai/codex, потом codex")))
		}
		s.cfg.Brain.Codex.Model = s.ask(i18n.Tr("Модель (пусто — та, что у codex по умолчанию)"), "")
		return nil
	case 4:
		s.askLLMScript()
		return nil
	}

	i := s.choose(i18n.Tr("Модель"), []string{
		"claude-opus-5 " + dim(i18n.Tr("умнее, около $0.40 за часовой созвон")),
		"claude-sonnet-5 " + dim(i18n.Tr("дешевле в два с половиной раза, для планёрок обычно хватает")),
	}, 0)
	s.cfg.Claude.Model = []string{"claude-opus-5", "claude-sonnet-5"}[i]
	fmt.Println(dim(i18n.Tr("  Усилие: ") + s.cfg.Claude.Effort + i18n.Tr(". Выше поднимать смысла нет: на замерах")))
	fmt.Println(dim(i18n.Tr("  усилие не добавило ни одной задачи, только время и расход.")))
	return nil
}

func (s *setupState) useClaudeCLI() {
	s.cfg.Claude.Via = "cli"
	// Потолок на запрос: у подписки нет счёта, который придёт в конце
	// месяца, но есть лимит, который можно выбрать одним циклом.
	if s.cfg.Claude.MaxUSDPerCall == 0 {
		s.cfg.Claude.MaxUSDPerCall = 2
	}
}

func (s *setupState) useClaudeKey() { s.cfg.Claude.Via = "api" }

// useOpenAI — совместимый с OpenAI адрес по заготовке. Свой адрес и имя
// переменной с ключом askOpenAI дописывает сверху: у заготовок они свои.
func (s *setupState) useOpenAI(preset, model string) {
	s.cfg.Brain.Provider = core.ProviderOpenAI
	s.cfg.Brain.OpenAI.Preset = preset
	s.cfg.Brain.OpenAI.Model = model
	// json_mode оставляем на "auto": какие модели за этим адресом умеют строгую
	// схему, отсюда не видно, а лишний вопрос на установке — это шанс ответить
	// неверно и получить отказ на первом же созвоне.
	s.cfg.Brain.OpenAI.JSONMode = "auto"
}

// askLLMScript — модель через внешний скрипт. Договор описан словами в
// adapters/ollama.sh; сам скрипт спрашиваем здесь и только здесь: в панели
// такого поля нет и не будет — она открыта всей команде по общему паролю, а
// поле, из которого сервис запускает команду, это чужой шелл на этой машине.
func (s *setupState) askLLMScript() {
	s.cfg.Brain.Provider = core.ProviderCommand
	fmt.Println(dim(i18n.Tr("  Скрипт получает запрос объектом JSON в stdin и отвечает объектом JSON")))
	fmt.Println(dim(i18n.Tr("  в stdout. Договор целиком — в шапке adapters/ollama.sh, с него же")))
	fmt.Println(dim(i18n.Tr("  удобно списать свой.")))
	fmt.Println()

	def := "./adapters/ollama.sh"
	path := brain.ExpandHome(s.ask(i18n.Tr("Скрипт"), def))
	s.cfg.LLM.Cmd = []string{path}
	s.cfg.LLM.Model = s.ask(i18n.Tr("Модель (её имя уедет скрипту полем model)"), "llama3.1")

	// `ok` здесь — имя функции вывода, поэтому результат проверки зовём иначе.
	ready, why := brain.LLMCmdAvailable(s.cfg)
	if ready {
		fmt.Println(ok(why))
	} else {
		fmt.Println(warn(why))
		fmt.Println(dim(i18n.Tr("  steno скажет об этом ещё раз при первом созвоне")))
	}
}

// customLLMKeyEnv — имя переменной для ключа к своему адресу. Одно на всех:
// у заготовок имя приходит от провайдера, а тут его брать неоткуда.
const customLLMKeyEnv = "STENO_LLM_API_KEY"

// askOpenAI — настройка любого сервера, говорящего на диалекте OpenAI.
//
// Заготовки здесь — только чтобы не набирать адрес руками. Последним пунктом
// всегда стоит «свой адрес»: смысл этого пути в том, что провайдера не надо
// добавлять в steno, — иначе получился бы тот же захардкоженный список, только
// длиннее.
func (s *setupState) askOpenAI() {
	presets := core.OpenAIPresets()
	opts := make([]string, 0, len(presets))
	for _, p := range presets {
		line := p.Name
		if p.BaseURL != "" {
			line += " " + dim(p.BaseURL)
		}
		if p.Hint != "" {
			line += "\n     " + dim(p.Hint)
		}
		opts = append(opts, line)
	}
	p := presets[s.choose(i18n.Tr("Куда ходить"), opts, 0)]
	if p.Key == core.PresetCustom {
		s.cfg.Brain.OpenAI.BaseURL = s.ask(i18n.Tr("Адрес (обычно кончается на /v1)"), "")
	}
	s.useOpenAI(p.Key, s.ask(i18n.Tr("Модель"), p.Model))

	// Ключ спрашиваем только там, где он нужен. У модели на своей машине его не
	// бывает, и вопрос про него читается как «а вдруг всё-таки надо».
	switch {
	case p.Local:
		fmt.Println(dim(i18n.Tr("  Ключ не нужен, расход нулевой — модель на этой же машине.")))
		fmt.Println(dim(i18n.Tr("  Запусти её до первого созвона: steno скажет, если не достучится.")))
	case p.KeyEnv != "":
		// Groq уже мог спросить ключ на шаге расшифровки — он там тот же самый.
		// Спрашивать второй раз значит либо получить его дважды, либо получить
		// пустую строку и стереть первый.
		if s.env[p.KeyEnv] != "" {
			fmt.Println(ok(i18n.Tr("ключ ") + p.KeyEnv + i18n.Tr(" уже задан выше — беру его")))
			break
		}
		if key := s.askSecret(i18n.Tr("Ключ"), p.Hint); key != "" {
			s.env[p.KeyEnv] = key
		}
	default:
		// Свой адрес: имя переменной выбираем сами. В панели его всё равно не
		// увидят — там секретов нет ни в каком виде, — а человеку одним именем
		// меньше держать в голове.
		s.cfg.Brain.OpenAI.APIKeyEnv = customLLMKeyEnv
		if key := s.askSecret(i18n.Tr("Ключ (пусто — если сервер его не спрашивает)"), ""); key != "" {
			s.env[customLLMKeyEnv] = key
		} else {
			s.cfg.Brain.OpenAI.APIKeyEnv = ""
		}
	}

	if !p.Local {
		fmt.Println(dim(i18n.Tr("  Цена этой модели steno неизвестна: `steno cost` покажет токены,")))
		fmt.Println(dim(i18n.Tr("  а деньги — только если впишешь её в brain.openai.prices.")))
	}
}

func (s *setupState) askSources(ctx context.Context) error {
	section(i18n.Tr("Как бот попадает в звонок"))
	fmt.Println(dim(i18n.Tr("  Можно включить несколько. Календарь закрывает запланированное,")))
	fmt.Println(dim(i18n.Tr("  остальные — внезапное.")))
	fmt.Println()

	if s.confirm(i18n.Tr("Ходить по календарям команды?"), false) {
		s.cfg.Calendar.Enabled = true
		fmt.Println(dim(i18n.Tr("  Нужны почтовые адреса тех, чьи встречи бот должен видеть.")))
		fmt.Println(dim(i18n.Tr("  Свой — чтобы ходить на собственные созвоны. Чужой сработает,")))
		fmt.Println(dim(i18n.Tr("  только если этот человек открыл боту доступ к своему календарю.")))
		s.cfg.Calendar.Calendars = core.CommaList(s.ask(i18n.Tr("Чьи календари (почта, через запятую)"), ""))
		s.askGoogleAccess()
	}
	if s.confirm(i18n.Tr("Приходить, когда бота добавляют в звонок по почте?"), false) {
		s.cfg.Gmail.Enabled = true
		s.cfg.Gmail.Account = s.ask(i18n.Tr("Почта аккаунта бота"), "")
		s.askGoogleAccess()
	}
	if s.confirm(i18n.Tr("Принимать ссылки в Telegram?"), true) {
		s.cfg.Telegram.Listen = true
		if tok := s.askSecret(i18n.Tr("Токен бота Telegram"), "@BotFather"); tok != "" {
			s.env["TELEGRAM_BOT_TOKEN"] = tok
		}
		s.cfg.Telegram.ChatID = s.ask(i18n.Tr("Из какого чата принимать (chat_id)"), "")
		if s.cfg.Telegram.ChatID != "" {
			s.cfg.Telegram.AllowedChats = []string{s.cfg.Telegram.ChatID}
		} else {
			fmt.Println(warn(i18n.Tr("без chat_id сервис не запустит приём: принимать ссылки от кого угодно нельзя")))
		}
	}
	return nil
}

func (s *setupState) askTargets(ctx context.Context) error {
	section(i18n.Tr("Куда складывать итоги"))
	fmt.Println(dim(i18n.Tr("  Панель есть всегда — там архив, поиск и проекты. Остальное по желанию.")))
	fmt.Println()

	// Спрашиваем всегда, даже если приём в Telegram уже включён: включить
	// отправку молча, по одному лишь факту приёма, — значит не сказать
	// человеку, куда пойдут итоги его созвонов.
	if s.cfg.Telegram.ChatID != "" {
		if s.confirm(i18n.Tr("Присылать follow-up в Telegram, в тот же чат?"), true) {
			s.cfg.Telegram.Enabled = true
		}
	} else if s.confirm(i18n.Tr("Присылать follow-up в Telegram?"), false) {
		if tok := s.askSecret(i18n.Tr("Токен бота Telegram"), "@BotFather"); tok != "" {
			s.env["TELEGRAM_BOT_TOKEN"] = tok
		}
		s.cfg.Telegram.ChatID = s.ask(i18n.Tr("В какой чат (chat_id)"), "")
		s.cfg.Telegram.Enabled = s.cfg.Telegram.ChatID != ""
	}
	if s.confirm(i18n.Tr("Публиковать в Google Docs?"), false) {
		s.cfg.GoogleDocs.Enabled = true
		s.askGoogleAccess()
		// От чужого имени умеет только ключ организации. По кнопке документы
		// создаются от того, кто её нажал, и спрашивать тут нечего.
		if s.cfg.GoogleDocs.CredentialsFile != "" {
			s.cfg.GoogleDocs.Subject = s.ask(i18n.Tr("От чьего имени создавать документы"), "")
		}
		s.cfg.GoogleDocs.FolderID = s.ask(i18n.Tr("Папка на Drive: хвост адреса после /folders/ (пусто — корень)"), "")
		s.cfg.GoogleDocs.ProjectDocs = s.confirm(i18n.Tr("Вести отдельный документ на каждый проект?"), true)
	}
	if s.confirm(i18n.Tr("Публиковать в Slack?"), false) {
		s.cfg.Slack.Enabled = true
		if tok := s.askSecret(i18n.Tr("Токен бота Slack (xoxb-…)"), "api.slack.com/apps"); tok != "" {
			s.env["SLACK_BOT_TOKEN"] = tok
		}
		s.cfg.Slack.Channel = s.ask(i18n.Tr("Канал"), i18n.Tr("#созвоны"))
		s.cfg.Slack.DMOwners = s.confirm(i18n.Tr("Писать в личку тем, на ком задача?"), true)
	}
	return nil
}

// defaultPanelAddr — адрес панели, который мастер подставляет в скобки.
const defaultPanelAddr = "127.0.0.1:8422"

func (s *setupState) askPanel(ctx context.Context) error {
	section(i18n.Tr("Панель"))
	fmt.Println(dim(i18n.Tr("  Веб-панель: архив, поиск, проекты, кнопки. Необязательна — то же есть")))
	fmt.Println(dim(i18n.Tr("  в steno ui и в строке меню. Меняется потом: steno panel on|off.")))
	addr := s.ask(i18n.Tr("Адрес"), defaultPanelAddr)
	pass := s.askSecret(i18n.Tr("Пароль (общий на команду)"), "")
	if pass == "" {
		pass = randomPassword()
		fmt.Println(ok(i18n.Tr("сгенерировал: ") + pass))
	}
	s.setPanel(addr, pass)
	// Адрес и пароль записаны в любом случае — чтобы `steno panel on` потом
	// работал без мастера. А поднимать ли её с сервисом — спрашиваем: раньше
	// панель включалась молча, и человек получал порт, о котором не просил.
	s.cfg.Panel.Enabled = s.confirm(i18n.Tr("Поднимать панель вместе с сервисом?"), true)
	if !s.cfg.Panel.Enabled {
		fmt.Println(dim(i18n.Tr("  выключена; включить потом:  steno panel on")))
		return nil
	}
	if !strings.HasPrefix(s.cfg.Panel.Addr, "127.0.0.1") &&
		!strings.HasPrefix(s.cfg.Panel.Addr, "localhost") {
		s.cfg.Panel.Secure = s.confirm(i18n.Tr("Панель за HTTPS?"), true)
		if !s.cfg.Panel.Secure {
			fmt.Println(warn(i18n.Tr("панель смотрит наружу без TLS — пароль и cookie пойдут открытым текстом")))
		}
	}
	return nil
}

// askAgent — отдавать ли задачи агенту. Два вопроса, и оба по умолчанию
// «нет»: ТЗ стоит денег на каждый разбор, а исполнение — это право писать
// файлы на этой машине. Тот, кто ставит steno ради заметок, должен получить
// «нет» пустым Enter'ом, а не разбираться, что такое рабочая копия.
func (s *setupState) askAgent(ctx context.Context) error {
	section(i18n.Tr("Задачи — агенту"))
	fmt.Println(dim(i18n.Tr("Задачу с созвона steno умеет превратить в ТЗ по репозиторию проекта, а ТЗ —")))
	fmt.Println(dim(i18n.Tr("отдать Claude Code или Codex: отдельная рабочая копия, своя ветка, никогда push.")))
	fmt.Println(dim(i18n.Tr("Оба выключателя меняются потом: steno agent on|off, steno agent auto on|off.")))
	s.cfg.Agent.AutoSpec = s.confirm(i18n.Tr("Собирать ТЗ самому после каждого разбора? (чтение кода и запрос к модели)"), false)
	s.cfg.Agent.Enabled = s.confirm(i18n.Tr("Разрешить агенту работать в репозитории на этой машине?"), false)
	if s.cfg.Agent.Enabled {
		// Исполнитель — тот же, кем платят за разбор; если это не агент,
		// сказать об этом здесь, а не при первом нажатии в панели.
		r := spec.Check(s.cfg, s.cfg.Agent, false)
		if r.Executor == "" {
			fmt.Println(warn(r.Why))
		} else {
			fmt.Println(ok(i18n.Tr("исполняет ") + r.Executor +
				dim(i18n.Tr("; ветки ")+r.BranchPrefix+i18n.Tr("…, рабочие копии в ")+
					filepath.Join(s.cfg.DataDir, "agent"))))
		}
	}
	return nil
}

func (s *setupState) setPanel(addr, pass string) {
	s.cfg.Panel.Enabled = true
	s.cfg.Panel.Addr = addr
	s.env["STENO_PANEL_PASSWORD"] = pass
}

// --- запись ------------------------------------------------------------------

func (s *setupState) write(configPath string) error {
	section(i18n.Tr("Готово"))
	w, err := s.save(configPath)
	if err != nil {
		return err
	}
	if w.env != "" {
		fmt.Println(ok(w.env + "  " + dim(i18n.Tr("права 0600, ")+strconv.Itoa(w.secrets)+i18n.Tr(" секретов"))))
	}
	fmt.Println(ok(w.config))
	if w.gitignore != "" {
		fmt.Println(ok(w.gitignore + "  " + dim(i18n.Tr("чтобы .env не уехал в репозиторий"))))
	}

	fmt.Println()
	fmt.Println(bold(i18n.Tr("Дальше:")))
	// Путь целиком, а не имя файла: мастер мог завести каталог не там, откуда
	// его запустили, и «steno doctor -c steno.json» из другого места не сработает.
	fmt.Printf("  cd %s\n", s.dir)
	fmt.Printf("  steno doctor   %s\n", dim(i18n.Tr("проверить, что всё на месте")))
	fmt.Printf("  steno serve    %s\n", dim(i18n.Tr("запустить")))
	if s.cfg.Panel.Enabled {
		fmt.Printf("  http://%s%s\n", s.cfg.Panel.Addr, dim(i18n.Tr("  — панель")))
	}
	fmt.Println()
	fmt.Println(dim(i18n.Tr("Проверить на живом созвоне, ничего больше не настраивая:")))
	fmt.Printf("  steno join --no-followup --captions %s\n",
		dim(i18n.Tr("https://meet.google.com/… или https://meet.jit.si/…")))
	return nil
}

// written — что save положил на диск; пустая строка — файл не писался.
type written struct {
	env       string
	secrets   int
	config    string
	gitignore string
}

// save записывает всё, что мастер собрал: .env, конфиг, указатель на него и
// .gitignore. Без единой строки на экран — этим же путём идёт quietSetup.
func (s *setupState) save(configPath string) (written, error) {
	var w written
	// Секреты кладём отдельным файлом с правами 0600 и не пускаем в конфиг:
	// конфиг хочется держать в репозитории, а токены — нет.
	// Общий секрет для вызова по ссылке. Заводим всегда, а не по вопросу: канал
	// включают потом в панели, а секретов в панели нет и не будет — и человек
	// остался бы с портом, который отказывается подниматься.
	if s.env["STENO_HTTP_TOKEN"] == "" {
		s.env["STENO_HTTP_TOKEN"] = randomPassword() + randomPassword()
	}
	envPath := filepath.Join(s.dir, ".env")
	if len(s.env) > 0 {
		var b strings.Builder
		b.WriteString(i18n.Tr("# Секреты steno. Файл читается при запуске.\n"))
		b.WriteString(i18n.Tr("# Не клади его в репозиторий: тут ключи, а не настройки.\n\n"))
		for _, k := range core.SortedKeys(s.env) {
			fmt.Fprintf(&b, "%s=%s\n", k, s.env[k])
		}
		if err := os.WriteFile(envPath, []byte(b.String()), 0o600); err != nil {
			return w, fmt.Errorf(i18n.Tr("не записался %s: %w"), envPath, err)
		}
		w.env, w.secrets = envPath, len(s.env)
	}

	// Каталог мог поменяться на шаге «Где хранить»: конфиг кладём туда же, где
	// данные, а не туда, откуда запустили мастер.
	if !s.dirChosen {
		configPath = filepath.Join(s.dir, filepath.Base(configPath))
	}
	raw, err := marshalConfig(s.cfg)
	if err != nil {
		return w, err
	}
	if err := os.WriteFile(configPath, raw, 0o644); err != nil {
		return w, err
	}
	w.config = configPath
	// Запоминаем, где настройка: иначе команды, набранные из другого каталога,
	// берут умолчания и ведут себя так, будто настройки не было.
	core.RememberConfigPath(configPath)

	// .gitignore рядом с секретами — чтобы они не уехали в первый же коммит.
	gi := filepath.Join(s.dir, ".gitignore")
	if _, err := os.Stat(gi); os.IsNotExist(err) && len(s.env) > 0 {
		_ = os.WriteFile(gi, []byte(".env\ndata/\n"), 0o644)
		w.gitignore = gi
	}
	return w, nil
}

// --- ввод --------------------------------------------------------------------

func (s *setupState) ask(question, def string) string {
	for {
		if def != "" {
			fmt.Printf("  %s [%s]: ", question, dim(def))
		} else {
			fmt.Printf("  %s: ", question)
		}
		line, err := s.in.ReadString('\n')
		if err != nil && strings.TrimSpace(line) == "" {
			return def
		}
		if v := strings.TrimSpace(line); v != "" {
			return v
		}
		return def
	}
}

// askSecret не показывает ввод: пароли и ключи не должны оставаться в истории
// терминала и на плече у соседа.
func (s *setupState) askSecret(question, where string) string {
	if where != "" {
		fmt.Printf("  %s %s\n", question, dim("— "+where))
		fmt.Print("  ")
	} else {
		fmt.Printf("  %s: ", question)
	}
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		line, _ := s.in.ReadString('\n')
		return strings.TrimSpace(line)
	}
	raw, err := term.ReadPassword(fd)
	fmt.Println()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func (s *setupState) confirm(question string, def bool) bool {
	hint := "y/N"
	if def {
		hint = "Y/n"
	}
	for {
		fmt.Printf("  %s [%s]: ", question, dim(hint))
		line, _ := s.in.ReadString('\n')
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "":
			return def
		case "y", "yes", "д", "да":
			return true
		case "n", "no", "н", "нет":
			return false
		}
	}
}

func (s *setupState) choose(question string, options []string, def int) int {
	fmt.Printf("  %s\n", question)
	for i, o := range options {
		fmt.Printf("    %d) %s\n", i+1, o)
	}
	for {
		fmt.Printf(i18n.Tr("  Номер [%s]: "), dim(strconv.Itoa(def+1)))
		line, _ := s.in.ReadString('\n')
		v := strings.TrimSpace(line)
		if v == "" {
			return def
		}
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= len(options) {
			return n - 1
		}
	}
}

// askGoogleAccess спрашивает, каким путём steno попадёт в Google, и спрашивает
// один раз: календарь, почта и Google Docs ходят туда одним и тем же способом.
//
// Путей два, и выбор между ними не про удобство, а про то, чей это Google.
// Ключ service-account читает календари всех сотрудников, никого не спрашивая,
// — но за ним стоят проект в Google Cloud, три включённых API и админка домена.
// У человека, который ставит steno себе, ничего этого нет и быть не может, и
// упереться в это на первом же шаге значит не поставить steno вовсе.
func (s *setupState) askGoogleAccess() {
	if s.cfg.Google.ClientID != "" || s.cfg.GoogleDocs.CredentialsFile != "" {
		return // уже спрашивали
	}
	i := s.choose(i18n.Tr("Как steno попадёт в Google?"), []string{
		i18n.Tr("По кнопке в панели ") +
			dim(i18n.Tr("человек соглашается один раз; steno видит ровно то, что видит он")),
		i18n.Tr("Ключом организации ") +
			dim(i18n.Tr("файл service-account: видит календари всех, нужен свой домен и админка")),
	}, 0)
	if i == 1 {
		s.cfg.GoogleDocs.CredentialsFile = s.askGoogleKey()
		return
	}
	// Адрес страницы, а не путь по меню: меню Google переставляет пункты
	// чаще, чем меняет адреса, и человек, который ищет «Credentials» глазами,
	// натыкается на переименованный раздел.
	fmt.Println(dim(i18n.Tr("  Открой https://console.cloud.google.com/apis/credentials")))
	fmt.Println(dim(i18n.Tr("  → Create credentials → OAuth client ID → тип Desktop app.")))
	fmt.Println(dim(i18n.Tr("  Google покажет две строки — скопируй их сюда.")))
	s.cfg.Google.ClientID = s.ask("Client ID", "")
	if sec := s.askSecret("Client secret", ""); sec != "" {
		s.env["GOOGLE_CLIENT_SECRET"] = sec
	}
	if s.cfg.Google.ClientID == "" {
		fmt.Println(warn(i18n.Tr("без этого кнопка в панели не появится")))
		return
	}
	fmt.Println(ok(i18n.Tr("осталось нажать «Подключить Google» в настройках панели")))
}

func (s *setupState) askGoogleKey() string {
	fmt.Println(dim(i18n.Tr("  Нужен ключ service-account с domain-wide delegation.")))
	fmt.Println(dim(i18n.Tr("  Консоль Google → IAM → сервисные аккаунты → ключи → создать JSON.")))
	for {
		p := s.ask(i18n.Tr("Путь к файлу ключа"), "")
		if p == "" {
			fmt.Println(warn(i18n.Tr("без него календарь, почта и Google Docs не заработают")))
			return ""
		}
		p = brain.ExpandHome(p)
		if c := checkGoogleKey(i18n.Tr("ключ"), p); c.state == "ok" {
			fmt.Println(ok(c.note))
			return p
		} else {
			fmt.Println(warn(c.note))
			for _, f := range c.fix {
				fmt.Println(dim("  " + f))
			}
		}
	}
}

// --- оформление ---------------------------------------------------------------
//
// Цвета включаются, только если вывод идёт в терминал: в логе systemd или в
// пайпе escape-последовательности только мешают.

var useColor = term.IsTerminal(int(os.Stdout.Fd())) && os.Getenv("NO_COLOR") == ""

func paint(code, s string) string {
	if !useColor {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func bold(s string) string { return paint("1", s) }
func dim(s string) string  { return paint("2", s) }

func title(s string) {
	fmt.Println()
	fmt.Println(bold(s))
	fmt.Println(dim(strings.Repeat("─", len([]rune(s)))))
}

// Шаги нумеруются на ходу. Человек, отвечающий на седьмой вопрос подряд, не
// знает, седьмой он из восьми или из тридцати, и это единственное, что отличает
// «сейчас закончим» от «конца не видно».
var (
	stepNo    int
	stepTotal int
)

func section(s string) {
	stepNo++
	fmt.Println()
	if stepTotal > 0 {
		fmt.Printf("%s  %s\n", bold("· "+s),
			dim(fmt.Sprintf(i18n.Tr("шаг %d из %d"), stepNo, stepTotal)))
		return
	}
	fmt.Println(bold("· " + s))
}

func ok(s string) string   { return "  " + paint("32", "✓") + " " + s }
func warn(s string) string { return "  " + paint("33", "!") + " " + s }

func randomPassword() string {
	const alphabet = "abcdefghijkmnpqrstuvwxyz23456789"
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

// loadDotEnv подхватывает секреты из файла рядом с конфигом. Просить человека
// каждый раз экспортировать шесть переменных — верный способ получить сервис,
// запущенный без половины из них.
func loadDotEnv(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		v = strings.Trim(v, `"'`)
		// Уже заданное окружение главнее файла: в проде переменные приходят от
		// systemd или docker, и файл не должен их перебивать.
		if _, exists := os.LookupEnv(k); !exists {
			_ = os.Setenv(k, v)
		}
	}
	return nil
}

// marshalConfig печатает конфиг человекочитаемо: его будут править руками.
func marshalConfig(c *core.Config) ([]byte, error) {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
