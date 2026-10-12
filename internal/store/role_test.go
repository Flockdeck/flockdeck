package store

import (
	"encoding/json"
	"strings"
	"testing"
)

// A pane's role is written as "role" and read back, and a pane with none writes
// nothing, so a layout from before roles is unchanged.
func TestPaneRolePersists(t *testing.T) {
	data, err := json.Marshal(Pane{ID: "p1", Role: "reviewer"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"role":"reviewer"`) {
		t.Errorf("pane = %s, want the role written", data)
	}
	var back Pane
	if err := json.Unmarshal(data, &back); err != nil || back.Role != "reviewer" {
		t.Errorf("read back %+v, %v", back, err)
	}

	data, err = json.Marshal(Pane{ID: "p2"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "role") {
		t.Errorf("pane with no role = %s, want the key left out", data)
	}
}
