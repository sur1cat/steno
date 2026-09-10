package core

import "time"

// Справка о проекте, собранная по его исходникам, — и её хранение.
//
// Сам сбор (git, README, состав репозитория, запрос к Claude) живёт в
// internal/brain: это работа, а не запись. Здесь — то, что от неё остаётся в
// базе, и это нужно и панели, и терминальному интерфейсу, и источникам.

type ProjectContext struct {
	Project     string
	Primer      string
	Sources     string
	BuiltAt     time.Time
	Fingerprint string
}

func (s *Store) ProjectContext(project string) (ProjectContext, error) {
	var c ProjectContext
	var built int64
	err := s.DB.QueryRow(`SELECT project,primer,sources,built_at,fingerprint
		FROM project_context WHERE project=?`, project).
		Scan(&c.Project, &c.Primer, &c.Sources, &built, &c.Fingerprint)
	c.BuiltAt = time.Unix(built, 0)
	return c, err
}

func (s *Store) SaveProjectContext(c ProjectContext) error {
	_, err := s.DB.Exec(`INSERT INTO project_context (project,primer,sources,built_at,fingerprint)
		VALUES (?,?,?,?,?)
		ON CONFLICT(project) DO UPDATE SET primer=excluded.primer, sources=excluded.sources,
		built_at=excluded.built_at, fingerprint=excluded.fingerprint`,
		c.Project, c.Primer, c.Sources, time.Now().Unix(), c.Fingerprint)
	return err
}
