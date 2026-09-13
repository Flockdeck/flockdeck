package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// A value of the wrong kind inside an entry, or inside "routing", is named in
// the file's own terms -- the key and what it was given -- rather than in the
// decoder's, which names Go types from inside this package.
func TestAValueOfTheWrongKindIsNamedInTheFilesTerms(t *testing.T) {
	c := Merge(&File{
		Agents: []json.RawMessage{
			json.RawMessage(`{"id": "claude", "models": "opus"}`),
			json.RawMessage(`{"id": "mine", "exe": "mycli", "hidden": "yes"}`),
		},
		Routing: json.RawMessage(`{"mode": "suggest", "rules": {"name": "one"}}`),
	})
	for _, want := range []string{`agent "claude": "models" cannot be string`, `agent "mine": "hidden" cannot be string`, `"rules" cannot be object`} {
		if !strings.Contains(c.Notice, want) {
			t.Errorf("notice %q should say %s", c.Notice, want)
		}
	}
	if strings.Contains(c.Notice, "Go struct") || strings.Contains(c.Notice, "agent.") {
		t.Errorf("notice %q names types from inside the program", c.Notice)
	}
}
