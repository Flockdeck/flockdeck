package server

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/workspace"
)

// twoProjects starts a server on a repository, the project on screen, and
// opens a second repository as a project that is not. Each has a file git has
// not been told about. It returns the pane working in the second project.
//
// The second project's first pane runs an API agent, which is started in
// process and says nothing to anybody until it is asked something, so opening
// it starts no program and reaches nothing outside the machine.
func twoProjects(t *testing.T) (*Server, *workspace.Workspace, string, string) {
	t.Helper()
	srv, ws, shown := newRepoServer(t)
	hidden := t.TempDir()
	for _, args := range [][]string{
		{"init", "--initial-branch=main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"config", "commit.gpgsign", "false"},
		{"commit", "--allow-empty", "-m", "initial"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = hidden
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v: %s", args, err, out)
		}
	}
	for _, dir := range []string{shown, hidden} {
		if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := ws.Catalog().Find("anthropic"); !ok {
		t.Skip("no built-in API agent to open the second project on")
	}
	if err := setAgentDefault(hidden, agentChoice{Agent: "anthropic"}); err != nil {
		t.Fatal(err)
	}
	ws.ReloadAgents()
	pane, ok := ask(srv, func() string {
		if err := ws.OpenProject(hidden); err != nil {
			t.Error(err)
			return ""
		}
		var id string
		for _, tab := range ws.VisibleTabs() {
			id = tab.Tree.Panes()[0]
		}
		ws.SelectProject(shown)
		return id
	})
	if !ok || pane == "" {
		t.Fatal("the second project was not opened")
	}
	return srv, ws, hidden, pane
}

// untrackedIn reads what the workspace holds for a pane's checkout.
func untrackedIn(t *testing.T, srv *Server, ws *workspace.Workspace, pane string) int {
	t.Helper()
	n, _ := ask(srv, func() int {
		if p := ws.Pane(pane); p != nil {
			return p.Git.Untracked
		}
		return -1
	})
	return n
}

// TestTheGitPollReadsOnlyTheProjectOnScreen covers the pane headers' git
// refresh with several projects open. Every checkout of every open project was
// read every fifteen seconds, a whole-tree git status each, for headers only
// the project on screen shows. A project brought on screen is read at once.
func TestTheGitPollReadsOnlyTheProjectOnScreen(t *testing.T) {
	srv, ws, hidden, pane := twoProjects(t)
	r := readControl(dialControl(t, srv))
	if _, ok := r.stateWithin(10*time.Second, func(s stateMsg) bool {
		for _, p := range s.Panes {
			if p.Untracked > 0 {
				return true
			}
		}
		return false
	}); !ok {
		t.Fatal("the project on screen was never read")
	}
	// Any refresh that read the project on screen has had ample time to read
	// the other one too, had it been asked.
	time.Sleep(2 * time.Second)
	if n := untrackedIn(t, srv, ws, pane); n != 0 {
		t.Errorf("the project not on screen was read (%d untracked), want it left alone", n)
	}

	ask(srv, func() bool { ws.SelectProject(hidden); return true })
	for deadline := time.Now().Add(5 * time.Second); untrackedIn(t, srv, ws, pane) < 1; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the project brought on screen was not read within five seconds")
		}
	}
}

// TestTheAgentsOverviewReadsEveryProject covers the overview of every pane in
// every project, which shows each one's uncommitted files. The git poll reads
// only the project on screen, so the overview has the rest read, and sends its
// list again once they have been.
func TestTheAgentsOverviewReadsEveryProject(t *testing.T) {
	srv, _, _, pane := twoProjects(t)
	conn := dialControl(t, srv)
	r := readControl(conn)
	sendCmd(t, conn, command{Cmd: "agents"})
	deadline := time.After(10 * time.Second)
	for {
		select {
		case data, ok := <-r.msgs:
			if !ok {
				t.Fatal("the control socket closed")
			}
			var msg agentsMsg
			if json.Unmarshal(data, &msg) != nil || msg.Type != "agents" {
				continue
			}
			for _, it := range msg.Items {
				if it.PaneID == pane && it.Dirty > 0 {
					return
				}
			}
		case <-deadline:
			t.Fatal("the overview never showed the uncommitted file in the project not on screen")
		}
	}
}
