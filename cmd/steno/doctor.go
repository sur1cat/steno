package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/sur1cat/steno/internal/audio"
	"github.com/sur1cat/steno/internal/bot"
	"github.com/sur1cat/steno/internal/brain"
	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/google"
	"github.com/sur1cat/steno/internal/i18n"
	"github.com/sur1cat/steno/internal/spec"
)

// steno doctor — предполётная проверка. Первый запуск сервиса упирается в
// десяток мелочей подряд: нет образа, пуста переменная, не читается ключ.
// Ловить их по одной, каждый раз заново заводя созвон, — это часы.

type check struct {
	name string
	// state: ok | fail | off
	state string
	note  string
	fix   []string
	// Без этого нельзя даже записать созвон.
	blocking bool
	// Чем это чинится прямо здесь, если человек у терминала: скачать модель,
	// принять ключ. Пусто — только словами. Doctor, который видит, что модели
	// нет, и печатает curl, вместо того чтобы спросить «скачать?», — это
	// констатация там, где нужна помощь.
	offer func(context.Context) error
}

func cmdDoctor(ctx context.Context, args []string) error {
	fs := newFlagSet("doctor")
	cfgPath := setupFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, st, err := open(*cfgPath)
	if err != nil {
		return err
	}
	defer st.Close()

	var cs []check
	cs = append(cs, checkData(cfg))
	cs = append(cs, checkBot(cfg))
	cs = append(cs, checkPlatforms(cfg))
	cs = append(cs, checkTranscribe(cfg))
	cs = append(cs, checkBrain(cfg, core.ResolveConfigPath(*cfgPath)))
	cs = append(cs, checkGoogle(cfg))
	cs = append(cs, checkSources(cfg)...)
	cs = append(cs, checkTargets(cfg)...)
	cs = append(cs, checkPanel(cfg))
	cs = append(cs, checkAgent(cfg))

	// Отсутствие конфига надо называть отсутствием. Раньше в шапке печаталось
	// «конфиг steno.json» и тогда, когда файла не было вовсе: doctor показывал
	// умолчания, человек читал их как свои настройки и не понимал, почему панель
	// выключена, модель не та, а адаптер не находится.
	path := core.ResolveConfigPath(*cfgPath)
	if _, err := os.Stat(path); err != nil {
		fmt.Printf("steno doctor · %s\n", paint("33", i18n.Tr("конфига ")+path+i18n.Tr(" нет — показываю умолчания")))
		fmt.Printf("%s\n", dim(i18n.Tr("настроить одной командой:  steno setup")))
		fmt.Printf("%s\n\n", dim(i18n.Tr("для заметок с микрофона хватит и `steno note` — минимальную настройку заведёт сама")))
	} else {
		fmt.Printf(i18n.Tr("steno doctor · конфиг %s\n\n"), path)
	}
	blocked, broken := false, false
	for _, c := range cs {
		mark := map[string]string{"ok": "✓", "fail": "✗", "off": "—"}[c.state]
		fmt.Printf("  %s %-16s %s\n", mark, c.name, c.note)
		for _, f := range c.fix {
			fmt.Printf("      %s\n", f)
		}
		if c.state == "fail" {
			broken = true
			if c.blocking {
				blocked = true
			}
		}
	}

	// То, что чинится на месте, — чиним, если есть кому ответить. В трубе и в
	// скрипте doctor остаётся отчётом: спросить некого, а качать гигабайт без
	// спроса нельзя.
	if stdinTTY() {
		for _, c := range cs {
			if c.state != "fail" || c.offer == nil {
				continue
			}
			fmt.Println()
			if err := c.offer(ctx); err != nil {
				fmt.Println(warn(err.Error()))
				continue
			}
			fmt.Println(ok(c.name + i18n.Tr(": готово — проверь ещё раз: steno doctor")))
		}
	}

	fmt.Println()
	if blocked {
		fmt.Println(i18n.Tr("Записать созвон пока нельзя — см. отмеченное выше."))
		return errors.New(i18n.Tr("проверка не пройдена"))
	}
	if broken {
		fmt.Println(i18n.Tr("Записать созвон можно. Часть шагов после записи не отработает — см. выше."))
		return nil
	}
	fmt.Println(i18n.Tr("Всё готово."))
	return nil
}

