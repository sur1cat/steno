package main

import (
	"fmt"
	"html/template"
	"strings"
	"time"
)

// Функции шаблонов панели. Всё, что касается «как человек читает дату и
// длительность», собрано здесь, а не размазано по разметке.

var monthsRu = [...]string{"января", "февраля", "марта", "апреля", "мая", "июня",
	"июля", "августа", "сентября", "октября", "ноября", "декабря"}

var panelFuncs = template.FuncMap{
	"clock": clock,

	// dateRu: «8 сентября, 11:00». Сегодняшнее и вчерашнее называем словом —
	// в списке созвонов это самые частые строки.
	"dateRu": func(t time.Time) string {
		if t.IsZero() || t.Unix() <= 0 {
			return "—"
		}
		now := time.Now()
		day := t.Format("15:04")
		switch {
		case sameDay(t, now):
			return "сегодня, " + day
		case sameDay(t, now.AddDate(0, 0, -1)):
			return "вчера, " + day
		case t.Year() == now.Year():
			return fmt.Sprintf("%d %s, %s", t.Day(), monthsRu[t.Month()-1], day)
		}
		return fmt.Sprintf("%d %s %d, %s", t.Day(), monthsRu[t.Month()-1], t.Year(), day)
	},

	"durRu": func(d time.Duration) string {
		if d <= 0 {
			return ""
		}
		if h := int(d.Hours()); h > 0 {
			return fmt.Sprintf("%d ч %d мин", h, int(d.Minutes())%60)
		}
		return fmt.Sprintf("%d мин", int(d.Minutes()))
	},

	"join": func(sep string, v []string) string { return strings.Join(v, sep) },

	// «1 задача», «2 задачи», «5 задач». Без согласования интерфейс выглядит
	// машинным переводом, а этот текст видно на каждой странице.
	"plural": plural,

	// Ключи публикаций хранятся как идентификаторы, а показывать их человеку
	// в таком виде незачем.
	"targetRu": func(t string) string {
		switch {
		case strings.HasPrefix(t, "google_docs"):
			return "Google Docs"
		case strings.HasPrefix(t, "slack"):
			return "Slack"
		case strings.HasPrefix(t, "telegram"):
			return "Telegram"
		}
		return t
	},

	// В индексе поиска лежит сырая расшифровка — вместе со всем, что люди
	// наговорили и что попало в название встречи из Telegram или по HTTP.
	// Поэтому сниппет сначала экранируется целиком, и только потом служебные
	// символы вокруг найденного заменяются на <mark>. Иначе достаточно было
	// назвать созвон «<script>…», чтобы код выполнился у каждого, кто ищет.
	"snippetHTML": func(s string) template.HTML {
		safe := template.HTMLEscapeString(s)
		safe = strings.ReplaceAll(safe, markStart, "<mark>")
		safe = strings.ReplaceAll(safe, markEnd, "</mark>")
		return template.HTML(safe)
	},

	"dueRu": func(due string) string {
		if strings.TrimSpace(due) == "" {
			return "срок не назван"
		}
		t, err := time.Parse("2006-01-02", due)
		if err != nil {
			return due
		}
		return fmt.Sprintf("%d %s", t.Day(), monthsRu[t.Month()-1])
	},

	// Просрочка считается по местной полуночи. Truncate работает по UTC:
	// в Алматы задача на 8-е оставалась «не просроченной» до трёх часов дня
	// девятого, а в Нью-Йорке краснела на день раньше срока.
	"overdue": func(due string) bool {
		t, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(due), time.Local)
		if err != nil {
			return false
		}
		now := time.Now()
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
		return t.Before(today)
	},

	"statusRu": func(s string) string {
		switch s {
		case "recording":
			return "идёт запись"
		case "recorded":
			return "записан"
		case "transcribed":
			return "расшифрован"
		case "summarized":
			return "есть follow-up"
		case "published":
			return "разослан"
		case "publish_failed":
			return "не разослан"
		case "failed":
			return "сорвался"
		}
		return s
	},

	// Незаконченное или сорвавшееся подсвечиваем: обрезанная запись снаружи
	// выглядит как нормальная, и это надо видеть.
	"statusAlarm": func(s string) bool {
		return s == "failed" || s == "publish_failed" || s == "recording"
	},

	"add": func(a, b int) int { return a + b },
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}
