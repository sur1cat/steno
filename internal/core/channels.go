package core

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sur1cat/steno/internal/i18n"
)

// Каналы — то, куда сервис ходит и что слушает: календарь, почта бота,
// Telegram, Slack, Google Docs, HTTP.
//
// Раньше они включались только в steno.json, а панель показывала их на
// просмотр. Это неправильно по той же причине, по какой из конфига уехали
// проекты: «завести бота в наш Slack» — работа того, кто в этом Slack сидит, а
// не того, кто умеет править JSON и перезапускать сервис.
//
// Поэтому настройки каналов живут в базе, а конфиг остаётся начальным
// значением: при первом запуске всё, что описано файлом, переезжает в базу, и
// дальше главнее база — ровно как у проектов.
//
// Секретов в панели нет вовсе — ни значений, ни имён переменных, ни отметки
// «задан». Токены задаёт `steno setup` в .env, и человеку, который завёл бота в
// свой Slack, знать про это незачем: имя переменной окружения на экране ничего
// ему не объясняет, а починить он по нему всё равно ничего не может.

// ChannelField описывает одно поле канала. Панель рисует форму по этому
// описанию, а не по своему списку полей: иначе новое поле пришлось бы заводить
// в двух местах, и рано или поздно они разошлись бы.
type ChannelField struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	// text | list | number | duration | switch | select | google
	Kind        string `json:"kind"`
	Hint        string `json:"hint"`
	Placeholder string `json:"placeholder"`
	// Только для «select»: из чего выбирать. Список приходит с сервера вместе с
	// полем — как и всё остальное описание формы, чтобы вариант, заведённый в
	// одном месте, не пришлось заводить во втором.
	Options []ChannelOption `json:"options,omitempty"`
	// Показывать поле, только когда другое поле равно этому значению. Нужно
	// ровно одному разделу — «Разбор», где адрес и имя модели имеют смысл лишь
	// у одного из провайдеров. Показывать их всегда значит спрашивать человека
	// про адрес OpenAI, когда он выбрал подписку Claude.
	ShowWhen  string `json:"showWhen,omitempty"`
	ShowValue string `json:"showValue,omitempty"`
}

// ChannelOption — один вариант выбора.
type ChannelOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Hint  string `json:"hint,omitempty"`
}

// Input — хранит ли поле значение. «google» — не поле, а кнопка: доступ в
// Google лежит отдельным файлом токена, и класть его копию в настройку канала
// значило бы завести второе место, где он может разойтись с первым.
func (f ChannelField) Input() bool { return f.Kind != "google" }

// Виды разделов настройки. Каналы приносят созвоны и уносят follow-up, и у них
// есть выключатель. «Разбор» — не канал: выключить его нельзя, и слова «вход» и
// «выход» к нему не относятся. Форма при этом та же самая, и рисуется она тем
// же кодом — заводить под один раздел вторую форму в панели и третью в
// терминале было бы куда хуже.
const (
	ChannelKindIO    = "io"
	ChannelKindBrain = "brain"
)

// Channel — раздел настройки в том виде, в каком его правит человек в панели.
type Channel struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	About string `json:"about"`
	// io | brain — см. ChannelKindIO.
	Kind    string            `json:"kind"`
	In      bool              `json:"in"`  // приносит созвоны
	Out     bool              `json:"out"` // уносит follow-up
	Enabled bool              `json:"enabled"`
	Values  map[string]string `json:"values"`
	Fields  []ChannelField    `json:"fields"`
	Summary string            `json:"summary"`
	// Live — применяется ли изменение к уже запущенному сервису. Адресаты
	// перечитываются перед каждой рассылкой, а источники слушают сеть с самого
	// старта, и переключать их на ходу значило бы ронять идущие записи.
	Live bool `json:"live"`
}

// channelDef — определение канала: как показать, как прочитать из конфига и
// как наложить обратно.
type channelDef struct {
	key, Name, about string
	in, out, Live    bool
	// Пусто — обычный канал (ChannelKindIO).
	kind    string
	Fields  []ChannelField
	summary func(map[string]string) string
	read    func(*Config) (bool, map[string]string)
	apply   func(*Config, bool, map[string]string)
}