func checkData(cfg *core.Config) check {
	probe := filepath.Join(cfg.DataDir, ".doctor")
	if err := os.WriteFile(probe, []byte("x"), 0o644); err != nil {
		return check{name: i18n.Tr("данные"), state: "fail",
			note: cfg.DataDir + i18n.Tr(" — нет записи"),
			fix: []string{
				"→ " + err.Error(),
			}, blocking: true}
	}
	os.Remove(probe)
	return check{name: i18n.Tr("данные"), state: "ok", note: cfg.DataDir, blocking: true}
}

func checkBot(cfg *core.Config) check {
	if cfg.Bot.Local {
		var missing []string
		for _, bin := range []string{"chromium", "ffmpeg", "pactl"} {
			if _, err := exec.LookPath(bin); err != nil {
				missing = append(missing, bin)
			}
		}
		if _, err := exec.LookPath("google-chrome"); err == nil && len(missing) > 0 {
			missing = removeString(missing, "chromium")
		}
		if len(missing) > 0 {
			return check{name: i18n.Tr("бот (local)"), state: "fail",
				note: i18n.Tr("не хватает: ") + strings.Join(missing, ", "),
				fix:  []string{i18n.Tr("→ либо поставь их, либо убери bot.local — тогда бот пойдёт в docker")}, blocking: true}
		}
		return check{name: i18n.Tr("бот (local)"), state: "ok",
			note: i18n.Tr("chromium, ffmpeg и pulseaudio на месте"), blocking: true}
	}

	if _, err := exec.LookPath("docker"); err != nil {
		return check{name: i18n.Tr("бот (docker)"), state: "fail",
			note: i18n.Tr("docker не найден"),
			fix:  []string{i18n.Tr("→ поставь Docker Desktop или задай bot.local = true")}, blocking: true}
	}
	// Через `docker images -q`: inspect по имени не находит образ, собранный
	// BuildKit в новом хранилище Docker Desktop. См. haveImage в main.go.
	out, err := exec.Command("docker", "images", "-q", cfg.Bot.Image).Output()
	if err == nil && strings.TrimSpace(string(out)) == "" {
		err = errors.New(i18n.Tr("нет такого образа"))
	}
	if err != nil {
		// Отсутствие образа из реестра — не поломка: steno скачает его сам перед
		// первым созвоном. Пугать этим человека, который только что поставил
		// steno, незачем — раньше здесь стояло «✗» и требование склонировать
		// репозиторий и собрать гигабайт руками.
		if strings.Contains(cfg.Bot.Image, "/") {
			return check{name: i18n.Tr("бот (docker)"), state: "ok",
				note: i18n.Tr("образа ") + cfg.Bot.Image + i18n.Tr(" нет — скачается перед первым созвоном"),
				fix:  []string{i18n.Tr("→ можно заранее:  docker pull ") + cfg.Bot.Image}}
		}
		return check{name: i18n.Tr("бот (docker)"), state: "fail",
			note: i18n.Tr("нет образа ") + cfg.Bot.Image,
			fix:  []string{"→ make bot-image"}, blocking: true}
	}
	_ = out
	// Запись, разбор страницы и снятие субтитров живут внутри образа, а не в
	// этом бинарнике. Обновив steno, легко остаться со вчерашним ботом и
	// полдня чинить то, что уже починено: человек ставит новую версию, идёт на
	// созвон и получает прежнее поведение. Молчать об этом нельзя.
	if age := imageAge(strings.TrimSpace(string(out))); age != "" {
		return check{name: i18n.Tr("бот (docker)"), state: "fail",
			note: i18n.Tr("образ ") + cfg.Bot.Image + i18n.Tr(" старше самого steno на ") + age,
			fix:  fixes(cfg)}
	}
	return check{name: i18n.Tr("бот (docker)"), state: "ok",
		note: i18n.Tr("образ ") + cfg.Bot.Image + i18n.Tr(" на месте"), blocking: true}
}

