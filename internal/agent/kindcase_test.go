package agent

import (
	"encoding/json"
	"testing"
)

// A rule's kind is read whatever its case, as its tier and the policy's mode
// are, rather than the whole rule being skipped over a capital letter.
func TestARulesKindIsReadWhateverItsCase(t *testing.T) {
	c := Merge(&File{Routing: json.RawMessage(
		`{"mode": "suggest", "rules": [{"name": "fan-outs", "tier": "small", "when": {"kind": " Fanout "}}]}`)})
	if c.Notice != "" || len(c.Routing.Rules) != 1 || c.Routing.Rules[0].When.Kind != "fanout" {
		t.Errorf("rules %+v, notice %q; want the rule kept, for kind fanout", c.Routing.Rules, c.Notice)
	}
}
