package main

import (
	"fmt"
	"strings"
	"unicode"
)

// Полнотекстовый поиск по всему, что накопилось: расшифровки и follow-up
// лежат в одной FTS5-таблице, поэтому один запрос находит и «кто это сказал»,
// и «в каком созвоне это решили».

const searchSchema = `
CREATE VIRTUAL TABLE IF NOT EXISTS search_fts USING fts5(
  text,
  meeting_id UNINDEXED,
  kind       UNINDEXED,
  at         UNINDEXED,
  speaker    UNINDEXED,
  tokenize = 'unicode61 remove_diacritics 2'
);`

type SearchHit struct {
	MeetingID string
	Title     string
	StartedAt int64
	Kind      string // "расшифровка" | "follow-up"
	At        float64
	Speaker   string
	// Найденное обрамлено управляющими символами U+0002/U+0003, а не готовой
	// разметкой: в индексе лежит сырой текст расшифровки, и подставить в него
	// <mark> означало бы отдать в браузер чужой HTML.
	Snippet string
}

const (
	markStart = "\x02"
	markEnd   = "\x03"
)

// ftsQuery превращает то, что человек набрал в поле поиска, в выражение FTS5.
// Напрямую отдавать ввод нельзя: одна кавычка или дефис — и запрос падает с
// синтаксической ошибкой вместо результатов.
func ftsQuery(q string) string {
	var words []string
	for _, w := range strings.FieldsFunc(q, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len([]rune(w)) < 2 {
			continue
		}
		// Префиксный поиск: «релиз» находит «релиза» и «релизом». Полноценной
		// морфологии это не заменяет, но в русском снимает большую часть боли.
		words = append(words, `"`+w+`"*`)
	}
	return strings.Join(words, " AND ")
}

func (s *Store) Search(q string, limit int) ([]SearchHit, error) {
	expr := ftsQuery(q)
	if expr == "" {
		return nil, nil
	}
	rows, err := s.db.Query(`
		SELECT f.meeting_id, f.kind, f.at, f.speaker,
		       snippet(search_fts, 0, char(2), char(3), '…', 14),
		       COALESCE(m.title,''), COALESCE(m.started_at,0)
		FROM search_fts f
		LEFT JOIN meetings m ON m.id = f.meeting_id
		WHERE search_fts MATCH ?
		ORDER BY rank
		LIMIT ?`, expr, limit)
	if err != nil {
		return nil, fmt.Errorf("поиск %q: %w", q, err)
	}
	defer rows.Close()
	var out []SearchHit
	for rows.Next() {
		var h SearchHit
		if err := rows.Scan(&h.MeetingID, &h.Kind, &h.At, &h.Speaker,
			&h.Snippet, &h.Title, &h.StartedAt); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *Store) reindexSegments(meetingID string, segs []Segment) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Удаление старого индекса — в той же транзакции, что и вставка нового.
	// Порознь сбой между ними убирал созвон из поиска навсегда, притом что
	// расшифровка оставалась на месте и всё выглядело исправным.
	if _, err := tx.Exec(`DELETE FROM search_fts WHERE meeting_id=? AND kind='расшифровка'`,
		meetingID); err != nil {
		return err
	}
	st, err := tx.Prepare(`INSERT INTO search_fts (text,meeting_id,kind,at,speaker)
		VALUES (?,?,'расшифровка',?,?)`)
	if err != nil {
		return err
	}
	defer st.Close()
	// Границы кусков идут по смене говорящего, а не по счёту реплик. Иначе
	// найденное приписывается не тому: кусок начинается с реплики одного
	// человека, а совпадение попадает в реплику соседа — и в результатах
	// поиска чужие слова выходят под чужим именем.
	//
	// Внутри длинного монолога кусок всё же режется, иначе часовая лекция
	// стала бы одним результатом с таймкодом на её начало.
	const maxRun = 8
	for i := 0; i < len(segs); {
		j := i + 1
		for j < len(segs) && segs[j].Speaker == segs[i].Speaker && j-i < maxRun {
			j++
		}
		var b strings.Builder
		for _, sg := range segs[i:j] {
			b.WriteString(sg.Text)
			b.WriteString(" ")
		}
		if _, err := st.Exec(b.String(), meetingID, segs[i].Start, segs[i].Speaker); err != nil {
			return err
		}
		i = j
	}
	return tx.Commit()
}

func (s *Store) reindexFollowup(meetingID string, f *Followup) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM search_fts WHERE meeting_id=? AND kind='follow-up'`,
		meetingID); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString(f.Title + "\n")
	for _, t := range f.TLDR {
		b.WriteString(t + "\n")
	}
	for _, d := range f.Decisions {
		b.WriteString(d.What + " " + d.Why + "\n")
	}
	for _, a := range f.ActionItems {
		b.WriteString(a.Owner + " " + a.What + "\n")
	}
	for _, q := range f.OpenQuestions {
		b.WriteString(q.Question + "\n")
	}
	for _, r := range f.Risks {
		b.WriteString(r + "\n")
	}
	if _, err := tx.Exec(`INSERT INTO search_fts (text,meeting_id,kind,at,speaker)
		VALUES (?,?,'follow-up',0,'')`, b.String(), meetingID); err != nil {
		return err
	}
	return tx.Commit()
}
