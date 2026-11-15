package workspace

import (
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/session"
)

// A pane is given the FLOCKDECK_ names and no PERCH_ copy of any of them.
func TestPaneEnvCarriesNoPerchNames(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "one")
	p := ws.FocusedPane()
	if p == nil {
		t.Fatal("the pane did not start")
	}
	env := ws.paneEnv(p, "claude", "m")
	if len(env) == 0 {
		t.Fatal("the pane has no environment")
	}
	for _, kv := range env {
		if strings.HasPrefix(strings.ToUpper(kv), "PERCH_") {
			t.Errorf("%q is a PERCH_ name", kv)
		}
	}
}
