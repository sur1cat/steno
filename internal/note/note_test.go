package note

import (
	"context"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sur1cat/steno/internal/audio"
	"github.com/sur1cat/steno/internal/bot"
	"github.com/sur1cat/steno/internal/brain"
	"github.com/sur1cat/steno/internal/core"
)

// --- расшифровка монолога ----------------------------------------------------

// Главная разница с RenderTranscript: у монолога говорящий один, и склейка
// подряд идущих реплик оставила бы от всей заметки одну строку с нулевым
// таймкодом. Ссылка «переслушать это место» после такого ведёт на начало
// записи, что бы ни было в пункте.
func TestRenderMonologueХранитТаймкодКаждойСтроки(t *testing.T) {
	segs := []core.Segment{
		{Start: 3, End: 6, Speaker: "Рустем", Text: "надо починить выгрузку"},
		{Start: 12, End: 18, Speaker: "Рустем", Text: "и спросить у Вики про макет"},
		{Start: 30, End: 33, Speaker: "Рустем", Text: "  "},
		{Start: 61, End: 65, Speaker: "Рустем", Text: "решил остаться на sqlite"},
	}
	got := renderMonologue(segs)
	for _, want := range []string{
		"[00:00:03] надо починить выгрузку",
		"[00:00:12] и спросить у Вики про макет",
		"[00:01:01] решил остаться на sqlite",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("нет строки %q в:\n%s", want, got)
		}
	}
	if lines := strings.Count(strings.TrimSpace(got), "\n") + 1; lines != 3 {
		t.Errorf("строк %d, ждали 3 (пустая реплика не в счёт):\n%s", lines, got)
	}
	// И то, ради чего всё это: у follow-up на тех же данных таймкод один.
	if strings.Count(brain.RenderTranscript(segs), "[") > 1 {
		t.Skip("renderTranscript перестал склеивать — разницы больше нет")
	}
}

// --- шапка и промпт ----------------------------------------------------------

func TestNoteHeadНазываетГоворящегоИРазметкуПроектов(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Claude.OutputLanguage = "русский"
	m := &core.Meeting{
		ID: "n1", Title: "Заметка 10.09 14:35", MeetURL: noteURL,
		StartedAt:    time.Date(2026, 9, 10, 14, 35, 0, 0, time.UTC),
		Participants: []string{"Рустем Тургельдин"},
	}
	projects := []core.Project{{
		Name: "steno", Aliases: []string{"стено"}, About: "бот на созвонах",
		People: []string{"Вика"}, Vocabulary: []string{"Вика", "whisper"},
	}}
	head := noteHead(cfg, m, projects, "", "уже висит открытым: item-1 починить выгрузку")

	for _, want := range []string{
		"Рустем Тургельдин",                       // исполнитель по умолчанию
		"2026-09-10 14:35",                        // от неё считаются «к пятнице» и «завтра»
		"Язык разбора: русский",                   // claude.output_language
		"люди: Вика",                              // на кого задачу назначить можно
		"сервисы и сокращения (не люди): whisper", // и на кого нельзя
		"уже висит открытым",                      // что закрывать в updates
	} {
		if !strings.Contains(head, want) {
			t.Errorf("в шапке нет %q:\n%s", want, head)
		}
	}
	// Название по умолчанию модели не показываем: она честно попробует его
	// использовать и вернёт «Заметка 10.09 14:35» вместо содержательного.
	if strings.Contains(head, "Человек назвал заметку так") {
		t.Errorf("в шапку попало название по умолчанию:\n%s", head)
	}
	m.Title = "Мысли по релизу"
	if !strings.Contains(noteHead(cfg, m, nil, "", ""), "Мысли по релизу") {
		t.Error("название, данное человеком, до модели не доехало")
	}
}

