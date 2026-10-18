package workspace

import (
	"strings"
	"testing"
)

// TestTheBriefingClaimsOnlyWhatIsTrueOfEveryRun covers a sentence of the
// briefing that described the usual case as though it were every case: that
// the interface is exposed to nothing beyond this machine, which remote access
// makes false. An agent repeats what it is told to the user.
func TestTheBriefingClaimsOnlyWhatIsTrueOfEveryRun(t *testing.T) {
	text := PaneContext{PaneName: "one", CanSpawn: true}.Render()
	if strings.Contains(text, "exposes nothing to the network") {
		t.Error("the briefing says the interface is exposed to nothing, though remote access serves it through the relay")
	}
	if !strings.Contains(text, "remote access") {
		t.Error("the briefing does not say that remote access reaches the interface")
	}
}
