package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config — весь steno настраивается одним JSON-файлом. Секреты берутся из
// окружения: в файле лежит имя переменной, а не значение, чтобы конфиг можно
// было держать в репозитории.
type Config struct {
	DataDir string `json:"data_dir"` // куда складывать БД и записи

	// Язык интерфейса: "en" или "ru". Пусто — берётся из STENO_LANG, а без
	// неё английский. Влияет и на CLI, и на панель, и на язык follow-up.
	Lang string `json:"lang,omitempty"`

	Bot struct {
		DisplayName string `json:"display_name"` // как бот подписан в списке участников
		Image       string `json:"image"`        // docker-образ бота
		// Сколько ждать, пока хост впустит бота из «комнаты ожидания».
		AdmissionTimeout Duration `json:"admission_timeout"`
		// Уйти, если в звонке остался один бот дольше этого времени.
		EmptyFor Duration `json:"empty_for"`
		// Аварийный потолок на длину записи.
		MaxDuration Duration `json:"max_duration"`
		// Язык субтитров Meet. Он же язык распознавания: по умолчанию там
		// английский, и русская речь превращается в бессмысленный английский
		// текст. Пусто — не трогать настройку.
		CaptionLanguage string `json:"caption_language"`
		// Путь к selectors.json; пусто — встроенные значения.
		Selectors string `json:"selectors"`
		// Запускать бота прямо на хосте, без docker. Нужны Chromium, ffmpeg и
		// поднятый PulseAudio.
		Local bool `json:"local"`
		// Сколько ждать идущие записи при остановке сервиса.
		ShutdownGrace Duration `json:"shutdown_grace"`
	} `json:"bot"`

	Transcribe struct {
		// "command" — внешний адаптер (whisper и подобные).
		// "captions" — брать текст прямо из субтитров Meet: качество ниже, но
		// не нужно ничего устанавливать, и имена говорящих уже проставлены.
		// Годится, чтобы завести конвейер целиком в первый же день.
		Source string `json:"source"`
		// Команда-адаптер. Получает путь к аудио как {{audio}} и печатает в
		// stdout JSON вида {"segments":[{"start":1.2,"end":3.4,"text":"..."}]}.
		Cmd []string `json:"cmd"`
		// Язык распознавания. Пусто — определять автоматически: на созвоне,
		// где переходят с русского на английский, жёстко заданный язык
		// заставляет whisper переводить вторую половину вместо расшифровки.
		Language string   `json:"language"`
		Timeout  Duration `json:"timeout"`
		// Сколько расшифровок идёт одновременно. По умолчанию одна: на
		// large-v3 она занимает всю машину на минуты, а четыре созвона,
		// кончившиеся в одну минуту, положили бы её целиком. Аудио лежит на
		// диске и никуда не денется — очередь просто рассосётся.
		MaxConcurrent int `json:"max_concurrent"`
		// Сколько ядер отдавать. 0 — решает сам адаптер (обычно все). На
		// рабочем ноуте разумно оставить половину.
		Threads int `json:"threads"`
		// Запускать через nice: расшифровка не срочная и должна уступать
		// интерактивной работе.
		Nice bool `json:"nice"`
		// Подсказывать ли распознаванию словарь проекта — имена людей из
		// коммитов, имена сервисов, названия проектов. Без него незнакомое
		// имя превращается в похожее обычное слово раз и навсегда: из
		// «Анвару» назад «Орынгали» не достать ничем.
		//
		// По умолчанию включено, но выключатель есть, и вот зачем. Whisper
		// продолжает подсказку как текст, поэтому она влияет не только на
		// слова, но и на разбивку: на записи созвона список слов склеил
		// тринадцать реплик в шесть. Текст от этого стал точнее, а границы
		// реплик — грубее, и там, где имена говорящих важнее слов, это может
		// оказаться плохим разменом.
		Vocabulary bool `json:"vocabulary"`
	} `json:"transcribe"`

	Claude struct {
		APIKeyEnv string `json:"api_key_env"`
		// Как обращаться к Claude:
		//   "api"  — по ключу. Нужен для сервера: работает без человека.
		//   "cli"  — через `claude -p`, то есть по подписке, которая уже есть
		//            у того, кто поставил steno себе на машину.
		//   "auto" — есть ключ, берём ключ; нет — смотрим, готов ли CLI.
		Via string `json:"via"`
		// Потолок расхода на один запрос при работе через CLI. Ноль — без
		// ограничения.
		MaxUSDPerCall float64 `json:"max_usd_per_call"`
		Model         string  `json:"model"`
		Effort        string  `json:"effort"`
		// Потолок ответа. Его делят между собой рассуждение модели и сам
		// follow-up, поэтому на длинных созвонах при высоком effort его
		// может не хватить.
		MaxTokens int `json:"max_tokens"`
		// Язык follow-up. Пусто — язык созвона.
		OutputLanguage string `json:"output_language"`
		// Цены за миллион токенов по моделям. Пусто — встроенная таблица.
		// Вынесено в конфиг, потому что цены меняются чаще релизов.
		Prices map[string]Price `json:"prices"`
	} `json:"claude"`

	// Доступ в Google по кнопке. Второй путь к календарю, почте и Drive — рядом
	// с файлом service-account, который остаётся в секциях ниже.
	//
	// Файл нужен компании: только он читает календари сорока человек, никого не
	// спрашивая. Одному человеку он недоступен — за ним стоят проект в Google
	// Cloud и админка домена, которой у частного человека нет. Здесь человек
	// один раз соглашается у Google, и steno дальше видит ровно то, что видит
	// он сам.
	//
	// Client ID не тайна: он уезжает на чужие машины вместе с программой и
	// виден в адресной строке при согласии. Секрет приложения типа «Desktop
	// app» Google секретом тоже не считает, но лежит он всё равно в окружении —
	// чтобы конфиг целиком можно было держать в репозитории.
	Google struct {
		ClientID        string `json:"client_id"`
		ClientSecretEnv string `json:"client_secret_env"`
	} `json:"google"`

	Calendar struct {
		Enabled bool `json:"enabled"`
		// Тот же service-account с domain-wide delegation. Пусто — берётся
		// ключ из google_docs. Не нужен, если Google подключён кнопкой.
		CredentialsFile string `json:"credentials_file"`
		// Чьи календари смотреть. Каждый читается от имени его владельца:
		// domain-wide delegation позволяет представиться любым сотрудником.
		Calendars []string `json:"calendars"`
		PollEvery Duration `json:"poll_every"`
		// За сколько до начала заводить бота в звонок.
		JoinBefore Duration `json:"join_before"`
		// Не ходить на встречи, где меньше стольких участников.
		MinAttendees int `json:"min_attendees"`
		// Пропускать встречи, в названии или описании которых есть это.
		SkipMarkers []string `json:"skip_markers"`
		// На сколько дней вперёд собирать расписание для панели.
		ScheduleDays int `json:"schedule_days"`
		// Напоминать о созвоне заранее. Почту читают не все и не всегда, а
		// пропущенная встреча стоит дороже одного сообщения в чат.
		Remind       bool     `json:"remind"`
		RemindBefore Duration `json:"remind_before"`
		// Сколько созвонов писать одновременно.
		MaxConcurrent int `json:"max_concurrent"`
	} `json:"calendar"`

	GoogleDocs struct {
		Enabled bool `json:"enabled"`
		// ID папки Drive, куда класть документы.
		FolderID string `json:"folder_id"`
		// Файл service-account с domain-wide delegation.
		CredentialsFile string `json:"credentials_file"`
		// От чьего имени создавать документы (impersonation).
		Subject string `json:"subject"`
		// Со scope drive.file приложение видит только те файлы, которые создало
		// само, — положить документ в заранее созданную руками папку с ним
		// нельзя. Поэтому по умолчанию берётся полный drive.
		Scopes []string `json:"scopes"`
		// Вести отдельный документ на каждый проект: одна ссылка, которая
		// всегда показывает текущее состояние, вместо тридцати документов по
		// одному на созвон.
		ProjectDocs bool `json:"project_docs"`
	} `json:"google_docs"`

	Slack struct {
		Enabled  bool   `json:"enabled"`
		TokenEnv string `json:"token_env"`
		// Подписывающий секрет приложения Slack. Без него слэш-команды не
		// принимаются: подтвердить, что запрос пришёл именно от Slack, а не от
		// того, кто узнал общий токен, больше нечем.
		SigningSecretEnv string `json:"signing_secret_env"`
		Channel          string `json:"channel"`     // куда постить итог
		DMOwners         bool   `json:"dm_owners"`   // писать в личку тем, на ком задача
		ThreadFull       bool   `json:"thread_full"` // полный транскрипт в тред
	} `json:"slack"`

	Telegram struct {
		Enabled  bool   `json:"enabled"`
		TokenEnv string `json:"token_env"`
		ChatID   string `json:"chat_id"`
		// Слушать входящие сообщения: кинул боту ссылку — он пошёл на созвон.
		Listen bool `json:"listen"`
		// Из каких чатов принимать ссылки. Пусто — только chat_id.
		AllowedChats []string `json:"allowed_chats"`
	} `json:"telegram"`

	// Почта аккаунта бота. Добавил steno@company.com в идущий звонок кнопкой
	// «Добавить людей» — Google прислал ему письмо со ссылкой, бот пришёл.
	Gmail struct {
		Enabled         bool     `json:"enabled"`
		CredentialsFile string   `json:"credentials_file"`
		Account         string   `json:"account"`
		AllowedDomains  []string `json:"allowed_domains"`
		PollEvery       Duration `json:"poll_every"`
	} `json:"gmail"`

	// Проекты команды. На одном созвоне обсуждают три-четыре сразу, и без
	// разметки follow-up превращается в кучу, из которой потом никто не
	// вытащит, что относилось к чему.
	Projects []Project `json:"projects"`

	// Периодическая сверка с репозиториями проектов. Разово склонированный
	// репозиторий устаревает за неделю, и бот начинает рассуждать о проекте
	// по прошлогоднему коду. Заодно по новым коммитам видно, какие задачи
	// закрылись, — а иначе задача, сделанная тихо, висит вечно.
	Sync struct {
		Enabled bool `json:"enabled"`
		// Как часто заглядывать в репозитории. Поход в Claude случается только
		// там, где появились коммиты, поэтому частая сверка почти ничего не
		// стоит — а задача, закрытая утренним коммитом, закрывается до
		// вечернего созвона, а не через сутки.
		Every Duration `json:"every"`
		// Пересобирать справку, если накопилось столько коммитов или прошло
		// столько времени. Каждый коммит — не повод платить за справку заново.
		RebuildAfterCommits int      `json:"rebuild_after_commits"`
		RebuildAfter        Duration `json:"rebuild_after"`
		// Закрывать задачу автоматически, если модель уверена. Ниже порога —
		// только сообщение, без закрытия.
		AutoClose bool `json:"auto_close"`
		// Куда сообщать о закрытых коммитами задачах.
		Notify bool `json:"notify"`
	} `json:"sync"`

	// Веб-панель: список созвонов, поиск по всем расшифровкам, плеер с
	// таймкодами, страница «кто что должен».
	Panel struct {
		Enabled bool   `json:"enabled"`
		Addr    string `json:"addr"`
		// Пароль общий на команду: заводить учётку каждому ради архива
		// созвонов — работа, которую никто не сделает.
		PasswordEnv string `json:"password_env"`
		// Ставить cookie только по HTTPS. Включать, когда панель за TLS.
		Secure bool `json:"secure"`
	} `json:"panel"`

	// Сколько держать записи и служебные отметки. Час созвона — это ~15 МБ
	// аудио плюс расшифровка; у команды на сорок созвонов в неделю это
	// заметный объём, и никто его не чистит, пока не кончится диск.
	Retention struct {
		Recordings Duration `json:"recordings"` // 0 — не удалять
		Events     Duration `json:"events"`
	} `json:"retention"`

	// Эндпоинт для всего остального: Slack-команда, ярлык на телефоне, curl.
	HTTP struct {
		Enabled  bool   `json:"enabled"`
		Addr     string `json:"addr"`
		TokenEnv string `json:"token_env"`
	} `json:"http"`

	// noPublish — флаг командной строки, а не настройка: `steno join
	// --no-publish` не должен ничего рассылать. Гасить для этого сами каналы
	// нельзя с тех пор, как они живут в базе: перед рассылкой настройки
	// перечитываются, и погашенное в памяти тут же вернулось бы включённым.
	noPublish bool
}

