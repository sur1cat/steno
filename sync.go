package main

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"
)

// Сверка с репозиториями проектов.
//
// Разово склонированный репозиторий устаревает за неделю, и справка начинает
// описывать прошлогодний код. Поэтому репозитории подтягиваются по расписанию.
//
// Заодно решается вторая задача, которую иначе решать нечем: задача, сделанная
// тихо и не упомянутая ни на одном созвоне, висит открытой вечно. А в коммитах
// ровно это и написано — что сделали.

type syncSource struct {
	Commits []Commit
	Head    string
}

type Commit struct {
	Hash    string
	Subject string
	Body    string
	Author  string
	When    time.Time
}

func (c Commit) Short() string {
	if len(c.Hash) > 8 {
		return c.Hash[:8]
	}
	return c.Hash
}

func (s *Store) RepoHead(project, source string) (string, time.Time, error) {
	var head string
	var at int64
	err := s.db.QueryRow(`SELECT head, synced_at FROM repo_state WHERE project=? AND source=?`,
		project, source).Scan(&head, &at)
	return head, time.Unix(at, 0), err
}

func (s *Store) SaveRepoHead(project, source, head string) error {
	_, err := s.db.Exec(`INSERT INTO repo_state (project,source,head,synced_at) VALUES (?,?,?,?)
		ON CONFLICT(project,source) DO UPDATE SET head=excluded.head, synced_at=excluded.synced_at`,
		project, source, head, time.Now().Unix())
	return err
}

// syncRepo подтягивает изменения и возвращает то, что появилось с прошлого раза.
//
// Локальный каталог (path) намеренно не трогаем: это рабочая копия человека, и
// делать в ней pull — влезать в чужую работу. Читаем как есть.
func syncRepo(ctx context.Context, dataDir, project string, src Source, st *Store) (syncSource, error) {
	var out syncSource
	dir := expandHome(src.Value)
	if src.Kind == "repo" {
		var err error
		if dir, err = shallowClone(ctx, dataDir, project, src.Value); err != nil {
			return out, err
		}
	}

	head, err := gitOut(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return out, fmt.Errorf(tr("не git-репозиторий: %w"), err)
	}
	out.Head = head

	prev, _, err := st.RepoHead(project, src.Value)
	if err != nil || prev == "" {
		// Первая сверка: историю задним числом не разбираем — там всё, что
		// было до появления проекта в steno, и задач для этого ещё нет.
		return out, st.SaveRepoHead(project, src.Value, head)
	}
	if prev == head {
		return out, nil
	}

	// Коммиты между тем, что видели, и тем, что есть сейчас. При поверхностном
	// клоне старого коммита может уже не быть — тогда берём последние.
	rangeArg := prev + "..HEAD"
	raw, err := gitOut(ctx, dir, "log", "--pretty=format:%H%x1f%s%x1f%b%x1f%an%x1f%aI", rangeArg)
	if err != nil {
		raw, err = gitOut(ctx, dir, "log", "--pretty=format:%H%x1f%s%x1f%b%x1f%an%x1f%aI", "-n", "40")
		if err != nil {
			return out, err
		}
	}
	for _, line := range strings.Split(raw, "\n") {
		parts := strings.Split(line, "\x1f")
		if len(parts) < 5 {
			continue
		}
		when, _ := time.Parse(time.RFC3339, parts[4])
		out.Commits = append(out.Commits, Commit{
			Hash: parts[0], Subject: strings.TrimSpace(parts[1]),
			Body: strings.TrimSpace(parts[2]), Author: parts[3], When: when,
		})
	}
	return out, st.SaveRepoHead(project, src.Value, head)
}

