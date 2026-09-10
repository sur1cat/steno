package main

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log"
	"strings"
	"time"

	"google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
)

// Документ проекта: одна ссылка, которая всегда показывает текущее состояние.
// Заводить новый документ на каждый созвон — значит через месяц иметь тридцать
// документов и ни одного актуального; поэтому содержимое переписывается на
// месте, а идентификатор запоминается.

func renderProjectHTML(name string, items []ProjectItem) string {
	var b strings.Builder
	e := html.EscapeString

	var tasks, questions, decisions, closed []ProjectItem
	for _, it := range items {
		if it.Status != "open" {
			closed = append(closed, it)
			continue
		}
		switch it.Kind {
		case KindTask:
			tasks = append(tasks, it)
		case KindQuestion:
			questions = append(questions, it)
		case KindDecision:
			decisions = append(decisions, it)
		}
	}

	fmt.Fprintf(&b, "<h1>%s</h1>\n", e(name))
	fmt.Fprintf(&b, tr("<p><i>Обновлено %s. Документ ведётся автоматически по итогам созвонов — ")+
		tr("правки в нём перезапишутся.</i></p>\n"), e(time.Now().Format("2 January 2006, 15:04")))

	section := func(title string, list []ProjectItem, row func(ProjectItem) string) {
		if len(list) == 0 {
			return
		}
		fmt.Fprintf(&b, "<h2>%s</h2>\n<ul>\n", e(title))
		for _, it := range list {
			fmt.Fprintf(&b, "<li>%s</li>\n", row(it))
		}
		b.WriteString("</ul>\n")
	}

	section(tr("Задачи"), tasks, func(it ProjectItem) string {
		s := ""
		if it.Owner != "" {
			s += "<b>" + e(it.Owner) + "</b> — "
		}
		s += e(it.Text)
		s += fmt.Sprintf(tr(" <i>(срок: %s, с %s, %s)</i>"),
			e(dueOrText(it.Due)), e(it.OpenedAt.Format("02.01.2006")), e(it.ID))
		if it.Quote != "" {
			s += "<br/><span>«" + e(it.Quote) + "»</span>"
		}
		return s
	})
	section(tr("Открытые вопросы"), questions, func(it ProjectItem) string {
		s := e(it.Text)
		if it.Owner != "" {
			s += tr(" <i>(ждём: ") + e(it.Owner) + ")</i>"
		}
		return s + fmt.Sprintf(tr(" <i>(с %s, %s)</i>"), e(it.OpenedAt.Format("02.01.2006")), e(it.ID))
	})
	section(tr("Решения"), decisions, func(it ProjectItem) string {
		s := "<b>" + e(it.Text) + "</b>"
		if it.Quote != "" {
			s += " — " + e(it.Quote)
		}
		return s + fmt.Sprintf(" <i>(%s)</i>", e(it.OpenedAt.Format("02.01.2006")))
	})
	section(tr("Закрыто"), closed, func(it ProjectItem) string {
		what := tr("снято")
		if it.Status == "done" {
			what = tr("сделано")
		}
		s := e(it.Text) + " <i>(" + what + ", " + e(it.UpdatedAt.Format("02.01.2006")) + ")</i>"
		if it.Note != "" {
			s += "<br/><span>" + e(it.Note) + "</span>"
		}
		return s
	})

	if len(items) == 0 {
		b.WriteString(tr("<p>Пока пусто.</p>\n"))
	}
	return b.String()
}

func dueOrText(due string) string {
	if strings.TrimSpace(due) == "" {
		return tr("не назван")
	}
	return due
}

// publishProjectDocs переписывает документы всех проектов, которых коснулся
// созвон. Ошибка по одному проекту не отменяет остальные.
func publishProjectDocs(ctx context.Context, cfg *Config, st *Store, f *Followup, meetingID string, lg *log.Logger) {
	if cfg.noPublish {
		return
	}
	cfg = activeChannels(st, cfg)
	if !cfg.GoogleDocs.Enabled || !cfg.GoogleDocs.ProjectDocs {
		return
	}
	for _, name := range touchedProjects(f, st, meetingID) {
		if name == unassignedProject {
			continue // отдельный документ для «не определён» никому не нужен
		}
		url, err := publishProjectDoc(ctx, cfg, st, name)
		if err != nil {
			lg.Printf(tr("документ проекта «%s»: %v"), name, err)
			continue
		}
		lg.Printf(tr("документ проекта «%s»: %s"), name, url)
	}
}

func publishProjectDoc(ctx context.Context, cfg *Config, st *Store, name string) (string, error) {
	items, err := st.ProjectItems(name)
	if err != nil {
		return "", err
	}
	scopes := cfg.GoogleDocs.Scopes
	if len(scopes) == 0 {
		scopes = []string{drive.DriveScope}
	}
	opt, err := googleClient(ctx, cfg, cfg.GoogleDocs.CredentialsFile, cfg.GoogleDocs.Subject, scopes...)
	if err != nil {
		return "", err
	}
	srv, err := drive.NewService(ctx, opt)
	if err != nil {
		return "", err
	}

	body := renderProjectHTML(name, items)
	docID, url, _ := st.ProjectDoc(name)

	if docID != "" {
		// Обновляем на месте: ссылка на документ проекта разошлась по людям и
		// меняться не должна.
		res, err := srv.Files.Update(docID, nil).
			Media(strings.NewReader(body), googleapi.ContentType("text/html")).
			SupportsAllDrives(true).Fields("id, webViewLink").Context(ctx).Do()
		if err == nil {
			_ = st.SaveProjectDoc(name, res.Id, res.WebViewLink)
			return res.WebViewLink, nil
		}
		// Документ могли удалить руками — тогда заводим заново.
		if !isNotFound(err) {
			return "", err
		}
	}

	file := &drive.File{
		Name:     tr("Проект: ") + name,
		MimeType: "application/vnd.google-apps.document",
	}
	if cfg.GoogleDocs.FolderID != "" {
		file.Parents = []string{cfg.GoogleDocs.FolderID}
	}
	res, err := srv.Files.Create(file).
		Media(strings.NewReader(body), googleapi.ContentType("text/html")).
		SupportsAllDrives(true).Fields("id, webViewLink").Context(ctx).Do()
	if err != nil {
		return "", err
	}
	_ = st.SaveProjectDoc(name, res.Id, res.WebViewLink)
	_ = url
	return res.WebViewLink, nil
}

func isNotFound(err error) bool {
	var ae *googleapi.Error
	if errors.As(err, &ae) {
		return ae.Code == 404
	}
	return false
}