func checkTranscribe(cfg *core.Config) check {
	if cfg.Transcribe.Source == "captions" {
		return check{name: i18n.Tr("расшифровка"), state: "ok",
			note: i18n.Tr("из субтитров площадки — ставить ничего не нужно"),
			fix:  []string{i18n.Tr("качество ниже whisper; для продакшена смени transcribe.source на command")}}
	}
	if len(cfg.Transcribe.Cmd) == 0 {
		return check{name: i18n.Tr("расшифровка"), state: "fail",
			note: i18n.Tr("не задан transcribe.cmd"),
			fix:  []string{i18n.Tr(`→ или поставь "source": "captions", чтобы взять текст из субтитров площадки`)}}
	}
	// Через AdapterPath: путь в конфиге мог указывать в каталог версии, которую
	// снёс brew upgrade, а адаптер при этом лежит рядом с новой.
	bin := core.AdapterPath(cfg.Transcribe.Cmd[0])
	if _, err := exec.LookPath(bin); err != nil {
		return check{name: i18n.Tr("расшифровка"), state: "fail", note: i18n.Tr("не запускается ") + bin,
			fix: []string{
				"→ " + err.Error(),
				i18n.Tr(`→ или поставь "source": "captions" — текст возьмётся из субтитров площадки`),
			}}
	}
	// У whisper.cpp два условия, которых у остальных адаптеров нет: бинарник и
	// модель на диске. Их doctor смотрит сам, до прогона адаптера, — чтобы
	// сказать теми же словами, что и `steno note`, и предложить то же самое.
	if w := inspectWhisper(cfg); w.adapter {
		if len(w.missing) > 0 {
			lines := strings.Split(noRecognizer(w.missing).Error(), "\n")
			return check{name: i18n.Tr("расшифровка"), state: "fail", note: lines[0], fix: lines[1:]}
		}
		if w.model == "" {
			return check{name: i18n.Tr("расшифровка"), state: "fail",
				note: i18n.Tr("речевой модели нет в ") + w.dir,
				fix: []string{
					i18n.Tr("→ скачать спросит и `steno note`, и doctor в терминале — один Enter"),
					i18n.Tr("→ или руками: ") + whisperModelsURL + whisperModelName() + ".bin",
				},
				offer: func(ctx context.Context) error { return ensureTranscriber(ctx, cfg) }}
		}
	}
	// Найти файл мало. Адаптеру нужна модель на полгигабайта, и без неё он
	// падает — а doctor до этого рапортовал «ok». Поэтому прогоняем его
	// по-настоящему на полусекунде тишины: это проверяет и бинарник, и модель,
	// и то, что на выходе получается обещанный JSON.
	out, err := probeTranscriber(cfg)
	if err != nil {
		// Показываем хвост ошибки, а не начало: адаптер пишет туда, чего ему
		// не хватило и какой командой это ставится, — а начало занято
		// служебным «exit status 1».
		fix := []string{}
		for _, l := range core.LastLines(err.Error(), 5) {
			fix = append(fix, "→ "+l)
		}
		fix = append(fix, i18n.Tr(`→ или поставь "source": "captions" — текст возьмётся из субтитров площадки`))
		return check{name: i18n.Tr("расшифровка"), state: "fail", note: i18n.Tr("адаптер не отработал"), fix: fix}
	}
	return check{name: i18n.Tr("расшифровка"), state: "ok", note: out}
}

