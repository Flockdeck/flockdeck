package workspace

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
)

// roleAgent makes a program that exists everywhere the tests run the project's
// default agent, so a spawned pane starts, and gives the catalog a role of the
// user's own beside the built-in ones.
func roleAgent(t *testing.T) {
	t.Helper()
	dir, err := store.Dir()
	if err != nil {
		t.Fatalf("state directory: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"version": 1, "defaults": {"agent": "gocli"},
		"agents": [{"id": "gocli", "name": "Go", "exe": "go", "args": [{"value": "{{prompt}}"}]}],
		"roles": [{"id": "security", "name": "Security", "briefing": "Look for holes."}]}`
	if err := os.WriteFile(filepath.Join(dir, agent.ConfigName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A pane with no role is briefed exactly as it was before roles: the section is
// left out, and so is the list of roles.
func TestBriefingWithoutARoleHasNoRoleSection(t *testing.T) {
	c := PaneContext{PaneName: "lead", Cwd: "/work", Roles: []string{"worker", "reviewer"}}
	text := c.Render()
	if strings.Contains(text, "Your role") || strings.Contains(text, "roles in this project") {
		t.Errorf("a pane with no role was told about roles:\n%s", text)
	}
}

func TestBriefingWithARoleCarriesItsBriefingAndTheCatalog(t *testing.T) {
	c := PaneContext{
		PaneName: "api", Cwd: "/work",
		Role: "worker", RoleKnown: true, RoleName: "Worker", RoleWorktree: agent.WorktreeOwn,
		RoleBriefing: "You carry out one task.", RoleSpawns: []string{"reviewer"},
		Roles: []string{"worker", "reviewer", "security"},
	}
	text := c.Render()
	for _, want := range []string{
		"## Your role", `the role "worker" (Worker)`, "You carry out one task.",
		"meant to be a worktree and branch of your own", "You may start agents in these roles: reviewer.",
		"The roles in this project's catalog: worker, reviewer, security.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("briefing does not say %q:\n%s", want, text)
		}
	}
	// The role comes before the other agents, which is where the pane is told
	// about itself.
	if strings.Index(text, "## Your role") > strings.Index(text, "## The other agents") {
		t.Error("the role section comes after the other agents")
	}

	// A role the catalog has since lost is named, with no briefing to give.
	gone := PaneContext{PaneName: "api", Role: "retired", Roles: []string{"worker"}}.Render()
	if !strings.Contains(gone, `"retired"`) || !strings.Contains(gone, "no longer defined") {
		t.Errorf("a role with no definition was not explained:\n%s", gone)
	}

	// The same briefing goes in front of an opening prompt, for agents with no
	// hook to answer.
	if p := c.OpeningPrompt(); !strings.Contains(p, "## Your role") {
		t.Error("the opening prompt leaves the role out")
	}
}

// A pane spawned in a role is briefed from the catalog, carries the role in its
// environment, and shell panes take none.
func TestSpawnedPaneKnowsItsRole(t *testing.T) {
	isolateConfig(t)
	roleAgent(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "lead")
	parent := ws.CurrentTab().Focus

	id, err := ws.Spawn(parent, SpawnOptions{Task: "audit the login", Role: "security"})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	p := ws.Pane(id)
	if p.Role != "security" {
		t.Fatalf("role = %q, want security", p.Role)
	}
	c, ok := ws.PaneContext(id)
	if !ok || !c.RoleKnown || c.RoleBriefing != "Look for holes." {
		t.Fatalf("context = %+v, %v", c, ok)
	}
	if !strings.Contains(strings.Join(c.Roles, ","), "manager") || !strings.Contains(strings.Join(c.Roles, ","), "security") {
		t.Errorf("roles = %v, want the built-ins and the user's", c.Roles)
	}
	if !strings.Contains(c.Render(), "Look for holes.") {
		t.Error("the briefing does not carry the role's text")
	}

	var found bool
	for _, kv := range ws.paneEnv(p, "gocli", "") {
		found = found || kv == "FLOCKDECK_ROLE=security"
	}
	if hasHook := ws.hookSrv != nil; hasHook && !found {
		t.Error("the pane's environment has no FLOCKDECK_ROLE")
	}

	// A pane started with no role has no variable, and the lead pane, a shell,
	// is told nothing about roles.
	plain, err := ws.Spawn(parent, SpawnOptions{Task: "tidy up"})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	for _, kv := range ws.paneEnv(ws.Pane(plain), "gocli", "") {
		if strings.HasPrefix(kv, "FLOCKDECK_ROLE=") {
			t.Errorf("a pane with no role has %s", kv)
		}
	}
	if lead, _ := ws.PaneContext(parent); lead.Role != "" || strings.Contains(lead.Render(), "Your role") {
		t.Error("a pane with no role was given one")
	}
}

// The role is saved with the layout and comes back with the pane.
func TestSaveRestoreKeepsTheRole(t *testing.T) {
	isolateConfig(t)
	roleAgent(t)
	root := t.TempDir()

	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "lead")
	id, err := ws.Spawn(ws.CurrentTab().Focus, SpawnOptions{Task: "audit the login", Role: "security"})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if err := ws.SaveAll(); err != nil {
		t.Fatalf("save: %v", err)
	}
	ws.Close()

	again := newTestWorkspace(t, root)
	if ok, err := again.Restore(); err != nil || !ok {
		t.Fatalf("restore: ok=%v err=%v", ok, err)
	}
	got := again.Pane(id)
	if got == nil {
		t.Fatal("the pane was not restored")
	}
	if got.Role != "security" {
		t.Errorf("restored role = %q, want security", got.Role)
	}
	// The variable comes from paneEnv alone, so a restored pane gets it too.
	if !slices.Contains(again.paneEnv(got, "gocli", ""), "FLOCKDECK_ROLE=security") {
		t.Error("the restored pane's environment has no FLOCKDECK_ROLE")
	}
}
