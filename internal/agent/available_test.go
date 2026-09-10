package agent

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// stubProbes points availability at a machine described by the test rather than
// at the one the test is running on, and puts everything back afterwards.
func stubProbes(t *testing.T, installed map[string]bool, keys string) {
	t.Helper()
	oldLook, oldKeys, oldProbe, oldNow := lookPath, keysFile, KeyProbe, now
	t.Cleanup(func() {
		lookPath, keysFile, KeyProbe, now = oldLook, oldKeys, oldProbe, oldNow
		Refresh()
	})
	lookPath = func(name string) (string, error) {
		if installed[name] {
			return filepath.Join("/usr/bin", name), nil
		}
		return "", errors.New("not found")
	}
	keysFile = func() string { return keys }
	KeyProbe = keyIsSet
	Refresh()
}

// TestAvailable is the whole of section 4's availability rule: a program on
// PATH, or a key, or an endpoint that needs none.
func TestAvailable(t *testing.T) {
	dir := t.TempDir()
	keys := filepath.Join(dir, KeysName)
	if err := os.WriteFile(keys, []byte(`{"openai":"sk-test","google":"  "}`), 0o600); err != nil {
		t.Fatalf("write keys: %v", err)
	}

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
		{
			name: "an API agent with a key in the key store",
			spec: Spec{ID: "openai", Runner: RunnerAPI, API: APISpec{Wire: "openai"}},
			want: true,
		},
		{
			name: "an API agent with nothing but whitespace stored for it",
			spec: Spec{ID: "google", Runner: RunnerAPI, API: APISpec{Wire: "gemini"}},
			want: false,
		},
		{
			name: "an API agent with no key anywhere",
			spec: Spec{ID: "anthropic", Runner: RunnerAPI, API: APISpec{Wire: "anthropic"}},
			want: false,
		},
		{
			name: "an endpoint on this machine needs no key",
			spec: Spec{ID: "local", Runner: RunnerAPI, API: APISpec{Wire: "openai", BaseURL: "http://127.0.0.1:11434/v1"}},
			want: true,
		},
		{
			name: "an endpoint somewhere else does",
			spec: Spec{ID: "gw", Runner: RunnerAPI, API: APISpec{Wire: "openai", BaseURL: "https://gw.example/v1"}},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubProbes(t, map[string]bool{"claude": true}, keys)
			if got := Available(tt.spec); got != tt.want {
				t.Errorf("Available = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestAvailableFromTheEnvironment: a key exported in the shell Perch was
// started from counts, and is looked at before the key store.
func TestAvailableFromTheEnvironment(t *testing.T) {
	stubProbes(t, nil, "")
	spec := Spec{ID: "anthropic", Runner: RunnerAPI, API: APISpec{Wire: "anthropic", KeyEnv: []string{"PERCH_TEST_KEY_A", "PERCH_TEST_KEY_B"}}}
	if Available(spec) {
		t.Fatal("nothing is set yet")
	}
	t.Setenv("PERCH_TEST_KEY_B", "sk-test")
	Refresh()
	if !Available(spec) {
		t.Error("the second name in the list should be looked at too")
	}
}

// TestAvailableIsRemembered: the picker asks for every agent each time it
// opens, and a lookup for a program that is not installed walks the whole of
// PATH.
func TestAvailableIsRemembered(t *testing.T) {
	stubProbes(t, nil, "")
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
	stubProbes(t, map[string]bool{"claude": true}, "")
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
	if got["openai-compatible"] {
		t.Error("an endpoint with no address is not something Perch can start")
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
