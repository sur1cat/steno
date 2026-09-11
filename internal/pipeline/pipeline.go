package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sur1cat/steno/internal/audio"
	"github.com/sur1cat/steno/internal/bot"
	"github.com/sur1cat/steno/internal/brain"
	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
	"github.com/sur1cat/steno/internal/note"
	"github.com/sur1cat/steno/internal/publish"
	"github.com/sur1cat/steno/internal/spec"
)

// Конвейер созвона: записать, расшифровать, разобрать, разослать.
//
// Здесь то, что вызывают три разных места — команда `steno join`, диспетчер
// источников и кнопка в панели, — и потому это не может жить рядом с main:
// пакет команд импортируют, а не импортируются.

// RecordOnce доводит созвон только до записи. Первая проверка на живом звонке
// упирается ровно в неё: зашёл ли бот, впустили ли его, есть ли звук и попали
// ли в субтитры имена. Расшифровка и follow-up к этому вопросу отношения не
// имеют, а требуют ключей и установленного whisper.
func RecordOnce(ctx context.Context, cfg *core.Config, st *core.Store, m *core.Meeting) error {
	if err := RecordMeeting(ctx, cfg, st, m); err != nil {
		return err
	}
	utts, err := audio.ReadUtterances(m.CaptionsPath)
	if err != nil {
		log.Printf(i18n.Tr("субтитры не прочитались: %v"), err)
	}
	named := map[string]int{}
	for _, u := range utts {
		named[u.Speaker]++
	}
	size := int64(0)
	if st, err := os.Stat(m.AudioPath); err == nil {
		size = st.Size()
	}
	log.Printf(i18n.Tr("готово: аудио %.1f МБ, реплик в субтитрах %d, говорящих %d"),
		float64(size)/(1<<20), len(utts), len(named))
	for who, n := range named {
		log.Printf(i18n.Tr("  %s — %d реплик"), core.OrDash(who), n)
	}
	if len(utts) == 0 {
		// Что именно случилось, бот уже сказал строкой «субтитры: …» — она
		// различает «площадка их не отдаёт» и «должны были быть, но не
		// включились». Повторять здесь догадку про Meet нельзя: у Jitsi
		// пустые субтитры это норма, а не поломка вёрстки.
		log.Print(i18n.Tr("субтитров нет — расшифровка пойдёт по звуку, без имён говорящих; ") +
			i18n.Tr("причину смотри выше, в строке бота «субтитры: …»"))
	}
	return nil
}

// TranscribeOnly доводит созвон до расшифровки и печатает её. Ступенька между
// «только записал» и «сделал follow-up»: проверить, что бот зашёл и текст
// получился, можно без ключа Claude и без единой копейки.
func TranscribeOnly(ctx context.Context, cfg *core.Config, st *core.Store, id string) error {
	m, err := st.Meeting(id)
	if err != nil {
		return err
	}
	segs, err := transcribeMeeting(ctx, cfg, st, m)
	if err != nil {
		_ = st.SetStatus(id, "failed", err.Error())
		return err
	}
	if err := audio.CheckTranscript(segs); err != nil {
		// Платить Claude за расшифровку из одной тишины незачем, а главное —
		// молчаливый пустой follow-up выглядит как настоящий.
		_ = st.SetStatus(id, "failed", err.Error())
		return err
	}
	if err := st.SaveSegments(id, segs); err != nil {
		_ = st.SetStatus(id, "failed", err.Error())
		return err
	}
	_ = st.SetStatus(id, "transcribed", "")
	log.Printf(i18n.Tr("реплик: %d, из них с именем: %d"), len(segs), namedCount(segs))
	fmt.Println()
	fmt.Print(brain.RenderTranscript(segs))
	fmt.Printf(i18n.Tr("\nfollow-up: steno process %s (нужен ключ Claude)\n"), id)
	return nil
}

