package agent

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// stubProbes points availability at a machine described by the test rather than
// at the one the test is running on, and puts everything back afterwards.
func stubProbes(t *testing.T, installed map[string]bool) {
	t.Helper()
	oldLook, oldNow := lookPath, now
	t.Cleanup(func() {
		lookPath, now = oldLook, oldNow
		Refresh()
	})
	lookPath = func(name string) (string, error) {
		if installed[name] {
			return filepath.Join("/usr/bin", name), nil
		}
		return "", errors.New("not found")
	}
	Refresh()
}

// TestAvailable: a program on PATH.
func TestAvailable(t *testing.T) {
	tests := []struct {
		name string
		spec Spec
		want bool
	}{
		{
			name: "a command-line agent that is installed",
			spec: Spec{ID: "claude", Runner: RunnerCLI, Exe: "claude"},
			want: true,
		},
		{
			name: "a command-line agent that is not",
			spec: Spec{ID: "codex", Runner: RunnerCLI, Exe: "codex"},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubProbes(t, map[string]bool{"claude": true})
			if got := Available(tt.spec); got != tt.want {
				t.Errorf("Available = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestAvailableIsRemembered: the picker asks for every agent each time it
// opens, and a lookup for a program that is not installed walks the whole of
// PATH.
func TestAvailableIsRemembered(t *testing.T) {
	stubProbes(t, nil)
	calls := 0
	lookPath = func(string) (string, error) {
		calls++
		return "", errors.New("not found")
	}
	at := time.Now()
	now = func() time.Time { return at }

	spec := Spec{ID: "codex", Runner: RunnerCLI, Exe: "codex"}
	for range 5 {
		Available(spec)
	}
	if calls != 1 {
		t.Errorf("looked %d times, want one remembered answer", calls)
	}

	at = at.Add(probeTTL + time.Second)
	Available(spec)
	if calls != 2 {
		t.Errorf("looked %d times, want the answer to have gone stale", calls)
	}

	// Opening the picker is worth a fresh look: it is where somebody who has
	// just installed an agent goes to find it.
	Refresh()
	Available(spec)
	if calls != 3 {
		t.Errorf("looked %d times, want Refresh to have forgotten the answer", calls)
	}
}

// TestProbeAllCoversTheCatalog, so the picker can ask once and draw everything.
func TestProbeAllCoversTheCatalog(t *testing.T) {
	stubProbes(t, map[string]bool{"claude": true})
	specs := normalizeAll(Builtins())
	got := ProbeAll(specs)
	if len(got) != len(specs) {
		t.Fatalf("got %d answers for %d agents", len(got), len(specs))
	}
	if !got["claude"] {
		t.Error("the installed agent should be available")
	}
	if got["codex"] {
		t.Error("an agent that is not installed should not be")
	}
}

// TestNeedsNoKey pins which addresses count as this machine's own.
func TestNeedsNoKey(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"http://127.0.0.1:11434/v1", true},
		{"http://localhost:1234/v1", true},
		{"http://LocalHost:1234/v1", true},
		{"http://[::1]:8080/v1", true},
		{"http://127.9.9.9/v1", true},
		{"https://api.openai.com/v1", false},
		{"https://localhost.example.com/v1", false},
		{"", false},
		{"not a url at all", false},
	}
	for _, tt := range tests {
		got := NeedsNoKey(Spec{API: APISpec{BaseURL: tt.url}})
		if got != tt.want {
			t.Errorf("NeedsNoKey(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}
