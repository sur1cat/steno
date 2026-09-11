package spec

import (
	"strings"
	"testing"
)

// good — ТЗ, по которому работать можно. Каждый тест ниже ломает в нём ровно
// одну вещь и проверяет, что Gate это ловит.
func good() *Spec {
	return &Spec{
		Title:   "Разобрать выгрузку sapar на синхронные запросы",
		Project: "taxi-kolesa",
		ItemID:  "T-a99f",
		Places:  []Place{{Path: "sapar/tasks.py", Why: "тут очередь", Found: true}},
		Steps:   []string{"Прочитать sapar/client", "Разделить пачку"},
		Unknowns: []Unknown{{
			Question: "Что считается «закончено» для sapar?",
			Why:      "без этого непонятно, где остановиться",
			Ask:      "Ануар",
		}},
	}
}

func TestGatePassesGoodSpec(t *testing.T) {
	if r := good().Gate(); len(r) != 0 {
		t.Fatalf("годное ТЗ не прошло: %v", r)
	}
}

// Главное правило всей затеи. ТЗ без единого вопроса — это не разобранная
// задача, а сочинённая: фраза с созвона не содержит всего, что нужно.
func TestGateBlocksSpecWithoutUnknowns(t *testing.T) {
	sp := good()
	sp.Unknowns = nil
	if !blockedBecause(sp, "открытого вопроса") {
		t.Fatalf("ТЗ без вопросов пропустили: %v", sp.Gate())
	}
}

func TestGateBlocksSpecWithoutPlaces(t *testing.T) {
	sp := good()
	sp.Places = nil
	if !blockedBecause(sp, "места в коде") {
		t.Fatalf("ТЗ без единого файла пропустили: %v", sp.Gate())
	}
}

// Путь, которого в репозитории нет, — признак того, что ТЗ придумано целиком.
// Один такой среди настоящих терпим (он будет виден в тексте), а вот когда не
// нашлось ни одного — опираться не на что.
func TestGateBlocksWhenEveryPathIsInvented(t *testing.T) {
	sp := good()
	sp.Places = []Place{{Path: "sapar/nowhere.py", Found: false}}
	if !blockedBecause(sp, "не нашёлся") {
		t.Fatalf("выдуманные пути пропустили: %v", sp.Gate())
	}
	sp.Places = append(sp.Places, Place{Path: "sapar/tasks.py", Found: true})
	if r := sp.Gate(); len(r) != 0 {
		t.Fatalf("один настоящий путь должен спасать ТЗ, вышло %v", r)
	}
}

func TestGateBlocksSpecWithoutSteps(t *testing.T) {
	sp := good()
	sp.Steps = nil
	if !blockedBecause(sp, "шага работы") {
		t.Fatalf("ТЗ без шагов пропустили: %v", sp.Gate())
	}
}

// Модель сама сказала, что без ответов начинать нельзя. Её слово здесь имеет
// силу: спорить с ним и запускать — худшее из возможного.
func TestGateHonoursModelsOwnBlock(t *testing.T) {
	sp := good()
	sp.Blocked, sp.Why = true, "непонятно, что чинить"
	if !blockedBecause(sp, "непонятно, что чинить") {
		t.Fatalf("blocked=true пропустили: %v", sp.Gate())
	}
	// Без объяснения всё равно блокируем — молча пропускать нельзя.
	sp.Why = ""
	if len(sp.Gate()) == 0 {
		t.Fatal("blocked=true без объяснения пропустили")
	}
}

// Раздел «чего не хватает» есть в документе всегда, даже когда модель его не
// заполнила: пустой раздел — это тоже сообщение, и оно должно быть видно.
func TestRenderAlwaysHasMissingSection(t *testing.T) {
	sp := good()
	sp.Unknowns = nil
	out := sp.Render()
	if !strings.Contains(out, "Чего не хватает") {
		t.Fatalf("раздел пропал:\n%s", out)
	}
	if !strings.Contains(out, "Ни одного вопроса не названо") {
		t.Fatalf("пустой раздел не назван вслух:\n%s", out)
	}
}

// Негодность ТЗ должна быть видна в первых строках, а не после того, как
// человек его прочитал и поверил.
func TestRenderShowsRefusalAtTheTop(t *testing.T) {
	sp := good()
	sp.Unknowns = nil
	out := sp.Render()
	warn := strings.Index(out, "нельзя запускать агента")
	steps := strings.Index(out, "Что сделать")
	if warn < 0 || steps < 0 || warn > steps {
		t.Fatalf("предупреждение стоит не раньше плана работ (%d/%d):\n%s", warn, steps, out)
	}
}

func TestRenderMarksInventedPath(t *testing.T) {
	sp := good()
	sp.Places = append(sp.Places, Place{Path: "sapar/nowhere.py", Why: "тут", Found: false})
	out := sp.Render()
	if !strings.Contains(out, "такого пути в репозитории нет") {
		t.Fatalf("выдуманный путь не помечен:\n%s", out)
	}
}

func blockedBecause(sp *Spec, want string) bool {
	for _, r := range sp.Gate() {
		if strings.Contains(r, want) {
			return true
		}
	}
	return false
}