// probeTranscriber прогоняет адаптер на полусекунде тишины и проверяет, что он
// вернул обещанный JSON. Тишина — самый безобидный вход, а проверяется весь
// путь целиком.
func probeTranscriber(cfg *core.Config) (string, error) {
	dir, err := os.MkdirTemp("", "steno-doctor")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	wav := filepath.Join(dir, "silence.wav")
	if err := os.WriteFile(wav, audio.SilentWAV(time.Second/2), 0o644); err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	probe := *cfg
	probe.Transcribe.Timeout = core.Duration(3 * time.Minute)
	_, notes, err := audio.RunTranscriber(ctx, &probe, wav, nil)
	if err != nil {
		return "", err
	}
	// Адаптер сообщает, какой моделью работал. Мелкая модель — рабочая, но на
	// русском созвоне выдаёт кашу, из которой Claude уверенно сочинит смысл,
	// которого не было. Это опаснее пустоты, и молчать об этом нельзя.
	if m := modelRe.FindStringSubmatch(notes); m != nil {
		if smallModelRe.MatchString(m[1]) {
			return i18n.Tr("работает на ") + m[1] + i18n.Tr(" — для русских созвонов мало, нужна large-v3"), nil
		}
		return i18n.Tr("адаптер отработал, модель ") + m[1], nil
	}
	return i18n.Tr("адаптер отработал на пробной записи"), nil
}

var (
	modelRe      = regexp.MustCompile(`(?:модель|model)\s+(\S+)`)
	smallModelRe = regexp.MustCompile(`(?i)tiny|base|small`)
)

// checkBrain — кем steno разбирает созвон и доступен ли он. Провайдеров стало
// больше одного, поэтому строка начинается с имени выбранного: увидеть здесь
// «Claude» на установке, работающей через Groq, значит искать поломку не там.
func checkBrain(cfg *core.Config, cfgPath string) check {
	name := i18n.Tr("разбор")
	title := brain.ProviderTitle(cfg)
	_, how, err := brain.ResolveVia(cfg)
	if err != nil {
		// Причина отказа уже написана человеческими словами тем, кто её знает,
		// — ResolveVia. Повторять её своими здесь значит однажды разойтись.
		var fix []string
		for _, l := range core.LastLines(err.Error(), 4) {
			fix = append(fix, "→ "+l)
		}
		c := check{name: name, state: "fail", note: title + i18n.Tr(" — нет доступа"), fix: fix}
		if cfg.BrainProvider() == core.ProviderClaude {
			c.fix = append(c.fix, i18n.Tr("→ или смени провайдера: `steno setup`, «Чем платить за follow-up»"))
			// Пока выбор не сделан, ключ можно просто вставить — как в
			// `steno note`. Названный провайдер чинится настройкой, не здесь.
			if cfg.Claude.Via == "auto" || cfg.Claude.Via == "api" {
				c.offer = func(context.Context) error { return ensureBrain(cfg, cfgPath) }
			}
		}
		return c
	}
	note := title + " · " + core.OrDash(cfg.BrainModel())
	if cfg.BrainProvider() == core.ProviderClaude {
		note += ", effort " + core.OrDash(cfg.Claude.Effort)
	}
	note += " · " + how

	var fix []string
	// Настройка может быть безупречной, а модель на этой же машине — не
	// запущена. Узнавать об этом на первом созвоне, когда час разговора уже
	// записан, поздно; doctor для того и есть, чтобы сходить туда заранее.
	if cfg.BrainProvider() == core.ProviderOpenAI {
		if ok, why := brain.OpenAIReach(context.Background(), cfg); !ok {
			return check{name: name, state: "fail", note: title + i18n.Tr(" не отвечает"), fix: []string{
				"→ " + why,
				i18n.Tr("→ адрес сейчас ") + cfg.OpenAIBaseURL(),
			}}
		}
	}
	// Скрипт проверяем запуском не здесь: он ходит к настоящей модели и может
	// стоить денег и минут. Что он на месте и запускается, уже сказала
	// ResolveVia — этого для предполётной проверки довольно.
	fix = append(fix, brainPriceFix(cfg)...)
	if cfg.BrainProvider() == core.ProviderCommand {
		fix = append(fix, i18n.Tr("→ договор со скриптом описан в adapters/ollama.sh"))
		if _, ok := cfg.LLM.Prices[cfg.BrainModel()]; !ok {
			fix = append(fix, i18n.Tr("→ деньги считает сам скрипт полем usd; молчит — `steno cost` покажет токены без денег"))
		}
	}
	return check{name: name, state: "ok", note: note, fix: fix}
}