// Промпт монолога не должен посылать модель к списку участников: у заметки его
// нет. Правило, скопированное из follow-up, здесь означает «исполнителя взять
// неоткуда» — то есть задачу с пометкой «не назначен» вместо имени того, кто
// её и наговорил.
func TestNoteSystemНеИщетУчастников(t *testing.T) {
	for _, bad := range []string{"списка участников", "Участники в списке", "аккаунт Google"} {
		if strings.Contains(noteSystem, bad) {
			t.Errorf("в промпт заметки просочилось правило созвона: %q", bad)
		}
	}
	for _, want := range []string{
		"Исполнитель по умолчанию — сам говорящий",
		"не забыть",    // ради этого заметку и наговаривают
		"передумывает", // речь вслух правит сама себя по ходу
		"waiting_on",   // у «спросить у X» есть адресат
		"не определён", // разметка по проектам
	} {
		if !strings.Contains(noteSystem, want) {
			t.Errorf("промпт заметки потерял правило про %q", want)
		}
	}
}

func TestNoteAuthorБерётсяИзУчастника(t *testing.T) {
	if got := noteAuthor(&core.Meeting{Participants: []string{"Рустем"}}); got != "Рустем" {
		t.Errorf("автор %q", got)
	}
	if got := noteAuthor(&core.Meeting{Participants: []string{"  ", "Вика"}}); got != "Вика" {
		t.Errorf("пустое имя перебило настоящее: %q", got)
	}
	if got := noteAuthor(&core.Meeting{}); got != defaultNoteAuthor {
		t.Errorf("без участника ждали %q, получили %q", defaultNoteAuthor, got)
	}
}

func TestIsNote(t *testing.T) {
	if !IsNote(&core.Meeting{MeetURL: noteURL}) {
		t.Error("заметку не узнали")
	}
	if IsNote(&core.Meeting{MeetURL: "https://meet.google.com/abc-defg-hij"}) {
		t.Error("созвон приняли за заметку")
	}
	// Ссылка заметки не должна проходить за ссылку на созвон: иначе бот по ней
	// куда-нибудь пойдёт.
	if u := bot.FindMeetURL(noteURL); u != "" {
		t.Errorf("ссылка заметки прошла как созвон: %q", u)
	}
}

// --- жизнь записи ------------------------------------------------------------

// Запись без микрофона и без Claude: подменяем и то и другое, как Dispatcher
// подменяет run в своих тестах.
func testHub(t *testing.T, dir string) (*NoteHub, *core.Config, *core.Store, chan string) {
	t.Helper()
	st, err := core.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := core.DefaultConfig()
	cfg.DataDir = dir

	processed := make(chan string, 4)
	h := &NoteHub{
		Record: func(path, device string, max time.Duration) (*audio.Recorder, error) {
			// Файл нужен настоящий: Stop у пустого пути не проверяет ничего,
			// а конвейер дальше смотрит, что запись на месте.
			if err := os.WriteFile(path, []byte(strings.Repeat("o", 2048)), 0o644); err != nil {
				return nil, err
			}
			return &audio.Recorder{Path: path, Started: time.Now()}, nil
		},
		Process: func(_ context.Context, _ *core.Config, _ *core.Store, id string, _ bool) error {
			processed <- id
			return nil
		},
	}
	return h, cfg, st, processed
}

// noteLog — журнал, который никуда не пишет: у тестов состояния своя правда,
// и она в базе, а не в выводе.
func noteLog() *log.Logger { return log.New(io.Discard, "", 0) }

