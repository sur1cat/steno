package publish

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sur1cat/steno/internal/brain"
	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
)

// Публикация вынесена за границу процесса тем же швом, что и расшифровка:
// адаптер — это любая команда, которая получает созвон на stdin и печатает в
// stdout ссылку на опубликованное.
//
//	stdin   один JSON: созвон, разбор, готовый текст, ссылки (см. Payload)
//	stdout  ссылка на опубликованное — и больше ничего. Ссылки нет — пусто
//	код 0   опубликовано; любой другой код — не опубликовано
//	stderr  всё остальное: что делает адаптер и почему не вышло
//
// Так Discord, Mattermost, Notion, вебхук, почта, файл в папке и задача в Jira
// заводятся десятком строк на bash, не трогая ни строки Go, — ровно как
// whisper.cpp, faster-whisper и Groq заводятся адаптером расшифровки.
//
// Три решения, которые стоит объяснить, потому что переиграть их потом нельзя.
//
// Данные идут на stdin, а не аргументами, как {{audio}} у расшифровки. Путь к
// файлу — это тридцать байт, а follow-up часового созвона вместе с расшифровкой
// — сотня килобайт: аргументами он упирается в ARG_MAX (на macOS это 256 КБ на
// всё окружение разом) и разваливается не на нашей машине, а на чужой и на
// самом длинном созвоне месяца. Заодно исчезает вопрос про кавычки: название
// созвона с апострофом ничего не ломает, потому что оно не проходит через
// разбор командной строки. Аргументы у адаптера остаются свои — ими задают
// адресата: ["./adapters/notes-to-file.sh", "~/steno/notes"].
//
// Окружением не уходит ничего, кроме STENO_LANG (на нём адаптер говорит с
// человеком — как у расшифровки). Разложить те же поля ещё и по переменным
// значило бы завести второе место, где они однажды разойдутся с первым, и
// придумать правила для списка участников и для многострочного текста.
//
// Наследует адаптер при этом всё окружение сервиса, включая .env: иначе ему
// неоткуда взять свой DISCORD_WEBHOOK_URL. Это то же доверие, что и у адаптера
// расшифровки, и его стоит понимать буквально — команда из publish.targets
// видит ключ Claude и токен Telegram. Своё в adapters/ клади так же осознанно,
// как в crontab.
//
// Адаптер получает и сырой разбор, и готовый текст. Это не жадность, а
// единственная форма, при которой обещание «пять строк bash» остаётся правдой
// для обоих видов адаптеров. Тому, кто шлёт follow-up в чат, нужен готовый
// текст: собирать его из JSON — это полсотни строк jq вместо пяти. Тому, кто
// заводит задачу в трекере на каждый пункт, готовый текст бесполезен, ему нужны
// поля — и разбирать нашу разметку назад он не должен. Оба лежат в одном JSON и
// стоят лишних несколько килобайт на stdin, то есть ничего.

// Payload — то, что адаптер читает со stdin.
type Payload struct {
	Meeting  PayloadMeeting `json:"meeting"`
	Followup core.Followup  `json:"followup"`
	Text     PayloadText    `json:"text"`
	// Links — куда этот же созвон уже уехал к моменту вызова адаптера. Сейчас
	// это "google_doc", если документ создался: с ним follow-up в чате может
	// быть коротким и вести на полные заметки.
	Links map[string]string `json:"links,omitempty"`
	// Расшифровка целиком, с таймкодами и именами. Единственное, чего адаптеру
	// больше неоткуда взять; тому, кто складывает созвоны файлами, нужна именно
	// она.
	Transcript string `json:"transcript,omitempty"`
}

// PayloadMeeting — сам созвон. Поля именно те, которых не хватает готовому
// тексту: по id адаптер находит созвон в панели, по projects раскладывает
// follow-up по каналам команд, по url возвращается к записи.
type PayloadMeeting struct {
	ID string `json:"id"`
	// Название из календаря. Название, которое дала созвону модель, лежит в
	// followup.title и обычно точнее — а в готовом тексте уже стоит оно.
	Title string `json:"title"`
	// Ссылка на сам созвон.
	URL          string   `json:"url"`
	StartedAt    string   `json:"started_at"`
	EndedAt      string   `json:"ended_at,omitempty"`
	DurationSec  int      `json:"duration_sec,omitempty"`
	Participants []string `json:"participants,omitempty"`
	// Проекты, которых созвон коснулся, — из пунктов разбора.
	Projects []string `json:"projects,omitempty"`
}

