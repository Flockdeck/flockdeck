package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
)

// TestAPaneLosesWhatEveryAgentAsksToStrip covers the environment a pane is
// started with. The markers of the session Flockdeck was launched from are taken
// out so that each pane is a clean top-level session, and which markers those
// are is each agent's to say in agents.json. Only the built-in list was ever
// used, so an agent the user added with a "stripEnv" of its own had it ignored.
func TestAPaneLosesWhatEveryAgentAsksToStrip(t *testing.T) {
	isolateConfig(t)
	const marker = "FLOCKDECK_TEST_PARENT_MARKER"
	t.Setenv(marker, "leaked")

	// An agent that prints the variable and exits.
	exe, args := "sh", []string{"-c", "echo [$" + marker + "]"}
	if runtime.GOOS == "windows" {
		exe, args = "cmd", []string{"/c", "echo [%" + marker + "%]"}
	}
	var argv []map[string]string
	for _, a := range args {
		argv = append(argv, map[string]string{"value": a})
	}
	body, err := json.Marshal(map[string]any{"version": 1, "agents": []map[string]any{
		{"id": "printer", "name": "Printer", "exe": exe, "args": argv, "stripEnv": []string{marker}},
	}})
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
	ws.NewTabWith(Choice{Kind: session.KindClaude, Agent: "printer"}, root, "")
	p := ws.FocusedPane()
	if p == nil || p.Sess == nil {
		t.Fatalf("the pane did not start: %v", p.Err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for !strings.Contains(p.Sess.RecentText(4096), "[") {
		if time.Now().After(deadline) {
			t.Fatalf("the pane printed nothing: %q", p.Sess.RecentText(4096))
		}
		time.Sleep(20 * time.Millisecond)
	}
	waitExited(t, p.Sess)
	if out := p.Sess.RecentText(4096); strings.Contains(out, "leaked") {
		t.Errorf("the pane inherited %s, which its agent asks to have stripped:\n%s", marker, out)
	}
}
