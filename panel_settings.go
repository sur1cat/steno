package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Настройки в панели. Описать проект, приложить сайт и репозиторий — работа
// того, кто в проекте разбирается, а не того, кто умеет править JSON и
// перезапускать сервис.
//
// Секреты здесь не заводятся и не хранятся: панель только показывает, задана
// ли нужная переменная окружения. Класть токен Slack в ту же базу, что и
// расшифровки всех разговоров, — плохой размен.

func (p *Panel) settings(w http.ResponseWriter, r *http.Request) {
	projects, err := p.st.Projects()
	if err != nil {
		p.fail(w, err)
		return
	}
	type ctxRow struct {
		Project string
		Built   time.Time
		Chars   int
		Sources string
	}
	var contexts []ctxRow
	for _, pr := range projects {
		c, err := p.st.ProjectContext(pr.Name)
		if err != nil {
			contexts = append(contexts, ctxRow{Project: pr.Name})
			continue
		}
		contexts = append(contexts, ctxRow{Project: pr.Name, Built: c.BuiltAt,
			Chars: len([]rune(c.Primer)), Sources: c.Sources})
	}

	// Секреты — только «задан / не задан», значений панель не видит.
	type secretRow struct {
		What, Env string
		Set       bool
	}
	secrets := []secretRow{
		{"Claude", p.cfg.Claude.APIKeyEnv, envSet(p.cfg.Claude.APIKeyEnv)},
		{"Панель", p.cfg.Panel.PasswordEnv, envSet(p.cfg.Panel.PasswordEnv)},
	}
	if p.cfg.Slack.Enabled || p.cfg.HTTP.Enabled {
		secrets = append(secrets,
			secretRow{"Slack", p.cfg.Slack.TokenEnv, envSet(p.cfg.Slack.TokenEnv)},
			secretRow{"Подпись Slack", p.cfg.Slack.SigningSecretEnv, envSet(p.cfg.Slack.SigningSecretEnv)})
	}
	if p.cfg.Telegram.Enabled || p.cfg.Telegram.Listen {
		secrets = append(secrets, secretRow{"Telegram", p.cfg.Telegram.TokenEnv, envSet(p.cfg.Telegram.TokenEnv)})
	}
	if p.cfg.HTTP.Enabled {
		secrets = append(secrets, secretRow{"HTTP", p.cfg.HTTP.TokenEnv, envSet(p.cfg.HTTP.TokenEnv)})
	}

	type channel struct {
		Name, Where string
		On          bool
		In, Out     bool
	}
	channels := []channel{
		{"Календарь", strings.Join(p.cfg.Calendar.Calendars, ", "), p.cfg.Calendar.Enabled, true, false},
		{"Почта бота", p.cfg.Gmail.Account, p.cfg.Gmail.Enabled, true, false},
		{"Telegram", p.cfg.Telegram.ChatID, p.cfg.Telegram.Enabled || p.cfg.Telegram.Listen,
			p.cfg.Telegram.Listen, p.cfg.Telegram.Enabled},
		{"Slack", p.cfg.Slack.Channel, p.cfg.Slack.Enabled, false, p.cfg.Slack.Enabled},
		{"HTTP", p.cfg.HTTP.Addr, p.cfg.HTTP.Enabled, true, false},
		{"Google Docs", p.cfg.GoogleDocs.FolderID, p.cfg.GoogleDocs.Enabled, false, true},
	}

	p.render(w, "settings", map[string]any{
		"Projects": projects, "Contexts": contexts,
		"Secrets": secrets, "Channels": channels, "Nav": "settings",
	})
}

func envSet(name string) bool {
	_, err := secret(name, "")
	return err == nil
}

func (p *Panel) projectForm(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	data := map[string]any{"Nav": "settings", "New": name == "new"}
	if name != "new" {
		pr, err := p.st.Project(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		c, _ := p.st.ProjectContext(name)
		data["P"] = pr
		data["Aliases"] = strings.Join(pr.Aliases, ", ")
		data["Sources"] = sourcesToText(pr.Sources)
		data["Primer"] = c.Primer
		data["BuiltAt"] = c.BuiltAt
	}
	p.render(w, "project_form", data)
}

func (p *Panel) projectSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "плохая форма", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, "у проекта должно быть название", http.StatusBadRequest)
		return
	}
	sources, err := parseSources(r.FormValue("sources"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	pr := Project{
		Name:    name,
		About:   strings.TrimSpace(r.FormValue("about")),
		Aliases: splitList(r.FormValue("aliases")),
		Sources: sources,
	}
	// Переименование: старое имя приходит скрытым полем.
	if old := strings.TrimSpace(r.FormValue("old_name")); old != "" && old != name {
		if err := p.st.DeleteProject(old); err != nil {
			p.fail(w, err)
			return
		}
	}
	if err := p.st.SaveProject(pr); err != nil {
		p.fail(w, err)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (p *Panel) projectDelete(w http.ResponseWriter, r *http.Request) {
	if err := p.st.DeleteProject(r.PathValue("name")); err != nil {
		p.fail(w, err)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// projectContext пересобирает справку. Это поход в Claude на несколько секунд,
// поэтому запускается в фоне, а страница возвращается сразу.
func (p *Panel) projectContext(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	pr, err := p.st.Project(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		material, fp, err := gatherSources(ctx, p.cfg.DataDir, pr)
		if err != nil {
			p.log.Printf("справка «%s»: %v", name, err)
			return
		}
		primer, spend, err := buildPrimer(ctx, p.cfg, pr, material)
		if err != nil {
			p.log.Printf("справка «%s»: %v", name, err)
			return
		}
		if err := p.st.SaveProjectContext(ProjectContext{
			Project: name, Primer: primer, Fingerprint: fp, Sources: sourcesSummary(pr),
		}); err != nil {
			p.log.Printf("справка «%s»: %v", name, err)
			return
		}
		p.log.Printf("справка «%s» пересобрана, %s", name, spend)
	}()
	http.Redirect(w, r, "/settings/projects/"+name+"?building=1", http.StatusSeeOther)
}

// --- разбор формы -----------------------------------------------------------

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// Источники в форме — по одному на строку: «вид значение». Разбирать так проще,
// чем городить динамические строки формы, и человеку понятнее.
func parseSources(s string) ([]Source, error) {
	var out []Source
	for i, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		kind, value, ok := strings.Cut(line, " ")
		if !ok {
			return nil, fmt.Errorf("строка %d: нужен вид и значение, например «url https://…»", i+1)
		}
		kind = strings.TrimSpace(strings.TrimSuffix(kind, ":"))
		value = strings.TrimSpace(value)
		switch kind {
		case "text", "path", "repo", "url":
		default:
			return nil, fmt.Errorf("строка %d: непонятный вид %q — бывают text, path, repo, url", i+1, kind)
		}
		if value == "" {
			return nil, fmt.Errorf("строка %d: пустое значение", i+1)
		}
		out = append(out, Source{Kind: kind, Value: value})
	}
	return out, nil
}

func sourcesToText(sources []Source) string {
	var b strings.Builder
	for _, s := range sources {
		fmt.Fprintf(&b, "%s %s\n", s.Kind, s.Value)
	}
	return b.String()
}
