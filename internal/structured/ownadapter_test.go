package structured

import (
	"context"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
)

// Only a runtime that starts one of Flockdeck's own adapters from the manifest
// says so. A command given for a test does not, and neither does an agent whose
// adapter someone else wrote, whatever the catalog entry is called.
func TestOwnAdapterIsOnlyFlockdecksOwn(t *testing.T) {
	spec := func(id, adapter string) agent.Spec {
		return agent.Spec{ID: id, Runner: agent.RunnerACP, ACP: agent.ACPSpec{Adapter: adapter}}
	}
	for name, tc := range map[string]struct {
		opts Options
		want bool
	}{
		"claude":                 {Options{Spec: spec("claude", "claude")}, true},
		"codex":                  {Options{Spec: spec("codex", "codex-go")}, true},
		"opencode":               {Options{Spec: spec("opencode", "opencode")}, false},
		"gemini":                 {Options{Spec: spec("gemini", "gemini")}, false},
		"an entry called claude": {Options{Spec: spec("claude", "opencode")}, false},
		"no such adapter":        {Options{Spec: spec("claude", "nothing")}, false},
		"not structured":         {Options{Spec: agent.Spec{ID: "claude", ACP: agent.ACPSpec{Adapter: "claude"}}}, false},
		"a command for a test":   {Options{Spec: spec("claude", "claude"), Command: &Command{Own: true}}, false},
	} {
		t.Run(name, func(t *testing.T) {
			r := &Runtime{ctx: context.Background(), opts: tc.opts}
			if got := r.OwnAdapter(); got != tc.want {
				t.Errorf("OwnAdapter() = %v, want %v", got, tc.want)
			}
		})
	}
}

// The bridge is told the keys an own adapter sends, so that a card notes any other
// key. A test command, and a key list set for a test, are not overridden.
func TestShownInputComesFromTheSpecOfAnOwnAdapter(t *testing.T) {
	spec := func(adapter string) agent.Spec {
		return agent.Spec{ID: "claude", Runner: agent.RunnerACP, ACP: agent.ACPSpec{Adapter: adapter}}
	}
	if got := shownInput(Options{Spec: spec("claude")}, nil); len(got) != 3 {
		t.Errorf("claude: %v", got)
	}
	if got := shownInput(Options{Spec: spec("opencode")}, nil); got != nil {
		t.Errorf("another adapter: %v", got)
	}
	if got := shownInput(Options{Spec: spec("claude"), Command: &Command{}}, nil); got != nil {
		t.Errorf("a test command: %v", got)
	}
	if got := shownInput(Options{Spec: spec("claude")}, []string{"x"}); len(got) != 1 || got[0] != "x" {
		t.Errorf("a set list: %v", got)
	}
}
