package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
)

// TestARestartedChildThatNeverBeganIsAskedItsTaskAgain covers a spawned agent
// that exited before it recorded anything: stopped at a question, interrupted,
// or crashed on the way up. It has no conversation to resume, and its opening
// task had been spent the moment its process started, so a restart brought it
// back idle with nothing to say what it had been spawned for.
func TestARestartedChildThatNeverBeganIsAskedItsTaskAgain(t *testing.T) {
	isolateConfig(t)
	dir, err := store.Dir()
	if err != nil {
		t.Fatalf("state directory: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// `go` with the task as its argument says it has no such command, naming
	// the task, and exits: an agent that dies at once, and that says what it
	// was asked.
	body := `{"version": 1, "agents": [{"id": "gocli", "name": "Go", "exe": "go",
		"args": [{"value": "{{prompt}}"}], "caps": {"resume": true}}]}`
	if err := os.WriteFile(filepath.Join(dir, agent.ConfigName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "lead")
	const task = "reticulate-the-splines"
	id, err := ws.Spawn(ws.CurrentTab().Focus, SpawnOptions{Task: task, Agent: "gocli"})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	p := ws.Pane(id)
	waitExited(t, p.Sess)

	ws.SelectTab(ws.TabIDOf(id))
	ws.RestartPane()
	if p.Sess == nil {
		t.Fatalf("the restarted pane did not start: %v", p.Err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for !strings.Contains(p.Sess.RecentText(4096), task) {
		if time.Now().After(deadline) || p.Sess.Exited() && !strings.Contains(p.Sess.RecentText(4096), task) {
			t.Fatalf("the restarted child was not given its task again; it printed:\n%s", p.Sess.RecentText(4096))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitExited waits for a session's process to be reaped.
func waitExited(t *testing.T, s *session.Session) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !s.Exited() {
		if time.Now().After(deadline) {
			t.Fatal("the process did not exit")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
