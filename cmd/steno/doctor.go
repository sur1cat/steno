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
}

func cmdDoctor(args []string) error {
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
	cs = append(cs, checkClaude(cfg))
	cs = append(cs, checkGoogle(cfg))
	cs = append(cs, checkSources(cfg)...)
	cs = append(cs, checkTargets(cfg)...)
	cs = append(cs, checkPanel(cfg))

	// Отсутствие конфига надо называть отсутствием. Раньше в шапке печаталось
	// «конфиг steno.json» и тогда, когда файла не было вовсе: doctor показывал
	// умолчания, человек читал их как свои настройки и не понимал, почему панель
	// выключена, модель не та, а адаптер не находится.
	path := core.ResolveConfigPath(*cfgPath)
	if _, err := os.Stat(path); err != nil {
		fmt.Printf("steno doctor · %s\n", paint("33", i18n.Tr("конфига ")+path+i18n.Tr(" нет — показываю умолчания")))
		fmt.Printf("%s\n\n", dim(i18n.Tr("настроить одной командой:  steno setup")))
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
		return check{i18n.Tr("данные"), "fail", cfg.DataDir + i18n.Tr(" — нет записи"), []string{
			"→ " + err.Error(),
		}, true}
	}
	os.Remove(probe)
	return check{i18n.Tr("данные"), "ok", cfg.DataDir, nil, true}
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
			return check{i18n.Tr("бот (local)"), "fail",
				i18n.Tr("не хватает: ") + strings.Join(missing, ", "),
				[]string{i18n.Tr("→ либо поставь их, либо убери bot.local — тогда бот пойдёт в docker")}, true}
		}
		return check{i18n.Tr("бот (local)"), "ok", i18n.Tr("chromium, ffmpeg и pulseaudio на месте"), nil, true}
	}

	if _, err := exec.LookPath("docker"); err != nil {
		return check{i18n.Tr("бот (docker)"), "fail", i18n.Tr("docker не найден"),
			[]string{i18n.Tr("→ поставь Docker Desktop или задай bot.local = true")}, true}
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
			return check{i18n.Tr("бот (docker)"), "ok",
				i18n.Tr("образа ") + cfg.Bot.Image + i18n.Tr(" нет — скачается перед первым созвоном"),
				[]string{i18n.Tr("→ можно заранее:  docker pull ") + cfg.Bot.Image}, false}
		}
		return check{i18n.Tr("бот (docker)"), "fail", i18n.Tr("нет образа ") + cfg.Bot.Image,
			[]string{"→ make bot-image"}, true}
	}
	_ = out
	// Запись, разбор страницы и снятие субтитров живут внутри образа, а не в
	// этом бинарнике. Обновив steno, легко остаться со вчерашним ботом и
	// полдня чинить то, что уже починено: человек ставит новую версию, идёт на
	// созвон и получает прежнее поведение. Молчать об этом нельзя.
	if age := imageAge(strings.TrimSpace(string(out))); age != "" {
		return check{i18n.Tr("бот (docker)"), "fail",
			i18n.Tr("образ ") + cfg.Bot.Image + i18n.Tr(" старше самого steno на ") + age,
			fixes(cfg), false}
	}
	return check{i18n.Tr("бот (docker)"), "ok", i18n.Tr("образ ") + cfg.Bot.Image + i18n.Tr(" на месте"), nil, true}
}

