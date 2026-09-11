package publish

import (
	"fmt"
	"html"
	"strings"

	"github.com/sur1cat/steno/internal/brain"
	"github.com/sur1cat/steno/internal/core"
	"github.com/sur1cat/steno/internal/i18n"
)

// Один follow-up, три адресата. Формат везде один и тот же по смыслу и разный
// по объёму: в Slack и Telegram — то, на что человек посмотрит с телефона,
// в Google Docs — всё, включая расшифровку.

func DueOr(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

// RenderHTML — то, что уходит в Drive и конвертируется в Google Doc. Drive сам
// разбирает заголовки, списки и жирный текст, поэтому строить документ через
// batchUpdate не нужно.
func RenderHTML(m *core.Meeting, f *core.Followup, segs []core.Segment) string {
	var b strings.Builder
	e := html.EscapeString

	fmt.Fprintf(&b, "<h1>%s</h1>\n", e(core.OrDash(f.Title)))
	fmt.Fprintf(&b, "<p><i>%s", e(m.StartedAt.Format("2 January 2006, 15:04")))
	if m.EndedAt != nil {
		fmt.Fprintf(&b, " — %s", e(m.EndedAt.Sub(m.StartedAt).Round(60000000000).String()))
	}
	if len(m.Participants) > 0 {
		fmt.Fprintf(&b, " · %s", e(strings.Join(m.Participants, ", ")))
	}
	b.WriteString("</i></p>\n")

	if len(f.TLDR) > 0 {
		b.WriteString(i18n.Tr("<h2>Коротко</h2>\n<ul>\n"))
		for _, s := range f.TLDR {
			fmt.Fprintf(&b, "<li>%s</li>\n", e(s))
		}
		b.WriteString("</ul>\n")
	}

	if len(f.ActionItems) > 0 {
		b.WriteString(i18n.Tr("<h2>Задачи</h2>\n<ul>\n"))
		for _, a := range f.ActionItems {
			fmt.Fprintf(&b, i18n.Tr("<li><b>%s</b> — %s <i>(срок: %s, %s)</i><br/><span>«%s»</span></li>\n"),
				e(a.Owner), e(a.What), e(DueOr(a.Due, i18n.Tr("не назван"))), e(core.Clock(a.At)), e(a.Quote))
		}
		b.WriteString("</ul>\n")
	}

	if len(f.Decisions) > 0 {
		b.WriteString(i18n.Tr("<h2>Решения</h2>\n<ul>\n"))
		for _, d := range f.Decisions {
			fmt.Fprintf(&b, "<li><b>%s</b> — %s <i>(%s)</i></li>\n", e(d.What), e(d.Why), e(core.Clock(d.At)))
		}
		b.WriteString("</ul>\n")
	}

	if len(f.OpenQuestions) > 0 {
		b.WriteString(i18n.Tr("<h2>Открытые вопросы</h2>\n<ul>\n"))
		for _, q := range f.OpenQuestions {
			fmt.Fprintf(&b, i18n.Tr("<li>%s <i>(ждём: %s, %s)</i></li>\n"),
				e(q.Question), e(DueOr(q.WaitingOn, i18n.Tr("не определено"))), e(core.Clock(q.At)))
		}
		b.WriteString("</ul>\n")
	}

	if len(f.Risks) > 0 {
		b.WriteString(i18n.Tr("<h2>Риски</h2>\n<ul>\n"))
		for _, r := range f.Risks {
			fmt.Fprintf(&b, "<li>%s</li>\n", e(r))
		}
		b.WriteString("</ul>\n")
	}

	if len(f.Timeline) > 0 {
		b.WriteString(i18n.Tr("<h2>Как шёл разговор</h2>\n<ul>\n"))
		for _, t := range f.Timeline {
			fmt.Fprintf(&b, "<li><b>%s %s</b> — %s</li>\n", e(core.Clock(t.At)), e(t.Title), e(t.Summary))
		}
		b.WriteString("</ul>\n")
	}

	if len(segs) > 0 {
		b.WriteString(i18n.Tr("<h2>Расшифровка</h2>\n"))
		for _, line := range strings.Split(brain.RenderTranscript(segs), "\n") {
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

// RenderSlack — mrkdwn Slack: *жирный*, _курсив_, без заголовков.
func RenderSlack(m *core.Meeting, f *core.Followup, docURL string) string {
	e := slackEsc
	var b strings.Builder
	fmt.Fprintf(&b, "*%s*\n_%s", e(core.OrDash(f.Title)), m.StartedAt.Format("2 Jan, 15:04"))
	if len(m.Participants) > 0 {
		fmt.Fprintf(&b, " · %s", e(strings.Join(m.Participants, ", ")))
	}
	b.WriteString("_\n")

	for _, s := range f.TLDR {
		fmt.Fprintf(&b, "• %s\n", e(s))
	}
	if len(f.ActionItems) > 0 {
		b.WriteString(i18n.Tr("\n*Задачи*\n"))
		for _, a := range f.ActionItems {
			fmt.Fprintf(&b, "• *%s* — %s _(%s)_\n", e(a.Owner), e(a.What), e(DueOr(a.Due, i18n.Tr("срок не назван"))))
		}
	}
	if len(f.Decisions) > 0 {
		b.WriteString(i18n.Tr("\n*Решения*\n"))
		for _, d := range f.Decisions {
			fmt.Fprintf(&b, "• %s\n", e(d.What))
		}
	}
	if len(f.OpenQuestions) > 0 {
		b.WriteString(i18n.Tr("\n*Открытые вопросы*\n"))
		for _, q := range f.OpenQuestions {
			fmt.Fprintf(&b, i18n.Tr("• %s _(ждём: %s)_\n"), e(q.Question), e(DueOr(q.WaitingOn, i18n.Tr("не определено"))))
		}
	}
	if docURL != "" {
		fmt.Fprintf(&b, i18n.Tr("\n<%s|Полные заметки и расшифровка>"), docURL)
	}
	return b.String()
}

// RenderTelegram — HTML Telegram: из спецсимволов экранировать нужно только
// три, в отличие от MarkdownV2, где экранируется полтора десятка.
func RenderTelegram(m *core.Meeting, f *core.Followup, docURL string) string {
	var b strings.Builder
	e := func(s string) string {
		r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
		return r.Replace(s)
	}
	fmt.Fprintf(&b, "<b>%s</b>\n<i>%s", e(core.OrDash(f.Title)), e(m.StartedAt.Format("2 Jan, 15:04")))
	if len(m.Participants) > 0 {
		fmt.Fprintf(&b, " · %s", e(strings.Join(m.Participants, ", ")))
	}
	b.WriteString("</i>\n\n")

	for _, s := range f.TLDR {
		fmt.Fprintf(&b, "• %s\n", e(s))
	}
	if len(f.ActionItems) > 0 {
		b.WriteString(i18n.Tr("\n<b>Задачи</b>\n"))
		for _, a := range f.ActionItems {
			fmt.Fprintf(&b, "• <b>%s</b> — %s <i>(%s)</i>\n",
				e(a.Owner), e(a.What), e(DueOr(a.Due, i18n.Tr("срок не назван"))))
		}
	}
	if len(f.Decisions) > 0 {
		b.WriteString(i18n.Tr("\n<b>Решения</b>\n"))
		for _, d := range f.Decisions {
			fmt.Fprintf(&b, "• %s\n", e(d.What))
		}
	}
	if len(f.OpenQuestions) > 0 {
		b.WriteString(i18n.Tr("\n<b>Открытые вопросы</b>\n"))
		for _, q := range f.OpenQuestions {
			fmt.Fprintf(&b, i18n.Tr("• %s <i>(ждём: %s)</i>\n"), e(q.Question), e(DueOr(q.WaitingOn, i18n.Tr("не определено"))))
		}
	}
	if docURL != "" {
		fmt.Fprintf(&b, i18n.Tr("\n<a href=\"%s\">Полные заметки и расшифровка</a>"), e(docURL))
	}
	return b.String()
}

// mdEsc гасит разметку в тексте, который пришёл от модели и из субтитров.
// Имя участника со звёздочкой или подчёркиванием превращает половину сообщения
// в курсив, а название задачи с квадратной скобкой — в поломанную ссылку; те же
// «<b>» из чужой вставки на GitHub и в Notion отрисуются как настоящий тег,
// потому что markdown там пропускает HTML насквозь.
var mdEscaper = strings.NewReplacer(
	`\`, `\\`, "`", "\\`", "*", `\*`, "_", `\_`, "~", `\~`, "|", `\|`,
	"[", `\[`, "]", `\]`, "<", `\<`, ">", `\>`,
)

func mdEsc(s string) string { return mdEscaper.Replace(s) }

// RenderMarkdown — тот же follow-up обычным markdown. Это текст, который
// адаптер публикации кладёт в Discord, Mattermost, Notion, GitHub или письмо:
// разметка у них расходится в мелочах, но markdown понимают все, и заводить
// под каждую площадку свой отрисовщик здесь значило бы, что «пять строк bash»
// начинаются с полусотни строк jq.
//
// Риски здесь есть, а «как шёл разговор» нет: риск — это то, ради чего человек
// читает follow-up в чате, а хронология длиннее всего остального вместе взятого
// и в чате её никто не читает. Кому она нужна — берёт её из followup.timeline
// в том же JSON.
func RenderMarkdown(m *core.Meeting, f *core.Followup, docURL string) string {
	var b strings.Builder
	e := mdEsc
	fmt.Fprintf(&b, "## %s\n_%s", e(core.OrDash(f.Title)), e(m.StartedAt.Format("2 Jan 2006, 15:04")))
	if len(m.Participants) > 0 {
		fmt.Fprintf(&b, " · %s", e(strings.Join(m.Participants, ", ")))
	}
	b.WriteString("_\n")

	for _, s := range f.TLDR {
		fmt.Fprintf(&b, "- %s\n", e(s))
	}
	if len(f.ActionItems) > 0 {
		fmt.Fprintf(&b, "\n### %s\n", i18n.Tr("Задачи"))
		for _, a := range f.ActionItems {
			fmt.Fprintf(&b, "- **%s** — %s _(%s)_\n",
				e(a.Owner), e(a.What), e(DueOr(a.Due, i18n.Tr("срок не назван"))))
		}
	}
	if len(f.Decisions) > 0 {
		fmt.Fprintf(&b, "\n### %s\n", i18n.Tr("Решения"))
		for _, d := range f.Decisions {
			fmt.Fprintf(&b, "- %s\n", e(d.What))
		}
	}
	if len(f.OpenQuestions) > 0 {
		fmt.Fprintf(&b, "\n### %s\n", i18n.Tr("Открытые вопросы"))
		for _, q := range f.OpenQuestions {
			fmt.Fprintf(&b, i18n.Tr("- %s _(ждём: %s)_\n"),
				e(q.Question), e(DueOr(q.WaitingOn, i18n.Tr("не определено"))))
		}
	}
	if len(f.Risks) > 0 {
		fmt.Fprintf(&b, "\n### %s\n", i18n.Tr("Риски"))
		for _, r := range f.Risks {
			fmt.Fprintf(&b, "- %s\n", e(r))
		}
	}
	if docURL != "" {
		fmt.Fprintf(&b, i18n.Tr("\n[Полные заметки и расшифровка](%s)"), docURL)
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

// RenderPlain — то же самое без разметки, для `steno show` в терминале.
func RenderPlain(m *core.Meeting, f *core.Followup) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n%s", core.OrDash(f.Title), m.StartedAt.Format("2 Jan 2006, 15:04"))
	if len(m.Participants) > 0 {
		fmt.Fprintf(&b, " · %s", strings.Join(m.Participants, ", "))
	}
	b.WriteString("\n\n")

	for _, s := range f.TLDR {
		fmt.Fprintf(&b, "  %s\n", s)
	}
	if len(f.ActionItems) > 0 {
		b.WriteString(i18n.Tr("\nЗадачи\n"))
		for _, a := range f.ActionItems {
			fmt.Fprintf(&b, "  [%s] %s — %s (%s)\n",
				core.Clock(a.At), a.Owner, a.What, DueOr(a.Due, i18n.Tr("срок не назван")))
		}
	}
	if len(f.Decisions) > 0 {
		b.WriteString(i18n.Tr("\nРешения\n"))
		for _, d := range f.Decisions {
			fmt.Fprintf(&b, "  [%s] %s — %s\n", core.Clock(d.At), d.What, d.Why)
		}
	}
	if len(f.OpenQuestions) > 0 {
		b.WriteString(i18n.Tr("\nОткрытые вопросы\n"))
		for _, q := range f.OpenQuestions {
			fmt.Fprintf(&b, i18n.Tr("  [%s] %s (ждём: %s)\n"),
				core.Clock(q.At), q.Question, DueOr(q.WaitingOn, i18n.Tr("не определено")))
		}
	}
	if len(f.Risks) > 0 {
		b.WriteString(i18n.Tr("\nРиски\n"))
		for _, x := range f.Risks {
			fmt.Fprintf(&b, "  %s\n", x)
		}
	}
	return b.String()
}
