package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
)

// steno panel on|off — поднимать ли веб-панель вместе с сервисом.
//
// Панель необязательна: терминальный интерфейс, строка меню и команды
// закрывают всё то же. Раньше она включалась при установке молча и
// поднималась с каждым `steno start` — человек, которому она не нужна,
// получал открытый порт, о котором не просил. Переключатель, как у агента:
// одна команда, без мастера.
func cmdPanel(args []string) error {
	fs := newFlagSet("panel")
	cfgPath := setupFlags(fs)
	rest, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	verb := ""
	if len(rest) > 0 {
		verb = strings.ToLower(strings.TrimSpace(rest[0]))
	}
	path := core.ResolveConfigPath(*cfgPath)
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf(i18n.Tr("конфига %s нет — сначала steno setup"), path)
	}
	switch verb {
	case "", "status":
		cfg, err := core.LoadConfig(path)
		if err != nil {
			return err
		}
		if cfg.Panel.Enabled {
			fmt.Println(i18n.Tr("панель включена: ") + cfg.Panel.Addr)
			fmt.Println(dim(i18n.Tr("  выключить:  steno panel off")))
		} else {
			fmt.Println(i18n.Tr("панель выключена — сервис поднимается без неё"))
			fmt.Println(dim(i18n.Tr("  включить:  steno panel on")))
		}
		return nil
	case "on", "включить":
		return setPanelEnabled(path, true)
	case "off", "выключить":
		return setPanelEnabled(path, false)
	}
	return fmt.Errorf(i18n.Tr("не понял «%s»: steno panel on | off | status"), verb)
}

// setPanelEnabled правит один ключ в JSON, остальное — как лежало.
func setPanelEnabled(path string, on bool) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf(i18n.Tr("конфиг %s не читается: %w"), path, err)
	}
	var p map[string]any
	if cur, ok := doc["panel"]; ok && len(cur) > 0 {
		if err := json.Unmarshal(cur, &p); err != nil {
			return fmt.Errorf(i18n.Tr("раздел panel в %s не читается: %w"), path, err)
		}
	}
	if p == nil {
		p = map[string]any{}
	}
	p["enabled"] = on
	pb, err := json.Marshal(p)
	if err != nil {
		return err
	}
	doc["panel"] = pb
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		return err
	}
	if on {
		fmt.Println(ok(i18n.Tr("панель включена — поднимется при следующем steno start")))
	} else {
		fmt.Println(ok(i18n.Tr("панель выключена — при следующем steno start порт не откроется")))
	}
	if _, _, state := findDaemon(path); state == daemonRunning {
		fmt.Println(dim(i18n.Tr("  сервис сейчас работает по-старому:  steno stop && steno start")))
	}
	return nil
}
