package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/baton"
	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// targetsFor sets the defaults of two projects, opens both, puts a go agent in the
// second (the pane that will spawn) and leaves the first on screen.
func targetsFor(t *testing.T, onScreen, ofParent string) (srv *Server, ws *workspace.Workspace, parent string) {
	t.Helper()
	srv, ws = newTestServer(t)
	path, err := agent.ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	first, _ := ask(srv, func() string { return ws.ActiveRoot() })
	second := t.TempDir()
	body, err := json.Marshal(map[string]any{
		"version": 1,
		"agents": []map[string]string{
			{"id": "gocli", "name": "Go", "exe": "go"},
			{"id": "gitcli", "name": "Git as an agent", "exe": "git"},
		},
		"projects": map[string]agent.Defaults{first: {Agent: onScreen}, second: {Agent: ofParent}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	parent, ok := ask(srv, func() string {
		ws.ReloadAgents()
		if err := ws.OpenProject(second); err != nil {
			t.Error(err)
		}
		tab := ws.NewTabWith(workspace.Choice{Kind: session.KindClaude, Agent: "gocli"}, second, "parent")
		id := tab.Focus
		ws.SelectProject(first)
		return id
	})
	if !ok || parent == "" {
		t.Fatal("the parent pane did not start")
	}
	return srv, ws, parent
}

// The agent a helper will run is the default of the project of the pane that
// spawns it, which is not the project on screen. The check that a baton may go
// there has to be about that agent.
func TestTheConsentCheckJudgesTheAgentTheHelperWillRun(t *testing.T) {
	for _, c := range []struct {
		name             string
		onScreen, parent string
		refused          bool
	}{
		{"the parent's project defaults to another company", "gocli", "gitcli", true},
		{"the project on screen would be another company and the parent's is not", "gitcli", "gocli", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv, ws, parent := targetsFor(t, c.onScreen, c.parent)
			hook := ws.HookServer()
			if hook == nil {
				t.Skip("no hook server")
			}
			_ = srv
			_, err := hooks.Spawn(hook.BaseURL(), hook.Token(), parent, hooks.SpawnRequest{Task: "x", Baton: "self"})
			refused := err != nil && strings.Contains(err.Error(), "different company")
			if refused != c.refused {
				t.Errorf("refused = %v, want %v (err: %v)", refused, c.refused, err)
			}
		})
	}
}

// Starting a helper from a stored baton is a use of that baton, and of the stored
// one, not only of the copy hardening may make.
func TestSpawningFromAStoredBatonRecordsTheUseOfTheOriginal(t *testing.T) {
	srv, ws := newTestServer(t)
	batonAgent(t, ws)
	pane := spawnGoPane(t, srv, ws)
	st, err := baton.Open()
	if err != nil {
		t.Fatal(err)
	}
	b := baton.Baton{ID: baton.NewID(time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)), Title: "Old",
		Sections: map[baton.Section]string{baton.Goal: "finish"}}
	if err := st.Save(b); err != nil {
		t.Fatal(err)
	}
	long := time.Now().Add(-90 * 24 * time.Hour)
	if err := os.Chtimes(st.Path(b.ID), long, long); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.resolveBaton(pane, b.ID); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(st.Path(b.ID))
	if err != nil || time.Since(fi.ModTime()) > time.Hour {
		t.Errorf("resolving the baton did not record its use: %v %v", fi, err)
	}
}