func (d channelDef) Kind() string {
	if d.kind == "" {
		return ChannelKindIO
	}
	return d.kind
}

func ChList(s string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' }) {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func ChBool(s string) bool { return s == "1" }

func ChFlag(b bool) string {
	if b {
		return "1"
	}
	return ""
}

// chKept — значение, которое человек когда-то задал в панели, а панель его уже
// не показывает: путь к ключу организации и «от чьего имени создавать». Оба
// поля из формы убраны — обычному человеку они не говорят ничего, — но у тех,
// кто успел их там задать, они должны остаться рабочими. Иначе обновление тихо
// переключило бы компанию на другой ключ или в другой Drive.
func chKept(v map[string]string, key, current string) string {
	if s := strings.TrimSpace(v[key]); s != "" {
		return s
	}
	return current
}

func chInt(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}

// chDur возвращает прежнее значение, если человек ввёл ерунду: молча обнулить
// период опроса значило бы заставить сервис долбить календарь без пауз.
func chDur(s string, def Duration) Duration {
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return def
	}
	return Duration(d)
}

// ChDurText печатает период так, как его пишут руками. Duration.String() даёт
// «2m0s» и «1h0m0s» — формально верно, но в поле, куда человек вводит «2m»,
// такое выглядит опечаткой сервиса.
func ChDurText(d Duration) string {
	v := d.D()
	switch {
	case v == 0:
		return ""
	case v%time.Hour == 0:
		return fmt.Sprintf("%dh", v/time.Hour)
	case v%time.Minute == 0:
		return fmt.Sprintf("%dm", v/time.Minute)
	case v%time.Second == 0:
		return fmt.Sprintf("%ds", v/time.Second)
	}
	return v.String()
}

