package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sur1cat/steno/internal/core"
)

// Модель возвращает путь как придётся. Всё это один и тот же файл, и объявлять
// его несуществующим из-за оформления — такое же враньё, как принять выдуманный
// за настоящий, только в другую сторону.
func TestNormalizePathUnderstandsWhatModelsWrite(t *testing.T) {
	repo := "/repo"
	cases := map[string]string{
		"sapar/tasks.py":         "sapar/tasks.py",
		"./sapar/tasks.py":       "sapar/tasks.py",
		"`sapar/tasks.py`":       "sapar/tasks.py",
		"sapar/tasks.py:120":     "sapar/tasks.py",
		"sapar/tasks.py:120-140": "sapar/tasks.py",
		"sapar/":                 "sapar",
		"/repo/sapar/tasks.py":   "sapar/tasks.py",
		"  sapar/tasks.py  ":     "sapar/tasks.py",
	}
	for in, want := range cases {
		if got := normalizePath(repo, in); got != want {
			t.Errorf("normalizePath(%q) = %q, ждали %q", in, got, want)
		}
	}
}

// Выход за пределы репозитория — не путь, а попытка. Такое ТЗ пометит его как
// ненайденное, но проверять его os.Stat мы всё равно не пойдём.
func TestNormalizePathRefusesEscapes(t *testing.T) {
	for _, in := range []string{"../secrets.env", "/etc/passwd", "..", ".", ""} {
		if got := normalizePath("/repo", in); got != "" {
			t.Errorf("normalizePath(%q) = %q, ждали пусто", in, got)
		}
	}
}

func TestVerifyPlacesTellsRealFromInvented(t *testing.T) {
	repo := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(repo, "sapar"), 0o755))
	write(t, filepath.Join(repo, "sapar", "tasks.py"), "x")

	places := []Place{
		{Path: "./sapar/tasks.py:12"},
		{Path: "sapar/"},
		{Path: "sapar/nowhere.py"},
		{Path: "../etc/passwd"},
	}
	verifyPlaces(repo, places)

	if !places[0].Found || places[0].Path != "sapar/tasks.py" {
		t.Errorf("настоящий файл не признан: %+v", places[0])
	}
	if !places[1].Found {
		t.Errorf("настоящий каталог не признан: %+v", places[1])
	}
	if places[2].Found {
		t.Errorf("выдуманный файл признан настоящим: %+v", places[2])
	}
	if places[3].Found {
		t.Errorf("путь наружу признан настоящим: %+v", places[3])
	}
}

// Отбор «кодом или руками» обязан видеть, кто в проекте человек, а что —
// сервис. Без этого «разобраться с Сапаром» читалось как «поговорить с
// подрядчиком Сапар», хотя Сапар вписан в проект как сервис.
func TestTriageHeadNamesProjectPeopleAndServices(t *testing.T) {
	p := core.Project{Name: "Такси", People: []string{"Ануар", "Гульназ"}, Vocabulary: []string{"Сапар", "МДС", "Ануар"}}
	it := core.ProjectItem{Text: "Разобраться с Сапаром", Owner: "Ануар"}
	head := triageHead(it, p, Evidence{})
	if !strings.Contains(head, "Люди проекта: Ануар, Гульназ") {
		t.Errorf("людей проекта в отборе нет:\n%s", head)
	}
	if !strings.Contains(head, "Сервисы и сокращения проекта (не люди): Сапар, МДС") {
		t.Errorf("сервисов проекта в отборе нет (или в них попал человек):\n%s", head)
	}
}