// Project — то, вокруг чего собираются решения и задачи. Псевдонимы нужны,
// потому что вслух проект называют не так, как он записан: «биллинг», «платежи»
// и «payments» — одно и то же.
type Project struct {
	Name    string   `json:"name"`
	Aliases []string `json:"aliases"`
	// Одна строка о том, что это: модель по ней различает похожие проекты.
	About string `json:"about"`
	// Материал, по которому собирается справка о проекте. Псевдонимов и одной
	// строки мало: на созвоне говорят «поправим вебхуки в биллинге», и понять,
	// что это тот же проект, можно только зная его устройство и слова, которыми
	// команда о нём говорит.
	Sources []Source `json:"sources"`
	// Кто в проекте участвует — именами, которыми людей зовут вслух, а не
	// подписью аккаунта. Это разные вещи и в этом вся беда: в git человек
	// «TomXemmings», в календаре «Rustem T.», а на созвоне звучит «Рустем», и
	// follow-up приписывает задачу тому, чьё имя он узнал.
	//
	// Люди вынесены из общего словаря отдельным списком по одной причине: цена
	// ошибки. Приписать задачу не тому человеку — это задача, которая не будет
	// сделана; принять сервис за человека — тоже (владельцем становится
	// «Сапар»). Всё остальное — сокращения, названия систем — модели достаточно
	// знать как слова этого проекта, отдельный список под каждый вид завёл бы
	// пять полей, из которых заполняют одно.
	People []string `json:"people"`
	// Слова, которые у команды звучат вслух, а в коде их нет: имена людей,
	// названия сервисов, сокращения. Идут в два места сразу.
	//
	// В whisper — подсказкой словаря. Без неё имя, которого нет в словаре
	// модели, превращается в похожее обычное слово: «Орынгали» стало «Анвару» и
	// пропало из задач вовсе. Распознавание чинится только так — задним числом
	// из «Анвару» имя не восстановить.
	//
	// И в промпт follow-up — чтобы модель знала, что «Сапар» это сервис, а не
	// человек, и не гадала по звучанию.
	//
	// Имена из People здесь тоже есть, и это не дубль, а обязательство: whisper
	// читает один плоский список, и имя, оставшееся только в People, до него бы
	// не доехало — то есть ровно тот случай, ради которого всё затевалось. За
	// тем, чтобы People всегда были внутри, следит SaveProject.
	Vocabulary []string `json:"vocabulary"`
}