// brainPriceFix — предупреждение про неизвестную цену. Отдельной функцией,
// чтобы его можно было проверить, не поднимая чужой сервер: у steno нет цен ни
// одного провайдера, кроме Anthropic, и молчать об этом нельзя — иначе `steno
// cost` покажет ноль там, где счёт придёт.
//
// У модели на этой же машине предупреждать не о чем: там ноль — правда.
func brainPriceFix(cfg *core.Config) []string {
	if cfg.BrainProvider() != core.ProviderOpenAI || cfg.OpenAILocal() {
		return nil
	}
	if _, ok := cfg.Brain.OpenAI.Prices[cfg.BrainModel()]; ok {
		return nil
	}
	return []string{
		i18n.Tr("→ цена этой модели неизвестна — `steno cost` покажет токены без денег"),
		i18n.Tr("→ чтобы считались деньги, впиши её в brain.openai.prices"),
	}
}

// checkGoogle — одной строкой: чем steno входит в Google и от чьего имени.
// Дальше по списку календарь, почта и Docs повторяют это каждый по-своему, но
// причина у них общая, и искать её в трёх строках не надо.
func checkGoogle(cfg *core.Config) check {
	key := google.CredentialsFile(cfg.Calendar.CredentialsFile, cfg.GoogleDocs.CredentialsFile)
	if t, err := google.LoadGoogleToken(cfg); err == nil {
		note := i18n.Tr("подключён как ") + t.Account
		var fix []string
		if missing := google.MissingScopes(t.Scopes, google.GoogleScopes); len(missing) > 0 {
			note += i18n.Tr("; доступ выдан не весь")
			fix = append(fix, i18n.Tr("→ не хватает доступа к ")+strings.Join(google.HumanScopes(missing), i18n.Tr(" и ")))
			fix = append(fix, i18n.Tr("→ подключи ещё раз в настройках панели"))
		}
		if key != "" {
			fix = append(fix, i18n.Tr("→ ключ организации при этом не используется: ")+
				i18n.Tr("подключённый аккаунт главнее"))
		}
		return check{name: "Google", state: "ok", note: note, fix: fix}
	}
	if key != "" {
		return check{name: "Google", state: "ok", note: i18n.Tr("доступ ключом организации")}
	}
	if !cfg.Calendar.Enabled && !cfg.Gmail.Enabled && !cfg.GoogleDocs.Enabled {
		return check{name: "Google", state: "off", note: i18n.Tr("не подключён, и никому не нужен")}
	}
	fix := []string{i18n.Tr("→ панель, «Настройки» → «Подключить Google»")}
	if !google.GoogleOAuthReady(cfg) {
		fix = append(fix, i18n.Tr("→ кнопки там пока нет: `steno setup` спросит client id и секрет"))
	}
	return check{name: "Google", state: "fail", note: i18n.Tr("не подключён"), fix: fix}
}

// checkGoogleAccess — доступ для одного канала: сначала подключённый аккаунт,
// потом ключ организации. Проверять только ключ нельзя с тех пор, как появилась
// кнопка: doctor рапортовал бы «нет ключа» на рабочей установке.
//
// subject — от чьего имени канал ходит. По кнопке steno умеет работать только
// от подключившегося, и несовпадение здесь значит, что канал не заработает
// вовсе: сказать об этом надо здесь, а не молчать до первого созвона.
func checkGoogleAccess(cfg *core.Config, name, keyFile, subject string) check {
	if t, err := google.LoadGoogleToken(cfg); err == nil {
		if subject != "" && !strings.EqualFold(subject, t.Account) {
			return check{name: name, state: "fail", note: i18n.Tr("нужен доступ от имени ") + subject, fix: []string{
				i18n.Tr("→ steno подключён как ") + t.Account + i18n.Tr(" и работает только от него"),
				i18n.Tr("→ либо поправь адрес, либо переходи на ключ организации"),
			}}
		}
		return check{name: name, state: "ok", note: i18n.Tr("от имени ") + t.Account}
	}
	return checkGoogleKey(name, keyFile)
}

