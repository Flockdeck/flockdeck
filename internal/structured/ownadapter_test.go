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