// Source — откуда брать материал о проекте.
//
//	{"kind": "text", "value": "Приём платежей, подписки, вебхуки провайдеров"}
//	{"kind": "path", "value": "~/work/payments"}       — локальный репозиторий
//	{"kind": "repo", "value": "git@github.com:org/pay"} — склонируем поверхностно
//	{"kind": "url",  "value": "https://pay.example.com"}
type Source struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// Duration — time.Duration, который в JSON выглядит как "5m", а не как 300000000000.
type Duration time.Duration

func (d Duration) D() time.Duration { return time.Duration(d) }

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	// time.ParseDuration не знает про дни, а сроки хранения естественно
	// задавать именно в днях.
	if n, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.ParseFloat(n, 64)
		if err != nil {
			return fmt.Errorf(tr("длительность %q: %w"), s, err)
		}
		*d = Duration(time.Duration(days * float64(24*time.Hour)))
		return nil
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf(tr("длительность %q: %w"), s, err)
	}
	*d = Duration(v)
	return nil
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// applyLangDefaults — те умолчания, которые зависят от языка интерфейса.
// Отдельной функцией, потому что язык бывает выбран уже после defaultConfig():
// так делает мастер, и пересчитать два поля ему дешевле, чем собирать конфиг
// заново, рискуя потерять уже введённое.
func applyLangDefaults(c *Config) {
	// Имя бота в списке участников: его читают те, кто на созвоне.
	c.Bot.DisplayName = tr("Steno · идёт запись")
	// Язык субтитров Meet — он же язык распознавания. Жёсткое "ru" здесь
	// означало, что английская установка слушает созвон по-русски и получает
	// бессмысленный текст; язык интерфейса — куда более близкая догадка.
	c.Bot.CaptionLanguage = langRU
	if uiLang == langEN {
		c.Bot.CaptionLanguage = langEN
	}
	// Язык follow-up по умолчанию — язык интерфейса. Промпт написан по-русски
	// и без этой строки отвечает по-русски же; человеку, который поставил
	// steno и увидел английский экран, это не то, чего он ждёт. Русская
	// установка остаётся с пустым значением, как была.
	c.Claude.OutputLanguage = ""
	if uiLang == langEN {
		c.Claude.OutputLanguage = "English"
	}
	// Русский маркер держим только в русской установке: в английском
	// интерфейсе он выглядит как чужая строка, а не как настройка.
	c.Calendar.SkipMarkers = []string{"#nosteno"}
	if uiLang == langRU {
		c.Calendar.SkipMarkers = append(c.Calendar.SkipMarkers, "#беззаписи")
	}
}

