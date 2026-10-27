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

func versionCalls(h *harness) int {
	n := 0
	for _, l := range readLog(h.log) {
		if l == "VERSION_CALL" {
			n++
		}
	}
	return n
}

// The version is read once for the adapter process, not once per session, and
// every untested session still gets its notice.
func TestVersionCheckRunsOnce(t *testing.T) {
	h := newHarnessWith(t, mustExe(t), []step{expect("never")}, envVersion+"=9.9.9 (Claude Code)")
	h.start()
	h.start()
	if n := versionCalls(h); n != 1 {
		t.Errorf("claude --version ran %d times for two sessions, want 1", n)
	}
	deadline := time.Now().Add(5 * time.Second)
	for untestedNotices(h) < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := untestedNotices(h); n != 2 {
		t.Errorf("%d notices for two sessions, want 2", n)
	}
}

// A version command that hangs or fails does not hold up the session: it starts,
// with the notice.
func TestVersionCommandHangsOrFails(t *testing.T) {
	old, oldGrace := versionTimeout, versionGrace
	versionTimeout, versionGrace = 300*time.Millisecond, 300*time.Millisecond
	defer func() { versionTimeout, versionGrace = old, oldGrace }()
	for _, out := range []string{"HANG", "EXIT1", "GRANDCHILD"} {
		t.Run(out, func(t *testing.T) {
			h := newHarnessWith(t, mustExe(t), []step{expect("never")}, envVersion+"="+out)
			began := time.Now()
			h.start()
			if d := time.Since(began); d > 5*time.Second {
				t.Errorf("session/new took %v", d)
			}
			h.waitFor(func(m rpcMsg) bool { return m.Method == "session/update" }, 5*time.Second)
			want := 1
			if out == "GRANDCHILD" {
				want = 0 // it answered with the tested version
			}
			if untestedNotices(h) != want {
				t.Errorf("notices = %d, want %d", untestedNotices(h), want)
			}
		})
	}
}

// An initialize answer that is not an object, and one that never comes, are a
// failed handshake like an error answer.
func TestHandshakeAnswerThatIsUnusableFailsClosed(t *testing.T) {
	t.Run("not an object", func(t *testing.T) {
		h := newHarnessWith(t, mustExe(t), []step{expect("never")}, envBadInit+"=1")
		h.call("initialize", map[string]any{"protocolVersion": 1})
		r := h.call("session/new", map[string]any{"cwd": h.cwd, "mcpServers": []any{}})
		if r.Error == nil || r.Error.Message != unavailable {
			t.Fatalf("session/new = %+v %s", r.Error, r.Result)
		}
	})
	t.Run("no answer in time", func(t *testing.T) {
		old := testControlTimeout
		testControlTimeout = 300 * time.Millisecond
		defer func() { testControlTimeout = old }()
		h := newHarnessWith(t, mustExe(t), []step{expect("never")}, envDelay+"=3000")
		h.call("initialize", map[string]any{"protocolVersion": 1})
		r := h.call("session/new", map[string]any{"cwd": h.cwd, "mcpServers": []any{}})
		if r.Error == nil || r.Error.Message != unavailable {
			t.Fatalf("session/new = %+v %s", r.Error, r.Result)
		}
	})
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