// RecordAndProcess — весь путь одного созвона: завести бота, дождаться конца,
// расшифровать, собрать follow-up, разослать. Одна и та же дорога у `steno
// join` и у календарного watcher'а.
func RecordAndProcess(ctx context.Context, cfg *core.Config, st *core.Store, m *core.Meeting) error {
	if err := RecordMeeting(ctx, cfg, st, m); err != nil {
		return err
	}
	return ProcessMeeting(ctx, cfg, st, m.ID, false)
}

// RecordMeeting — только запись: завести бота, дождаться конца, сохранить.
func RecordMeeting(ctx context.Context, cfg *core.Config, st *core.Store, m *core.Meeting) error {
	outDir := st.RecordingDir(m.ID)
	m.AudioPath = filepath.Join(outDir, "audio.ogg")
	m.CaptionsPath = filepath.Join(outDir, "captions.jsonl")
	if err := st.CreateMeeting(m); err != nil {
		return err
	}
	log.Printf(i18n.Tr("созвон %s"), m.ID)

	var (
		res *bot.BotResult
		err error
	)
	if cfg.Bot.Local {
		res, err = runBotHere(ctx, cfg, m.MeetURL, outDir)
	} else {
		res, err = runBotInDocker(ctx, cfg, m.ID, m.MeetURL, outDir)
	}
	if err != nil {
		_ = st.FinishMeeting(m.ID, time.Time{}, time.Now(), nil, "failed", err.Error(), "")
		return err
	}
	if err := st.FinishMeeting(m.ID, res.Started, res.Ended, res.Participants,
		"recorded", "", res.LeftReason); err != nil {
		return err
	}
	log.Printf(i18n.Tr("записано: %s (%s)"), m.AudioPath, res.LeftReason)
	return nil
}

func runBotHere(ctx context.Context, cfg *core.Config, meetURL, outDir string) (*bot.BotResult, error) {
	sel, err := bot.LoadSelectors(cfg.Bot.Selectors)
	if err != nil {
		return nil, err
	}
	return bot.RunBot(ctx, bot.BotOptions{
		MeetURL:          meetURL,
		DisplayName:      cfg.Bot.DisplayName,
		OutDir:           outDir,
		AudioSource:      core.EnvOr("STENO_AUDIO_SOURCE", "meet_out.monitor"),
		Selectors:        sel,
		AdmissionTimeout: cfg.Bot.AdmissionTimeout.D(),
		EmptyFor:         cfg.Bot.EmptyFor.D(),
		MaxDuration:      cfg.Bot.MaxDuration.D(),
		CaptionLanguage:  cfg.Bot.CaptionLanguage,
		UserDataDir:      core.EnvOr("STENO_CHROME_PROFILE", ""),
		Log:              log.New(os.Stderr, "bot: ", log.Ltime),
	})
}

