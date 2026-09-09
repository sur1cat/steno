package main

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Однострочное поле ввода. Своё, а не из bubbles: всё состояние тогда лежит в
// нашей модели и проверяется обычным тестом «нажали клавишу — вот что вышло»,
// без запуска терминала.

type textField struct {
	runes []rune
	cur   int // позиция курсора в рунах, 0..len(runes)
}

func newTextField(s string) textField {
	f := textField{}
	f.set(s)
	return f
}

func (f *textField) set(s string) {
	f.runes = []rune(s)
	f.cur = len(f.runes)
}

func (f textField) String() string { return string(f.runes) }

func (f textField) empty() bool { return len(f.runes) == 0 }

func (f *textField) insert(rs []rune) {
	if len(rs) == 0 {
		return
	}
	f.clamp()
	out := make([]rune, 0, len(f.runes)+len(rs))
	out = append(out, f.runes[:f.cur]...)
	out = append(out, rs...)
	out = append(out, f.runes[f.cur:]...)
	f.runes = out
	f.cur += len(rs)
}

func (f *textField) backspace() {
	f.clamp()
	if f.cur == 0 {
		return
	}
	f.runes = append(f.runes[:f.cur-1], f.runes[f.cur:]...)
	f.cur--
}

func (f *textField) del() {
	f.clamp()
	if f.cur >= len(f.runes) {
		return
	}
	f.runes = append(f.runes[:f.cur], f.runes[f.cur+1:]...)
}

func (f *textField) left() {
	f.clamp()
	if f.cur > 0 {
		f.cur--
	}
}

func (f *textField) right() {
	f.clamp()
	if f.cur < len(f.runes) {
		f.cur++
	}
}

func (f *textField) home() { f.cur = 0 }
func (f *textField) end()  { f.cur = len(f.runes) }

// killWord убирает слово слева от курсора: ctrl+w, как в любой оболочке.
func (f *textField) killWord() {
	f.clamp()
	i := f.cur
	for i > 0 && f.runes[i-1] == ' ' {
		i--
	}
	for i > 0 && f.runes[i-1] != ' ' {
		i--
	}
	f.runes = append(f.runes[:i], f.runes[f.cur:]...)
	f.cur = i
}

func (f *textField) clear() {
	f.runes = nil
	f.cur = 0
}

func (f *textField) clamp() {
	if f.cur < 0 {
		f.cur = 0
	}
	if f.cur > len(f.runes) {
		f.cur = len(f.runes)
	}
}

// view рисует поле в отведённые width колонок. Длинная строка не растягивает
// экран, а едет под курсором: набранный путь к репозиторию длиннее восьмидесяти
// колонок — обычное дело, и видеть при этом надо то место, где стоит курсор.
func (f textField) view(width int, focused bool) string {
	if width < 1 {
		width = 1
	}
	cur := f.cur
	if cur > len(f.runes) {
		cur = len(f.runes)
	}
	if cur < 0 {
		cur = 0
	}
	if !focused {
		return uiFit(string(f.runes), width)
	}

	// Курсор занимает колонку: под ним рисуется символ или пробел в конце.
	from := 0
	for uiRunesWidth(f.runes[from:cur])+1 > width {
		from++
	}
	visible := f.runes[from:]
	// Хвост обрезаем так, чтобы курсор остался внутри окна.
	end := len(visible)
	for uiRunesWidth(visible[:end])+1 > width && end > cur-from {
		end--
	}
	visible = visible[:end]

	rel := cur - from
	var b strings.Builder
	b.WriteString(string(visible[:rel]))
	if rel < len(visible) {
		b.WriteString(uiCursorStyle.Render(string(visible[rel])))
		b.WriteString(string(visible[rel+1:]))
	} else {
		// Курсор за последним символом — рисуем его на пустом месте, иначе в
		// конце строки не видно, куда попадёт следующая буква.
		b.WriteString(uiCursorStyle.Render(" "))
	}
	return uiFit(b.String(), width)
}

func uiRunesWidth(rs []rune) int { return ansi.StringWidth(string(rs)) }
