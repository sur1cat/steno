package main

import (
	"database/sql"
	"os"
	"strings"
	"time"
)

// Удаление созвона целиком.
//
// Записанного и разобранного созвона касаются семь таблиц и один каталог на
// диске. Удалить строку в meetings и на этом остановиться — значит оставить
// расшифровку в поиске, задачи в списке задач и мегабайты звука на диске: в
// панели созвона нет, а найти его поиском по-прежнему можно. Поэтому удаление
// живёт здесь одной функцией, а не расходится по трём местам, откуда его зовут.
//
// Самое тонкое — project_items. Эта таблица не знает meeting_id: у пункта два
// поля, opened_in (созвон, где он возник) и closed_in (созвон, где его
// закрыли), и обращаться с ними надо по-разному.
//
//   - opened_in — пункт родился здесь, вместе с созвоном и уходит;
//   - closed_in — пункт родился в другом созвоне и этому не принадлежит. Но
//     основание, по которому его закрыли, человек только что стёр, — значит
//     вопрос снова открыт. Оставить его закрытым — молча потерять живое дело.

// meetingToll — цена удаления: что уйдёт вместе с созвоном и что вернётся в
// работу. Считает её одна функция на все три места — командную строку,
// терминальный интерфейс и панель, — иначе цифры в вопросе «удалить?»
// разошлись бы между ними на первой же правке.
type meetingToll struct {
	// Что уйдёт вместе с созвоном.
	Segments     int  `json:"segments"`     // реплик расшифровки
	Tasks        int  `json:"tasks"`        // задач из follow-up
	Followup     bool `json:"followup"`     // разбор Claude
	Publications int  `json:"publications"` // отметок о публикации
	Events       int  `json:"events"`       // отметок «на эту встречу уже сходили»
	Indexed      int  `json:"indexed"`      // кусков в поиске

	// Пункты проектов, родившиеся на этом созвоне: уйдут вместе с ним.
	OpenedTasks     int `json:"openedTasks"`
	OpenedDecisions int `json:"openedDecisions"`
	OpenedQuestions int `json:"openedQuestions"`

	// Пункты, закрытые на этом созвоне: вернутся в работу.
	Reopen int `json:"reopen"`

	// Каталог записи: звук, субтитры, лог ffmpeg.
	Bytes int64 `json:"bytes"`

	// Готовая фраза для вопроса «удалить?». Уезжает и в панель: так вопрос
	// звучит одинаково во всех трёх местах, а не пересобирается ещё раз на
	// другом языке в браузере.
	Text string `json:"text"`
}

// MeetingToll считает цену удаления, ничего не трогая.
func (s *Store) MeetingToll(id string) (meetingToll, error) {
	var t meetingToll
	count := func(n *int, q string, args ...any) error {
		return s.db.QueryRow(q, args...).Scan(n)
	}
	for _, c := range []struct {
		n *int
		q string
	}{
		{&t.Segments, `SELECT COUNT(*) FROM segments WHERE meeting_id=?`},
		{&t.Tasks, `SELECT COUNT(*) FROM tasks WHERE meeting_id=?`},
		{&t.Publications, `SELECT COUNT(*) FROM publications WHERE meeting_id=?`},
		{&t.Events, `SELECT COUNT(*) FROM seen_events WHERE meeting_id=?`},
		{&t.Indexed, `SELECT COUNT(*) FROM search_fts WHERE meeting_id=?`},
	} {
		if err := count(c.n, c.q, id); err != nil {
			return t, err
		}
	}

	var followups int
	if err := count(&followups, `SELECT COUNT(*) FROM followups WHERE meeting_id=?`, id); err != nil {
		return t, err
	}
	t.Followup = followups > 0

	// opened_in<>? — не придирка: пункт, и родившийся, и закрытый на этом
	// созвоне, уходит вместе с ним, и обещать, что он «откроется заново»,
	// значит соврать в самом вопросе «удалить?».
	if err := count(&t.Reopen,
		`SELECT COUNT(*) FROM project_items WHERE closed_in=? AND opened_in<>?`, id, id); err != nil {
		return t, err
	}

	rows, err := s.db.Query(
		`SELECT kind, COUNT(*) FROM project_items WHERE opened_in=? GROUP BY kind`, id)
	if err != nil {
		return t, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var n int
		if err := rows.Scan(&kind, &n); err != nil {
			return t, err
		}
		switch ItemKind(kind) {
		case KindTask:
			t.OpenedTasks += n
		case KindDecision:
			t.OpenedDecisions += n
		case KindQuestion:
			t.OpenedQuestions += n
		}
	}
	if err := rows.Err(); err != nil {
		return t, err
	}

	// Ошибку не возвращаем: каталога может не быть вовсе — запись убрал prune
	// по сроку хранения, — и это не повод отказываться удалять созвон.
	t.Bytes, _ = dirSize(s.RecordingDir(id))

	t.Text = t.words()
	return t, nil
}