// runBotInDocker поднимает контейнер с Chromium и PulseAudio, отдаёт ему папку
// созвона и ждёт. Внутри крутится тот же бинарник в роли `steno bot`.
func runBotInDocker(ctx context.Context, cfg *core.Config, meetingID, meetURL, outDir string) (*bot.BotResult, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	// Внутри контейнера бот работает под uid 1000 (PulseAudio отказывается
	// стартовать под root). Если сервис на хосте — другой пользователь, папка
	// созвона окажется ему недоступна на запись, и первым же упадёт лог ffmpeg.
	if err := os.Chmod(outDir, 0o777); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(outDir)
	if err != nil {
		return nil, err
	}
	// Имя берём то, которое вернули: подмена внутри функции не помогала бы —
	// запуск контейнера ниже всё равно взял бы старое из конфига.
	image, err := ensureBotImage(ctx, cfg.Bot.Image, log.Default())
	if err != nil {
		return nil, err
	}
	// Имя и метка нужны, чтобы контейнер можно было найти и погасить снаружи:
	//   docker kill $(docker ps -q -f label=steno)
	name := "steno-" + meetingID
	args := []string{
		"run", "--rm",
		"--name", name,
		"--label", "steno=1",
		"-v", abs + ":/out",
		"--shm-size=1g",
	}
	if p := os.Getenv("STENO_CHROME_PROFILE"); p != "" {
		args = append(args, "-v", p+":/profile", "-e", "STENO_CHROME_PROFILE=/profile")
	}
	// Свой selectors.json — обещанный способ починить бота без пересборки
	// образа. Без проброса внутрь контейнера правка файла ничего не меняла.
	botArgs := []string{
		"steno", "bot",
		"--url", meetURL,
		"--out", "/out",
		"--name", cfg.Bot.DisplayName,
		"--admission", cfg.Bot.AdmissionTimeout.D().String(),
		"--empty-for", cfg.Bot.EmptyFor.D().String(),
		"--max", cfg.Bot.MaxDuration.D().String(),
	}
	if cfg.Bot.CaptionLanguage != "" {
		botArgs = append(botArgs, "--caption-language", cfg.Bot.CaptionLanguage)
	}
	if os.Getenv("STENO_DEBUG_CAPTIONS") != "" {
		botArgs = append(botArgs, "--debug-captions")
	}
	if cfg.Bot.Selectors != "" {
		selAbs, err := filepath.Abs(cfg.Bot.Selectors)
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(selAbs); err != nil {
			return nil, fmt.Errorf("bot.selectors: %w", err)
		}
		args = append(args, "-v", selAbs+":/selectors.json:ro")
		botArgs = append(botArgs, "--selectors", "/selectors.json")
	}
	args = append(args, image)
	args = append(args, botArgs...)

	cmd := exec.CommandContext(ctx, "docker", args...)
	// docker run — это клиент демона. Убить его недостаточно: контейнер
	// останется работать и будет держать процессор и гигабайт shm, пока его
	// не погасят руками.
	cmd.Cancel = func() error {
		_ = exec.Command("docker", "kill", name).Run()
		return cmd.Process.Kill()
	}
	cmd.WaitDelay = 20 * time.Second
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	log.Printf(i18n.Tr("запускаю бота в контейнере %s"), name)
	if err := cmd.Run(); err != nil {
		// Контекст мог закончиться уже после того, как бот дописал результат.
		if _, statErr := os.Stat(filepath.Join(outDir, "result.json")); statErr != nil {
			_ = exec.Command("docker", "kill", name).Run()
			return nil, fmt.Errorf(i18n.Tr("контейнер бота: %w"), err)
		}
	}
	return readBotResult(outDir)
}