// PayloadText — один и тот же follow-up в трёх видах.
//
// Здесь нет разметки Telegram и Slack, и это намеренно: туда steno публикует
// сам, и адаптеру, который повторял бы встроенный канал, взяться неоткуда.
// Markdown — общий язык Discord, Mattermost, Notion, GitHub и почтовых
// рассылок; plain — для файла и SMS-подобного; html — для почты и Confluence.
type PayloadText struct {
	Plain    string `json:"plain"`
	Markdown string `json:"markdown"`
	HTML     string `json:"html"`
}

// Command — один адаптер публикации.
// Commands — включённые адаптеры из настройки.
func Commands(cfg *core.Config) []Command {
	var out []Command
	for _, t := range cfg.Publish.Targets {
		if !t.On() || len(t.Cmd) == 0 {
			continue
		}
		out = append(out, Command{Name: t.Name, Cmd: t.Cmd, Timeout: t.Timeout.D()})
	}
	return out
}

type Command struct {
	// Name — под каким именем публикация ложится в publications.target и
	// печатается в лог. Пусто — имя берётся из команды: discord.sh → discord.
	// Имена обязаны различаться: в publications ключ — (созвон, адресат), и
	// два адаптера с одним именем затирали бы ссылку друг друга.
	Name string
	// Cmd — команда и её аргументы, как в transcribe.cmd.
	Cmd     []string
	Timeout time.Duration
}

// Сколько ждать адаптер. Публикация — это один-два запроса по сети, и минуты
// на них хватает с запасом; потолок нужен не ради скорости, а чтобы повисший
// адаптер не держал созвон в статусе «не разослан» до перезапуска сервиса.
const DefaultCommandTimeout = 2 * time.Minute

// Ссылка длиннее этого — не ссылка, а вывод, который адаптер забыл увести в
// stderr. В базу такое пускать незачем.
const maxLinkLen = 2000

// Target — имя, под которым адаптер виден человеку.
func (c Command) Target() string {
	if n := strings.TrimSpace(c.Name); n != "" {
		return n
	}
	return CommandName(c.Cmd)
}

// CommandName — имя адаптера по его команде: ./adapters/discord.sh → discord.
func CommandName(cmd []string) string {
	if len(cmd) == 0 {
		return "cmd"
	}
	base := filepath.Base(strings.TrimSpace(cmd[0]))
	base = strings.TrimSuffix(base, filepath.Ext(base))
	switch base {
	case "", ".", "..", string(filepath.Separator):
		return "cmd"
	}
	return base
}

// NewPayload собирает то, что уедет адаптеру на stdin.
func NewPayload(m *core.Meeting, f *core.Followup, segs []core.Segment, docURL string) *Payload {
	if f == nil {
		f = &core.Followup{}
	}
	p := &Payload{
		Meeting: PayloadMeeting{
			ID:           m.ID,
			Title:        m.Title,
			URL:          m.MeetURL,
			StartedAt:    m.StartedAt.Format(time.RFC3339),
			Participants: m.Participants,
			Projects:     projectsOf(f),
		},
		Followup: *f,
		Text: PayloadText{
			Plain:    RenderPlain(m, f),
			Markdown: RenderMarkdown(m, f, docURL),
			HTML:     RenderHTML(m, f, segs),
		},
		Transcript: brain.RenderTranscript(segs),
	}
	if m.EndedAt != nil {
		p.Meeting.EndedAt = m.EndedAt.Format(time.RFC3339)
		if d := m.EndedAt.Sub(m.StartedAt); d > 0 {
			p.Meeting.DurationSec = int(d.Seconds())
		}
	}
	if docURL != "" {
		p.Links = map[string]string{"google_doc": docURL}
	}
	return p
}

// projectsOf — проекты, которых созвон коснулся. Отдельным полем, а не
// вычислением на стороне адаптера: пункты разбора лежат в трёх списках, и
// собирать из них список проектов на jq в каждом адаптере заново — работа,
// которую мы уже сделали.
func projectsOf(f *core.Followup) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p = strings.TrimSpace(p); p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, a := range f.ActionItems {
		add(a.Project)
	}
	for _, d := range f.Decisions {
		add(d.Project)
	}
	for _, q := range f.OpenQuestions {
		add(q.Project)
	}
	return out
}

