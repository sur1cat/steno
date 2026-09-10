package tui

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/sur1cat/steno/internal/core"
)

// Временный съём экрана для глазной проверки. Удаляется после разбора.
func TestUILook(t *testing.T) {
	if os.Getenv("LOOK") == "" {
		t.Skip("нужен LOOK")
	}
	// Ровно то состояние, которое видит человек: почти пустая база.
	one := func(st *core.Store) {
		started := time.Date(2026, 9, 9, 18, 52, 0, 0, time.Local)
		st.CreateMeeting(&core.Meeting{ID: "2026-09-09-1852-c3d4", Title: "Проверка",
			MeetURL: "https://meet.example/x", StartedAt: started, Status: "recording"})
		st.FinishMeeting("2026-09-09-1852-c3d4", started, started.Add(6*time.Minute),
			[]string{"Rustem Turgeldin"}, "published", "", "")
		st.SaveFollowup("2026-09-09-1852-c3d4", "claude", &core.Followup{
			Title: "Короткая проверка", TLDR: []string{"Записали пробный созвон."},
			ActionItems: []core.ActionItem{{Owner: "Rustem", What: "проверить рассылку", At: 30}},
		})
		st.AddItem(core.ProjectItem{ID: "T-0001", Kind: core.KindTask, Text: "проверить рассылку",
			Owner: "Rustem", OpenedIn: "2026-09-09-1852-c3d4"})
	}
	for _, scene := range []struct {
		name string
		seed func(*core.Store)
		keys []string
	}{
		{"один созвон", one, nil},
		{"пустая база", nil, nil},
		{"задачи", one, []string{"2"}},
		{"карточка", one, []string{"enter"}},
	} {
		m := uiTestModel(t, scene.seed)
		m.Update(tea.WindowSizeMsg{Width: 170, Height: 44})
		for _, k := range scene.keys {
			press(m, k)
		}
		fmt.Printf("\n########## %s — 170x44 ##########\n", scene.name)
		for i, l := range strings.Split(m.View(), "\n") {
			fmt.Printf("%3d |%s|\n", i, ansi.Strip(l))
		}
	}
}
