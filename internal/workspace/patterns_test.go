package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
)

// TestAnAgentWithoutHooksIsWatchedForItsQuestions covers every agent that does
// not report its own lifecycle — Codex, Gemini, Aider and whatever the user
// adds. Their status is read from their output, through the lines their entry
// in the catalog says mean waiting; but the pane's session was started without
// the entry, so it watched for none, and such a pane never turned amber on a
// question.
func TestAnAgentWithoutHooksIsWatchedForItsQuestions(t *testing.T) {
	isolateConfig(t)
	// An agent that asks a question and then sits there, as one waiting on an
	// answer does. It waits in the shell itself: a sleep or a ping would be a
	// second process, one closing the pane does not wait for, still sitting in
	// the test's directory when the directory is removed.
	exe, args := "sh", []string{"-c", "echo 'Do you want to proceed?'; read answer"}
	if runtime.GOOS == "windows" {
		exe, args = "cmd", []string{"/c", "echo Do you want to proceed?& pause >nul"}
	}
	var argv []map[string]string
	for _, a := range args {
		argv = append(argv, map[string]string{"value": a})
	}
	body, err := json.Marshal(map[string]any{"version": 1, "agents": []map[string]any{{
		"id": "asker", "name": "Asker", "exe": exe, "args": argv,
		"patterns": map[string][]string{"waiting": {"want to proceed?"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := store.Dir()
	if err != nil {
		t.Fatalf("state directory: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, agent.ConfigName), body, 0o600); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTabWith(Choice{Kind: session.KindClaude, Agent: "asker"}, root, "")
	p := ws.FocusedPane()
	if p == nil || p.Sess == nil {
		t.Fatalf("the pane did not start: %v", p.Err)
	}
	// Closing a session does not wait for its process, and Windows will not
	// remove a directory a dying process is still in. Cleanups run last first,
	// so this one is done before the directory goes.
	t.Cleanup(func() {
		_ = p.Sess.Close()
		waitExited(t, p.Sess)
	})
	deadline := time.Now().Add(15 * time.Second)
	for {
		if st, _ := p.Status(); st == session.StatusWaiting {
			return
		}
		if time.Now().After(deadline) {
			st, _ := p.Status()
			t.Fatalf("the pane asked a question its agent's patterns name and is %v, want waiting; it printed:\n%s",
				st, p.Sess.RecentText(2048))
		}
		time.Sleep(50 * time.Millisecond)
	}
}