func gitOut(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// --- цикл -------------------------------------------------------------------

type syncer struct {
	cfg *Config
	st  *Store
	log *log.Logger
}

func (s *syncer) Name() string { return tr("репозитории") }

func (s *syncer) Run(ctx context.Context) error {
	every := s.cfg.Sync.Every.D()
	if every <= 0 {
		every = time.Hour
	}
	s.log.Printf(tr("репозитории: сверяюсь раз в %s"), every)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		s.once(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

func (s *syncer) once(ctx context.Context) {
	projects := activeProjects(s.st, s.cfg)
	for _, p := range projects {
		var commits []Commit
		changed := false
		for _, src := range p.Sources {
			if src.Kind != "repo" && src.Kind != "path" {
				continue
			}
			res, err := syncRepo(ctx, s.cfg.DataDir, p.Name, src, s.st)
			if err != nil {
				s.log.Printf(tr("репозитории: %s / %s: %v"), p.Name, src.Value, err)
				continue
			}
			if len(res.Commits) > 0 {
				changed = true
				commits = append(commits, res.Commits...)
			}
		}
		if changed {
			s.log.Printf(tr("репозитории: %s — новых коммитов %d"), p.Name, len(commits))
			if err := s.closeByCommits(ctx, p, commits); err != nil {
				s.log.Printf(tr("репозитории: сверка задач по %s: %v"), p.Name, err)
			}
		}
		// Справку проверяем всегда, а не только при новых коммитах: код мог не
		// меняться, а README, сайт или описание — да, и тогда словарь тихо
		// устаревает. Лишних трат это не даёт: внутри стоит сверка отпечатка,
		// и на неизменившемся материале поход в Claude не случается.
		s.maybeRebuild(ctx, p, len(commits))
	}
}

// maybeRebuild пересобирает справку не на каждый коммит: она стоит денег, а от
// одной правки опечатки не меняется.
func (s *syncer) maybeRebuild(ctx context.Context, p Project, newCommits int) {
	c, err := s.st.ProjectContext(p.Name)
	stale := err != nil ||
		newCommits >= s.cfg.Sync.RebuildAfterCommits ||
		time.Since(c.BuiltAt) > s.cfg.Sync.RebuildAfter.D()
	if !stale {
		return
	}
	material, fp, err := gatherSources(ctx, s.cfg.DataDir, p)
	if err != nil {
		s.log.Printf(tr("репозитории: материал для «%s»: %v"), p.Name, err)
		return
	}
	if err == nil && c.Fingerprint == fp {
		return
	}
	primer, spend, err := buildPrimer(ctx, s.cfg, p, material)
	if err != nil {
		s.log.Printf(tr("репозитории: справка «%s»: %v"), p.Name, err)
		return
	}
	if err := s.st.SaveProjectContext(ProjectContext{
		Project: p.Name, Primer: primer, Fingerprint: fp, Sources: sourcesSummary(p),
	}); err != nil {
		s.log.Printf(tr("репозитории: %v"), err)
		return
	}
	s.log.Printf(tr("репозитории: справка «%s» обновлена, %s"), p.Name, spend)
}

// --- суточная сводка --------------------------------------------------------

// syncDigest — то, что человек читает раз в сутки. Смысл не в самом закрытии
// задач, а в том, чтобы движение по проекту было видно: что коммитами сделано,
// что похоже на сделанное, и сколько ещё висит.
type syncDigest struct {
	Project   string
	Commits   int
	Closed    []string
	Maybe     []string
	StillOpen int
}

func (d syncDigest) empty() bool { return len(d.Closed) == 0 && len(d.Maybe) == 0 }

func (d syncDigest) text() string {
	var b strings.Builder
	fmt.Fprintf(&b, tr("%s — за сутки %s\n"), d.Project,
		plural(d.Commits, tr("коммит"), tr("коммита"), tr("коммитов")))
	if len(d.Closed) > 0 {
		b.WriteString(tr("\nЗакрыто коммитами:\n"))
		for _, l := range d.Closed {
			fmt.Fprintf(&b, "  • %s\n", l)
		}
	}
	if len(d.Maybe) > 0 {
		b.WriteString(tr("\nПохоже, сделано — но не закрывал:\n"))
		for _, l := range d.Maybe {
			fmt.Fprintf(&b, "  • %s\n", l)
		}
	}
	if d.StillOpen > 0 {
		fmt.Fprintf(&b, tr("\nЕщё открыто: %s\n"),
			plural(d.StillOpen, tr("задача"), tr("задачи"), tr("задач")))
	}
	return b.String()
}

// plural — то же согласование, что и в панели: «1 коммит», «2 коммита»,
// «5 коммитов». Машинный текст в чате команды читают каждый день.
func plural(n int, one, few, many string) string {
	w := many
	switch {
	case n%10 == 1 && n%100 != 11:
		w = one
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14):
		w = many
		if n%100 < 12 || n%100 > 14 {
			w = few
		}
	}
	return fmt.Sprintf("%d %s", n, w)
}

func (s *syncer) notify(ctx context.Context, d syncDigest) {
	if d.empty() {
		return
	}
	body := d.text()
	// Каналы перечитываются перед отправкой: сверка идёт раз в сутки, и за это
	// время адресата в панели могли поменять.
	cfg := activeChannels(s.st, s.cfg)
	if cfg.Telegram.Enabled && cfg.Telegram.ChatID != "" {
		if err := sendTelegramText(ctx, cfg, cfg.Telegram.ChatID, body); err != nil {
			s.log.Printf(tr("репозитории: telegram: %v"), err)
		}
	}
	if cfg.Slack.Enabled && cfg.Slack.Channel != "" {
		if err := sendSlackText(ctx, cfg, cfg.Slack.Channel, body); err != nil {
			s.log.Printf(tr("репозитории: slack: %v"), err)
		}
	}
}