// См. uiTabTitles: описание каналов собирается лениво и один раз — на языке,
// который к тому моменту уже прочитан из конфига.
var builtinChannelDefs = sync.OnceValue(func() []channelDef {
	return []channelDef{
		brainDef(),
		{
			key: "calendar", Name: i18n.Tr("Календарь"), in: true,
			about: i18n.Tr("Смотрит календари команды и заводит бота на встречи со ссылкой на созвон (Google Meet, Jitsi). ") +
				i18n.Tr("Это единственный канал, который работает сам, без просьбы человека."),
			Fields: []ChannelField{
				{Key: "google", Label: i18n.Tr("Доступ в Google"), Kind: "google",
					Hint: i18n.Tr("Без него встреч не видно.")},
				{Key: "calendars", Label: i18n.Tr("Чьи календари смотреть"), Kind: "list",
					Placeholder: "name@example.com",
					Hint: i18n.Tr("Свой календарь виден сразу; чужой — только если человек ") +
						i18n.Tr("сам открыл его боту.")},
				{Key: "poll_every", Label: i18n.Tr("Как часто заглядывать"), Kind: "duration", Placeholder: "2m"},
				{Key: "join_before", Label: i18n.Tr("Заходить заранее"), Kind: "duration", Placeholder: "1m",
					Hint: i18n.Tr("За сколько до начала заводить бота в звонок.")},
				{Key: "min_attendees", Label: i18n.Tr("Минимум участников"), Kind: "number", Placeholder: "2",
					Hint: i18n.Tr("Встречу с одним человеком записывать нечего.")},
				{Key: "skip_markers", Label: i18n.Tr("Не ходить, если в названии есть"), Kind: "list",
					Placeholder: i18n.Tr("#беззаписи")},
				{Key: "max_concurrent", Label: i18n.Tr("Сколько записей разом"), Kind: "number", Placeholder: "4"},
			},
			summary: func(v map[string]string) string { return v["calendars"] },
			read: func(c *Config) (bool, map[string]string) {
				return c.Calendar.Enabled, map[string]string{
					"calendars":      strings.Join(c.Calendar.Calendars, ", "),
					"poll_every":     ChDurText(c.Calendar.PollEvery),
					"join_before":    ChDurText(c.Calendar.JoinBefore),
					"min_attendees":  strconv.Itoa(c.Calendar.MinAttendees),
					"skip_markers":   strings.Join(c.Calendar.SkipMarkers, ", "),
					"max_concurrent": strconv.Itoa(c.Calendar.MaxConcurrent),
				}
			},
			apply: func(c *Config, on bool, v map[string]string) {
				c.Calendar.Enabled = on
				c.Calendar.Calendars = ChList(v["calendars"])
				c.Calendar.CredentialsFile = chKept(v, "credentials_file", c.Calendar.CredentialsFile)
				c.Calendar.PollEvery = chDur(v["poll_every"], c.Calendar.PollEvery)
				c.Calendar.JoinBefore = chDur(v["join_before"], c.Calendar.JoinBefore)
				c.Calendar.MinAttendees = chInt(v["min_attendees"], c.Calendar.MinAttendees)
				c.Calendar.SkipMarkers = ChList(v["skip_markers"])
				c.Calendar.MaxConcurrent = chInt(v["max_concurrent"], c.Calendar.MaxConcurrent)
			},
		},
		{
			key: "gmail", Name: i18n.Tr("Почта бота"), in: true,
			about: i18n.Tr("Добавил steno@company.com в идущий звонок кнопкой «Добавить людей» — ") +
				i18n.Tr("Google прислал ему письмо со ссылкой, бот пришёл."),
			Fields: []ChannelField{
				{Key: "google", Label: i18n.Tr("Доступ в Google"), Kind: "google",
					Hint: i18n.Tr("Без него письма бота не прочитать.")},
				{Key: "account", Label: i18n.Tr("Ящик бота"), Kind: "text", Placeholder: "steno@example.com"},
				{Key: "allowed_domains", Label: i18n.Tr("От кого принимать"), Kind: "list",
					Placeholder: "example.com",
					Hint: i18n.Tr("Часть адреса после собаки. Пусто — только те, у кого почта того же ") +
						i18n.Tr("вида, что у бота: иначе увести бота на созвон сможет любой, кто ") +
						i18n.Tr("узнал адрес.")},
				{Key: "poll_every", Label: i18n.Tr("Как часто проверять почту"), Kind: "duration", Placeholder: "45s"},
			},
			summary: func(v map[string]string) string { return v["account"] },
			read: func(c *Config) (bool, map[string]string) {
				return c.Gmail.Enabled, map[string]string{
					"account":         c.Gmail.Account,
					"allowed_domains": strings.Join(c.Gmail.AllowedDomains, ", "),
					"poll_every":      ChDurText(c.Gmail.PollEvery),
				}
			},
			apply: func(c *Config, on bool, v map[string]string) {
				c.Gmail.Enabled = on
				c.Gmail.Account = strings.TrimSpace(v["account"])
				c.Gmail.CredentialsFile = chKept(v, "credentials_file", c.Gmail.CredentialsFile)
				c.Gmail.AllowedDomains = ChList(v["allowed_domains"])
				c.Gmail.PollEvery = chDur(v["poll_every"], c.Gmail.PollEvery)
			},
		},
		{
			key: "telegram", Name: "Telegram", in: true, out: true, Live: true,
			about: i18n.Tr("Шлёт follow-up в чат команды. С включённым «слушать» ещё и принимает ") +
				i18n.Tr("ссылки: кинул боту ссылку на созвон — он пошёл."),
			Fields: []ChannelField{
				{Key: "chat_id", Label: i18n.Tr("Чат для follow-up"), Kind: "text", Placeholder: "-1001234567890",
					Hint: i18n.Tr("Номер чата — длинное число со знаком минус.")},
				{Key: "listen", Label: i18n.Tr("Слушать входящие"), Kind: "switch",
					Hint: i18n.Tr("Применится после перезапуска сервиса.")},
				{Key: "allowed_chats", Label: i18n.Tr("Откуда принимать ссылки"), Kind: "list",
					Placeholder: "-1001234567890",
					Hint:        i18n.Tr("Пусто — только чат выше.")},
			},
			summary: func(v map[string]string) string { return v["chat_id"] },
			read: func(c *Config) (bool, map[string]string) {
				return c.Telegram.Enabled, map[string]string{
					"chat_id":       c.Telegram.ChatID,
					"listen":        ChFlag(c.Telegram.Listen),
					"allowed_chats": strings.Join(c.Telegram.AllowedChats, ", "),
				}
			},
			apply: func(c *Config, on bool, v map[string]string) {
				c.Telegram.Enabled = on
				c.Telegram.ChatID = strings.TrimSpace(v["chat_id"])
				c.Telegram.Listen = ChBool(v["listen"])
				c.Telegram.AllowedChats = ChList(v["allowed_chats"])
			},
		},
		{
			key: "slack", Name: "Slack", out: true, Live: true,
			about: i18n.Tr("Кладёт итог созвона в канал команды: о чём договорились, кто что должен, ") +
				i18n.Tr("что осталось нерешённым."),
			Fields: []ChannelField{
				{Key: "channel", Label: i18n.Tr("Канал"), Kind: "text", Placeholder: i18n.Tr("#созвоны")},
				{Key: "dm_owners", Label: i18n.Tr("Писать в личку тем, на ком задача"), Kind: "switch"},
				{Key: "thread_full", Label: i18n.Tr("Класть полную расшифровку в тред"), Kind: "switch",
					Hint: i18n.Tr("Часовой созвон — это несколько экранов текста в канале.")},
			},
			summary: func(v map[string]string) string { return v["channel"] },
			read: func(c *Config) (bool, map[string]string) {
				return c.Slack.Enabled, map[string]string{
					"channel":     c.Slack.Channel,
					"dm_owners":   ChFlag(c.Slack.DMOwners),
					"thread_full": ChFlag(c.Slack.ThreadFull),
				}
			},
			apply: func(c *Config, on bool, v map[string]string) {
				c.Slack.Enabled = on
				c.Slack.Channel = strings.TrimSpace(v["channel"])
				c.Slack.DMOwners = ChBool(v["dm_owners"])
				c.Slack.ThreadFull = ChBool(v["thread_full"])
			},
		},
		{
			key: "http", Name: i18n.Tr("Вызов по ссылке"), in: true,
			about: i18n.Tr("Позвать бота ссылкой из чего угодно: с ярлыка на телефоне, из другой ") +
				i18n.Tr("программы, командой в Slack. Пригодится, когда созвона нет в календаре."),
			Fields: []ChannelField{
				{Key: "addr", Label: i18n.Tr("Адрес, на котором ждать вызова"), Kind: "text",
					Placeholder: ":8787",
					Hint:        i18n.Tr("Менять есть смысл, только если этот адрес занят другой программой.")},
			},
			summary: func(v map[string]string) string { return v["addr"] },
			read: func(c *Config) (bool, map[string]string) {
				return c.HTTP.Enabled, map[string]string{"addr": c.HTTP.Addr}
			},
			apply: func(c *Config, on bool, v map[string]string) {
				c.HTTP.Enabled = on
				if a := strings.TrimSpace(v["addr"]); a != "" {
					c.HTTP.Addr = a
				}
			},
		},
		{
			key: "google_docs", Name: "Google Docs", out: true, Live: true,
			about: i18n.Tr("Складывает follow-up документами в папку Drive. С «документами проектов» ") +
				i18n.Tr("ведёт ещё по одному живому документу на проект — одна ссылка вместо тридцати."),
			Fields: []ChannelField{
				{Key: "google", Label: i18n.Tr("Доступ в Google"), Kind: "google",
					Hint: i18n.Tr("Документы появятся на том же Drive, к которому подключились.")},
				{Key: "folder_id", Label: i18n.Tr("Папка на Drive"), Kind: "text", Placeholder: "1AbC…",
					Hint: i18n.Tr("Открой папку на Drive и скопируй сюда хвост адреса — набор букв ") +
						i18n.Tr("и цифр после /folders/. Пусто — документы лягут в корень.")},
				{Key: "project_docs", Label: i18n.Tr("Вести документ на каждый проект"), Kind: "switch"},
			},
			summary: func(v map[string]string) string { return v["folder_id"] },
			read: func(c *Config) (bool, map[string]string) {
				return c.GoogleDocs.Enabled, map[string]string{
					"folder_id":    c.GoogleDocs.FolderID,
					"project_docs": ChFlag(c.GoogleDocs.ProjectDocs),
				}
			},
			apply: func(c *Config, on bool, v map[string]string) {
				c.GoogleDocs.Enabled = on
				c.GoogleDocs.FolderID = strings.TrimSpace(v["folder_id"])
				c.GoogleDocs.CredentialsFile = chKept(v, "credentials_file", c.GoogleDocs.CredentialsFile)
				c.GoogleDocs.Subject = chKept(v, "subject", c.GoogleDocs.Subject)
				c.GoogleDocs.ProjectDocs = ChBool(v["project_docs"])
			},
		},
	}
})

