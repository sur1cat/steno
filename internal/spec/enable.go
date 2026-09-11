package spec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
)

// Правка раздела "agent" в steno.json — для `steno agent on|off` и кнопки в
// строке меню.
//
// Файл правится, а не перезаписывается из core.Config: конфиг человек держит
// руками, в нём есть порядок разделов и бывают ключи, о которых эта версия
// steno не знает. Пересобрать его из структуры значило бы разложить разделы
// по алфавиту и потерять чужое. Поэтому здесь разбор по токенам: верхние ключи
// в том порядке, в каком лежат, значения — как есть, и меняется ровно один
// раздел, а в нём — ровно названные поля.

type entry struct {
	key string
	raw json.RawMessage
}

// PatchAgent меняет поля раздела "agent" и пишет файл обратно. change
// получает раздел таким, каким он записан (или пустым, если раздела нет), и
// возвращает, что должно быть записано.
func PatchAgent(configPath string, change func(Settings) Settings) error {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf(i18n.Tr("конфига %s нет — сначала steno setup"), configPath)
	}
	top, err := entries(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", configPath, err)
	}

	var cur Settings
	var agent []entry
	found := -1
	for i, e := range top {
		if e.key != "agent" {
			continue
		}
		found = i
		if err := json.Unmarshal(e.raw, &cur); err != nil {
			return fmt.Errorf(i18n.Tr("раздел agent в %s не читается: %w"), configPath, err)
		}
		if agent, err = entries(e.raw); err != nil {
			return fmt.Errorf(i18n.Tr("раздел agent в %s не читается: %w"), configPath, err)
		}
	}
	if found < 0 {
		// Раздела не было — заводим целиком, с умолчаниями: человек, который
		// откроет файл после `steno agent on`, должен увидеть все поля, а не
		// одно "enabled".
		cur = core.DefaultConfig().Agent
	}
	next := change(cur)

	// Поля раздела — те, что знает эта версия, в порядке структуры; чужие
	// ключи остаются на своих местах и со своими значениями. В уже
	// написанный раздел дописываются только изменённые поля: короткий
	// {"enabled": false} человека не должен разрастись до "timeout": "0s" от
	// одного нажатия выключателя.
	known, err := entries(mustJSON(next))
	if err != nil {
		return err
	}
	was, err := entries(mustJSON(cur))
	if err != nil {
		return err
	}
	merged := make([]entry, 0, len(agent)+len(known))
	seen := map[string]bool{}
	for _, e := range agent {
		if k := lookup(known, e.key); k != nil {
			merged = append(merged, entry{key: e.key, raw: k})
		} else {
			merged = append(merged, e)
		}
		seen[e.key] = true
	}
	for _, e := range known {
		if seen[e.key] {
			continue
		}
		if found >= 0 && string(lookup(was, e.key)) == string(e.raw) {
			continue // не менялось и не было записано — не дописываем
		}
		merged = append(merged, e)
	}
	section := render(merged, "  ")
	if found >= 0 {
		top[found].raw = section
	} else {
		top = append(top, entry{key: "agent", raw: section})
	}
	out := render(top, "")
	return os.WriteFile(configPath, append(out, '\n'), 0o644)
}

// SetEnabled — выключатель исполнения.
func SetEnabled(configPath string, on bool) error {
	return PatchAgent(configPath, func(s Settings) Settings {
		s.Enabled = on
		return s
	})
}

// SetAutoSpec — выключатель автоматической сборки ТЗ.
func SetAutoSpec(configPath string, on bool) error {
	return PatchAgent(configPath, func(s Settings) Settings {
		s.AutoSpec = on
		return s
	})
}

// entries разбирает объект JSON в список пар, не теряя порядка ключей.
func entries(raw []byte) ([]entry, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New(i18n.Tr("ожидался объект JSON"))
	}
	var out []entry
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, errors.New(i18n.Tr("ожидался ключ"))
		}
		var val json.RawMessage
		if err := dec.Decode(&val); err != nil {
			return nil, err
		}
		out = append(out, entry{key: key, raw: val})
	}
	if _, err := dec.Token(); err != nil && err != io.EOF {
		return nil, err
	}
	return out, nil
}

func lookup(es []entry, key string) json.RawMessage {
	for _, e := range es {
		if e.key == key {
			return e.raw
		}
	}
	return nil
}

// render печатает объект с отступом в два пробела — как marshalConfig в
// мастере, чтобы правленный файл не отличался от написанного.
func render(es []entry, indent string) json.RawMessage {
	var b bytes.Buffer
	b.WriteString("{\n")
	for i, e := range es {
		b.WriteString(indent + "  ")
		k, _ := json.Marshal(e.key)
		b.Write(k)
		b.WriteString(": ")
		var v bytes.Buffer
		if err := json.Indent(&v, e.raw, indent+"  ", "  "); err != nil {
			v.Reset()
			v.Write(e.raw)
		}
		b.Write(v.Bytes())
		if i < len(es)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString(indent + "}")
	return b.Bytes()
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return b
}

// Describe — раздел словами, для `steno agent` без аргументов и для строки
// в doctor. Одна функция на оба места, чтобы они не разошлись.
func Describe(r Readiness, set Settings, dataDir string) []string {
	var out []string
	if r.Enabled {
		out = append(out, i18n.Tr("исполнение: включено — steno может писать файлы и выполнять команды на этой машине"))
	} else {
		out = append(out, i18n.Tr("исполнение: выключено — ТЗ собираются, ветки не заводятся"))
	}
	if r.AutoSpec {
		out = append(out, i18n.Tr("ТЗ: собираются сами после каждого разбора"))
	} else {
		out = append(out, i18n.Tr("ТЗ: по запросу (steno spec <задача>, кнопка в панели)"))
	}
	switch {
	case r.Executor == "":
		out = append(out, i18n.Tr("исполнитель: не выбран — ")+r.Why)
	case r.Probed && !r.Ready:
		out = append(out, i18n.Trf("исполнитель: %s — %s", r.Executor, r.Why))
	default:
		out = append(out, i18n.Trf("исполнитель: %s", r.Executor))
	}
	out = append(out, i18n.Trf("ветки: %s…, рабочие копии в %s",
		prefixOf(set), worktreeDirOf(set, dataDir)))
	return out
}
