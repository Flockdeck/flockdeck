package chat

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
)

// TestLiveModelsAreAccepted is the live-API half of the compatibility check
// described in flockdeck-planning/13-ci-agent-model-compatibility.md: for
// every model an API-run built-in agent lists, it sends the wire's own
// smallest possible request and checks the model is accepted rather than
// refused as retired or unknown -- exactly the Gemini-preview-retirement
// scenario builtin.go's own comments already warn about.
//
// It never runs on an ordinary PR: it costs real API usage, and needs a real
// key. It is skipped, per agent, wherever that agent's key is not set in the
// environment, and skipped entirely unless FLOCKDECK_LIVE_AGENT_TESTS is set.
// .github/workflows/agent-compat.yml sets both on a schedule, and again on a
// pull_request that touches builtin.go.
func TestLiveModelsAreAccepted(t *testing.T) {
	if os.Getenv("FLOCKDECK_LIVE_AGENT_TESTS") == "" {
		t.Skip("set FLOCKDECK_LIVE_AGENT_TESTS=1 to run this against the real APIs; see .github/workflows/agent-compat.yml")
	}
	for _, spec := range agent.Builtins() {
		if spec.Runner != agent.RunnerAPI || spec.API.Wire == "" || len(spec.Models) == 0 {
			continue
		}
		t.Run(spec.ID, func(t *testing.T) {
			key := firstSetKey(spec.API.KeyEnv)
			if key == "" {
				t.Skipf("no key set for %q; tried %v", spec.ID, spec.API.KeyEnv)
			}
			wire, err := NewWire(spec.API.Wire, spec.API.BaseURL, key)
			if err != nil {
				t.Fatalf("NewWire: %v", err)
			}
			for _, m := range spec.Models {
				if m.ID == "" {
					continue
				}
				t.Run(m.ID, func(t *testing.T) {
					liveModelIsAccepted(t, wire, spec.ID, m.ID)
				})
			}
		})
	}
}

// liveModelIsAccepted sends one minimal live request for a model and reports
// whether it was refused as unknown or retired -- the one failure mode this
// test exists to catch. Anything else the API says (a bad key, a busy
// endpoint, a network problem, a model the key has no access to) is not that
// failure, and is left as a skip that names it rather than a flaky failure on
// exactly the days the key or the network is at fault instead of the model.
func liveModelIsAccepted(t *testing.T, wire Wire, agentID, modelID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req := Request{
		Model:     modelID,
		Messages:  []Message{{Role: RoleUser, Text: "Reply with the single word OK and nothing else."}},
		MaxTokens: 16,
	}
	err := wire.Stream(ctx, req, func(Event) {})
	switch {
	case err == nil:
		return
	case modelUnknown(err):
		t.Errorf("%s: model %q was refused as unknown or retired: %v", agentID, modelID, err)
	case refusedKey(err):
		t.Skipf("the key set for %q was refused, so this could not be checked: %v", agentID, err)
	default:
		if e, ok := busy(err); ok {
			t.Skipf("%s answered busy rather than accepting or refusing the model: %v", agentID, e)
			return
		}
		t.Skipf("%s: %q was neither clearly accepted nor clearly refused as unknown: %v", agentID, modelID, err)
	}
}

// firstSetKey returns the first of names that has a non-empty value in the
// environment, or "".
func firstSetKey(names []string) string {
	for _, name := range names {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v
		}
	}
	return ""
}
