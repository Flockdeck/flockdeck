package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/store"
)

// The server writes prefs.json through three paths: updatePrefsThen (every
// ordinary setting), updateAccess (a device's role, pruning) and
// updateAccessReport (approvals). Each reads the file, makes one change and
// saves it whole, so each must hand the pane choice back as it found it: no
// choice stays no choice, an explicit terminal stays terminal, and the older
// structuredPanes: true is not turned into anything else.

var serverPanesStarts = []struct {
	name     string
	seed     store.Prefs
	choice   string
	panesKey bool
	olderKey bool
}{
	{"no choice", store.Prefs{}, "", false, false},
	{"terminal chosen", store.Prefs{Panes: store.PanesTerminal}, store.PanesTerminal, true, false},
	{"structured chosen", store.Prefs{Panes: store.PanesStructured}, store.PanesStructured, true, false},
	{"older true", store.Prefs{StructuredPanes: true}, store.PanesStructured, false, true},
}

func prefsFileKeys(t *testing.T) map[string]bool {
	t.Helper()
	dir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "prefs.json"))
	if err != nil {
		t.Fatalf("prefs.json was not written: %v", err)
	}
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

// checkServerPanesKept seeds the file, runs write on a test server and checks
// the choice. write must change the file, and says so by returning.
func checkServerPanesKept(t *testing.T, write func(t *testing.T, srv *Server)) {
	t.Helper()
	for _, st := range serverPanesStarts {
		t.Run(st.name, func(t *testing.T) {
			srv, _ := newTestServer(t)
			if err := store.SavePrefs(st.seed); err != nil {
				t.Fatal(err)
			}
			write(t, srv)
			got := store.LoadPrefs()
			if got.PanesChoice() != st.choice {
				t.Errorf("choice = %q, want %q", got.PanesChoice(), st.choice)
			}
			keys := prefsFileKeys(t)
			if keys["panes"] != st.panesKey || keys["structuredPanes"] != st.olderKey {
				t.Errorf("file holds panes %v, structuredPanes %v; want %v, %v", keys["panes"], keys["structuredPanes"], st.panesKey, st.olderKey)
			}
		})
	}
}

func waitDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the save did not finish")
	}
}

// Writer: updatePrefsThen, which every ordinary setting uses.
func TestAnOrdinarySettingKeepsThePanesChoice(t *testing.T) {
	checkServerPanesKept(t, func(t *testing.T, srv *Server) {
		done := make(chan struct{})
		var result prefsResult
		srv.updatePrefsThen(nil, func(p *store.Prefs) bool { p.Theme = "light"; return true }, func(r prefsResult) {
			result = r
			close(done)
		})
		waitDone(t, done)
		if result != prefsSaved {
			t.Fatalf("the setting was not saved (%v), so the check means nothing", result)
		}
	})
}

// Writer: updateAccess, which a device's role and the pruning of removed
// devices use.
func TestADeviceRoleChangeKeepsThePanesChoice(t *testing.T) {
	checkServerPanesKept(t, func(t *testing.T, srv *Server) {
		done := make(chan struct{})
		var saveErr error
		srv.updateAccess(func(p *store.Prefs) bool {
			next, ok := p.WithAccess("d1", store.DeviceAccess{Role: store.RoleViewer})
			*p = next
			return ok
		}, func(err error) {
			saveErr = err
			close(done)
		})
		waitDone(t, done)
		if saveErr != nil {
			t.Fatalf("save: %v", saveErr)
		}
		if store.LoadPrefs().AccessFor("d1").EffectiveRole() != store.RoleViewer {
			t.Fatal("the change was not written, so the check means nothing")
		}
	})
}

// Writer: updateAccessReport, which turning approvals on and off uses.
func TestAnApprovalChangeKeepsThePanesChoice(t *testing.T) {
	checkServerPanesKept(t, func(t *testing.T, srv *Server) {
		done := make(chan struct{})
		var applied bool
		var saveErr error
		srv.updateAccessReport(true, func(p *store.Prefs) bool {
			next, ok := p.WithAccess("d1", store.DeviceAccess{Role: store.RoleViewer})
			*p = next
			return ok
		}, func(a bool, err error) {
			applied, saveErr = a, err
			close(done)
		})
		waitDone(t, done)
		if !applied || saveErr != nil {
			t.Fatalf("applied %v, save error %v", applied, saveErr)
		}
	})
}
