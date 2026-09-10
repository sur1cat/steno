package core

import "time"

// Докуда синхронизация репозитория дошла в прошлый раз.
//
// Сама синхронизация (git fetch, разбор коммитов, сверка с задачами) живёт в
// internal/sources. Здесь только отметка в базе: её ставит синхронизация, а
// читает и панель, показывая, насколько свежи справки по проекту.

func (s *Store) RepoHead(project, source string) (string, time.Time, error) {
	var head string
	var at int64
	err := s.DB.QueryRow(`SELECT head, synced_at FROM repo_state WHERE project=? AND source=?`,
		project, source).Scan(&head, &at)
	return head, time.Unix(at, 0), err
}

func (s *Store) SaveRepoHead(project, source, head string) error {
	_, err := s.DB.Exec(`INSERT INTO repo_state (project,source,head,synced_at) VALUES (?,?,?,?)
		ON CONFLICT(project,source) DO UPDATE SET head=excluded.head, synced_at=excluded.synced_at`,
		project, source, head, time.Now().Unix())
	return err
}
