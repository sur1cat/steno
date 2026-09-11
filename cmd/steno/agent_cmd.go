package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
	"github.com/sur1cat/steno/internal/spec"
)

// `steno agent` — выключатель исполнения ТЗ, по образцу `steno autostart`.
//
// Раздел "agent" в steno.json — это разрешение писать файлы и выполнять
// команды на этой машине, и его нарочно нет в веб-панели: пароль панели общий
// на команду, и форма, которая выдаёт такое право, — не форма. А вот команда
// в терминале — это человек за этой машиной, и ей место здесь. Ту же команду
// зовёт кнопка в строке меню: она тоже локальная.
//
// Файл правится на месте (spec.PatchAgent), а не перезаписывается: порядок
// разделов и незнакомые ключи остаются как были. Сервис перезапускать не
// нужно — раздел перечитывается на каждое обращение.

const agentUsage = `steno agent             — задачи агенту: выключатель и состояние

  steno agent                что включено, кто исполняет, куда кладутся ветки
  steno agent on|off         разрешить или запретить исполнение ТЗ на этой
                             машине: рабочая копия, ветка, коммит — никогда push
  steno agent auto on|off    собирать ли ТЗ самому после каждого разбора
                             (это только чтение кода и запрос к модели)

Флаги:
  -c <path>                  путь к steno.json
`

func cmdAgent(args []string) error {
	fs := newFlagSet("agent")
	cfgPath := setupFlags(fs)
	rest, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	verb := ""
	if len(rest) > 0 {
		verb = strings.ToLower(strings.TrimSpace(rest[0]))
	}
	switch verb {
	case "help", "-h":
		fmt.Print(i18n.Tr(agentUsage))
		return nil
	case "", "status", "показать":
		return agentStatus(*cfgPath)
	case "on", "включить", "enable":
		return agentSet(*cfgPath, spec.SetEnabled, true)
	case "off", "выключить", "disable":
		return agentSet(*cfgPath, spec.SetEnabled, false)
	case "auto":
		sub := ""
		if len(rest) > 1 {
			sub = strings.ToLower(strings.TrimSpace(rest[1]))
		}
		switch sub {
		case "on", "включить":
			return agentSet(*cfgPath, spec.SetAutoSpec, true)
		case "off", "выключить":
			return agentSet(*cfgPath, spec.SetAutoSpec, false)
		}
		return fmt.Errorf(i18n.Tr("не понял «auto %s»: steno agent auto on | off"), sub)
	}
	return fmt.Errorf(i18n.Tr("не понял «%s»: steno agent on | off | auto on | auto off | status"), verb)
}

// agentSet правит файл и показывает, что вышло: человек, нажавший
// выключатель, должен увидеть новое состояние, а не тишину.
func agentSet(cfgPath string, set func(string, bool) error, on bool) error {
	path := core.ResolveConfigPath(cfgPath)
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf(i18n.Tr("конфига %s нет — сначала steno setup"), path)
	}
	if err := set(path, on); err != nil {
		return err
	}
	return agentStatus(cfgPath)
}

func agentStatus(cfgPath string) error {
	cfg, st, err := open(cfgPath)
	if err != nil {
		return err
	}
	defer st.Close()
	set := spec.SettingsFor(cfg)
	r := spec.Check(cfg, set, true)
	fmt.Printf(i18n.Tr("steno agent · конфиг %s\n\n"), core.OrDash(cfg.Path))
	for _, line := range spec.Describe(r, set, cfg.DataDir) {
		fmt.Printf("  %s\n", line)
	}
	fmt.Println()
	switch {
	case !set.Enabled:
		fmt.Println(dim(i18n.Tr("включить исполнение:  steno agent on")))
	case r.Probed && !r.Ready:
		fmt.Println(dim(i18n.Tr("исполнение включено, но исполнителя нет — см. выше")))
	default:
		fmt.Println(dim(i18n.Tr("выключить исполнение:  steno agent off")))
	}
	if !set.AutoSpec {
		fmt.Println(dim(i18n.Tr("собирать ТЗ самому:   steno agent auto on")))
	}
	return nil
}
