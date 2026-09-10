package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/sur1cat/steno/internal/audio"
	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
	"github.com/sur1cat/steno/internal/note"
	"github.com/sur1cat/steno/internal/publish"
	"golang.org/x/term"
)

// `steno note` — та же заметка, но для тех, у кого строки меню нет: сервер,
// Linux, ssh. Команда сама себе кнопка: запустил — пишется, нажал Enter —
// разобралось.

func cmdNote(ctx context.Context, args []string) error {
	fs := newFlagSet("note")
	cfgPath := setupFlags(fs)
	title := fs.String("title", "", i18n.Tr("как назвать заметку (иначе название придумает Claude)"))
	author := fs.String("author", "", i18n.Tr("чьи это задачи; иначе — имя из системы"))
	device := fs.String("device", core.EnvOr("STENO_MIC", ""),
		i18n.Tr("микрофон: номер или имя из --devices"))
	list := fs.Bool("devices", false, i18n.Tr("показать микрофоны и выйти"))
	maxDur := fs.Duration("max", audio.NoteMaxDefault, i18n.Tr("потолок записи"))
	noPublish := fs.Bool("no-publish", false, i18n.Tr("не рассылать, только разобрать"))
	rest, err := parseArgs(fs, args)
	if err != nil {
		return err
	}

	if *list {
		return printMics()
	}

	cfg, st, err := open(*cfgPath)
	if err != nil {
		return err
	}
	defer st.Close()

	// С идентификатором — разобрать уже записанное. Нужно после сбоя: звук
	// лежит на диске, а `steno process` разобрал бы заметку промптом созвона.
	if len(rest) > 0 {
		return note.ProcessNote(ctx, cfg, st, rest[0], *noPublish)
	}

	lg := log.New(os.Stderr, "", log.Ltime)
	s, err := note.Notes.Start(cfg, st, lg, note.NoteOptions{
		Title: *title, Author: *author, Device: *device,
		Max: *maxDur, NoPublish: *noPublish,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, i18n.Tr("● пишу с микрофона. Enter — остановить и разобрать. Заметка %s\n"), s.ID)
	waitForStop(ctx, s)

	// Свой контекст: Ctrl-C гасит ctx, а наговоренное к этому моменту уже
	// записано, и терять его из-за того, что человек остановил запись
	// привычным способом, нельзя.
	stopCtx := context.WithoutCancel(ctx)
	if _, err := note.Notes.Stop(stopCtx, cfg, st, lg, true); err != nil {
		return err
	}
	m, err := st.Meeting(s.ID)
	if err != nil {
		return err
	}
	f, err := st.Followup(s.ID)
	if err != nil {
		return fmt.Errorf(i18n.Tr("разбора заметки %s нет: %w"), s.ID, err)
	}
	fmt.Println()
	fmt.Println(publish.RenderPlain(m, f))
	return nil
}

// waitForStop ждёт Enter, Ctrl-C или потолка записи, показывая секундомер.
//
// Ctrl-C здесь означает то же, что Enter, а не «выбросить»: к этому моменту
// человек уже наговорил заметку, и отменять её по привычному способу остановки
// — самый дорогой из возможных сюрпризов.
func waitForStop(ctx context.Context, s *note.NoteSession) {
	// Enter ждём только у живого терминала. Закрытый или перенаправленный
	// stdin отдаёт EOF сразу же, и запись, запущенная из скрипта или из-под
	// сервиса, останавливалась бы в ту же секунду, не записав ни слова.
	typed := make(chan struct{})
	if term.IsTerminal(int(os.Stdin.Fd())) {
		go func() {
			defer close(typed)
			_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		}()
	}

	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	limit := time.NewTimer(time.Until(s.Until))
	defer limit.Stop()
	live := term.IsTerminal(int(os.Stderr.Fd()))
	for {
		select {
		case <-typed:
			clearLine(live)
			return
		case <-ctx.Done():
			clearLine(live)
			return
		case <-limit.C:
			clearLine(live)
			fmt.Fprintf(os.Stderr, i18n.Tr("потолок записи (%s) — останавливаю\n"), s.Until.Sub(s.Started))
			return
		case <-tick.C:
			if live {
				fmt.Fprintf(os.Stderr, "\r● %s  ", core.Clock(s.Elapsed().Seconds()))
			}
		}
	}
}

func clearLine(live bool) {
	if live {
		fmt.Fprint(os.Stderr, "\r          \r")
	}
}

func printMics() error {
	devs, err := audio.MicDevices()
	if err != nil {
		return err
	}
	fmt.Println(i18n.Tr("микрофоны (номер — для --device):"))
	for _, d := range devs {
		fmt.Printf("  %s  %s\n", d.Index, d.Name)
	}
	fmt.Println(i18n.Tr("  без --device берётся тот, что выбран в системе"))
	return nil
}
