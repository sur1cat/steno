package sources

import (
	"strings"
	"testing"

	"github.com/sur1cat/steno/internal/core"
)

// Схема сверки коммитов — вторая из двух, что уходят к модели, и требования к
// ней те же: строгий режим совместимых с OpenAI серверов принимает её как
// есть. См. TestFollowupSchemaFitsStrictMode.
func TestCommitMatchSchemaFitsStrictMode(t *testing.T) {
	if problems := core.StrictSchemaProblems(commitMatchSchema()); len(problems) > 0 {
		t.Errorf("схема сверки коммитов не годится для strict:\n  %s", strings.Join(problems, "\n  "))
	}
}

func TestCommitMatchSchemaCheckIsNotBlind(t *testing.T) {
	s := commitMatchSchema()
	s["additionalProperties"] = true
	if len(core.StrictSchemaProblems(s)) == 0 {
		t.Error("схему с разрешёнными лишними полями приняли как годную")
	}
}