func defaultConfig() *Config {
	var c Config
	c.DataDir = "./data"
	c.Bot.Image = defaultBotImage()
	c.Bot.AdmissionTimeout = Duration(5 * time.Minute)
	c.Bot.EmptyFor = Duration(2 * time.Minute)
	c.Bot.MaxDuration = Duration(4 * time.Hour)
	c.Bot.ShutdownGrace = Duration(15 * time.Minute)
	c.Transcribe.Source = "command"
	c.Transcribe.Cmd = []string{"./adapters/whisper-cpp.sh", "{{audio}}", "{{language}}"}
	c.Transcribe.Language = ""
	// large-v3 без GPU расшифровывает час созвона дольше часа — потолок должен
	// это переживать, иначе сервис убьёт работу на середине.
	c.Transcribe.Timeout = Duration(4 * time.Hour)
	c.Transcribe.MaxConcurrent = 1
	c.Transcribe.Nice = true
	c.Transcribe.Vocabulary = true
	c.Claude.APIKeyEnv = "ANTHROPIC_API_KEY"
	c.Claude.Model = "claude-opus-5"
	c.Claude.Via = "auto"
	// low, а не high, и это измерено, а не выбрано из осторожности. Один и тот
	// же созвон прогнан десятью сочетаниями модели и усилия: все десять достали
	// одни и те же пять поручений с верными исполнителями и сроками. Усилие не
	// добавило ни одной задачи — только время и деньги, вплоть до 23 минут и
	// $1.39 против 40 секунд и $0.12 на том же тексте. Выше поднимать стоит
	// ради формулировок в рисках, а не ради полноты списков.
	c.Claude.Effort = "low"
	c.Claude.MaxTokens = 16000
	c.Calendar.PollEvery = Duration(2 * time.Minute)
	c.Calendar.JoinBefore = Duration(time.Minute)
	c.Calendar.MinAttendees = 2
	c.Calendar.MaxConcurrent = 4
	c.Calendar.ScheduleDays = 7
	c.Calendar.Remind = true
	c.Calendar.RemindBefore = Duration(10 * time.Minute)
	c.Google.ClientSecretEnv = "GOOGLE_CLIENT_SECRET"
	c.GoogleDocs.Scopes = []string{"https://www.googleapis.com/auth/drive"}
	c.Slack.TokenEnv = "SLACK_BOT_TOKEN"
	c.Slack.SigningSecretEnv = "SLACK_SIGNING_SECRET"
	c.Telegram.TokenEnv = "TELEGRAM_BOT_TOKEN"
	c.Gmail.PollEvery = Duration(45 * time.Second)
	c.Retention.Recordings = Duration(30 * 24 * time.Hour)
	c.Retention.Events = Duration(7 * 24 * time.Hour)
	c.Sync.Enabled = true
	// Раз в четыре часа, а не раз в сутки: задача, закрытая утренним коммитом,
	// должна закрыться до вечернего созвона, а не через день. Дороже это почти
	// не делает — поход в Claude случается только там, где появились коммиты.
	c.Sync.Every = Duration(4 * time.Hour)
	c.Sync.RebuildAfterCommits = 30
	// Раз в сутки. Справка стоит копейки (замер: $0.07–0.18 на проект), а
	// устаревший словарь тихо разваливает разбор по проектам — экономить тут
	// не на чем. Материал не изменился — сверка отпечатка не даст потратиться.
	c.Sync.RebuildAfter = Duration(24 * time.Hour)
	c.Sync.AutoClose = true
	c.Sync.Notify = true
	// 8422, а не 8080: восьмидесятый с хвостиком — самый занятый порт на машине
	// разработчика. На этой он и оказался занят чужим `go run ./cmd/server`, и
	// панель steno просто не поднялась. Умолчание, которое конфликтует у первого
	// же человека, — плохое умолчание.
	c.Panel.Addr = ":8422"
	c.Panel.PasswordEnv = "STENO_PANEL_PASSWORD"
	c.HTTP.Addr = ":8787"
	c.HTTP.TokenEnv = "STENO_HTTP_TOKEN"
	applyLangDefaults(&c)
	return &c
}