// words — цена словами: «уйдут: запись (12,4 МБ), расшифровка (243 реплики),
// follow-up, 4 задачи, 2 решения; 1 пункт откроется заново».
//
// Человек соглашается на удаление, зная только название созвона, — а уходит с
// ним и живое состояние проектов, которое к названию отношения не имеет.
func (t meetingToll) words() string {
	var gone []string
	if t.Bytes > 0 {
		gone = append(gone, trf("запись (%s)", trf("%.1f МБ", float64(t.Bytes)/(1<<20))))
	}
	if t.Segments > 0 {
		gone = append(gone, trf("расшифровка (%s)",
			plural(t.Segments, tr("реплика"), tr("реплики"), tr("реплик"))))
	}
	if t.Followup {
		gone = append(gone, "follow-up")
	}
	if t.OpenedTasks > 0 {
		gone = append(gone, plural(t.OpenedTasks, tr("задача"), tr("задачи"), tr("задач")))
	}
	if t.OpenedDecisions > 0 {
		gone = append(gone, plural(t.OpenedDecisions, tr("решение"), tr("решения"), tr("решений")))
	}
	if t.OpenedQuestions > 0 {
		gone = append(gone, plural(t.OpenedQuestions, tr("вопрос"), tr("вопроса"), tr("вопросов")))
	}

	out := tr("уйдёт только строка в списке")
	if len(gone) > 0 {
		out = trf("уйдут: %s", strings.Join(gone, ", "))
	}
	if t.Reopen > 0 {
		out += trf("; %s откроется заново", t.reopenWords())
	}
	return out
}

// reopenWords — «1 пункт», «3 пункта»: столько чужих пунктов вернётся в работу.
func (t meetingToll) reopenWords() string {
	return plural(t.Reopen, tr("пункт"), tr("пункта"), tr("пунктов"))
}

// meetingTables — всё, что привязано к созвону колонкой meeting_id. Список
// здесь один на удаление и на счёт: разъехавшись, они дали бы вопрос «удалить?»
// с честными цифрами и удаление, которое половину из них не трогает.
var meetingTables = []string{
	"segments", "tasks", "followups", "publications", "seen_events", "search_fts",
}

// DeleteMeeting забывает созвон целиком: строку в списке, расшифровку, разбор,
// отметки о публикации, место в поиске, пункты проектов, которые на нём
// родились, и каталог записи на диске.
//
// Всё, что в базе, — одной транзакцией. Половинчатое удаление хуже, чем
// никакое: созвона нет в списке, а поиск по нему находит, задачи висят и
// закрывать их некому.
//
// Каталог сносится после успешной фиксации, а не до неё: сорвавшаяся
// транзакция при обратном порядке оставила бы строку без записи — то есть
// молча потерянный звук у созвона, который человек видит целым.
func (s *Store) DeleteMeeting(id string) (meetingToll, error) {
	toll, err := s.MeetingToll(id)
	if err != nil {
		return toll, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return toll, err
	}
	defer tx.Rollback()

	for _, table := range meetingTables {
		if _, err := tx.Exec(`DELETE FROM `+table+` WHERE meeting_id=?`, id); err != nil {
			return toll, err
		}
	}
	// opened_in главнее closed_in: пункт, и родившийся, и закрытый здесь,
	// уходит вместе с созвоном, а не остаётся сиротой без созвона-родителя.
	// DELETE на closed_in не смотрит, поэтому открывать заново оказывается
	// нечего — в каком бы порядке эти два запроса ни стояли.
	if _, err := tx.Exec(`DELETE FROM project_items WHERE opened_in=?`, id); err != nil {
		return toll, err
	}
	// note не стираем: это единственный оставшийся след того, почему пункт
	// когда-то закрыли, — а созвона, на который ссылался closed_in, уже нет.
	if _, err := tx.Exec(`UPDATE project_items SET status='open', closed_in='', updated_at=?
		WHERE closed_in=?`, time.Now().Unix(), id); err != nil {
		return toll, err
	}
	if _, err := tx.Exec(`DELETE FROM meetings WHERE id=?`, id); err != nil {
		return toll, err
	}
	if err := tx.Commit(); err != nil {
		return toll, err
	}

	if err := os.RemoveAll(s.RecordingDir(id)); err != nil {
		return toll, err
	}
	return toll, nil
}

// meetingExists нужен командам, которым мало «ноль удалённых строк»: человек,
// опечатавшийся в id, должен услышать об этом, а не увидеть бодрое «удалено».
func (s *Store) meetingExists(id string) (bool, error) {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM meetings WHERE id=?`, id).Scan(&one)
	switch {
	case err == sql.ErrNoRows:
		return false, nil
	case err != nil:
		return false, err
	}
	return true, nil
}