func checkSources(cfg *core.Config) []check {
	var cs []check
	if cfg.Calendar.Enabled {
		c := checkGoogleAccess(cfg, i18n.Tr("календарь"),
			google.CredentialsFile(cfg.Calendar.CredentialsFile, cfg.GoogleDocs.CredentialsFile), "")
		if c.state == "ok" {
			switch {
			case len(cfg.Calendar.Calendars) == 0:
				c = check{name: i18n.Tr("календарь"), state: "fail",
					note: i18n.Tr("не указан ни один календарь"),
					fix:  []string{i18n.Tr("→ панель, «Каналы» → «Календарь» → «Чьи календари смотреть»")}}
			default:
				c.note = fmt.Sprintf(i18n.Tr("%d календарей, %s"), len(cfg.Calendar.Calendars), c.note)
				// По кнопке steno видит только свой календарь. Чужой в списке
				// будет молча пропускаться на каждом опросе — и человек узнает
				// об этом, когда бот не придёт на чужую встречу.
				if t, err := google.LoadGoogleToken(cfg); err == nil {
					if foreign := notMine(cfg.Calendar.Calendars, t.Account); len(foreign) > 0 {
						c.state = "fail"
						c.note = i18n.Tr("чужие календари так не открыть: ") + strings.Join(foreign, ", ")
						c.fix = []string{
							i18n.Tr("→ steno подключён как ") + t.Account + i18n.Tr(" и видит только его встречи"),
							i18n.Tr("→ либо убери чужие из списка, либо переходи на ключ организации"),
						}
					}
				}
			}
		}
		cs = append(cs, c)
	} else {
		cs = append(cs, check{name: i18n.Tr("календарь"), state: "off", note: i18n.Tr("выключен")})
	}

	if cfg.Gmail.Enabled {
		c := checkGoogleAccess(cfg, i18n.Tr("почта бота"),
			google.CredentialsFile(cfg.Gmail.CredentialsFile, cfg.GoogleDocs.CredentialsFile),
			cfg.Gmail.Account)
		if c.state == "ok" && cfg.Gmail.Account == "" {
			c = check{name: i18n.Tr("почта бота"), state: "fail",
				note: i18n.Tr("не указан ящик бота"),
				fix:  []string{i18n.Tr("→ панель, «Каналы» → «Почта бота» → «Ящик бота»")}}
		}
		cs = append(cs, c)
	} else {
		cs = append(cs, check{name: i18n.Tr("почта бота"), state: "off", note: i18n.Tr("выключена")})
	}

	if cfg.Telegram.Listen {
		if len(cfg.Telegram.AllowedChats) == 0 && cfg.Telegram.ChatID == "" {
			cs = append(cs, check{name: i18n.Tr("Telegram вход"), state: "fail",
				note: i18n.Tr("не задан ни chat_id, ни allowed_chats"),
				fix:  []string{i18n.Tr("→ без этого сервис не стартует: принимать ссылки от кого угодно нельзя")}})
		} else {
			cs = append(cs, checkEnv(i18n.Tr("Telegram вход"), cfg.Telegram.TokenEnv))
		}
	} else {
		cs = append(cs, check{name: i18n.Tr("Telegram вход"), state: "off", note: i18n.Tr("выключен")})
	}

	if cfg.HTTP.Enabled {
		c := checkEnv(i18n.Tr("HTTP вход"), cfg.HTTP.TokenEnv)
		if c.state == "ok" && os.Getenv(cfg.Slack.SigningSecretEnv) == "" {
			c.note += i18n.Tr("; слэш-команды Slack не принимаются")
			c.fix = append(c.fix, i18n.Tr("→ для них нужен ")+cfg.Slack.SigningSecretEnv)
		}
		cs = append(cs, c)
	} else {
		cs = append(cs, check{name: i18n.Tr("HTTP вход"), state: "off", note: i18n.Tr("выключен")})
	}
	return cs
}

