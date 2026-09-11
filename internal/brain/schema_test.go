package brain

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sur1cat/steno/internal/core"
)

// Схема follow-up обязана годиться для строгого режима совместимых с OpenAI
// серверов как есть, без перевода на лету.
//
// Это не наблюдение, а требование, и держится оно на одном человеке с
// хорошей памятью — пока за ним не следит тест. Дописать в схему свойство и
// забыть его в required стоит секунды; сломается от этого не сборка и не
// разбор, а один провайдер у одного человека, который будет чинить это сам и
// без понятия, с чего начать. Отсюда проверка.
func TestFollowupSchemaFitsStrictMode(t *testing.T) {
	problems := core.StrictSchemaProblems(FollowupSchema())
	if len(problems) > 0 {
		t.Errorf("схема follow-up не годится для strict:\n  %s", strings.Join(problems, "\n  "))
	}
}

// Проверка не должна сторожить пустоту: убедимся, что она вообще умеет
// придираться к этой самой схеме.
func TestFollowupSchemaCheckIsNotBlind(t *testing.T) {
	s := FollowupSchema()
	req, _ := s["required"].([]string)
	if len(req) < 2 {
		t.Fatalf("у корня схемы всего %d обязательных свойств — обход сломался", len(req))
	}
	s["required"] = req[:1]
	if len(core.StrictSchemaProblems(s)) == 0 {
		t.Error("схему с неполным required приняли как годную")
	}
}

// Схема должна пережить дорогу через JSON: в запрос она уходит именно так, и
// проверять её в виде map, а слать в виде текста — значит проверять не то.
func TestFollowupSchemaSurvivesJSON(t *testing.T) {
	raw, err := json.Marshal(FollowupSchema())
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if problems := core.StrictSchemaProblems(back); len(problems) > 0 {
		t.Errorf("после сериализации схема перестала годиться:\n  %s", strings.Join(problems, "\n  "))
	}
}