func TestNoteСтартИОстановка(t *testing.T) {
	dir := t.TempDir()
	h, cfg, st, processed := testHub(t, dir)

	s, err := h.Start(cfg, st, noteLog(), NoteOptions{Author: "Рустем"})
	if err != nil {
		t.Fatal(err)
	}
	m, err := st.Meeting(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != "recording" || m.EndedAt != nil {
		t.Errorf("идущая заметка выглядит как %q, ended_at=%v", m.Status, m.EndedAt)
	}
	if !IsNote(m) {
		t.Errorf("заметка записалась как созвон: meet_url=%q", m.MeetURL)
	}
	if len(m.Participants) != 1 || m.Participants[0] != "Рустем" {
		t.Errorf("участники заметки: %v", m.Participants)
	}
	// Ровно тот запрос, которым идущую запись находит приложение в строке меню
	// (Db.swift). Без совпадения красная точка не загорится.
	var live int
	if err := st.DB.QueryRow(
		`SELECT COUNT(*) FROM meetings WHERE status='recording' AND ended_at IS NULL`,
	).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 1 {
		t.Errorf("строка меню увидит %d идущих записей, а идёт одна", live)
	}

	if _, err := h.Stop(context.Background(), cfg, st, noteLog(), false); err != nil {
		t.Fatal(err)
	}
	m, _ = st.Meeting(s.ID)
	if m.Status != "recorded" || m.EndedAt == nil {
		t.Errorf("после остановки статус %q, ended_at=%v", m.Status, m.EndedAt)
	}
	if m.LeftReason != noteLeftReason {
		t.Errorf("откуда взялась запись, не записано: %q", m.LeftReason)
	}
	select {
	case id := <-processed:
		if id != s.ID {
			t.Errorf("в разбор ушла %s вместо %s", id, s.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("остановленную заметку никто не разобрал")
	}
	if h.Live() != nil {
		t.Error("после остановки заметка всё ещё числится идущей")
	}
}

func TestNoteВтораяЗаписьНеНачинается(t *testing.T) {
	dir := t.TempDir()
	h, cfg, st, _ := testHub(t, dir)
	if _, err := h.Start(cfg, st, noteLog(), NoteOptions{}); err != nil {
		t.Fatal(err)
	}
	_, err := h.Start(cfg, st, noteLog(), NoteOptions{})
	if err == nil {
		t.Fatal("вторая запись началась поверх первой — микрофон-то один")
	}
	if !strings.Contains(err.Error(), "уже пишется") {
		t.Errorf("невнятный отказ: %v", err)
	}
	var rows int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM meetings`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("отказ оставил в базе %d строк вместо одной", rows)
	}
}

// Человек забыл нажать «стоп» и говорит час. Запись обязана закрыться сама и
// уйти в разбор: сказанное не теряется, а расшифровка часа тишины не
// оплачивается.
func TestNoteПотолокОстанавливаетСам(t *testing.T) {
	dir := t.TempDir()
	h, cfg, st, processed := testHub(t, dir)
	s, err := h.Start(cfg, st, noteLog(), NoteOptions{Max: 80 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case id := <-processed:
		if id != s.ID {
			t.Errorf("разобрали %s вместо %s", id, s.ID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("запись не остановилась по потолку")
	}
	m, _ := st.Meeting(s.ID)
	if m.Status != "recorded" {
		t.Errorf("статус после потолка: %q", m.Status)
	}
	if h.Live() != nil {
		t.Error("после потолка заметка числится идущей")
	}
}

// Промах по кнопке не должен стоить ни расшифровки, ни строки «сорвалась» в
// списке навсегда.
func TestNoteОтменаУбираетСлед(t *testing.T) {
	dir := t.TempDir()
	h, cfg, st, processed := testHub(t, dir)
	s, err := h.Start(cfg, st, noteLog(), NoteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Cancel(st, noteLog()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Meeting(s.ID); err == nil {
		t.Error("отменённая заметка осталась в базе")
	}
	if _, err := os.Stat(st.RecordingDir(s.ID)); !os.IsNotExist(err) {
		t.Errorf("запись отменённой заметки осталась на диске: %v", err)
	}
	select {
	case id := <-processed:
		t.Errorf("отменённую заметку отправили в разбор: %s", id)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestNoteНеЗаписываетсяНаСубтитрах(t *testing.T) {
	dir := t.TempDir()
	h, cfg, st, _ := testHub(t, dir)
	cfg.Transcribe.Source = "captions"
	_, err := h.Start(cfg, st, noteLog(), NoteOptions{})
	if err == nil {
		t.Fatal("заметку начали писать там, где текст берут из субтитров Meet")
	}
	if !strings.Contains(err.Error(), "субтитр") {
		t.Errorf("отказ не объясняет причину: %v", err)
	}
}

// --- разбор ------------------------------------------------------------------

// Тишина вместо речи — самый частый способ испортить заметку: не тот микрофон,
// выключенный звук, закрытая крышка. Идти с этим к Claude нельзя: он сочинит
// пустой разбор и возьмёт за это деньги, а человеку нужно знать, что записи не
// вышло.
func TestProcessNoteМолчаниеНеИдётВClaude(t *testing.T) {
	dir := t.TempDir()
	st, err := core.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := core.DefaultConfig()
	cfg.DataDir = dir
	// Адаптер расшифровки — это любая команда, печатающая JSON. Здесь ею
	// работает cat: «звук» и есть готовый ответ.
	cfg.Transcribe.Cmd = []string{"cat", "{{audio}}"}
	cfg.Transcribe.Vocabulary = false

	audio := filepath.Join(dir, "audio.json")
	if err := os.WriteFile(audio, []byte(`{"segments":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &core.Meeting{ID: "n-empty", Title: "Заметка 10.09 14:35", MeetURL: noteURL,
		StartedAt: time.Now(), AudioPath: audio, Status: "recorded",
		Participants: []string{"Рустем"}}
	if err := st.CreateMeeting(m); err != nil {
		t.Fatal(err)
	}
	err = ProcessNote(context.Background(), cfg, st, m.ID, true)
	if err == nil {
		t.Fatal("пустая расшифровка прошла как удачная заметка")
	}
	if !strings.Contains(err.Error(), "микрофон") {
		t.Errorf("сообщение не подсказывает, что проверить: %v", err)
	}
	got, _ := st.Meeting(m.ID)
	if got.Status != "failed" {
		t.Errorf("статус пустой заметки: %q", got.Status)
	}
}

// Название придумывает модель, но данное человеком не трогаем: он назвал
// заметку сам и не просил переименовать.
func TestRenameMeetingТолькоУИмениПоУмолчанию(t *testing.T) {
	if !isDefaultNoteTitle(noteTitle(time.Now())) {
		t.Errorf("своё же название по умолчанию не узнаётся: %q", noteTitle(time.Now()))
	}
	if isDefaultNoteTitle("Мысли по релизу") {
		t.Error("название человека приняли за автоматическое")
	}

	dir := t.TempDir()
	st, err := core.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := &core.Meeting{ID: "n2", Title: "старое", MeetURL: noteURL,
		StartedAt: time.Now(), Status: "recorded"}
	if err := st.CreateMeeting(m); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTitle("n2", "Мысли по релизу"); err != nil {
		t.Fatal(err)
	}
	got, _ := st.Meeting("n2")
	if got.Title != "Мысли по релизу" {
		t.Errorf("название после переименования: %q", got.Title)
	}
}

// --- обрыв записи -------------------------------------------------------------

// Наушники ушли из зоны, микрофон выдернули — ffmpeg умирает, а человек
// продолжает говорить и видит тикающую красную точку. Сторож обязан это
// заметить, а сказанное до обрыва — разобрать, а не выбросить.
func TestNoteСторожЛовитОтвалившийсяМикрофон(t *testing.T) {
	dir := t.TempDir()
	h, cfg, st, processed := testHub(t, dir)
	dead := make(chan struct{})
	h.Record = func(path, device string, max time.Duration) (*audio.Recorder, error) {
		if err := os.WriteFile(path, make([]byte, noteMinBytes+1), 0o644); err != nil {
			return nil, err
		}
		return &audio.Recorder{Path: path, Started: time.Now(), Done: dead}, nil
	}

	s, err := h.Start(cfg, st, noteLog(), NoteOptions{Max: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	close(dead) // ffmpeg умер сам

	select {
	case id := <-processed:
		if id != s.ID {
			t.Errorf("разобрали %s вместо %s", id, s.ID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("смерть ffmpeg никто не заметил — заметка так и «пишется»")
	}
	if h.Live() != nil {
		t.Error("после обрыва заметка числится идущей")
	}
	m, _ := st.Meeting(s.ID)
	if m.Status != "recorded" {
		t.Errorf("статус оборванной заметки: %q (записанное потеряли)", m.Status)
	}
}

// А вот обрыв на первой секунде разбирать нечего: там не «неполная запись», а
// её отсутствие, и человеку надо сказать это, а не показать пустой разбор.
func TestNoteКороткийОбрывПомечаетсяСорвавшимся(t *testing.T) {
	dir := t.TempDir()
	h, cfg, st, processed := testHub(t, dir)
	h.Record = func(path, device string, max time.Duration) (*audio.Recorder, error) {
		if err := os.WriteFile(path, make([]byte, 2048), 0o644); err != nil {
			return nil, err
		}
		// Настоящий процесс, вышедший с ошибкой: Stop разберёт его код возврата
		// ровно так же, как разобрал бы упавший ffmpeg. Ждём его конца прямо
		// здесь, иначе наш же SIGINT догонит ещё живой /bin/sh, и обрыв станет
		// неотличим от обычной остановки.
		cmd := exec.Command("/bin/sh", "-c", "exit 3")
		f, err := os.Create(path + ".log")
		if err != nil {
			return nil, err
		}
		cmd.Stdout, cmd.Stderr = f, f
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		r := audio.NewRecorder(cmd, f, path)
		<-r.Exited()
		return r, nil
	}
	s, err := h.Start(cfg, st, noteLog(), NoteOptions{Max: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	// Останавливать не надо: запись уже мертва, и всё делает сторож.
	var m *core.Meeting
	for i := 0; i < 60; i++ {
		if m, _ = st.Meeting(s.ID); m != nil && m.Status != "recording" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if m == nil || m.Status != "failed" {
		t.Fatalf("статус оборвавшейся на первой секунде записи: %+v", m)
	}
	if m.Error == "" {
		t.Error("причина обрыва не записана — в панели будет пусто")
	}
	select {
	case id := <-processed:
		t.Errorf("пустой обрывок отправили в расшифровку: %s", id)
	case <-time.After(200 * time.Millisecond):
	}
}

// `steno stop` посреди заметки. Строка не должна остаться «пишущейся»: иначе
// следующий запуск сервиса показывает в строке меню призрак — бегущий
// секундомер записи, которой нет.
func TestNoteParkЗакрываетЗаметкуНаОстановкеСервиса(t *testing.T) {
	dir := t.TempDir()
	h, cfg, st, processed := testHub(t, dir)
	s, err := h.Start(cfg, st, noteLog(), NoteOptions{Max: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	h.Park(st, noteLog())

	m, err := st.Meeting(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != "recorded" {
		t.Errorf("после остановки сервиса статус заметки %q", m.Status)
	}
	if m.EndedAt == nil {
		t.Error("время окончания не проставлено — строка так и останется идущей")
	}
	if h.Live() != nil {
		t.Error("заметка всё ещё числится идущей")
	}
	// Расшифровку не начинаем: процесс всё равно уходит, а начатая расшифровка
	// умерла бы на середине.
	select {
	case id := <-processed:
		t.Errorf("на выходе сервиса начали разбирать заметку %s", id)
	case <-time.After(200 * time.Millisecond):
	}
	// Park без записи ничего не ломает: его зовут на каждой остановке.
	h.Park(st, noteLog())
}

// Сервис убили по kill -9 — Park не позвали никто. Строка осталась «пишется»
// от мёртвого процесса, и следующая заметка обязана её закрыть: живой она уже
// быть не может, сервис на одну настройку запускается один.
func TestNoteСтартЗакрываетНезакрытуюОтПрошлогоЗапуска(t *testing.T) {
	dir := t.TempDir()
	h, cfg, st, _ := testHub(t, dir)
	stale := &core.Meeting{ID: "старая", Title: "Заметка вчера", MeetURL: noteURL,
		StartedAt: time.Now().Add(-3 * time.Hour), Status: "recording",
		Participants: []string{"Рустем"}}
	if err := st.CreateMeeting(stale); err != nil {
		t.Fatal(err)
	}
	// И живой созвон рядом: его трогать нельзя, его пишет бот.
	call := &core.Meeting{ID: "созвон", Title: "Планёрка",
		MeetURL: "https://meet.google.com/abc-defg-hij", StartedAt: time.Now(),
		Status: "recording"}
	if err := st.CreateMeeting(call); err != nil {
		t.Fatal(err)
	}

	if _, err := h.Start(cfg, st, noteLog(), NoteOptions{}); err != nil {
		t.Fatal(err)
	}
	got, _ := st.Meeting("старая")
	if got.Status != "failed" {
		t.Errorf("незакрытая заметка от прошлого запуска осталась в статусе %q", got.Status)
	}
	if got.Error == "" {
		t.Error("причина не записана — в панели будет пусто")
	}
	if c, _ := st.Meeting("созвон"); c.Status != "recording" {
		t.Errorf("зацепили чужую запись: созвон стал %q", c.Status)
	}
}
