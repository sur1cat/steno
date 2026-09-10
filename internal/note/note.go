package note

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sur1cat/steno/internal/audio"
	"github.com/sur1cat/steno/internal/brain"
	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
	"github.com/sur1cat/steno/internal/publish"
)

// Надиктованная заметка — второй вход в тот же конвейер.
//
// Человек нажимает кнопку, наговаривает мысли и задачи, отпускает — и получает
// их разобранными ровно так же, как после созвона. Ни бота, ни докера, ни
// Google, ни второго участника: только микрофон.
//
// Заметка хранится той же сущностью, что и встреча (Meeting). Это не экономия
// на таблице, а способ получить даром всё, что уже написано: задачи, проекты,
// поиск по расшифровке, плеер с таймкодами, публикация. Ни одна из этих частей
// про заметки не знает и знать не должна.
//
// Отличает заметку от созвона ссылка: у встречи там адрес комнаты, у заметки —
// noteURL. Поле meet_url объявлено NOT NULL и у заметки всё равно чем-то
// занято; выбранное значение ни на что не похоже, никуда не ведёт и не
// проходит проверку FindMeetURL — то есть бот по нему никуда не уйдёт.
const noteURL = "steno://note"

// Что попадёт в left_reason. Панель показывает это в строке созвона, и «бот
// вышел, потому что остался один» там читается как объяснение обрезанной
// записи; у заметки на этом месте должно стоять, откуда она взялась.
const noteLeftReason = "надиктовано в микрофон"

const defaultNoteAuthor = "Я"

// IsNote — заметка это или созвон. Единственное место, где знание о том, чем
// они различаются в базе, записано словами.
func IsNote(m *core.Meeting) bool { return m != nil && m.MeetURL == noteURL }

// NoteOptions — с чем заводится запись.
type NoteOptions struct {
	Title  string
	Author string
	Device string        // индекс или имя микрофона; пусто — системный по умолчанию
	Max    time.Duration // потолок; 0 — noteMaxDefault
	// Куда ответить, если попросили не из панели. Работает даром: этим
	// занимается publishToOrigin.
	ReplyTo   core.ReplyTo
	NoPublish bool
}

// NoteSession — одна идущая запись.
type NoteSession struct {
	ID      string
	Title   string
	Author  string
	Device  string
	Started time.Time
	Until   time.Time // когда сработает потолок

	rec  *audio.Recorder
	opt  NoteOptions
	stop sync.Once
	// Закрывается, когда заметка перестала быть идущей, — по нему сторож
	// остановки понимает, что его работа больше не нужна.
	closed chan struct{}
	once   sync.Once
}

func (s *NoteSession) done() {
	s.once.Do(func() { close(s.closed) })
}

func (s *NoteSession) Elapsed() time.Duration { return time.Since(s.Started) }

// NoteHub — идущая заметка. Одна на процесс: микрофон один, и человек,
// наговаривающий две заметки сразу, — это не сценарий, а промах по кнопке.
//
// Живёт отдельной переменной, а не полем Panel, потому что запись зовут из
// двух мест: из панели (кнопка в строке меню) и из терминала (steno note).
// Оба живут в одном процессе не одновременно, но код у них общий.
type NoteHub struct {
	mu  sync.Mutex
	cur *NoteSession

	// Чем писать звук и что делать с записанным. Отдельными полями — чтобы
	// тесты состояния не открывали микрофон и не звали Claude; ровно тот же
	// приём, что у Dispatcher.run в dispatch.go.
	Record  func(path, device string, max time.Duration) (*audio.Recorder, error)
	Process func(ctx context.Context, cfg *core.Config, st *core.Store, id string, noPublish bool) error
}

var Notes = &NoteHub{}

func (h *NoteHub) recorder() func(string, string, time.Duration) (*audio.Recorder, error) {
	if h.Record != nil {
		return h.Record
	}
	return audio.StartMicRecording
}

func (h *NoteHub) processor() func(context.Context, *core.Config, *core.Store, string, bool) error {
	if h.Process != nil {
		return h.Process
	}
	return ProcessNote
}

// Live — идущая заметка или nil.
func (h *NoteHub) Live() *NoteSession {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cur
}

