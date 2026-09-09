package main

import (
	"fmt"
	"html"
	"strings"
)

// Один follow-up, три адресата. Формат везде один и тот же по смыслу и разный
// по объёму: в Slack и Telegram — то, на что человек посмотрит с телефона,
// в Google Docs — всё, включая расшифровку.

func (f *Followup) empty() bool {
	return len(f.TLDR) == 0 && len(f.Decisions) == 0 && len(f.ActionItems) == 0
}

func dueOr(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

// renderHTML — то, что уходит в Drive и конвертируется в Google Doc. Drive сам
// разбирает заголовки, списки и жирный текст, поэтому строить документ через
// batchUpdate не нужно.
func renderHTML(m *Meeting, f *Followup, segs []Segment) string {
	var b strings.Builder
	e := html.EscapeString

	fmt.Fprintf(&b, "<h1>%s</h1>\n", e(orDash(f.Title)))
	fmt.Fprintf(&b, "<p><i>%s", e(m.StartedAt.Format("2 January 2006, 15:04")))
	if m.EndedAt != nil {
		fmt.Fprintf(&b, " — %s", e(m.EndedAt.Sub(m.StartedAt).Round(60000000000).String()))
	}
	if len(m.Participants) > 0 {
		fmt.Fprintf(&b, " · %s", e(strings.Join(m.Participants, ", ")))
	}
	b.WriteString("</i></p>\n")

	if len(f.TLDR) > 0 {
		b.WriteString("<h2>Коротко</h2>\n<ul>\n")
		for _, s := range f.TLDR {
			fmt.Fprintf(&b, "<li>%s</li>\n", e(s))
		}
		b.WriteString("</ul>\n")
	}

	if len(f.ActionItems) > 0 {
		b.WriteString("<h2>Задачи</h2>\n<ul>\n")
		for _, a := range f.ActionItems {
			fmt.Fprintf(&b, "<li><b>%s</b> — %s <i>(срок: %s, %s)</i><br/><span>«%s»</span></li>\n",
				e(a.Owner), e(a.What), e(dueOr(a.Due, "не назван")), e(clock(a.At)), e(a.Quote))
		}
		b.WriteString("</ul>\n")
	}

	if len(f.Decisions) > 0 {
		b.WriteString("<h2>Решения</h2>\n<ul>\n")
		for _, d := range f.Decisions {
			fmt.Fprintf(&b, "<li><b>%s</b> — %s <i>(%s)</i></li>\n", e(d.What), e(d.Why), e(clock(d.At)))
		}
		b.WriteString("</ul>\n")
	}

	if len(f.OpenQuestions) > 0 {
		b.WriteString("<h2>Открытые вопросы</h2>\n<ul>\n")
		for _, q := range f.OpenQuestions {
			fmt.Fprintf(&b, "<li>%s <i>(ждём: %s, %s)</i></li>\n",
				e(q.Question), e(dueOr(q.WaitingOn, "не определено")), e(clock(q.At)))
		}
		b.WriteString("</ul>\n")
	}

	if len(f.Risks) > 0 {
		b.WriteString("<h2>Риски</h2>\n<ul>\n")
		for _, r := range f.Risks {
			fmt.Fprintf(&b, "<li>%s</li>\n", e(r))
		}
		b.WriteString("</ul>\n")
	}

	if len(f.Timeline) > 0 {
		b.WriteString("<h2>Как шёл разговор</h2>\n<ul>\n")
		for _, t := range f.Timeline {
			fmt.Fprintf(&b, "<li><b>%s %s</b> — %s</li>\n", e(clock(t.At)), e(t.Title), e(t.Summary))
		}
		b.WriteString("</ul>\n")
	}

	if len(segs) > 0 {
		b.WriteString("<h2>Расшифровка</h2>\n")
		for _, line := range strings.Split(renderTranscript(segs), "\n") {
			if line == "" {
				continue
			}
			fmt.Fprintf(&b, "<p>%s</p>\n", e(line))
		}
	}
	return b.String()
}

// esc экранирует то, что Slack считает разметкой. Имена участников и
// владельцев задач приходят из субтитров Meet и из ответа модели: участник,
// назвавшийся <!channel>, иначе поднимает пингом весь канал от имени бота.
func slackEsc(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// renderSlack — mrkdwn Slack: *жирный*, _курсив_, без заголовков.
func renderSlack(m *Meeting, f *Followup, docURL string) string {
	e := slackEsc
	var b strings.Builder
	fmt.Fprintf(&b, "*%s*\n_%s", e(orDash(f.Title)), m.StartedAt.Format("2 Jan, 15:04"))
	if len(m.Participants) > 0 {
		fmt.Fprintf(&b, " · %s", e(strings.Join(m.Participants, ", ")))
	}
	b.WriteString("_\n")

	for _, s := range f.TLDR {
		fmt.Fprintf(&b, "• %s\n", e(s))
	}
	if len(f.ActionItems) > 0 {
		b.WriteString("\n*Задачи*\n")
		for _, a := range f.ActionItems {
			fmt.Fprintf(&b, "• *%s* — %s _(%s)_\n", e(a.Owner), e(a.What), e(dueOr(a.Due, "срок не назван")))
		}
	}
	if len(f.Decisions) > 0 {
		b.WriteString("\n*Решения*\n")
		for _, d := range f.Decisions {
			fmt.Fprintf(&b, "• %s\n", e(d.What))
		}
	}
	if len(f.OpenQuestions) > 0 {
		b.WriteString("\n*Открытые вопросы*\n")
		for _, q := range f.OpenQuestions {
			fmt.Fprintf(&b, "• %s _(ждём: %s)_\n", e(q.Question), e(dueOr(q.WaitingOn, "не определено")))
		}
	}
	if docURL != "" {
		fmt.Fprintf(&b, "\n<%s|Полные заметки и расшифровка>", docURL)
	}
	return b.String()
}

// renderTelegram — HTML Telegram: из спецсимволов экранировать нужно только
// три, в отличие от MarkdownV2, где экранируется полтора десятка.
func renderTelegram(m *Meeting, f *Followup, docURL string) string {
	var b strings.Builder
	e := func(s string) string {
		r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
		return r.Replace(s)
	}
	fmt.Fprintf(&b, "<b>%s</b>\n<i>%s", e(orDash(f.Title)), e(m.StartedAt.Format("2 Jan, 15:04")))
	if len(m.Participants) > 0 {
		fmt.Fprintf(&b, " · %s", e(strings.Join(m.Participants, ", ")))
	}
	b.WriteString("</i>\n\n")

	for _, s := range f.TLDR {
		fmt.Fprintf(&b, "• %s\n", e(s))
	}
	if len(f.ActionItems) > 0 {
		b.WriteString("\n<b>Задачи</b>\n")
		for _, a := range f.ActionItems {
			fmt.Fprintf(&b, "• <b>%s</b> — %s <i>(%s)</i>\n",
				e(a.Owner), e(a.What), e(dueOr(a.Due, "срок не назван")))
		}
	}
	if len(f.Decisions) > 0 {
		b.WriteString("\n<b>Решения</b>\n")
		for _, d := range f.Decisions {
			fmt.Fprintf(&b, "• %s\n", e(d.What))
		}
	}
	if len(f.OpenQuestions) > 0 {
		b.WriteString("\n<b>Открытые вопросы</b>\n")
		for _, q := range f.OpenQuestions {
			fmt.Fprintf(&b, "• %s <i>(ждём: %s)</i>\n", e(q.Question), e(dueOr(q.WaitingOn, "не определено")))
		}
	}
	if docURL != "" {
		fmt.Fprintf(&b, "\n<a href=\"%s\">Полные заметки и расшифровка</a>", e(docURL))
	}
	return b.String()
}

// splitForTelegram режет сообщение по лимиту в 4096 символов, стараясь рвать
// по переводам строк, чтобы не разбить HTML-тег пополам. Резка по символам
// живёт в chunk — здесь она была продублирована и разошлась: байтовый индекс
// сравнивался с бюджетом в символах, и кириллица рвалась вчетверо раньше.
func splitForTelegram(s string) []string {
	return chunk(s, 3900)
}

// renderPlain — то же самое без разметки, для `steno show` в терминале.
func renderPlain(m *Meeting, f *Followup) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n%s", orDash(f.Title), m.StartedAt.Format("2 Jan 2006, 15:04"))
	if len(m.Participants) > 0 {
		fmt.Fprintf(&b, " · %s", strings.Join(m.Participants, ", "))
	}
	b.WriteString("\n\n")

	for _, s := range f.TLDR {
		fmt.Fprintf(&b, "  %s\n", s)
	}
	if len(f.ActionItems) > 0 {
		b.WriteString("\nЗадачи\n")
		for _, a := range f.ActionItems {
			fmt.Fprintf(&b, "  [%s] %s — %s (%s)\n",
				clock(a.At), a.Owner, a.What, dueOr(a.Due, "срок не назван"))
		}
	}
	if len(f.Decisions) > 0 {
		b.WriteString("\nРешения\n")
		for _, d := range f.Decisions {
			fmt.Fprintf(&b, "  [%s] %s — %s\n", clock(d.At), d.What, d.Why)
		}
	}
	if len(f.OpenQuestions) > 0 {
		b.WriteString("\nОткрытые вопросы\n")
		for _, q := range f.OpenQuestions {
			fmt.Fprintf(&b, "  [%s] %s (ждём: %s)\n",
				clock(q.At), q.Question, dueOr(q.WaitingOn, "не определено"))
		}
	}
	if len(f.Risks) > 0 {
		b.WriteString("\nРиски\n")
		for _, x := range f.Risks {
			fmt.Fprintf(&b, "  %s\n", x)
		}
	}
	return b.String()
}
