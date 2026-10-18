package agent

import (
	"encoding/json"
	"testing"
)

// A rule's kind is read whatever its case, as its tier and the policy's mode
// are, rather than the whole rule being skipped over a capital letter.
func TestARulesKindIsReadWhateverItsCase(t *testing.T) {
	c := Merge(&File{Routing: json.RawMessage(
		`{"mode": "auto", "rules": [{"name": "helpers", "tier": "small", "when": {"kind": " Spawn "}}]}`)})
	if c.Notice != "" || len(c.Routing.Rules) != 1 || c.Routing.Rules[0].When.Kind != "spawn" {
		t.Errorf("rules %+v, notice %q; want the rule kept, for kind spawn", c.Routing.Rules, c.Notice)
	}
}
