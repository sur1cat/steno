package spec

import "github.com/sur1cat/steno/internal/i18n"

// Тесты проверяют русский вывод — на нём написан исходник, и он же служит
// ключом каталога. Английский проверяется отдельно, в internal/i18n.
func init() { i18n.UILang = i18n.LangRU }