func checkTargets(cfg *core.Config) []check {
	var cs []check
	if cfg.GoogleDocs.Enabled {
		c := checkGoogleAccess(cfg, "Google Docs", cfg.GoogleDocs.CredentialsFile,
			cfg.GoogleDocs.Subject)
		if c.state == "ok" && cfg.GoogleDocs.FolderID == "" {
			c.note += i18n.Tr("; папка не указана — документы лягут в корень Drive")
		}
		cs = append(cs, c)
	} else {
		cs = append(cs, check{name: "Google Docs", state: "off", note: i18n.Tr("выключен")})
	}
	if cfg.Slack.Enabled {
		c := checkEnv("Slack", cfg.Slack.TokenEnv)
		if c.state == "ok" && cfg.Slack.Channel == "" {
			c = check{name: "Slack", state: "fail", note: i18n.Tr("не указан канал")}
		}
		cs = append(cs, c)
	} else {
		cs = append(cs, check{name: "Slack", state: "off", note: i18n.Tr("выключен")})
	}
	if cfg.Telegram.Enabled {
		c := checkEnv("Telegram", cfg.Telegram.TokenEnv)
		if c.state == "ok" && cfg.Telegram.ChatID == "" {
			c = check{name: "Telegram", state: "fail", note: i18n.Tr("не указан chat_id")}
		}
		cs = append(cs, c)
	} else {
		cs = append(cs, check{name: "Telegram", state: "off", note: i18n.Tr("выключен")})
	}
	return cs
}

func checkPanel(cfg *core.Config) check {
	if !cfg.Panel.Enabled {
		return check{name: i18n.Tr("панель"), state: "off", note: i18n.Tr("выключена")}
	}
	c := checkEnv(i18n.Tr("панель"), cfg.Panel.PasswordEnv)
	if c.state == "ok" {
		c.note = i18n.Tr("слушает ") + cfg.Panel.Addr
	}
	return c
}

// checkAgent — есть ли кому исполнять ТЗ. Не блокирует: созвон записывается и
// без агента. Но включённое исполнение без исполнителя — поломка, о которой
// человек узнал бы только нажав кнопку, а doctor для того и есть.
func checkAgent(cfg *core.Config) check {
	name := i18n.Tr("агент")
	set := spec.SettingsFor(cfg)
	r := spec.Check(cfg, set, set.Enabled)
	auto := ""
	if set.AutoSpec {
		auto = i18n.Tr(", ТЗ собираются сами")
	}
	if !set.Enabled {
		note := i18n.Tr("выключен — ТЗ собираются, ветки не заводятся") + auto
		return check{name: name, state: "off", note: note,
			fix: []string{i18n.Tr("→ включить: steno agent on")}}
	}
	if r.Executor == "" || !r.Ready {
		var fix []string
		for _, l := range core.LastLines(r.Why, 3) {
			fix = append(fix, "→ "+l)
		}
		return check{name: name, state: "fail",
			note: i18n.Tr("включён, но исполнять некому") + auto, fix: fix}
	}
	return check{name: name, state: "ok",
		note: i18n.Trf("включён, исполняет %s, ветки %s…", r.Executor, r.BranchPrefix) + auto}
}

func checkEnv(name, env string) check {
	if env == "" {
		return check{name: name, state: "fail", note: i18n.Tr("не указано имя переменной с секретом")}
	}
	if strings.TrimSpace(os.Getenv(env)) == "" {
		return check{name: name, state: "fail",
			note: i18n.Tr("переменная ") + env + i18n.Tr(" пуста"),
			fix:  []string{"→ export " + env + "=..."}}
	}
	return check{name: name, state: "ok", note: env + i18n.Tr(" задана")}
}

// checkGoogleKey читает ключ service-account и проверяет, что это он и есть.
// Самая частая ошибка здесь — скачать не тот JSON из консоли Google.
func checkGoogleKey(name, path string) check {
	if path == "" {
		return check{name: name, state: "fail", note: i18n.Tr("нет доступа в Google"), fix: []string{
			i18n.Tr("→ панель, «Настройки» → «Подключить Google»"),
			i18n.Tr("→ либо ключ организации в google_docs.credentials_file"),
		}}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return check{name: name, state: "fail",
			note: i18n.Tr("ключ не читается"),
			fix:  []string{"→ " + err.Error()}}
	}
	var key struct {
		Type        string `json:"type"`
		ClientEmail string `json:"client_email"`
	}
	if err := json.Unmarshal(raw, &key); err != nil {
		return check{name: name, state: "fail", note: i18n.Tr("ключ — не JSON"), fix: []string{"→ " + err.Error()}}
	}
	if key.Type != "service_account" || key.ClientEmail == "" {
		return check{name: name, state: "fail",
			note: i18n.Tr("это не ключ service-account"),
			fix:  []string{i18n.Tr("→ в консоли Google: IAM → сервисные аккаунты → ключи → создать JSON")}}
	}
	return check{name: name, state: "ok", note: key.ClientEmail}
}

