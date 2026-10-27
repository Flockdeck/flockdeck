package claudeacp

import (
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/acp/manifest"
)

func untestedNotices(h *harness) int {
	n := 0
	for _, u := range h.updates() {
		if u["sessionUpdate"] == "flockdeck_notice" && u["code"] == "untested_version" {
			n++
		}
	}
	return n
}

func TestTestedSeriesIsTheManifests(t *testing.T) {
	a, ok := manifest.Default().Lookup("claude")
	if !ok {
		t.Fatal("no claude entry in the manifest")
	}
	if got, want := testedSeries(), series(a.TestedWith); got == "" || got != want {
		t.Errorf("tested series %q, want %q (testedWith %s)", got, want, a.TestedWith)
	}
	if got := series("2.1.312 (Claude Code)"); got != "2.1" {
		t.Errorf("series = %q", got)
	}
}

func TestVersionNotice(t *testing.T) {
	for _, tc := range []struct {
		name, out string
		notice    int
	}{
		{"same series, newer patch", "2.1.999 (Claude Code)", 0},
		{"the pinned version", "2.1.311 (Claude Code)", 0},
		{"next minor", "2.2.0 (Claude Code)", 1},
		{"next major", "3.0.1 (Claude Code)", 1},
		{"older minor", "2.0.9 (Claude Code)", 1},
		{"no version in the output", "claude", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarnessWith(t, mustExe(t), []step{expect("never")}, envVersion+"="+tc.out)
			h.start()
			// The commands update follows the answer to session/new, and the
			// notice is sent before it.
			h.waitFor(func(m rpcMsg) bool { return m.Method == "session/update" }, 5*time.Second)
			if got := untestedNotices(h); got != tc.notice {
				t.Errorf("%d notices for %q, want %d", got, tc.out, tc.notice)
			}
		})
	}
}

// A claude that does not answer the handshake is not used.
func TestHandshakeFailureFailsClosed(t *testing.T) {
	h := newHarnessWith(t, mustExe(t), []step{expect("never")}, envCtlErr+"=initialize")
	h.call("initialize", map[string]any{"protocolVersion": 1})
	r := h.call("session/new", map[string]any{"cwd": h.cwd, "mcpServers": []any{}})
	if r.Error == nil || r.Error.Message != unavailable {
		t.Fatalf("session/new = %+v %s", r.Error, r.Result)
	}
	if len(h.updates()) != 0 {
		t.Errorf("updates after a refused session: %v", h.updates())
	}
}
