package publish

import (
	"strings"
	"testing"

	"github.com/sur1cat/steno/internal/core"
)

func TestRenderProjectHTMLEscapes(t *testing.T) {
	items := []core.ProjectItem{
		{ID: "T-1", Kind: core.KindTask, Status: "open", Owner: "<b>Участник Б</b>",
			Text: "миграция <script>alert(1)</script>", Quote: "«цитата»"},
	}
	out := renderProjectHTML("Платежи & Co", items)
	if strings.Contains(out, "<script>alert(1)</script>") {
		t.Error("в документ проекта утёк живой HTML")
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Error("текст не экранирован")
	}
	if !strings.Contains(out, "Платежи &amp; Co") {
		t.Error("название проекта не экранировано")
	}
}