func readBotResult(outDir string) (*bot.BotResult, error) {
	b, err := os.ReadFile(filepath.Join(outDir, "result.json"))
	if err != nil {
		return nil, fmt.Errorf(i18n.Tr("бот не оставил result.json: %w"), err)
	}
	var res bot.BotResult
	if err := json.Unmarshal(b, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func ProcessMeeting(ctx context.Context, cfg *core.Config, st *core.Store, id string, noPublish bool) error {
	m, err := st.Meeting(id)
	if err != nil {
		return fmt.Errorf(i18n.Tr("созвон %s: %w"), id, err)
	}
	// Заметку разбирает свой промпт: у монолога нет ни участников, ни спора,
	// и правила follow-up ищут в ней людей, которых там нет.
	if note.IsNote(m) {
		return note.ProcessNote(ctx, cfg, st, id, noPublish)
	}

	if cfg.Transcribe.Source != "captions" && m.AudioPath == "" {
		// Запись удалена по сроку хранения. Расшифровка и follow-up при этом
		// целы, и портить им статус на «сорвался» незачем.
		return fmt.Errorf(i18n.Tr("запись созвона %s удалена по сроку хранения — расшифровывать нечего"), id)
	}
	if cfg.Transcribe.Source != "captions" {
		if _, err := os.Stat(m.AudioPath); err != nil {
			return fmt.Errorf(i18n.Tr("запись %s недоступна: %w"), m.AudioPath, err)
		}
	}
	segs, err := transcribeMeeting(ctx, cfg, st, m)
	if err != nil {
		_ = st.SetStatus(id, "failed", err.Error())
		return err
	}
	// Та же проверка, что и у записи из терминала: расшифровка из одной тишины
	// до модели не доходит. Здесь её не было, и календарный бот, зашедший в
	// пустую комнату, отдал тишину в Claude — и получил за деньги follow-up
	// из одного открытого вопроса, который выглядит как настоящий.
	if err := audio.CheckTranscript(segs); err != nil {
		_ = st.SetStatus(id, "failed", err.Error())
		return err
	}
	if err := st.SaveSegments(id, segs); err != nil {
		// Молча вернуться нельзя: созвон остался бы в статусе «записан», а это
		// спокойный статус, который никто не перепроверяет и никто не
		// перезапускает.
		_ = st.SetStatus(id, "failed", err.Error())
		return err
	}
	_ = st.SetStatus(id, "transcribed", "")
	log.Printf(i18n.Tr("реплик: %d, из них с именем: %d"), len(segs), namedCount(segs))

	// Через ResolveVia, а не через ClaudeClient: провайдеров больше одного, и
	// «делаю follow-up (claude-opus-5)» на установке, работающей через Groq,
	// отправляет искать поломку не туда.
	if _, how, err := brain.ResolveVia(cfg); err == nil {
		log.Printf(i18n.Tr("делаю follow-up (%s, доступ: %s)"), cfg.BrainModel(), how)
	} else {
		log.Printf(i18n.Tr("делаю follow-up (%s)"), cfg.BrainModel())
	}
	// Модель должна видеть, что уже висит открытым: иначе каждый созвон
	// заводит копии тех же задач, и состояние проекта тонет в дублях.
	open, err := st.OpenItems("")
	if err != nil {
		log.Printf(i18n.Tr("не прочитал открытые пункты: %v"), err)
	}
	projects := core.ActiveProjects(st, cfg)
	f, spend, err := brain.MakeFollowup(ctx, cfg, m, segs, projects,
		brain.RenderPrimers(st, projects), core.RenderOpenItems(open))
	if err != nil {
		_ = st.SetStatus(id, "failed", err.Error())
		return err
	}
	log.Printf(i18n.Tr("расход: %s"), spend)
	// Порядок важен: расход дописывается в строку follow-up, а создаёт её
	// SaveFollowup. Наоборот UPDATE не находил строки и молча терял расход —
	// на повторном запуске всё сходилось, на первом `steno cost` показывал ноль.
	if err := st.SaveFollowup(id, cfg.BrainModel(), f); err != nil {
		_ = st.SetStatus(id, "failed", err.Error())
		return err
	}
	if err := st.SaveSpend(id, spend); err != nil {
		log.Printf(i18n.Tr("не записал расход: %v"), err)
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
		spec.AfterFollowup(ctx, cfg, st, id)
		return nil
	}
	errs := publish.PublishAll(ctx, cfg, st, m, f, segs, log.New(os.Stderr, "", log.Ltime))
	// ТЗ — после рассылки, а не до: follow-up ждут в чате сразу, а ТЗ по
	// каждой задаче — это минута на репозиторий и модель. И независимо от
	// того, дошла ли рассылка: задачи в проекте уже лежат.
	spec.AfterFollowup(ctx, cfg, st, id)
	if len(errs) > 0 {
		msg := errors.Join(errs...).Error()
		_ = st.SetStatus(id, "publish_failed", msg)
		return fmt.Errorf(i18n.Tr("follow-up сделан, но не разослан: %s"), msg)
	}
	return st.SetStatus(id, "published", "")
}

// transcribeMeeting превращает записанный созвон в реплики с именами — из
// whisper или прямо из субтитров Meet, смотря что настроено.
func transcribeMeeting(ctx context.Context, cfg *core.Config, st *core.Store, m *core.Meeting) ([]core.Segment, error) {
	if cfg.Transcribe.Source == "captions" {
		log.Printf(i18n.Tr("беру текст из субтитров Meet (%s)"), m.CaptionsPath)
		return audio.SegmentsFromCaptions(m.CaptionsPath)
	}
	log.Printf(i18n.Tr("расшифровываю %s"), m.AudioPath)
	segs, notes, err := audio.RunTranscriber(ctx, cfg, m.AudioPath, audio.MeetingVocabulary(ctx, cfg, st, m))
	if err != nil {
		return nil, err
	}
	for _, line := range core.LastLines(notes, 4) {
		log.Printf("  %s", line)
	}
	utts, uerr := audio.ReadUtterances(m.CaptionsPath)
	if uerr != nil {
		log.Printf(i18n.Tr("субтитры не прочитались (%v) — расшифровка будет без имён"), uerr)
	}
	// Третий источник имён — лента активного говорящего, снятая ботом со
	// страницы. Она нужна там, где субтитров нет или почти нет: у Jitsi их на
	// публичном сервере не бывает вовсе, а Meet на неверном языке распознаёт
	// так мало, что имён не хватает даже на треть разговора.
	return audio.AlignSpeakers(segs, utts, audio.ReadSpeakerSpans(m.CaptionsPath)...), nil
}

func namedCount(segs []core.Segment) int {
	n := 0
	for _, s := range segs {
		if s.Speaker != "" {
			n++
		}
	}
	return n
}

// ensureBotImage подтягивает образ бота, если его нет.
//
// Раньше человеку говорили «нет образа → make bot-image», а это значило
// склонировать репозиторий и собрать гигабайт у себя — последнее место в
// установке, где требовались исходники. Образ той же версии лежит в реестре,
// и притащить его steno может сам.
//
// Локально собранный образ (steno-bot:latest) не трогаем: у него нет реестра,
// откуда тянуть, и попытка скачивания только запутает сообщением об ошибке.
func ensureBotImage(ctx context.Context, image string, log *log.Logger) (string, error) {
	// Со сроком: зависший демон Docker не отвечает и не отваливается, а
	// `docker image inspect` ждёт его вечно. На живом созвоне это выглядело
	// так: steno написал «иду на созвон» и замер навсегда, ничего не объяснив.
	look, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if haveImage(look, image) {
		return image, nil
	}
	if look.Err() != nil && ctx.Err() == nil {
		return "", errors.New(i18n.Tr("docker не отвечает уже 20 секунд — похоже, он завис;\n") +
			i18n.Tr("  перезапусти Docker Desktop и попробуй снова"))
	}
	if !strings.Contains(image, "/") {
		// Настройки, написанные до появления образа в реестре, хранят имя
		// «steno-bot:latest». Такое имя некуда тянуть — но это не повод
		// отправлять человека собирать гигабайт руками: у его версии есть
		// готовый образ, и правильнее взять его, сказав об этом вслух.
		reg := core.DefaultBotImage()
		if !strings.Contains(reg, "/") {
			return "", fmt.Errorf(i18n.Tr("нет образа %s — он собирается из исходников:\n")+
				"  git clone https://github.com/sur1cat/steno && cd steno && make bot-image", image)
		}
		log.Printf(i18n.Tr("в настройке образ %s, которого нет; беру %s"), image, reg)
		image = reg
		if haveImage(ctx, image) {
			return image, nil
		}
	}
	log.Printf(i18n.Tr("образа %s нет, скачиваю (около гигабайта, один раз)"), image)
	// Скачивание — дело долгое (гигабайт), но не бесконечное.
	pullCtx, cancelPull := context.WithTimeout(ctx, 20*time.Minute)
	defer cancelPull()
	cmd := exec.CommandContext(pullCtx, "docker", "pull", image)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf(i18n.Tr("не скачался образ %s: %s\n")+
			i18n.Tr("  можно собрать самому: git clone https://github.com/sur1cat/steno && cd steno && make bot-image"),
			image, i18n.Tail(string(out), 300))
	}
	log.Printf(i18n.Tr("образ %s готов"), image)
	return image, nil
}

// haveImage — есть ли образ на машине.
//
// Через `docker images -q`, а не `docker image inspect`: Docker Desktop с новым
// хранилищем образов кладёт собранное BuildKit так, что inspect по имени его не
// находит, хотя `docker images` показывает, а контейнер из него запускается.
// Проверено на живой машине: inspect по имени — «No such image», по
// идентификатору — находит. Из-за этого steno отказывался идти на созвон,
// требуя собрать образ, который уже был собран.
func haveImage(ctx context.Context, image string) bool {
	out, err := exec.CommandContext(ctx, "docker", "images", "-q", image).Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}
