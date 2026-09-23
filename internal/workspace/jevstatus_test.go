package workspace

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/store"
)

// The workspace hands every pane one assistant whose switch is the user's
// setting, read live: off until it is turned on, and off again the moment it
// is turned off, without a restart.
func TestTheJevAssistantFollowsTheSettingLive(t *testing.T) {
	isolateConfig(t)
	ws, err := New(Options{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("new workspace: %v", err)
	}
	t.Cleanup(ws.Close)

	if ws.statusAssist == nil || ws.statusAssist.Enabled == nil {
		t.Fatal("the workspace has no assistant to give its panes")
	}
	if ws.statusAssist.Enabled() {
		t.Fatal("sending terminal output to TypeSafe is on with nothing saved")
	}
	if err := store.SavePrefs(store.Prefs{JevStatus: true}); err != nil {
		t.Fatal(err)
	}
	if !ws.statusAssist.Enabled() {
		t.Error("turning the setting on was not seen")
	}
	if err := store.SavePrefs(store.Prefs{}); err != nil {
		t.Fatal(err)
	}
	if ws.statusAssist.Enabled() {
		t.Error("turning the setting off was not seen")
	}
}