// notMine — чьи календари не принадлежат подключённому аккаунту. "primary" —
// свой по определению, как его ни зови.
func notMine(calendars []string, account string) []string {
	var out []string
	for _, c := range calendars {
		c = strings.TrimSpace(c)
		if c == "" || c == "primary" || strings.EqualFold(c, account) {
			continue
		}
		out = append(out, c)
	}
	return out
}

func removeString(list []string, s string) []string {
	out := list[:0]
	for _, v := range list {
		if v != s {
			out = append(out, v)
		}
	}
	return out
}

// checkPlatforms говорит, с какими площадками бот работает и чего ждать от
// субтитров. Субтитры — единственный источник имён говорящих, и узнавать об их
// отсутствии на живом созвоне поздно.
func checkPlatforms(cfg *core.Config) check {
	var names, noCaptions []string
	for _, p := range bot.AllPlatforms() {
		names = append(names, p.Title())
		if p.Captions() != bot.CaptionsBuiltIn {
			noCaptions = append(noCaptions, p.Title())
		}
	}
	note := i18n.Tr("умею: ") + strings.Join(names, ", ")
	if len(noCaptions) == 0 {
		return check{name: i18n.Tr("площадки"), state: "ok", note: note}
	}
	return check{name: i18n.Tr("площадки"), state: "ok", note: note, fix: []string{
		i18n.Tr("→ у ") + strings.Join(noCaptions, ", ") + i18n.Tr(" субтитры есть не всегда: на публичном ") +
			i18n.Tr("meet.jit.si они выключены на сервере (transcription.enabled=false)"),
		i18n.Tr("→ без них расшифровка идёт по звуку и без имён говорящих; что вышло на ") +
			i18n.Tr("самом деле, бот пишет строкой «субтитры: …» и полем captions в result.json"),
		i18n.Tr("→ свой сервер Jitsi добавляется в selectors.json, ключ jitsi.hosts"),
	}}
}

// fixes — чем чинить отставший образ. Готовый в реестре есть не для всякого
// имени: локально собранный «steno-bot:latest» тянуть неоткуда, и предлагать
// это значит послать человека за несуществующим.
func fixes(cfg *core.Config) []string {
	out := []string{i18n.Tr("→ пересобрать:  make bot-image")}
	if reg := core.DefaultBotImage(); strings.Contains(reg, "/") && reg != cfg.Bot.Image {
		out = append(out, i18n.Tr("→ или взять готовый:  docker pull ")+reg)
	}
	return append(out,
		i18n.Tr("запись и разбор страницы идут внутри образа — старый образ ведёт себя по-старому"))
}

// imageAge — насколько образ старше бинарника. Пустая строка, если не старше
// или если сравнить не с чем.
//
// Сравниваем со временем сборки самого steno: у собранного из исходников номера
// версии нет, а дата есть всегда. Порог в час — чтобы обычная разница между
// сборкой образа и бинарника в одном заходе не считалась расхождением.
func imageAge(imageID string) string {
	if imageID == "" {
		return ""
	}
	out, err := exec.Command("docker", "image", "inspect", imageID, "--format", "{{.Created}}").Output()
	if err != nil {
		return ""
	}
	built, err := time.Parse(time.RFC3339, strings.TrimSpace(string(out)))
	if err != nil {
		return ""
	}
	self, err := os.Executable()
	if err != nil {
		return ""
	}
	st, err := os.Stat(self)
	if err != nil {
		return ""
	}
	d := st.ModTime().Sub(built)
	if d < time.Hour {
		return ""
	}
	return sinceText(d)
}