// brainDef — раздел «Разбор»: кем steno разбирает созвон.
//
// Это единственное место, где провайдер меняется без правки JSON, и потому
// самое важное из всего, что здесь описано: человек, поставивший steno ради
// созвонов, не должен лезть в файл, чтобы переехать с Claude на свою модель.
// Форма — та же самая, что у каналов, и рисуют её те же двое: панель и
// терминал. Свой экран под один раздел означал бы третий список полей, который
// однажды разойдётся с остальными.
//
// Секретов здесь нет и не будет — как и у каналов. Ни значений ключей, ни имён
// переменных, в которых они лежат. Имя переменной подставляется по заготовке
// (Groq — GROQ_API_KEY), а сам ключ задаёт `steno setup` в .env с правами 0600.
// Тому, кто открыл панель выбрать модель подешевле, слово OPENROUTER_API_KEY не
// объясняет ничего и починить он по нему ничего не может.
func brainDef() channelDef {
	presets := make([]ChannelOption, 0, len(OpenAIPresets()))
	for _, p := range OpenAIPresets() {
		o := ChannelOption{Value: p.Key, Label: p.Name, Hint: p.Hint}
		if p.BaseURL != "" {
			o.Label = p.Name + " — " + p.BaseURL
		}
		presets = append(presets, o)
	}
	return channelDef{
		// Live: false — и это не забытая строка. Настройки каналов
		// перечитываются перед каждой рассылкой, а конфиг, по которому идёт
		// разбор, сервис держит с самого старта: смена провайдера доходит до
		// него только при перезапуске. Панель и терминал говорят об этом прямо,
		// потому что «сохранено» на неприменившейся настройке — это полчаса
		// поисков поломки, которой нет.
		key: "brain", Name: i18n.Tr("Разбор"), kind: ChannelKindBrain,
		about: i18n.Tr("Кто читает расшифровку и достаёт из неё задачи, решения и вопросы. ") +
			i18n.Tr("Подписка, которая уже есть, ключ провайдера или модель на этой же машине."),
		Fields: []ChannelField{
			{Key: "provider", Label: i18n.Tr("Чем разбирать"), Kind: "select",
				Options: []ChannelOption{
					{Value: ProviderClaude, Label: "Claude",
						Hint: i18n.Tr("Ключ Anthropic или подписка Claude Code через `claude -p`.")},
					{Value: ProviderOpenAI, Label: i18n.Tr("Совместимый с OpenAI"),
						Hint: i18n.Tr("OpenAI, Groq, OpenRouter, Together, DeepSeek — и Ollama, ") +
							i18n.Tr("LM Studio, llama.cpp на этой же машине.")},
					{Value: ProviderCodex, Label: i18n.Tr("Codex (подписка ChatGPT)"),
						Hint: i18n.Tr("Через `codex exec`. Нужен установленный codex и выполненный вход.")},
					{Value: ProviderCommand, Label: i18n.Tr("Свой скрипт"),
						Hint: i18n.Tr("Всё остальное: llamafile, своя обёртка, эндпоинт за VPN. ") +
							i18n.Tr("Сам скрипт задаётся в `steno setup` — отсюда командами не запускают.")},
				}},
			{Key: "preset", Label: i18n.Tr("Куда ходить"), Kind: "select",
				ShowWhen: "provider", ShowValue: ProviderOpenAI, Options: presets,
				Hint: i18n.Tr("Ключ к выбранному задаёт `steno setup` — здесь секретов нет.")},
			{Key: "base_url", Label: i18n.Tr("Свой адрес"), Kind: "text",
				ShowWhen: "provider", ShowValue: ProviderOpenAI,
				Placeholder: "https://api.example.com/v1",
				Hint:        i18n.Tr("Пусто — адрес из строки выше. Заполнено — главнее её.")},
			{Key: "model", Label: i18n.Tr("Модель"), Kind: "text",
				ShowWhen: "provider", ShowValue: ProviderOpenAI,
				Placeholder: "gpt-5",
				Hint:        i18n.Tr("Ровно как её зовёт провайдер: у Ollama — как в `ollama list`.")},
			{Key: "json_mode", Label: i18n.Tr("Как просить разметку"), Kind: "select",
				ShowWhen: "provider", ShowValue: ProviderOpenAI,
				Options: []ChannelOption{
					{Value: "auto", Label: i18n.Tr("Подобрать самому"),
						Hint: i18n.Tr("Сначала строгой схемой, при отказе мягче. Стоит одного лишнего запроса.")},
					{Value: "schema", Label: i18n.Tr("Строгой схемой"),
						Hint: i18n.Tr("Так умеют OpenAI, Groq и большинство новых моделей.")},
					{Value: "object", Label: i18n.Tr("Просто просить JSON")},
					{Value: "prompt", Label: i18n.Tr("Только словами"),
						Hint: i18n.Tr("Для совсем старых моделей и самодельных серверов.")},
				}},
			{Key: "codex_model", Label: i18n.Tr("Модель"), Kind: "text",
				ShowWhen: "provider", ShowValue: ProviderCodex,
				Hint: i18n.Tr("Пусто — та, что у codex по умолчанию.")},
		},
		summary: func(v map[string]string) string {
			switch v["provider"] {
			case ProviderOpenAI:
				where := v["base_url"]
				if where == "" {
					if p, ok := OpenAIPresetByKey(v["preset"]); ok {
						where = p.Name
					}
				}
				return strings.TrimSpace(FirstNonEmpty(v["model"], i18n.Tr("модель не выбрана")) +
					" · " + where)
			case ProviderCodex:
				return strings.TrimSpace("codex exec " + v["codex_model"])
			case ProviderCommand:
				// Путь к скрипту здесь не показывается и не правится: панель
				// открыта всей команде по общему паролю, и поле, из которого
				// сервис запускает команду, — это чужой шелл на этой машине.
				// Задаётся он в `steno setup`, как и всё, чего в панели нет.
				return i18n.Tr("свой скрипт")
			}
			// Модель Claude сюда не подставляется: её в этой форме нет, и
			// показать в списке то, чего в форме не окажется, — обещание,
			// которого раздел не сдержит.
			return "Claude"
		},
		read: func(c *Config) (bool, map[string]string) {
			return true, map[string]string{
				"provider":    c.BrainProvider(),
				"preset":      c.Brain.OpenAI.Preset,
				"base_url":    c.Brain.OpenAI.BaseURL,
				"model":       c.Brain.OpenAI.Model,
				"json_mode":   FirstNonEmpty(c.Brain.OpenAI.JSONMode, "auto"),
				"codex_model": c.Brain.Codex.Model,
			}
		},
		apply: func(c *Config, _ bool, v map[string]string) {
			// Выключателя у этого раздела нет: разбор — то, ради чего steno
			// вообще существует, и «выключить» его значит писать созвоны в
			// стол. Поэтому первый аргумент здесь не используется.
			c.Brain.Provider = strings.TrimSpace(v["provider"])
			c.Brain.OpenAI.Preset = strings.TrimSpace(v["preset"])
			c.Brain.OpenAI.BaseURL = strings.TrimSpace(v["base_url"])
			c.Brain.OpenAI.Model = strings.TrimSpace(v["model"])
			c.Brain.OpenAI.JSONMode = strings.TrimSpace(v["json_mode"])
			c.Brain.Codex.Model = strings.TrimSpace(v["codex_model"])
			// Имя переменной с ключом в панели не показывается и оттуда не
			// приходит — сохраняем то, что уже задано конфигом. Иначе правка
			// модели в панели молча обнуляла бы ключ.
			c.Brain.OpenAI.APIKeyEnv = chKept(v, "api_key_env", c.Brain.OpenAI.APIKeyEnv)
		},
	}
}

