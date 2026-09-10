package main

import (
	"fmt"
	"log"
	"os"
	"time"
)

// Час созвона — примерно 15 МБ аудио плюс расшифровка. У команды на сорок
// созвонов в неделю это сотни мегабайт в день, и никто их не чистит, пока не
// кончится место на диске.
//
// В список статусов входит и recording: жёсткое убийство сервиса оставляет
// созвон навсегда в этом статусе с недописанным файлом, и без него такой
// огрызок нельзя было вычистить вообще ничем.
//
// Удаляется только аудио: расшифровка, follow-up и ссылки на публикации
// остаются в базе навсегда. Именно за ними обычно и возвращаются через
// полгода, а переслушивать запись почти никогда не нужно.

type PruneResult struct {
	Recordings int
	Bytes      int64
	Events     int
}

func prune(st *Store, cfg *Config, lg *log.Logger) (PruneResult, error) {
	var res PruneResult

	if d := cfg.Retention.Recordings.D(); d > 0 {
		cutoff := time.Now().Add(-d).Unix()
		rows, err := st.db.Query(`SELECT id FROM meetings
			WHERE started_at < ? AND audio_path != ''
			  AND status IN ('published','publish_failed','summarized','transcribed','failed','recording')`, cutoff)
		if err != nil {
			return res, err
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return res, err
			}
			ids = append(ids, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return res, err
		}

		for _, id := range ids {
			dir := st.RecordingDir(id)
			size, err := dirSize(dir)
			if err != nil {
				continue // уже удалено
			}
			if err := os.RemoveAll(dir); err != nil {
				lg.Printf(tr("не удалил %s: %v"), dir, err)
				continue
			}
			// Путь обнуляем, чтобы повторный проход не считал их снова и
			// чтобы `steno process` честно сказал, что аудио больше нет.
			if _, err := st.db.Exec(`UPDATE meetings SET audio_path='' WHERE id=?`, id); err != nil {
				lg.Printf(tr("не обновил %s: %v"), id, err)
			}
			res.Recordings++
			res.Bytes += size
		}
	}

	if d := cfg.Retention.Events.D(); d > 0 {
		// Заодно сироты: ключ, чей созвон удалён, держит ссылку занятой и
		// отвечает «уже иду» на приглашение, хотя идти некому.
		if r, err := st.db.Exec(`DELETE FROM seen_events WHERE meeting_id NOT IN
			(SELECT id FROM meetings)`); err == nil {
			if n, _ := r.RowsAffected(); n > 0 {
				fmt.Printf(tr("отметок без созвона: %d\n"), n)
			}
		}
		r, err := st.db.Exec(`DELETE FROM seen_events WHERE created_at < ?`,
			time.Now().Add(-d).Unix())
		if err != nil {
			return res, err
		}
		n, _ := r.RowsAffected()
		res.Events = int(n)
	}
	return res, nil
}

func dirSize(dir string) (int64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, e := range entries {
		if info, err := e.Info(); err == nil {
			total += info.Size()
		}
	}
	return total, nil
}

func (r PruneResult) String() string {
	return fmt.Sprintf(tr("удалено записей: %d (%.1f ГБ), отметок о событиях: %d"),
		r.Recordings, float64(r.Bytes)/(1<<30), r.Events)
}
