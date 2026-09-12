package main

import (
	"errors"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/session"
)

// The first pane of a fresh project runs the agent new panes start as when
// that agent can start, and a shell otherwise. It asked about Claude Code
// whichever agent that was: with Codex chosen and no Claude Code, a shell.
func TestFirstPaneRunsTheAgentItWillStartAs(t *testing.T) {
	codex := func(id string) (agent.Spec, error) {
		if id != "" {
			t.Errorf("asked about %q, want the agent new panes start as", id)
		}
		return agent.Spec{ID: "codex", Exe: "codex"}, nil
	}
	missing := func(string) (agent.Spec, error) {
		return agent.Spec{ID: "codex", Exe: "codex"}, errors.New("the `codex` CLI was not found on PATH")
	}
	cases := []struct {
		name  string
		shell bool
		spec  func(string) (agent.Spec, error)
		want  session.Kind
	}{
		{"its agent can start", false, codex, session.KindAgent},
		{"its agent cannot start", false, missing, session.KindShell},
		{"-shell", true, codex, session.KindShell},
	}
	for _, c := range cases {
		if got := firstPaneKind(c.shell, c.spec); got != c.want {
			t.Errorf("%s: the first pane is kind %v, want %v", c.name, got, c.want)
		}
	}
}
