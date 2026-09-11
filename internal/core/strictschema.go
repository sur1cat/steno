package core

import (
	"fmt"
	"sort"
	"strings"
)

// Годится ли схема для строгого режима совместимых с OpenAI серверов.
//
// Строгий режим (response_format=json_schema, strict=true) — единственный, где
// сервер обязуется вернуть ровно то, что описано, а не «постарается». Платой
// за это идёт список требований, и они жёстче обычного JSON Schema: в каждом
// объекте перечислены все свойства без исключения, везде запрещены лишние
// поля, из ключевых слов доступно немногое.
//
// Наши схемы (FollowupSchema, схема сверки коммитов) этим требованиям
// удовлетворяют — переводить их на лету не нужно. Но «удовлетворяют сегодня»
// стоит ровно до первой правки: дописать в схему minLength или забыть новое
// свойство в required ничего не стоит, а сломается от этого не сборка и не
// разбор, а один конкретный провайдер у одного конкретного человека, и он же
// будет это чинить.
//
// Поэтому проверка живёт в коде, а не в голове. Тесты сторожат ею наши схемы,
// а клиент — свой запрос: схема, не годящаяся для строгого режима, уходит на
// ступеньку ниже, а не превращается в четырёхсотку у человека посреди созвона.
func StrictSchemaProblems(schema map[string]any) []string {
	if schema == nil {
		return nil
	}
	c := &strictCheck{}
	c.node(schema, "", 1)
	if c.props > strictMaxProps {
		c.add("", fmt.Sprintf("свойств всего %d, а строгий режим берёт не больше %d",
			c.props, strictMaxProps))
	}
	sort.Strings(c.problems)
	return c.problems
}

const (
	// Пределы строгого режима. Оба с большим запасом от наших схем — в
	// FollowupSchema два десятка свойств и три уровня.
	strictMaxProps = 100
	strictMaxDepth = 5
)

// Ключевые слова, которых в строгом режиме нет. Список нарочно короткий: сюда
// попало только то, что не поддержано наверняка и во всех редакциях. Ограничения
// на длины и диапазоны (minLength, minimum, minItems) в разное время были то
// запрещены, то разрешены, и сторожить ими значило бы ронять тест на ровном
// месте у того, кто просто обновил провайдера.
var strictForbidden = []string{
	"allOf", "oneOf", "not", "if", "then", "else",
	"patternProperties", "propertyNames", "unevaluatedProperties",
	"dependentRequired", "dependentSchemas", "contains", "default",
}

type strictCheck struct {
	problems []string
	props    int
}

func (c *strictCheck) add(path, why string) {
	if path == "" {
		path = "корень"
	}
	c.problems = append(c.problems, path+": "+why)
}

func (c *strictCheck) node(node map[string]any, path string, depth int) {
	if depth > strictMaxDepth {
		c.add(path, fmt.Sprintf("вложенность глубже %d уровней", strictMaxDepth))
		return
	}
	for _, bad := range strictForbidden {
		if _, ok := node[bad]; ok {
			c.add(path, "строгий режим не знает ключа "+bad)
		}
	}

	switch typeOf(node) {
	case "object":
		props, ok := node["properties"].(map[string]any)
		if !ok {
			c.add(path, "объект без properties")
			return
		}
		if v, ok := node["additionalProperties"]; !ok || v != false {
			c.add(path, "нужен additionalProperties: false")
		}
		req := stringSet(node["required"])
		var missing []string
		for name := range props {
			if !req[name] {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			c.add(path, "в required нет "+strings.Join(missing, ", ")+
				" — строгий режим требует перечислить все свойства")
		}
		for name := range req {
			if _, ok := props[name]; !ok {
				c.add(path, "в required есть "+name+", а свойства такого нет")
			}
		}
		names := make([]string, 0, len(props))
		for name := range props {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			c.props++
			child, ok := props[name].(map[string]any)
			if !ok {
				c.add(join(path, name), "свойство описано не объектом схемы")
				continue
			}
			c.node(child, join(path, name), depth+1)
		}
	case "array":
		items, ok := node["items"].(map[string]any)
		if !ok {
			c.add(path, "массив без items")
			return
		}
		// Массив — не уровень вложенности сам по себе: уровни считаются по
		// объектам, и items объекта живёт на том же уровне, что и массив.
		c.node(items, join(path, "[]"), depth)
	case "string", "number", "integer", "boolean":
		// Простые типы годятся как есть; enum на строке тоже.
	case "":
		c.add(path, "не указан type")
	default:
		c.add(path, "строгий режим не знает типа "+typeOf(node))
	}
}

func typeOf(node map[string]any) string {
	s, _ := node["type"].(string)
	return s
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

// stringSet принимает и []string, и []any — схема бывает и написана в коде, и
// разобрана из JSON, и обходить её приходится в обоих видах.
func stringSet(v any) map[string]bool {
	out := map[string]bool{}
	switch list := v.(type) {
	case []string:
		for _, s := range list {
			out[s] = true
		}
	case []any:
		for _, item := range list {
			if s, ok := item.(string); ok {
				out[s] = true
			}
		}
	}
	return out
}
