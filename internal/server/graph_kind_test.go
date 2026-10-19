package server

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/jmwri/flockdeck/internal/session"
)

func TestEveryNodeIsATerminal(t *testing.T) {
	e := newGraphEnv(t)
	g := e.snapshotGraph(t)
	if len(g.Nodes) == 0 {
		t.Fatal("no nodes")
	}
	for _, n := range g.Nodes {
		if n.Kind != graphKindTerminal {
			t.Errorf("node %s has kind %q, want terminal", n.Pane, n.Kind)
		}
	}
}

func TestPermissionOptionsAreOnlyThoseDocumentedForTheAgent(t *testing.T) {
	want := []permissionOption{
		{ID: "yes", Name: "Yes", Kind: "allow_once"},
		{ID: "no", Name: "No, and tell Claude what to do differently", Kind: "reject_once"},
	}
	for _, tool := range []string{"Bash", "Edit", "MultiEdit", "Write"} {
		if got := permissionOptions("claude", tool); !reflect.DeepEqual(got, want) {
			t.Errorf("claude %s = %+v", tool, got)
		}
	}
	for _, agent := range []string{"", "codex", "gemini", "Claude"} {
		if got := permissionOptions(agent, "Bash"); got == nil || len(got) != 0 {
			t.Errorf("agent %q = %#v, want an empty, non-nil list", agent, got)
		}
	}
}

// A tool whose prompt is not the Yes / No one has no options, even for Claude.
func TestPermissionOptionsAreNotOfferedForOtherTools(t *testing.T) {
	for _, tool := range []string{"ExitPlanMode", "WebFetch", "SomeNewTool", ""} {
		if got := permissionOptions("claude", tool); got == nil || len(got) != 0 {
			t.Errorf("tool %q = %#v, want an empty, non-nil list", tool, got)
		}
	}
}

// Terminal panes never list the always kinds.
func TestTerminalOptionsUseOnlyTheOnceKinds(t *testing.T) {
	for _, o := range permissionOptions("claude", "Bash") {
		if o.Kind != optionAllowOnce && o.Kind != optionRejectOnce {
			t.Errorf("option %+v has kind %q", o, o.Kind)
		}
	}
}

// A pane waiting on a permission prompt carries the options in the state push,
// and one that is not a Claude pane carries an empty list.
func TestPermissionViewCarriesOptions(t *testing.T) {
	e := newGraphEnv(t)
	ask(e.srv, func() bool {
		e.ws.Pane(e.dev).Agent = "claude"
		e.ws.Pane(e.dev).Sess.SetStatusFull(session.StatusWaiting, "Bash", `{"command":"go test ./..."}`)
		e.ws.Pane(e.eyes).Agent = "codex"
		e.ws.Pane(e.eyes).Sess.SetStatusFull(session.StatusWaiting, "Bash", `{"command":"ls"}`)
		return true
	})
	snap, _ := ask(e.srv, func() stateMsg { return e.srv.snapshot() })
	dev, eyes := snap.Panes[e.dev].Permission, snap.Panes[e.eyes].Permission
	if dev == nil || len(dev.Options) != 2 || dev.Options[0].ID != "yes" || dev.Options[1].ID != "no" {
		t.Errorf("claude pane's permission = %+v", dev)
	}
	if eyes == nil || eyes.Options == nil || len(eyes.Options) != 0 {
		t.Errorf("codex pane's permission = %+v", eyes)
	}
}

// The permission view, with and without options, validates against the schema.
func TestPermissionViewMatchesTheSchema(t *testing.T) {
	schema := loadGraphSchema(t)
	for _, agent := range []string{"claude", "codex"} {
		v := permissionViewFor("Bash", waitingToolInput{Command: "ls", Description: "list"})
		v.Options = permissionOptions(agent, "Bash")
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if errs := validateDef(schema, "permission", b); len(errs) != 0 {
			t.Errorf("%s: %v\n%s", agent, errs, b)
		}
	}
}
