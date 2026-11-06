package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/server"
	"github.com/jmwri/flockdeck/internal/store"
)

// Each command below rewrites prefs.json. None may add a pane choice the
// person did not make, drop one they did, or turn the older structuredPanes
// true into anything else. The state directory is the test's own
// (isolateState sets APPDATA, XDG_CONFIG_HOME and HOME to a temp dir).

type panesStart struct {
	name string
	// seed is what the starting file holds for the pane choice.
	seed store.Prefs
	// choice is what PanesChoice must say before and after.
	choice string
	// keys is whether the file must hold the key: "panes" and "structuredPanes".
	panesKey, olderKey bool
}

var panesStarts = []panesStart{
	{"no choice", store.Prefs{}, "", false, false},
	{"terminal chosen", store.Prefs{Panes: store.PanesTerminal}, store.PanesTerminal, true, false},
	{"structured chosen", store.Prefs{Panes: store.PanesStructured}, store.PanesStructured, true, false},
	{"older true", store.Prefs{StructuredPanes: true}, store.PanesStructured, false, true},
}

func keysOf(t *testing.T, data []byte) map[string]bool {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("prefs.json is not JSON: %v: %s", err, data)
	}
	out := map[string]bool{}
	for k := range m {
		out[k] = true
	}
	return out
}

// rawPrefsKeys is the top-level keys of prefs.json as saved.
func rawPrefsKeys(t *testing.T) map[string]bool {
	t.Helper()
	dir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "prefs.json"))
	if err != nil {
		t.Fatalf("prefs.json was not written: %v", err)
	}
	return keysOf(t, data)
}

// checkPanesKept runs write against each starting file and checks the choice.
// seed adds whatever the command needs to find before it will write at all.
func checkPanesKept(t *testing.T, seed func(*store.Prefs), write func(t *testing.T)) {
	t.Helper()
	for _, st := range panesStarts {
		t.Run(st.name, func(t *testing.T) {
			isolateState(t)
			p := st.seed
			seed(&p)
			if err := store.SavePrefs(p); err != nil {
				t.Fatal(err)
			}
			write(t)
			got := store.LoadPrefs()
			if got.PanesChoice() != st.choice {
				t.Errorf("choice = %q, want %q", got.PanesChoice(), st.choice)
			}
			keys := rawPrefsKeys(t)
			if keys["panes"] != st.panesKey || keys["structuredPanes"] != st.olderKey {
				t.Errorf("file holds panes %v, structuredPanes %v; want %v, %v", keys["panes"], keys["structuredPanes"], st.panesKey, st.olderKey)
			}
		})
	}
}

func TestKnowledgeCommandKeepsThePanesChoice(t *testing.T) {
	checkPanesKept(t, func(*store.Prefs) {}, func(t *testing.T) {
		if _, err := runK(t, testKnowledgeEnv(false, "public", nil), "on"); err != nil {
			t.Fatal(err)
		}
		if store.LoadPrefs().KnowledgeSetting("/work/proj") != "on" {
			t.Fatal("the command wrote nothing, so the check means nothing")
		}
	})
}

func TestWakeCommandKeepsThePanesChoice(t *testing.T) {
	checkPanesKept(t, func(*store.Prefs) {}, func(t *testing.T) {
		if _, err := runW(t, testWakeEnv(false), "off", "-agent", "aider"); err != nil {
			t.Fatal(err)
		}
		if w := store.LoadPrefs().Wake; len(w.Agents) != 1 {
			t.Fatalf("the command wrote nothing: %+v", w)
		}
	})
}

func TestRemoteApprovalsOffKeepsThePanesChoice(t *testing.T) {
	seed := func(p *store.Prefs) {
		now := time.Now().UTC()
		*p, _ = p.WithAccess("d1", store.DeviceAccess{Role: store.RoleFull, Approve: true, ApproveV: server.ApproveAckVersion, ApproveKey: "k", ApproveAt: now})
	}
	checkPanesKept(t, seed, func(t *testing.T) {
		if _, err := runApprovals(t, nil, "off"); err != nil {
			t.Fatal(err)
		}
		if store.LoadPrefs().AccessFor("d1").Approve {
			t.Fatal("the command wrote nothing, so the check means nothing")
		}
	})
}

func TestRemoteArtifactsOffKeepsThePanesChoice(t *testing.T) {
	seed := func(p *store.Prefs) { p.RemoteArtifacts.SetKind("recordings", true) }
	checkPanesKept(t, seed, func(t *testing.T) {
		if _, err := runArtifacts(t, nil, "off"); err != nil {
			t.Fatal(err)
		}
		if store.LoadPrefs().RemoteArtifacts.KindOn("recordings") {
			t.Fatal("the command wrote nothing, so the check means nothing")
		}
	})
}