func checkTranscribe(cfg *core.Config) check {
	if cfg.Transcribe.Source == "captions" {
		return check{i18n.Tr("расшифровка"), "ok",
			i18n.Tr("из субтитров площадки — ставить ничего не нужно"),
			[]string{i18n.Tr("качество ниже whisper; для продакшена смени transcribe.source на command")}, false}
	}
	if len(cfg.Transcribe.Cmd) == 0 {
		return check{i18n.Tr("расшифровка"), "fail", i18n.Tr("не задан transcribe.cmd"),
			[]string{i18n.Tr(`→ или поставь "source": "captions", чтобы взять текст из субтитров площадки`)}, false}
	}
	// Через AdapterPath: путь в конфиге мог указывать в каталог версии, которую
	// снёс brew upgrade, а адаптер при этом лежит рядом с новой.
	bin := core.AdapterPath(cfg.Transcribe.Cmd[0])
	if _, err := exec.LookPath(bin); err != nil {
		return check{i18n.Tr("расшифровка"), "fail", i18n.Tr("не запускается ") + bin,
			[]string{
				"→ " + err.Error(),
				i18n.Tr(`→ или поставь "source": "captions" — текст возьмётся из субтитров площадки`),
			}, false}
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
		return check{i18n.Tr("расшифровка"), "fail", i18n.Tr("адаптер не отработал"), fix, false}
	}
	return check{i18n.Tr("расшифровка"), "ok", out, nil, false}
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
	_, notes, err := audio.RunTranscriber(ctx, &probe, wav)
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

func checkClaude(cfg *core.Config) check {
	_, how, err := brain.ResolveVia(cfg)
	if err != nil {
		return check{"Claude", "fail", i18n.Tr("нет доступа"),
			[]string{
				i18n.Tr("→ ключ API: console.anthropic.com → API keys, потом export ") +
					cfg.Claude.APIKeyEnv + "=sk-ant-…",
				i18n.Tr("→ либо подписка: поставь Claude Code и войди — steno возьмёт её через `claude -p`"),
			}, false}
	}
	return check{"Claude", "ok",
		cfg.Claude.Model + ", effort " + core.OrDash(cfg.Claude.Effort) + " · " + how, nil, false}
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
		return check{"Google", "ok", note, fix, false}
	}
	if key != "" {
		return check{"Google", "ok", i18n.Tr("доступ ключом организации"), nil, false}
	}
	if !cfg.Calendar.Enabled && !cfg.Gmail.Enabled && !cfg.GoogleDocs.Enabled {
		return check{"Google", "off", i18n.Tr("не подключён, и никому не нужен"), nil, false}
	}
	fix := []string{i18n.Tr("→ панель, «Настройки» → «Подключить Google»")}
	if !google.GoogleOAuthReady(cfg) {
		fix = append(fix, i18n.Tr("→ кнопки там пока нет: `steno setup` спросит client id и секрет"))
	}
	return check{"Google", "fail", i18n.Tr("не подключён"), fix, false}
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
			return check{name, "fail", i18n.Tr("нужен доступ от имени ") + subject, []string{
				i18n.Tr("→ steno подключён как ") + t.Account + i18n.Tr(" и работает только от него"),
				i18n.Tr("→ либо поправь адрес, либо переходи на ключ организации"),
			}, false}
		}
		return check{name, "ok", i18n.Tr("от имени ") + t.Account, nil, false}
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
				c = check{i18n.Tr("календарь"), "fail", i18n.Tr("не указан ни один календарь"),
					[]string{i18n.Tr("→ панель, «Каналы» → «Календарь» → «Чьи календари смотреть»")}, false}
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
		cs = append(cs, check{i18n.Tr("календарь"), "off", i18n.Tr("выключен"), nil, false})
	}

	if cfg.Gmail.Enabled {
		c := checkGoogleAccess(cfg, i18n.Tr("почта бота"),
			google.CredentialsFile(cfg.Gmail.CredentialsFile, cfg.GoogleDocs.CredentialsFile),
			cfg.Gmail.Account)
		if c.state == "ok" && cfg.Gmail.Account == "" {
			c = check{i18n.Tr("почта бота"), "fail", i18n.Tr("не указан ящик бота"),
				[]string{i18n.Tr("→ панель, «Каналы» → «Почта бота» → «Ящик бота»")}, false}
		}
		cs = append(cs, c)
	} else {
		cs = append(cs, check{i18n.Tr("почта бота"), "off", i18n.Tr("выключена"), nil, false})
	}

	if cfg.Telegram.Listen {
		if len(cfg.Telegram.AllowedChats) == 0 && cfg.Telegram.ChatID == "" {
			cs = append(cs, check{i18n.Tr("Telegram вход"), "fail",
				i18n.Tr("не задан ни chat_id, ни allowed_chats"),
				[]string{i18n.Tr("→ без этого сервис не стартует: принимать ссылки от кого угодно нельзя")}, false})
		} else {
			cs = append(cs, checkEnv(i18n.Tr("Telegram вход"), cfg.Telegram.TokenEnv))
		}
	} else {
		cs = append(cs, check{i18n.Tr("Telegram вход"), "off", i18n.Tr("выключен"), nil, false})
	}

	if cfg.HTTP.Enabled {
		c := checkEnv(i18n.Tr("HTTP вход"), cfg.HTTP.TokenEnv)
		if c.state == "ok" && os.Getenv(cfg.Slack.SigningSecretEnv) == "" {
			c.note += i18n.Tr("; слэш-команды Slack не принимаются")
			c.fix = append(c.fix, i18n.Tr("→ для них нужен ")+cfg.Slack.SigningSecretEnv)
		}
		cs = append(cs, c)
	} else {
		cs = append(cs, check{i18n.Tr("HTTP вход"), "off", i18n.Tr("выключен"), nil, false})
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
		cs = append(cs, check{"Google Docs", "off", i18n.Tr("выключен"), nil, false})
	}
	if cfg.Slack.Enabled {
		c := checkEnv("Slack", cfg.Slack.TokenEnv)
		if c.state == "ok" && cfg.Slack.Channel == "" {
			c = check{"Slack", "fail", i18n.Tr("не указан канал"), nil, false}
		}
		cs = append(cs, c)
	} else {
		cs = append(cs, check{"Slack", "off", i18n.Tr("выключен"), nil, false})
	}
	if cfg.Telegram.Enabled {
		c := checkEnv("Telegram", cfg.Telegram.TokenEnv)
		if c.state == "ok" && cfg.Telegram.ChatID == "" {
			c = check{"Telegram", "fail", i18n.Tr("не указан chat_id"), nil, false}
		}
		cs = append(cs, c)
	} else {
		cs = append(cs, check{"Telegram", "off", i18n.Tr("выключен"), nil, false})
	}
	return cs
}

