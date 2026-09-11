package spec

import (
	"testing"

	"github.com/sur1cat/steno/internal/core"
)

func TestSaveAndReadBack(t *testing.T) {
	d := testDeps(t)
	sp := good()
	sp.Status, sp.Repo, sp.USD, sp.Model = StatusDraft, "/repo", 0.11, "claude-opus-5"
	sp.Places = append(sp.Places, Place{Path: "sapar/nowhere.py", Why: "нет", Found: false})
	if err := d.Sp.Save(sp); err != nil {
		t.Fatal(err)
	}
	got, err := d.Sp.Get(sp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != sp.Title || got.Repo != "/repo" || got.USD != 0.11 {
		t.Fatalf("не то прочиталось: %+v", got)
	}
	if len(got.Unknowns) != 1 || got.Unknowns[0].Ask != "Ануар" {
		t.Fatalf("вопросы потерялись: %+v", got.Unknowns)
	}
	// Found обязан пережить запись: это результат похода на диск, и повторять
	// его при каждом чтении значило бы проверять уже проверенное.
	if !got.Places[0].Found || got.Places[1].Found {
		t.Fatalf("отметки о найденных путях разъехались: %+v", got.Places)
	}
	if len(got.Gate()) != 0 {
		t.Fatalf("прочитанное ТЗ вдруг стало негодным: %v", got.Gate())
	}
}

// По одной задаче ТЗ бывает несколько: собрали, прочитали, задали вопросы,
// собрали второе. Прошлое переписывать нельзя — по нему уже могли завести ветку.
func TestForItemKeepsHistory(t *testing.T) {
	d := testDeps(t)
	for i := 0; i < 3; i++ {
		sp := good()
		sp.Status = StatusDraft
		if err := d.Sp.Save(sp); err != nil {
			t.Fatal(err)
		}
	}
	all, err := d.Sp.ForItem("T-a99f")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("сохранили три ТЗ, прочитали %d", len(all))
	}
	latest, err := d.Sp.Latest("")
	if err != nil {
		t.Fatal(err)
	}
	if len(latest) != 1 {
		t.Fatalf("последних ТЗ по одной задаче должно быть одно, вышло %d", len(latest))
	}
}

func TestFindItemOnlyOpenOnes(t *testing.T) {
	d := testDeps(t)
	it := core.ProjectItem{ID: "T-1", Project: "p", Kind: core.KindTask, Text: "жив"}
	if err := d.St.AddItem(it); err != nil {
		t.Fatal(err)
	}
	if _, err := FindItem(d.St, "T-1"); err != nil {
		t.Fatalf("открытая задача не нашлась: %v", err)
	}
	if err := d.St.CloseItem("T-1", "done", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := FindItem(d.St, "T-1"); err == nil {
		t.Fatal("закрытая задача нашлась — по ней завели бы ТЗ и ветку")
	}
}