// Start заводит запись: строка в базе, ffmpeg на микрофоне, сторож потолка.
//
// Строка заводится до того, как пошёл звук, и сразу в статусе recording — по
// нему идущую запись видят и панель, и приложение в строке меню (оно смотрит
// прямо в базу, а не спрашивает сервис). Если микрофон не открылся, строка
// закрывается тут же с ошибкой: молча удалить её нельзя, человек нажал кнопку
// и должен увидеть, чем это кончилось.
func (h *NoteHub) Start(cfg *core.Config, st *core.Store, lg *log.Logger, o NoteOptions) (*NoteSession, error) {
	if cfg.Transcribe.Source == "captions" {
		return nil, errors.New(i18n.Tr("сейчас текст берётся из субтитров Meet, а у заметки их нет: ") +
			i18n.Tr("для записи с микрофона нужен whisper или Groq — поменяй это в настройках расшифровки"))
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cur != nil {
		return nil, fmt.Errorf(i18n.Tr("заметка уже пишется (%s) — сначала останови её"),
			h.cur.Elapsed().Round(time.Second))
	}

	// Заметки, оставшиеся «пишущимися» от умершего запуска, закрываем здесь:
	// раз в этом процессе идущей заметки нет, чужая живая запись невозможна —
	// сервис на одну настройку запускается один, это стережёт замок pid-файла.
	h.healStaleNotes(st, lg)

	now := time.Now()
	max := o.Max
	if max <= 0 {
		max = audio.NoteMaxDefault
	}
	s := &NoteSession{
		ID:      core.NewID(now),
		Title:   strings.TrimSpace(o.Title),
		Author:  noteAuthorName(o.Author),
		Device:  o.Device,
		Started: now,
		Until:   now.Add(max),
		opt:     o,
		closed:  make(chan struct{}),
	}
	if s.Title == "" {
		s.Title = noteTitle(now)
	}

	dir := st.RecordingDir(s.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	m := &core.Meeting{
		ID:        s.ID,
		Title:     s.Title,
		MeetURL:   noteURL,
		StartedAt: now,
		AudioPath: filepath.Join(dir, "audio.ogg"),
		// Участник ровно один — тот, кто говорит. Дальше это имя работает в
		// трёх местах сразу: подсказкой распознаванию (MeetingVocabulary),
		// исполнителем задач (noteAuthor) и подписью в панели.
		Participants: []string{s.Author},
		Status:       "recording",
		ReplyTo:      o.ReplyTo,
	}
	if err := st.CreateMeeting(m); err != nil {
		return nil, err
	}

	rec, err := h.recorder()(m.AudioPath, o.Device, max)
	if err != nil {
		_ = st.FinishMeeting(s.ID, time.Time{}, time.Now(), m.Participants,
			"failed", err.Error(), noteLeftReason)
		return nil, err
	}
	s.rec = rec
	h.cur = s
	lg.Printf(i18n.Tr("заметка %s: пишу с микрофона, потолок %s"), s.ID, max)

	go h.watch(cfg, st, lg, s, max)
	return s, nil
}

// Park закрывает идущую заметку, когда сервис останавливают.
//
// Зовётся из остановки сервиса, синхронно, и вот почему именно так. Сначала я
// сделал это подпиской на SIGTERM внутри заметки — и на живой проверке она
// проиграла: сервис уходит через три миллисекунды после сигнала, горутину
// просто не успевает выполнить планировщик. Такую работу нельзя делать
// «параллельно с выходом», её должен делать сам выход.
//
// Что здесь происходит: строка в базе закрывается, ffmpeg получает SIGINT и
// дописывает контейнер. Расшифровку не начинаем — процесс всё равно уходит, а
// начатая расшифровка умерла бы на середине. Записанное лежит на диске, и лог
// говорит, чем его доделать.
func (h *NoteHub) Park(st *core.Store, lg *log.Logger) {
	h.mu.Lock()
	s := h.cur
	h.cur = nil
	h.mu.Unlock()
	if s == nil {
		return
	}
	// Строка первой: она дороже. Незакрытая строка остаётся «пишется» навсегда,
	// а недописанный ffmpeg сам закончит по своему -t.
	_ = st.FinishMeeting(s.ID, time.Time{}, time.Now(), []string{s.Author},
		"recorded", "", i18n.Tr("сервис остановился во время заметки"))
	if s.rec != nil && s.rec.Cmd != nil && s.rec.Cmd.Process != nil {
		_ = s.rec.Cmd.Process.Signal(syscall.SIGINT)
	}
	s.stop.Do(func() {}) // Stop уже не нужен: ffmpeg попрощался
	s.done()
	lg.Printf(i18n.Tr("заметка %s: сервис останавливается — запись закрыл, разобрать её: steno note %s"),
		s.ID, s.ID)
}

// healStaleNotes закрывает заметки, оставшиеся «пишущимися» от прошлого
// запуска.
//
// Сюда попадают те, кого не спас Park: сервис убили по kill -9, машина ушла в
// перезагрузку, кончилось питание. Признак надёжный: заметку пишет процесс, и
// если в этом процессе идущей заметки нет, то строка в статусе «пишется» —
// след умершего. Не убираем её совсем: звук на диске лежит, и его ещё можно
// разобрать через `steno note <id>`.
func (h *NoteHub) healStaleNotes(st *core.Store, lg *log.Logger) {
	rows, err := st.DB.Query(
		`SELECT id FROM meetings WHERE meet_url=? AND status='recording' AND ended_at IS NULL`,
		noteURL)
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		_ = st.FinishMeeting(id, time.Time{}, time.Now(), nil,
			"failed", i18n.Tr("сервис остановился во время заметки"), noteLeftReason)
		lg.Printf(i18n.Tr("заметка %s осталась незакрытой от прошлого запуска — разобрать её: steno note %s"),
			id, id)
	}
}

// watch сторожит идущую запись: потолок и смерть ffmpeg.
//
// Потолок — про человека, забывшего нажать «стоп»: он получит разобранную
// заметку, а не час тишины и счёт за расшифровку.
//
// Смерть ffmpeg — про наушники, ушедшие из зоны, и про выдернутый микрофон.
// Без сторожа человек продолжает говорить, красная точка в строке меню
// продолжает тикать, а записи с какого-то момента больше нет. Сказанное до
// обрыва при этом целое, и его надо не выбросить, а разобрать.
func (h *NoteHub) watch(cfg *core.Config, st *core.Store, lg *log.Logger, s *NoteSession, max time.Duration) {
	t := time.NewTimer(max)
	defer t.Stop()
	select {
	case <-t.C:
		if h.Live() != s {
			return
		}
		lg.Printf(i18n.Tr("заметка %s: прошёл потолок %s — останавливаю сама"), s.ID, max)
	case <-s.rec.Exited():
		if h.Live() != s {
			return // остановили обычным способом, ffmpeg вышел по нашему SIGINT
		}
		lg.Printf(i18n.Tr("заметка %s: микрофон отвалился на %s — разбираю записанное"),
			s.ID, s.Elapsed().Round(time.Second))
	}
	if _, err := h.Stop(context.Background(), cfg, st, lg, false); err != nil {
		lg.Printf(i18n.Tr("заметка %s: %v"), s.ID, err)
	}
}

// Stop останавливает запись и отдаёт её в разбор. wait=false — разбор уходит в
// фон: расшифровка часовой заметки занимает минуты, и держать на них запрос
// браузера незачем.
func (h *NoteHub) Stop(ctx context.Context, cfg *core.Config, st *core.Store, lg *log.Logger, wait bool) (*NoteSession, error) {
	h.mu.Lock()
	s := h.cur
	h.cur = nil
	h.mu.Unlock()
	if s == nil {
		return nil, errors.New(i18n.Tr("заметка сейчас не пишется"))
	}

	var stopErr error
	s.stop.Do(func() { stopErr = s.rec.Stop() })
	s.done()
	ended := time.Now()
	reason := noteLeftReason
	if stopErr != nil {
		lg.Printf(i18n.Tr("заметка %s: %v"), s.ID, stopErr)
		// Оборванная запись — не повод выбросить сказанное. ffmpeg роняют
		// отвалившиеся наушники и вынутый микрофон, и звук до этого момента
		// лежит целым. Разбираем, если в файле есть что разбирать; если нет —
		// это уже не «неполная запись», а её отсутствие.
		if !worthParsing(s.rec.Path) {
			_ = st.FinishMeeting(s.ID, time.Time{}, ended, []string{s.Author},
				"failed", stopErr.Error(), reason)
			return s, stopErr
		}
		reason = i18n.Tr("запись оборвалась: ") + stopErr.Error()
	}
	if err := st.FinishMeeting(s.ID, time.Time{}, ended, []string{s.Author},
		"recorded", "", reason); err != nil {
		return s, err
	}
	lg.Printf(i18n.Tr("заметка %s: записано %s"), s.ID, s.Elapsed().Round(time.Second))

	run := h.processor()
	if wait {
		return s, run(ctx, cfg, st, s.ID, s.opt.NoPublish)
	}
	go func() {
		// Свой контекст: разбор переживает запрос, по которому его затеяли.
		bg, cancel := context.WithTimeout(context.WithoutCancel(ctx),
			cfg.Transcribe.Timeout.D()+time.Hour)
		defer cancel()
		if err := run(bg, cfg, st, s.ID, s.opt.NoPublish); err != nil {
			lg.Printf(i18n.Tr("заметка %s: %v"), s.ID, err)
		}
	}()
	return s, nil
}

// noteMinBytes — сколько байт делают оборванную запись достойной расшифровки.
// Четыре секунды речи при 32 кбит/с: этого хватает на фразу, а фраза — это уже
// задача, которую человек иначе потеряет.
//
// Меряем байтами, а не длительностью: ffprobe в системе может не стоять вовсе,
// и тогда oggDuration отвечает нулём на любую запись, включая целую, — то есть
// в самый неудачный момент мы выбросили бы хорошую заметку.
const noteMinBytes = 16 << 10

func worthParsing(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Size() >= noteMinBytes
}

// Cancel выбрасывает идущую запись целиком: и файл, и строку в базе.
//
// Промах по кнопке — обычное дело, и он не должен стоить ни расшифровки, ни
// запроса к Claude, ни строки «сорвалась» в списке созвонов навсегда.
func (h *NoteHub) Cancel(st *core.Store, lg *log.Logger) (*NoteSession, error) {
	h.mu.Lock()
	s := h.cur
	h.cur = nil
	h.mu.Unlock()
	if s == nil {
		return nil, errors.New(i18n.Tr("заметка сейчас не пишется"))
	}
	s.stop.Do(func() { _ = s.rec.Stop() })
	s.done()
	// DeleteMeeting уносит и каталог записи: отдельный RemoveAll здесь стоял,
	// пока удаление было одной строкой в meetings.
	if _, err := st.DeleteMeeting(s.ID); err != nil {
		lg.Printf(i18n.Tr("заметка %s: %v"), s.ID, err)
	}
	lg.Printf(i18n.Tr("заметка %s: отменена, запись удалена"), s.ID)
	return s, nil
}

// ProcessNote — путь заметки после записи: расшифровать, разобрать своим
// промптом, разложить по проектам, разослать.
//
// Это не ветка ProcessMeeting, а его сосед: отличается ровно разбор (makeNote
// вместо MakeFollowup), всё остальное — те же функции в том же порядке.
func ProcessNote(ctx context.Context, cfg *core.Config, st *core.Store, id string, noPublish bool) error {
	m, err := st.Meeting(id)
	if err != nil {
		return fmt.Errorf(i18n.Tr("заметка %s: %w"), id, err)
	}
	if m.AudioPath == "" {
		return fmt.Errorf(i18n.Tr("запись заметки %s удалена по сроку хранения — расшифровывать нечего"), id)
	}
	if _, err := os.Stat(m.AudioPath); err != nil {
		return fmt.Errorf(i18n.Tr("запись %s недоступна: %w"), m.AudioPath, err)
	}

	log.Printf(i18n.Tr("расшифровываю заметку %s"), m.AudioPath)
	segs, notesTxt, err := audio.RunTranscriber(ctx, cfg, m.AudioPath,
		audio.MeetingVocabulary(ctx, cfg, st, m))
	if err != nil {
		_ = st.SetStatus(id, "failed", err.Error())
		return err
	}
	for _, line := range core.LastLines(notesTxt, 4) {
		log.Printf("  %s", line)
	}
	// Тишина вместо речи — самый частый способ испортить заметку: выбран не тот
	// микрофон, звук выключен, человек говорил в закрытую крышку. Дальше идти
	// незачем: Claude по пустой расшифровке сочинит пустой разбор и возьмёт за
	// это деньги, а человеку нужно знать, что записи не вышло.
	if len(segs) == 0 {
		err := errors.New(i18n.Tr("в заметке не разобрано ни слова — проверь, тот ли микрофон выбран и не выключен ли звук"))
		_ = st.SetStatus(id, "failed", err.Error())
		return err
	}
	// Говорящий один и он известен по имени. Проставляем его каждой реплике:
	// расшифровка в панели и поиск по ней устроены вокруг имени говорящего.
	author := noteAuthor(m)
	for i := range segs {
		segs[i].Speaker = author
	}
	if err := st.SaveSegments(id, segs); err != nil {
		_ = st.SetStatus(id, "failed", err.Error())
		return err
	}
	_ = st.SetStatus(id, "transcribed", "")
	log.Printf(i18n.Tr("реплик в заметке: %d"), len(segs))

	open, err := st.OpenItems("")
	if err != nil {
		log.Printf(i18n.Tr("не прочитал открытые пункты: %v"), err)
	}
	projects := core.ActiveProjects(st, cfg)
	f, spend, err := makeNote(ctx, cfg, m, segs, projects,
		brain.RenderPrimers(st, projects), core.RenderOpenItems(open))
	if err != nil {
		_ = st.SetStatus(id, "failed", err.Error())
		return err
	}
	log.Printf(i18n.Tr("расход: %s"), spend)
	if err := st.SaveFollowup(id, cfg.Claude.Model, f); err != nil {
		_ = st.SetStatus(id, "failed", err.Error())
		return err
	}
	if err := st.SaveSpend(id, spend); err != nil {
		log.Printf(i18n.Tr("не записал расход: %v"), err)
	}
	// Название придумывает модель, и оно лучше даты: в списке из двадцати
	// заметок «Заметка 10.09 14:35» не отличается ни от одной соседней.
	// Данное человеком не трогаем — он назвал её сам и не просил переименовать.
	if t := strings.TrimSpace(f.Title); t != "" && isDefaultNoteTitle(m.Title) {
		if err := st.SetTitle(id, t); err != nil {
			log.Printf(i18n.Tr("не переименовал заметку: %v"), err)
		} else {
			m.Title = t
		}
	}
	_ = st.SetStatus(id, "summarized", "")
	log.Printf(i18n.Tr("задач: %d, решений: %d, открытых вопросов: %d"),
		len(f.ActionItems), len(f.Decisions), len(f.OpenQuestions))
	if addedN, closedN, err := core.ApplyFollowup(st, projects, id, f); err != nil {
		log.Printf(i18n.Tr("состояние проектов: %v"), err)
	} else if addedN > 0 || closedN > 0 {
		log.Printf(i18n.Tr("по проектам: добавлено %d, закрыто %d"), addedN, closedN)
	}
	publish.PublishProjectDocs(ctx, cfg, st, f, id, log.New(os.Stderr, "", log.Ltime))

	if noPublish {
		return nil
	}
	errs := publish.PublishAll(ctx, cfg, st, m, f, segs, log.New(os.Stderr, "", log.Ltime))
	if len(errs) > 0 {
		msg := errors.Join(errs...).Error()
		_ = st.SetStatus(id, "publish_failed", msg)
		return fmt.Errorf(i18n.Tr("заметка разобрана, но не разослана: %s"), msg)
	}
	return st.SetStatus(id, "published", "")
}

// noteAuthorName — чьим именем подписывать задачи. Спрошенное явно главнее
// всего; иначе берём имя из системы — то самое, которым подписан аккаунт мака.
// Оно не обязано совпадать с именем в списке людей проекта, и это не беда:
// задача с именем «Рустем Тургельдин» найдётся, а задача «не назначен» — нет.
func noteAuthorName(given string) string {
	if s := strings.TrimSpace(given); s != "" {
		return s
	}
	if s := strings.TrimSpace(os.Getenv("STENO_NOTE_AUTHOR")); s != "" {
		return s
	}
	if u, err := user.Current(); err == nil {
		if s := strings.TrimSpace(u.Name); s != "" {
			return s
		}
		if s := strings.TrimSpace(u.Username); s != "" {
			return s
		}
	}
	return defaultNoteAuthor
}

// --- две правки строки созвона, которым место в store.go --------------------
//
