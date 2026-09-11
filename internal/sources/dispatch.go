package sources

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/sur1cat/steno/internal/bot"
	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
	"github.com/sur1cat/steno/internal/pipeline"
)

// Dispatcher — общая точка, через которую созвон попадает в работу, откуда бы
// про него ни узнали: из календаря, из Telegram, из почты бота или по HTTP.
// Он же держит два инварианта: на один созвон заводится один бот, и больше
// MaxConcurrent записей одновременно не идёт.
type Dispatcher struct {
	cfg *core.Config
	st  *core.Store
	log *log.Logger
	sem chan struct{}

	// Что делать с принятым созвоном. Отдельным полем — чтобы тесты
	// дедупликации не тащили за собой запись: она поднимает docker или, при
	// bot.local, настоящий Chrome.
	Run func(ctx context.Context, cfg *core.Config, st *core.Store, m *core.Meeting) error
}

func NewDispatcher(cfg *core.Config, st *core.Store, lg *log.Logger) *Dispatcher {
	n := cfg.Calendar.MaxConcurrent
	if n < 1 {
		n = 4
	}
	return &Dispatcher{cfg: cfg, st: st, log: lg, sem: make(chan struct{}, n),
		Run: pipeline.RecordAndProcess}
}

// StartResult различает три исхода. Раньше все три были одним false, и
// человеку, чей созвон пропустили из-за нехватки места, отвечали «я уже иду».
type StartResult int

const (
	Started    StartResult = iota // бот пошёл
	Duplicate                     // на этот созвон уже идут
	NoCapacity                    // все слоты заняты, вернёмся позже
	StartError                    // не смогли записать в базу
)

// StartOnce — то же, что Start, но для источников, которые зовут сами и по
// кругу: событие, на которое бот уже ходил, не считается снова, как бы тот
// заход ни кончился. Иначе календарный опрос звал бы в пустую комнату каждые
// две минуты до конца события.
func (d *Dispatcher) StartOnce(ctx context.Context, key string, m *core.Meeting, why string) StartResult {
	if been, err := d.st.EventAttempted(key); err != nil {
		d.log.Printf(i18n.Tr("проверка «%s»: %v"), key, err)
		return StartError
	} else if been {
		return Duplicate
	}
	return d.Start(ctx, key, m, why)
}

// Start заводит бота на созвон. key должен быть одинаковым для одного и того
// же созвона, как бы про него ни узнали, — иначе на встречу придут два бота.
func (d *Dispatcher) Start(ctx context.Context, key string, m *core.Meeting, why string) StartResult {
	if seen, err := d.st.EventSeen(key); err != nil {
		d.log.Printf(i18n.Tr("проверка «%s»: %v"), key, err)
		return StartError
	} else if seen {
		return Duplicate
	}
	// Слот берём до отметки: иначе созвон, которому не хватило места, был бы
	// помечен виденным и не вернулся бы уже никогда — даже когда место есть.
	select {
	case d.sem <- struct{}{}:
	default:
		d.log.Printf(i18n.Tr("пропускаю «%s»: уже пишу %d созвонов"), core.OrDash(m.Title), cap(d.sem))
		return NoCapacity
	}
	// Занять ключ и убедиться, что его занял именно ты, — это один шаг.
	// Проверка выше только экономит работу; решает эта вставка.
	claimed, err := d.st.MarkEventSeen(key, m.ID)
	if err != nil {
		d.log.Printf(i18n.Tr("не смог отметить «%s»: %v"), key, err)
		<-d.sem
		return StartError
	}
	if !claimed {
		<-d.sem
		return Duplicate
	}

	// Запись живёт своим контекстом. Если отдать ей ctx сервиса, SIGTERM убьёт
	// контейнер посреди созвона — ровно то, чего WaitIdle обещает не делать.
	// Свой дедлайн всё равно нужен, иначе зависший бот держал бы слот вечно.
	recCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx),
		d.cfg.Bot.MaxDuration.D()+d.cfg.Transcribe.Timeout.D()+30*time.Minute)

	go func() {
		defer cancel()
		defer func() { <-d.sem }()
		d.log.Printf(i18n.Tr("иду на «%s» (%s, повод: %s)"), core.OrDash(m.Title), m.MeetURL, why)
		started := time.Now()
		err := d.Run(recCtx, d.cfg, d.st, m)
		if err == nil {
			return
		}
		d.log.Printf("«%s»: %v", core.OrDash(m.Title), err)

		// Провал за первые секунды — это отказ инфраструктуры: не нашёлся
		// docker, нет образа, не создалась папка. Бот при этом никуда не
		// ходил, и созвон стоит попробовать снова. Снимаем отметку, чтобы
		// источник предложил его на следующем опросе.
		//
		// Провал после — это уже неудачная попытка попасть в звонок, и
		// повторять её нельзя: каждая новая попытка поднимает контейнер, а
		// сорваться она может все тридцать минут подряд.
		if time.Since(started) < 15*time.Second {
			if e := d.st.UnmarkEvent(key); e != nil {
				d.log.Printf(i18n.Tr("не снял отметку с «%s»: %v"), key, e)
			} else {
				d.log.Printf(i18n.Tr("«%s»: попробую ещё раз на следующем опросе"), core.OrDash(m.Title))
			}
		}
	}()
	return Started
}

// WaitIdle держит процесс, пока не закончатся идущие записи: обрывать запись
// на середине хуже, чем подождать. Но не бесконечно — иначе `systemctl
// restart` посреди четырёхчасового созвона повис бы на четыре часа.
//
// Если ждать надоело, процесс уходит с работающими записями. Потерь при этом
// меньше, чем кажется: audio.ogg лежит на примонтированном томе, и созвон
// доводится до конца через `steno process <id>`.
func (d *Dispatcher) WaitIdle(grace time.Duration) {
	done := make(chan struct{})
	go func() {
		for i := 0; i < cap(d.sem); i++ {
			d.sem <- struct{}{}
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(grace):
		d.log.Printf(i18n.Tr("не дождался записей за %s — выхожу; незавершённые созвоны ")+
			i18n.Tr("доводятся командой `steno process <id>`"), grace)
	}
}

// Ключи всех источников считаются здесь и в одном формате. Раньше календарь
// складывал ключ по-своему — с секундами и с сырым URL из события, — поэтому
// с ад-хок ключом он не мог совпасть в принципе: встречу из календаря и её же
// ссылку, брошенную в Telegram, записывали два бота.
func meetingKey(meetURL string, bucket string) string {
	canon := bot.FindMeetURL(meetURL)
	if canon == "" {
		// Сюда не должно доходить — все источники проверяют ссылку заранее.
		// Но если дойдёт, пустой ключ склеил бы разные созвоны в один.
		canon = strings.ToLower(strings.TrimSpace(meetURL))
	}
	return strings.ToLower(canon) + "@" + bucket
}

// plannedKey — для встречи из календаря: у неё есть точное время начала.
func plannedKey(meetURL string, start time.Time) string {
	return meetingKey(meetURL, start.UTC().Format("2006-01-02T15:04Z"))
}

// adHocKey — для звонка, о котором узнали не из календаря. Точного времени у
// него нет, поэтому округляем до получаса: повторная ссылка на тот же созвон
// не приведёт второго бота, а завтрашняя встреча в той же комнате — приведёт.
func adHocKey(meetURL string, at time.Time) string {
	return meetingKey(meetURL, at.UTC().Truncate(30*time.Minute).Format("2006-01-02T15:04Z"))
}
