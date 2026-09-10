package core

import "fmt"

// Followup — то, ради чего всё затевалось. Структура выбрана под то, как это
// читают: сначала три строки, дальше решения и задачи с владельцем и сроком.
// Пункт без владельца и срока не читают вообще, поэтому оба поля обязательны в
// схеме, а модели запрещено их выдумывать.
type Followup struct {
	Title         string         `json:"title"`
	TLDR          []string       `json:"tldr"`
	Decisions     []Decision     `json:"decisions"`
	ActionItems   []ActionItem   `json:"action_items"`
	OpenQuestions []OpenQuestion `json:"open_questions"`
	Risks         []string       `json:"risks"`
	Timeline      []TimelineItem `json:"timeline"`
	// Что случилось с тем, что уже висело по проектам. Без этого каждый созвон
	// заводил бы новые копии тех же задач, и живое состояние проекта тонуло бы
	// в дублях.
	Updates []ItemUpdate `json:"updates"`
}

// ItemUpdate — судьба пункта, который висел открытым до этого созвона.
type ItemUpdate struct {
	ID     string `json:"id"`
	Status string `json:"status"` // done | dropped | still_open
	Note   string `json:"note"`
}

type Decision struct {
	What    string  `json:"what"`
	Why     string  `json:"why"`
	At      float64 `json:"at"`
	Project string  `json:"project"`
}

type ActionItem struct {
	Owner   string  `json:"owner"`
	What    string  `json:"what"`
	Due     string  `json:"due"`   // YYYY-MM-DD или "" если срок не назвали
	Quote   string  `json:"quote"` // дословно из транскрипта — чтобы можно было проверить
	At      float64 `json:"at"`
	Project string  `json:"project"`
}

type OpenQuestion struct {
	Question  string  `json:"question"`
	WaitingOn string  `json:"waiting_on"`
	At        float64 `json:"at"`
	Project   string  `json:"project"`
}

type TimelineItem struct {
	At      float64 `json:"at"`
	Title   string  `json:"title"`
	Summary string  `json:"summary"`
}

func (f *Followup) Empty() bool {
	return len(f.TLDR) == 0 && len(f.Decisions) == 0 && len(f.ActionItems) == 0
}

func Clock(sec float64) string {
	s := int(sec)
	return fmt.Sprintf("%02d:%02d:%02d", s/3600, (s%3600)/60, s%60)
}

func OrDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