// ChannelDefs — разделы настройки: встроенные каналы и адаптеры публикации.
// Встроенные собираются один раз, а адаптеры приходят из настройки — их
// сколько угодно, и имена у них свои, поэтому постоянным список быть не может.
//
// Параметр вариативный не для красоты: ChannelByKey зовут из панели и из
// терминала, и конфига у них в этот момент нет.
func ChannelDefs(cfg ...*Config) []channelDef {
	defs := append([]channelDef(nil), builtinChannelDefs()...)
	if len(cfg) > 0 && cfg[0] != nil {
		for _, t := range cfg[0].Publish.Targets {
			defs = append(defs, publishCmdDef(PublishTargetName(t)))
		}
	}
	return defs
}

const publishCmdPrefix = "cmd:"

// PublishTargetName — имя адресата: своё, если задано, иначе из команды. То же
// правило, что в publish.CommandName; повторено здесь, потому что core не может
// импортировать publish — publish импортирует core.
func PublishTargetName(t PublishTarget) string {
	if n := strings.TrimSpace(t.Name); n != "" {
		return n
	}
	if len(t.Cmd) == 0 {
		return "cmd"
	}
	base := filepath.Base(strings.TrimSpace(t.Cmd[0]))
	base = strings.TrimSuffix(base, filepath.Ext(base))
	switch base {
	case "", ".", "..":
		return "cmd"
	}
	return base
}

