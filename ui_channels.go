package main

import (
	"fmt"
	"strings"
)

// Каналы в терминале. Форма рисуется по channelDefs — по тому же описанию, по
// которому её рисует веб-панель. Свой список полей означал бы, что новое поле
// заводится в трёх местах вместо одного, и рано или поздно терминал начал бы
// показывать не то, что панель.
//
// Секретов здесь нет вовсе — ни значений, ни имён переменных окружения. Токены
// задаёт `steno setup` в .env, и правило это то же самое, что в панели.
//
// Пишем в Store, а не в конфиг: после первого запуска каналы живут в базе, и
// правка steno.json для них уже ничего не делает. Молча.

// uiChanFieldState — состояние одного поля в форме канала.
type uiChanFieldState struct {
	def   ChannelField
	text  textField   // text | number | duration
	on    bool        // switch
	items []textField // list — по значению на строку
}

// uiChanForm — открытая форма канала.
type uiChanForm struct {
	key, name, about string
	live             bool
	enabled          bool
	fields           []uiChanFieldState
	cursor           int
}

// uiChanRow — строка формы под курсором. Форма плоская: переключатель канала,
// затем поля, а у списков — по строке на значение плюс строка «добавить».
type uiChanRow struct {
	field int // -1 — переключатель самого канала
	item  int // -1 — само поле; >=0 — значение списка; -2 — «добавить»
}

const (
	uiRowSelf = -1
	uiRowAdd  = -2
)

func newChanForm(ch Channel) *uiChanForm {
	f := &uiChanForm{key: ch.Key, name: ch.Name, about: ch.About,
		live: ch.Live, enabled: ch.Enabled}
	for _, def := range ch.Fields {
		st := uiChanFieldState{def: def}
		v := ch.Values[def.Key]
		switch def.Kind {
		case "switch":
			st.on = chBool(v)
		case "list":
			for _, item := range chList(v) {
				st.items = append(st.items, newTextField(item))
			}
		case "google":
			// Значения нет: это состояние доступа, а не поле.
		default:
			st.text = newTextField(v)
		}
		f.fields = append(f.fields, st)
	}
	return f
}

// rows — плоский список того, по чему ходит курсор. Поля вида «google» в него
// не попадают: там нечего править, а курсор, застревающий на строке, которую
// нельзя изменить, читается как поломка.
func (f *uiChanForm) rows() []uiChanRow {
	out := []uiChanRow{{field: -1, item: uiRowSelf}}
	for i, fs := range f.fields {
		switch fs.def.Kind {
		case "google":
		case "list":
			for j := range fs.items {
				out = append(out, uiChanRow{field: i, item: j})
			}
			out = append(out, uiChanRow{field: i, item: uiRowAdd})
		default:
			out = append(out, uiChanRow{field: i, item: uiRowSelf})
		}
	}
	return out
}

func (f *uiChanForm) clampCursor() {
	n := len(f.rows())
	if f.cursor < 0 {
		f.cursor = n - 1
	}
	if f.cursor >= n {
		f.cursor = 0
	}
}

func (f *uiChanForm) current() uiChanRow {
	rows := f.rows()
	if f.cursor < 0 || f.cursor >= len(rows) {
		return uiChanRow{field: -1, item: uiRowSelf}
	}
	return rows[f.cursor]
}

// currentText — поле ввода под курсором; nil, если там переключатель или
// «добавить значение».
func (f *uiChanForm) currentText() *textField {
	r := f.current()
	if r.field < 0 || r.field >= len(f.fields) {
		return nil
	}
	fs := &f.fields[r.field]
	switch {
	case fs.def.Kind == "list" && r.item >= 0 && r.item < len(fs.items):
		return &fs.items[r.item]
	case fs.def.Kind == "switch" || fs.def.Kind == "list" || fs.def.Kind == "google":
		return nil
	case r.item == uiRowSelf:
		return &fs.text
	}
	return nil
}

// addItem заводит пустое значение списка и ставит на него курсор.
func (f *uiChanForm) addItem(field int) {
	if field < 0 || field >= len(f.fields) || f.fields[field].def.Kind != "list" {
		return
	}
	f.fields[field].items = append(f.fields[field].items, textField{})
	for i, r := range f.rows() {
		if r.field == field && r.item == len(f.fields[field].items)-1 {
			f.cursor = i
			return
		}
	}
}

func (f *uiChanForm) removeItem(field, item int) bool {
	if field < 0 || field >= len(f.fields) {
		return false
	}
	fs := &f.fields[field]
	if fs.def.Kind != "list" || item < 0 || item >= len(fs.items) {
		return false
	}
	fs.items = append(fs.items[:item], fs.items[item+1:]...)
	f.clampCursor()
	return true
}

// values собирает то, что уйдёт в базу: по одному описанному полю. Список
// склеивается запятыми — так он и лежит в хранилище. Переключатель пишется
// через chFlag: «1» и ничто, а не «on» или «true», — chBool узнаёт только его.
func (f *uiChanForm) values() map[string]string {
	out := map[string]string{}
	for _, fs := range f.fields {
		if !fs.def.input() {
			continue
		}
		switch fs.def.Kind {
		case "switch":
			out[fs.def.Key] = chFlag(fs.on)
		case "list":
			var vals []string
			for _, it := range fs.items {
				if v := strings.TrimSpace(it.String()); v != "" {
					vals = append(vals, v)
				}
			}
			out[fs.def.Key] = strings.Join(vals, ", ")
		default:
			out[fs.def.Key] = strings.TrimSpace(fs.text.String())
		}
	}
	return out
}

// uiSaveChannel кладёт форму в базу тем же путём, что и панель: описанные поля
// перезаписываются, всё прочее из базы сохраняется. В базе у давних установок
// лежат поля, которых в форме больше нет — путь к ключу организации например, —
// и терять их из-за правки соседнего поля нельзя.
func uiSaveChannel(st *Store, key string, enabled bool, fresh map[string]string) error {
	def, ok := channelByKey(key)
	if !ok {
		return fmt.Errorf(tr("нет такого канала: %s"), key)
	}
	values := map[string]string{}
	if have, err := st.ChannelSettings(); err == nil {
		for k, v := range have[key].Values {
			values[k] = v
		}
	}
	for _, fd := range def.fields {
		if fd.input() {
			values[fd.Key] = strings.TrimSpace(fresh[fd.Key])
		}
	}
	return st.SaveChannel(key, enabled, values)
}

// uiChannelWhat — что канал делает: приносит созвоны, уносит follow-up или и то
// и другое. Строка короткая, потому что стоит в колонке списка.
func uiChannelWhat(ch Channel) string {
	switch {
	case ch.In && ch.Out:
		return tr("и приносит, и шлёт")
	case ch.In:
		return tr("приносит созвоны")
	case ch.Out:
		return tr("шлёт follow-up")
	}
	return ""
}

func uiFilterChannels(rows []Channel, text string) []Channel {
	needle := strings.ToLower(strings.TrimSpace(text))
	if needle == "" {
		return rows
	}
	out := make([]Channel, 0, len(rows))
	for _, c := range rows {
		if strings.Contains(strings.ToLower(c.Name+" "+c.Key+" "+c.About+" "+c.Summary), needle) {
			out = append(out, c)
		}
	}
	return out
}
