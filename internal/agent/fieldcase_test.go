package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// The decoder takes a key whatever its case, so a key written in another case
// is used -- and the notice must not say it was not.
func TestAKeyInAnotherCaseIsUsedAndNotCalledUnread(t *testing.T) {
	c := Merge(&File{Agents: []json.RawMessage{json.RawMessage(
		`{"id": "mine", "Exe": "mycli", "defaultmodel": "m1", "Models": [{"id": "m1"}]}`)}})
	spec, ok := c.Find("mine")
	if !ok || spec.DefaultModel != "m1" || len(spec.Models) != 1 {
		t.Fatalf("spec %+v, want the default model and models it was given", spec)
	}
	if strings.Contains(c.Notice, "defaultmodel") || strings.Contains(c.Notice, "Exe") || strings.Contains(c.Notice, "Models") {
		t.Errorf("notice %q calls keys unread that were used", c.Notice)
	}
}