func checkPanel(cfg *core.Config) check {
	if !cfg.Panel.Enabled {
		return check{i18n.Tr("панель"), "off", i18n.Tr("выключена"), nil, false}
	}
	c := checkEnv(i18n.Tr("панель"), cfg.Panel.PasswordEnv)
	if c.state == "ok" {
		c.note = i18n.Tr("слушает ") + cfg.Panel.Addr
	}
	return c
}

func checkEnv(name, env string) check {
	if env == "" {
		return check{name, "fail", i18n.Tr("не указано имя переменной с секретом"), nil, false}
	}
	if strings.TrimSpace(os.Getenv(env)) == "" {
		return check{name, "fail", i18n.Tr("переменная ") + env + i18n.Tr(" пуста"),
			[]string{"→ export " + env + "=..."}, false}
	}
	return check{name, "ok", env + i18n.Tr(" задана"), nil, false}
}

// checkGoogleKey читает ключ service-account и проверяет, что это он и есть.
// Самая частая ошибка здесь — скачать не тот JSON из консоли Google.
func checkGoogleKey(name, path string) check {
	if path == "" {
		return check{name, "fail", i18n.Tr("нет доступа в Google"), []string{
			i18n.Tr("→ панель, «Настройки» → «Подключить Google»"),
			i18n.Tr("→ либо ключ организации в google_docs.credentials_file"),
		}, false}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return check{name, "fail", i18n.Tr("ключ не читается"), []string{"→ " + err.Error()}, false}
	}
	var key struct {
		Type        string `json:"type"`
		ClientEmail string `json:"client_email"`
	}
	if err := json.Unmarshal(raw, &key); err != nil {
		return check{name, "fail", i18n.Tr("ключ — не JSON"), []string{"→ " + err.Error()}, false}
	}
	if key.Type != "service_account" || key.ClientEmail == "" {
		return check{name, "fail", i18n.Tr("это не ключ service-account"),
			[]string{i18n.Tr("→ в консоли Google: IAM → сервисные аккаунты → ключи → создать JSON")}, false}
	}
	return check{name, "ok", key.ClientEmail, nil, false}
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
		return check{i18n.Tr("площадки"), "ok", note, nil, false}
	}
	return check{i18n.Tr("площадки"), "ok", note, []string{
		i18n.Tr("→ у ") + strings.Join(noCaptions, ", ") + i18n.Tr(" субтитры есть не всегда: на публичном ") +
			i18n.Tr("meet.jit.si они выключены на сервере (transcription.enabled=false)"),
		i18n.Tr("→ без них расшифровка идёт по звуку и без имён говорящих; что вышло на ") +
			i18n.Tr("самом деле, бот пишет строкой «субтитры: …» и полем captions в result.json"),
		i18n.Tr("→ свой сервер Jitsi добавляется в selectors.json, ключ jitsi.hosts"),
	}, false}
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