// publishCmdDef — форма одного адаптера публикации. Она одинаковая у всех, а
// ключ несёт имя, поэтому определение собирается по ключу целиком: так строку
// «cmd:discord» находит и ChannelByKey, у которого конфига нет.
func publishCmdDef(name string) channelDef {
	return channelDef{
		key: publishCmdPrefix + name, Name: name, out: true, Live: true,
		about: i18n.Tr("Свой адаптер: команда, которая получает созвон на stdin и публикует его куда угодно. ") +
			i18n.Tr("Заводится строкой в publish.targets, а здесь включается и выключается."),
		Fields: []ChannelField{
			{Key: "cmd", Label: i18n.Tr("Команда"), Kind: "text",
				Placeholder: "./adapters/discord.sh",
				Hint:        i18n.Tr("Аргументы через пробел. Свои ключи адаптер берёт из .env — здесь секретов нет.")},
			{Key: "timeout", Label: i18n.Tr("Сколько ждать"), Kind: "duration", Placeholder: "2m"},
		},
		summary: func(v map[string]string) string { return v["cmd"] },
		read: func(c *Config) (bool, map[string]string) {
			for _, t := range c.Publish.Targets {
				if PublishTargetName(t) == name {
					return t.On(), map[string]string{
						"cmd":     strings.Join(t.Cmd, " "),
						"timeout": ChDurText(t.Timeout),
					}
				}
			}
			return false, map[string]string{}
		},
		apply: func(c *Config, on bool, v map[string]string) {
			for i, t := range c.Publish.Targets {
				if PublishTargetName(t) == name {
					c.Publish.Targets[i].Enabled = &on
					if cmd := strings.Fields(v["cmd"]); len(cmd) > 0 {
						c.Publish.Targets[i].Cmd = cmd
					}
					c.Publish.Targets[i].Timeout = chDur(v["timeout"], t.Timeout)
					return
				}
			}
		},
	}
}

