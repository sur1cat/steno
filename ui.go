package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"

	tea "github.com/charmbracelet/bubbletea"
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

func runUI(ctx context.Context, cfg *Config, st *Store) error {
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return fmt.Errorf("steno ui — полноэкранный интерфейс, ему нужен терминал.\n" +
			"Для вывода в файл или в конвейер есть steno list, steno projects и steno show")
	}

	m := newUIModel(cfg, st)
	if err := m.reload(); err != nil {
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
