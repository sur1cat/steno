package core

import (
	"strings"
	"testing"
)

func strictOK() map[string]any {
	str := map[string]any{"type": "string"}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title": str,
			"items": map[string]any{"type": "array", "items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"who": str,
					"how": map[string]any{"type": "string", "enum": []string{"a", "b"}},
				},
				"required":             []string{"who", "how"},
				"additionalProperties": false,
			}},
		},
		"required":             []string{"title", "items"},
		"additionalProperties": false,
	}
}

func TestStrictSchemaAcceptsWhatItShould(t *testing.T) {
	if p := StrictSchemaProblems(strictOK()); len(p) > 0 {
		t.Errorf("годная схема забракована: %v", p)
	}
	if p := StrictSchemaProblems(nil); len(p) > 0 {
		t.Errorf("пустая схема — не схема, а «без схемы»: %v", p)
	}
}

// Каждая поломка должна называться отдельно: список проблем читает человек,
// который правил схему минуту назад.
func TestStrictSchemaCatchesEachBreak(t *testing.T) {
	cases := []struct {
		name  string
		spoil func(map[string]any)
		want  string
	}{
		{"свойство забыли в required", func(s map[string]any) {
			s["required"] = []string{"title"}
		}, "в required нет items"},
		{"разрешили лишние поля", func(s map[string]any) {
			s["additionalProperties"] = true
		}, "additionalProperties"},
		{"убрали additionalProperties вовсе", func(s map[string]any) {
			delete(s, "additionalProperties")
		}, "additionalProperties"},
		{"required у вложенного объекта неполон", func(s map[string]any) {
			arr := s["properties"].(map[string]any)["items"].(map[string]any)
			arr["items"].(map[string]any)["required"] = []string{"who"}
		}, "items.[]: в required нет how"},
		{"в required имя несуществующего свойства", func(s map[string]any) {
			s["required"] = []string{"title", "items", "выдумка"}
		}, "выдумка"},
		{"массив без items", func(s map[string]any) {
			s["properties"].(map[string]any)["items"] = map[string]any{"type": "array"}
		}, "массив без items"},
		{"запрещённое ключевое слово", func(s map[string]any) {
			s["oneOf"] = []any{}
		}, "oneOf"},
		{"тип, которого нет", func(s map[string]any) {
			s["properties"].(map[string]any)["title"] = map[string]any{"type": "date"}
		}, "не знает типа date"},
		{"свойство без типа", func(s map[string]any) {
			s["properties"].(map[string]any)["title"] = map[string]any{}
		}, "не указан type"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := strictOK()
			tc.spoil(s)
			got := StrictSchemaProblems(s)
			if len(got) == 0 {
				t.Fatal("поломка не замечена")
			}
			if !strings.Contains(strings.Join(got, "\n"), tc.want) {
				t.Errorf("жалоба не про то:\n  ждали %q\n  вышло %v", tc.want, got)
			}
		})
	}
}

// Пределы строгого режима: глубина и число свойств.
func TestStrictSchemaCountsDepthAndProps(t *testing.T) {
	// Шесть уровней объектов подряд — на один больше, чем можно.
	deep := map[string]any{"type": "string"}
	for i := 0; i < 6; i++ {
		deep = map[string]any{
			"type":                 "object",
			"properties":           map[string]any{"x": deep},
			"required":             []string{"x"},
			"additionalProperties": false,
		}
	}
	if p := StrictSchemaProblems(deep); len(p) == 0 {
		t.Error("слишком глубокая схема принята")
	} else if !strings.Contains(strings.Join(p, "\n"), "вложенность") {
		t.Errorf("жалоба не про глубину: %v", p)
	}

	props := map[string]any{}
	var req []string
	for i := 0; i < 101; i++ {
		name := string(rune('a'+i%26)) + strings.Repeat("x", i/26+1)
		props[name] = map[string]any{"type": "string"}
		req = append(req, name)
	}
	wide := map[string]any{"type": "object", "properties": props,
		"required": req, "additionalProperties": false}
	if p := StrictSchemaProblems(wide); len(p) == 0 {
		t.Error("схема на сто с лишним свойств принята")
	}
}