func ChannelByKey(key string) (channelDef, bool) {
	for _, d := range ChannelDefs() {
		if d.key == key {
			return d, true
		}
	}
	// Адаптеров публикации в постоянном списке нет: их имена задаёт человек.
	// Форма у всех одна, и по ключу «cmd:discord» она собирается целиком.
	if n, ok := strings.CutPrefix(key, publishCmdPrefix); ok && n != "" {
		return publishCmdDef(n), true
	}
	return channelDef{}, false
}

// --- хранилище --------------------------------------------------------------

type storedChannel struct {
	Enabled bool
	Values  map[string]string
}

func (s *Store) ChannelSettings() (map[string]storedChannel, error) {
	rows, err := s.DB.Query(`SELECT key, enabled, settings FROM channels`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]storedChannel{}
	for rows.Next() {
		var key, settings string
		var on int
		if err := rows.Scan(&key, &on, &settings); err != nil {
			return nil, err
		}
		v := map[string]string{}
		_ = json.Unmarshal([]byte(settings), &v)
		out[key] = storedChannel{Enabled: on == 1, Values: v}
	}
	return out, rows.Err()
}

func (s *Store) SaveChannel(key string, enabled bool, values map[string]string) error {
	b, _ := json.Marshal(values)
	on := 0
	if enabled {
		on = 1
	}
	_, err := s.DB.Exec(`INSERT INTO channels (key,enabled,settings,updated_at) VALUES (?,?,?,?)
		ON CONFLICT(key) DO UPDATE SET enabled=excluded.enabled, settings=excluded.settings,
		updated_at=excluded.updated_at`, key, on, string(b), time.Now().Unix())
	return err
}