func loadConfig(path string) (*Config, error) {
	c := defaultConfig()
	if path == "" {
		return c, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	// Все пути — относительно самого конфига, а не текущего каталога. Иначе
	// конфиг в /etc/steno работает ровно до первой записи созвона, а потом
	// падает на ненайденном адаптере расшифровки — то есть после того, как час
	// разговора уже записан.
	base := filepath.Dir(path)
	rel := []*string{&c.DataDir, &c.Bot.Selectors,
		&c.GoogleDocs.CredentialsFile, &c.Calendar.CredentialsFile, &c.Gmail.CredentialsFile}
	for _, p := range rel {
		if *p != "" && !filepath.IsAbs(*p) {
			*p = filepath.Join(base, *p)
		}
	}
	// Первый элемент команды расшифровки — тоже путь, если он выглядит как
	// путь, а не как имя в PATH.
	if len(c.Transcribe.Cmd) > 0 {
		if cmd := c.Transcribe.Cmd[0]; strings.ContainsRune(cmd, filepath.Separator) &&
			!filepath.IsAbs(cmd) {
			c.Transcribe.Cmd[0] = filepath.Join(base, cmd)
		}
	}
	return c, nil
}

// secret читает значение по имени переменной окружения из конфига.
func secret(envName, what string) (string, error) {
	v := strings.TrimSpace(os.Getenv(envName))
	if v == "" {
		return "", fmt.Errorf(tr("%s: переменная окружения %s пуста"), what, envName)
	}
	return v, nil
}
