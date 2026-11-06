package workspace

import (
	"slices"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/remote"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
)

func offered(c *agent.Catalog, id string) bool {
	for _, s := range c.Visible() {
		if s.ID == id {
			return true
		}
	}
	return false
}

// Structured agents are offered unless the person chose terminal panes, or
// remote devices can reach this machine and nothing was chosen. The catalog the
// workspace builds lists them accordingly.
func TestTheCatalogListsStructuredAgentsByTheChoiceAndTheDefault(t *testing.T) {
	isolateConfig(t)
	// Nothing chosen: the default is on.
	if c := loadCatalog(); !c.Structured || !offered(c, "opencode-acp") {
		t.Fatalf("opencode-acp is not offered with no choice made (Structured %v)", c.Structured)
	}
	// The older opt-in still reads as structured.
	if err := store.SavePrefs(store.Prefs{StructuredPanes: true}); err != nil {
		t.Fatalf("SavePrefs: %v", err)
	}
	if !offered(loadCatalog(), "opencode-acp") {
		t.Error("opencode-acp is not offered with structuredPanes: true")
	}
	// An explicit terminal choice survives the round trip and turns them off.
	if err := store.SavePrefs(store.Prefs{Panes: store.PanesTerminal}); err != nil {
		t.Fatal(err)
	}
	if got := store.LoadPrefs().PanesChoice(); got != store.PanesTerminal {
		t.Fatalf("the terminal choice came back as %q", got)
	}
	c := loadCatalog()
	if c.Structured || offered(c, "opencode-acp") {
		t.Errorf("opencode-acp is offered after terminal was chosen (Structured %v)", c.Structured)
	}
	if !slices.Contains(ids(c.Visible()), "opencode") {
		t.Error("the terminal opencode entry should still be offered")
	}
	// An explicit structured choice is the same as the default.
	if err := store.SavePrefs(store.Prefs{Panes: store.PanesStructured}); err != nil {
		t.Fatal(err)
	}
	if !offered(loadCatalog(), "opencode-acp") {
		t.Error("opencode-acp is not offered with structured chosen")
	}
}

// With no choice made, a machine that remote access is on for keeps terminal
// panes as the default; a choice made either way still decides.
func TestTheDefaultIsTerminalWhereRemoteAccessIsOn(t *testing.T) {
	isolateConfig(t)
	cfg := remote.Config{Relay: "https://relay.example", HostID: "h1", AccountID: "a1", Token: "fdh_x"}
	if err := cfg.Save(); err != nil {
		t.Fatalf("enrolment: %v", err)
	}
	if offered(loadCatalog(), "opencode-acp") {
		t.Error("opencode-acp is offered by default on a machine remote access is on for")
	}
	if err := store.SavePrefs(store.Prefs{Panes: store.PanesStructured}); err != nil {
		t.Fatal(err)
	}
	if !offered(loadCatalog(), "opencode-acp") {
		t.Error("opting in did not offer opencode-acp on a machine remote access is on for")
	}
}

// Writer: the one-time knowledge notice goes through store.UpdatePrefs. It must
// leave the pane choice as it found it.
func TestTheKnowledgeNoticeKeepsThePanesChoice(t *testing.T) {
	for _, tc := range []struct {
		name   string
		prefs  store.Prefs
		choice string
	}{
		{"no choice", store.Prefs{}, ""},
		{"terminal", store.Prefs{Panes: store.PanesTerminal}, store.PanesTerminal},
		{"older true", store.Prefs{StructuredPanes: true}, store.PanesStructured},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateConfig(t)
			if err := store.SavePrefs(tc.prefs); err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			ws := newTestWorkspace(t, root)
			ws.NewTabWith(Choice{Kind: session.KindShell}, root, "")
			ws.askAboutKnowledge(ws.FocusedPane().ID, root)
			got := store.LoadPrefs()
			if !got.Dismissed("knowledge-ask:" + store.MetaKey(root)) {
				t.Fatal("the notice was not recorded, so nothing was written")
			}
			if got.PanesChoice() != tc.choice {
				t.Errorf("choice = %q, want %q", got.PanesChoice(), tc.choice)
			}
		})
	}
}

func ids(specs []agent.Spec) []string {
	var out []string
	for _, s := range specs {
		out = append(out, s.ID)
	}
	return out
}