// ImportChannels переносит каналы из конфига в базу при первом запуске: у тех,
// кто уже описал их файлом, ничего не пропадёт.
func ImportChannels(st *Store, cfg *Config) (int, error) {
	have, err := st.ChannelSettings()
	if err != nil {
		return 0, err
	}
	if len(have) > 0 {
		return 0, nil // база уже главнее конфига
	}
	n := 0
	for _, d := range ChannelDefs(cfg) {
		on, values := d.read(cfg)
		if err := st.SaveChannel(d.key, on, values); err != nil {
			return 0, err
		}
		// Считаем только каналы: про них сервис пишет «перенёс N каналов», и
		// раздел «Разбор», уехавший тем же путём, каналом от этого не стал.
		if d.Kind() == ChannelKindIO {
			n++
		}
	}
	return n, nil
}

// ApplyChannels накладывает настройки из базы на конфиг на месте. Годится
// только на старте, пока конфиг ещё никто не читает параллельно.
func ApplyChannels(st *Store, cfg *Config) error {
	have, err := st.ChannelSettings()
	if err != nil {
		return err
	}
	for _, d := range ChannelDefs(cfg) {
		if row, ok := have[d.key]; ok {
			d.apply(cfg, row.Enabled, row.Values)
		}
	}
	return nil
}

// ActiveChannels возвращает КОПИЮ конфига со свежими настройками каналов.
// Копию, а не правку на месте: конфиг читают несколько горутин сразу, и менять
// его под ними — гонка. Зато канал, включённый в панели, начинает работать без
// перезапуска: перед каждой рассылкой настройки перечитываются.
func ActiveChannels(st *Store, cfg *Config) *Config {
	c := *cfg
	if err := ApplyChannels(st, &c); err != nil {
		return cfg
	}
	return &c
}

// PanelChannels — то, что видит панель: описание полей и текущие значения.
//
// Наружу уходят только описанные поля. Не для красоты: в базе у тех, кто уже
// пользуется steno, лежат значения полей, которые из формы убраны, — путь к
// ключу организации например. Отдавать их панели ровно тем и было бы плохо, что
// человек их там увидел бы.
func PanelChannels(st *Store, cfg *Config) []Channel {
	have, _ := st.ChannelSettings()
	out := make([]Channel, 0, len(ChannelDefs(cfg)))
	for _, d := range ChannelDefs(cfg) {
		on, stored := d.read(cfg)
		if row, ok := have[d.key]; ok {
			on = row.Enabled
			// Значения из базы кладём поверх конфига, а не вместо: поле,
			// добавленное в новой версии, иначе показывалось бы пустым, хотя в
			// конфиге у него есть значение.
			for k, v := range row.Values {
				stored[k] = v
			}
		}
		values := map[string]string{}
		for _, f := range d.Fields {
			if f.Input() {
				values[f.Key] = stored[f.Key]
			}
		}
		ch := Channel{
			Key: d.key, Name: d.Name, About: d.about, Kind: d.Kind(), In: d.in, Out: d.out,
			Enabled: on, Values: values, Fields: d.Fields, Live: d.Live,
		}
		if d.summary != nil {
			ch.Summary = d.summary(values)
		}
		out = append(out, ch)
	}
	return out
}
