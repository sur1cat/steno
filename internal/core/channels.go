package core

import (
	"encoding/json"
	"fmt"
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
	// text | list | number | duration | switch | google
	Kind        string `json:"kind"`
	Hint        string `json:"hint"`
	Placeholder string `json:"placeholder"`
}

// Input — хранит ли поле значение. «google» — не поле, а кнопка: доступ в
// Google лежит отдельным файлом токена, и класть его копию в настройку канала
// значило бы завести второе место, где он может разойтись с первым.
func (f ChannelField) Input() bool { return f.Kind != "google" }

// Channel — канал в том виде, в каком его правит человек в панели.
type Channel struct {
	Key     string            `json:"key"`
	Name    string            `json:"name"`
	About   string            `json:"about"`
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
	Fields           []ChannelField
	summary          func(map[string]string) string
	read             func(*Config) (bool, map[string]string)
	apply            func(*Config, bool, map[string]string)
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
var ChannelDefs = sync.OnceValue(func() []channelDef {
	return []channelDef{
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

func ChannelByKey(key string) (channelDef, bool) {
	for _, d := range ChannelDefs() {
		if d.key == key {
			return d, true
		}
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
	for _, d := range ChannelDefs() {
		on, values := d.read(cfg)
		if err := st.SaveChannel(d.key, on, values); err != nil {
			return 0, err
		}
	}
	return len(ChannelDefs()), nil
}

// ApplyChannels накладывает настройки из базы на конфиг на месте. Годится
// только на старте, пока конфиг ещё никто не читает параллельно.
func ApplyChannels(st *Store, cfg *Config) error {
	have, err := st.ChannelSettings()
	if err != nil {
		return err
	}
	for _, d := range ChannelDefs() {
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
	out := make([]Channel, 0, len(ChannelDefs()))
	for _, d := range ChannelDefs() {
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
			Key: d.key, Name: d.Name, About: d.about, In: d.in, Out: d.out,
			Enabled: on, Values: values, Fields: d.Fields, Live: d.Live,
		}
		if d.summary != nil {
			ch.Summary = d.summary(values)
		}
		out = append(out, ch)
	}
	return out
}