// RunCommand зовёт один адаптер. Возвращает ссылку на опубликованное и то, что
// адаптер написал в stderr, — ровно как RunTranscriber: там человек объясняет,
// что происходило, и без этого отказ выглядит как пустой код возврата.
func RunCommand(ctx context.Context, c Command, p *Payload) (link, notes string, err error) {
	if len(c.Cmd) == 0 {
		return "", "", errors.New(i18n.Tr("у адаптера публикации не задана команда"))
	}
	body, err := json.Marshal(p)
	if err != nil {
		return "", "", err
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultCommandTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := append([]string(nil), c.Cmd...)
	args[0] = core.AdapterPath(args[0])
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdin = bytes.NewReader(body)
	// Адаптер печатает диагностику человеку и потому говорит на языке steno.
	// Передаём всегда: язык мог прийти из конфига, а не из окружения.
	cmd.Env = append(os.Environ(), "STENO_LANG="+i18n.UILang)
	// По таймауту гасим всю группу мягко: адаптер — это обёртка вокруг curl,
	// и SIGKILL оставил бы за ней временные файлы и осиротевший запрос.
	core.SetProcessGroup(cmd)
	cmd.Cancel = func() error { return core.TerminateGroup(cmd) }
	cmd.WaitDelay = 10 * time.Second

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	notes = strings.TrimSpace(stderr.String())
	if runErr != nil {
		return "", notes, commandError(ctx, args, timeout, runErr, notes)
	}
	link, junk := linkFrom(stdout.String())
	if junk != "" {
		// Не отказ: адаптер отработал и вернул ноль. Но молчать нельзя — иначе
		// «ссылка не появилась» выглядит как поломка steno, а не как адаптер,
		// печатающий в stdout свой лог.
		notes = strings.TrimSpace(notes + "\n" +
			fmt.Sprintf(i18n.Tr("в stdout не ссылка, а %q — по договору туда идёт только она"), junk))
	}
	return link, notes, nil
}

// commandError переводит отказ запуска в то, что человек сможет починить.
// «exit status 1» без единого слова о том, чего не хватило, — самый частый
// способ потратить полчаса на опечатку в пути.
func commandError(ctx context.Context, args []string, timeout time.Duration, err error, notes string) error {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return fmt.Errorf(i18n.Tr("адаптер публикации %v не уложился в %s"), args, timeout)
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf(i18n.Tr("адаптера публикации нет: %s"), args[0])
	case errors.Is(err, fs.ErrPermission):
		return fmt.Errorf(i18n.Tr("адаптер публикации %s без права на запуск: chmod +x %s"), args[0], args[0])
	}
	if notes != "" {
		return fmt.Errorf(i18n.Tr("адаптер публикации %v: %w\n%s"), args, err, i18n.Tail(notes, 800))
	}
	return fmt.Errorf(i18n.Tr("адаптер публикации %v: %w — и ни слова в stderr"), args, err)
}

// linkFrom достаёт ссылку из stdout. Всё, что не похоже на ссылку, возвращается
// вторым значением: договор нарушен, но публикация состоялась, и разница между
// «отказ» и «адаптер болтлив» должна быть видна.
func linkFrom(out string) (link, junk string) {
	out = strings.TrimSpace(out)
	if out == "" {
		return "", ""
	}
	last := core.LastLines(out, 1)
	if len(last) == 0 {
		return "", ""
	}
	l := last[0]
	if (strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "http://")) &&
		!strings.ContainsAny(l, " \t") && len([]rune(l)) <= maxLinkLen {
		return l, ""
	}
	return "", i18n.Cut(out, 200)
}

// PublishCommands прогоняет адаптеры по очереди и записывает каждый в
// publications под своим именем.
//
// По очереди, а не разом: адаптеры пишут в один лог, и вперемешку его не
// прочитать. Отказ одного не отменяет остальных — то же правило, что и у
// встроенных адресатов в PublishAll: опубликовать в двух местах из трёх лучше,
// чем нигде.
func PublishCommands(ctx context.Context, st *core.Store, lg *log.Logger, cmds []Command, p *Payload) []error {
	var errs []error
	for _, c := range cmds {
		target := c.Target()
		link, notes, err := RunCommand(ctx, c, p)
		for _, line := range core.LastLines(notes, 4) {
			lg.Printf("  %s", line)
		}
		record(st, lg, p.Meeting.ID, target, link, err)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", target, err))
		}
	}
	return errs
}
