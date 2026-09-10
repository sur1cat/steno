package main

import (
	"context"
	"errors"
	"io"
	"log"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
	"github.com/sur1cat/steno/internal/tui"
	"golang.org/x/term"
)

// steno ui — то же, что панель, но в терминале и без запущенного сервиса.
//
// Панель хороша, пока её есть где открыть. Тому, кто сидит на сервере по ssh
// или просто не хочет держать запущенным ещё один процесс, она недоступна, и до
// сих пор у него оставались только разрозненные команды: посмотреть список,
// посмотреть один созвон, завести проект. Править проект из терминала было
// нельзя вовсе — только панелью.
//
// Читает ту же базу теми же выборками, что и панель. Ничего не слушает и
// никуда не ходит, кроме сборки справки по проекту, — её человек запускает сам.

func cmdUI(ctx context.Context, args []string) error {
	fs := newFlagSet("ui")
	cfgPath := setupFlags(fs)
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	cfg, st, err := open(*cfgPath)
	if err != nil {
		return err
	}
	defer st.Close()
	return runUI(ctx, cfg, st)
}

func runUI(ctx context.Context, cfg *core.Config, st *core.Store) error {
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return errors.New(i18n.Tr("steno ui — полноэкранный интерфейс, ему нужен терминал.\n") +
			i18n.Tr("Для вывода в файл или в конвейер есть steno list, steno projects и steno show"))
	}

	m := tui.NewUIModel(cfg, st)
	if err := m.Reload(); err != nil {
		return err
	}

	// Лог в это время должен молчать: строка от фоновой сборки справки,
	// напечатанная поверх альтернативного экрана, рвёт вёрстку и не стирается
	// до следующей перерисовки. Всё, что человеку нужно знать, интерфейс
	// говорит сам — строкой состояния.
	prev := log.Writer()
	log.SetOutput(io.Discard)
	defer log.SetOutput(prev)

	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))
	if _, err := p.Run(); err != nil {
		// Отменённый контекст — это ctrl+c снаружи, а не сбой.
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	return nil
}
