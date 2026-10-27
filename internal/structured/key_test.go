package structured

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp/acptest"
	"github.com/jmwri/flockdeck/internal/acp/manifest"
	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/record"
)

const testKey = "sk-test-key-0123456789"

func keySpec(adapter string) agent.Spec {
	return agent.Spec{ID: "keyed", Runner: agent.RunnerACP,
		API: agent.APISpec{Wire: "gemini", KeyEnv: []string{"GEMINI_API_KEY"}},
		ACP: agent.ACPSpec{Adapter: adapter, PassEnv: []string{"KEEP_ME"}}}
}

type lineRecorder struct {
	mu    sync.Mutex
	lines []string
}

func (l *lineRecorder) Tap(_ string, raw []byte) {
	l.mu.Lock()
	l.lines = append(l.lines, string(raw))
	l.mu.Unlock()
}
func (l *lineRecorder) Flush() error { return nil }
func (l *lineRecorder) Close() error { return nil }
func (l *lineRecorder) Gap(int)      {}
func (l *lineRecorder) all() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

// The key reaches the agent's environment under its one variable, and the
// agent's process has nothing else added; the recording and the snapshot do not
// have it.
func TestTheKeyReachesTheAgentOnlyInItsEnvironment(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	rec := &lineRecorder{}
	o := Options{
		Spec:     keySpec("gemini"),
		Cwd:      acptest.SessionDir(t),
		Environ:  []string{"KEEP_ME=kept", "GEMINI_API_KEY=exported-not-passed", "OTHER_API_KEY=other"},
		Key:      testKey,
		Command:  &Command{Path: exe, Args: []string{acptest.ArgAgent}},
		Recorder: rec,
	}
	r := begin(t, o)
	if err := r.Send("env"); err != nil {
		t.Fatal(err)
	}
	s := until(t, r, "the environment", func(s Snapshot) bool { return idle(s) && strings.Contains(replies(s), "=") })
	got := replies(s)
	if n := strings.Count(got, "GEMINI_API_KEY="); n != 1 || !strings.Contains(got, "GEMINI_API_KEY="+testKey) {
		t.Errorf("the agent has %d GEMINI_API_KEY entries, want the stored key once:\n%s", n, got)
	}
	for _, bad := range []string{"exported-not-passed", "OTHER_API_KEY", "other"} {
		if strings.Contains(got, bad) {
			t.Errorf("the agent saw %q:\n%s", bad, got)
		}
	}
}

// The runtime's own records do not hold the key: not the recording of the
// traffic, not the snapshot, not its printed form.
func TestTheKeyIsNotInTheRecordingOrTheSnapshot(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	rec := &lineRecorder{}
	r := begin(t, Options{
		Spec: keySpec("gemini"), Cwd: acptest.SessionDir(t), Environ: []string{}, Key: testKey,
		Command: &Command{Path: exe, Args: []string{acptest.ArgAgent}}, Recorder: rec,
	})
	if err := r.Send("hello"); err != nil {
		t.Fatal(err)
	}
	s := until(t, r, "a reply", func(s Snapshot) bool { return idle(s) && replies(s) != "" })
	if rec.all() == "" {
		t.Fatal("nothing was recorded")
	}
	if strings.Contains(rec.all(), testKey) {
		t.Error("the key is in the recording")
	}
	if b, _ := json.Marshal(s); strings.Contains(string(b), testKey) {
		t.Error("the key is in the snapshot")
	}
	if strings.Contains(fmt.Sprintf("%+v %#v", s, r.opts.Spec), testKey) {
		t.Error("the key is in a printed form")
	}
}

// An agent whose entry names no key variable is given no key, whatever the
// options carry, and the variable is never invented from the key.
func TestAKeyIsIgnoredWithoutAVariableInTheEntry(t *testing.T) {
	r := &Runtime{ctx: context.Background(), opts: Options{
		Spec: agent.Spec{ID: "x", Runner: agent.RunnerACP, ACP: agent.ACPSpec{Adapter: "gemini"}},
		Key:  testKey, Environ: []string{},
	}}
	for _, kv := range r.withKey([]string{"PATH=/bin"}) {
		if strings.Contains(kv, testKey) {
			t.Errorf("the key was set as %q", kv)
		}
	}
	r.opts.Spec = keySpec("gemini")
	r.opts.Key = ""
	if got := r.withKey([]string{"PATH=/bin"}); len(got) != 1 {
		t.Errorf("an empty key added %v", got)
	}
	// A name that the platform reads without regard to case is replaced.
	r.opts.Key = testKey
	got := r.withKey([]string{"gemini_api_key=old", "PATH=/bin"})
	if len(got) != 2 || got[1] != "GEMINI_API_KEY="+testKey {
		t.Errorf("withKey = %v", got)
	}
}

// The launch check starts another program, and is not given the key; the
// agent is.
func TestTheLaunchCheckIsNotGivenTheKey(t *testing.T) {
	var seen []string
	run := func(_ context.Context, _ string, _ []string, _ string, env []string) ([]byte, error) {
		seen = append(seen, env...)
		return []byte("{}"), nil
	}
	dir := t.TempDir()
	prog := filepath.Join(dir, "opencode")
	if err := os.WriteFile(prog, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	r := &Runtime{ctx: context.Background(), opts: Options{
		Spec: keySpec("opencode"), Cwd: dir, Key: testKey,
		Environ:          []string{"KEEP_ME=kept"},
		PermissionRunner: run,
		Probe:            &manifest.Probe{LookPath: func(string) (string, error) { return prog, nil }},
	}}
	cmd, setup, err := r.resolve()
	if err != nil || setup != nil {
		t.Fatalf("resolve: %v, %v", setup, err)
	}
	if len(seen) == 0 {
		t.Fatal("the launch check did not run")
	}
	for _, kv := range seen {
		if strings.Contains(kv, testKey) {
			t.Errorf("the launch check was given %q", name(kv))
		}
	}
	if v, ok := envHas(cmd.env, "GEMINI_API_KEY"); !ok || v != testKey {
		t.Errorf("the agent's GEMINI_API_KEY = %q, %v", v, ok)
	}
}

func name(kv string) string { n, _, _ := strings.Cut(kv, "="); return n }

// Starting an agent with a key registers it with the redactor, so a recording
// of an agent that echoes its environment does not hold a key of any shape.
func TestTheKeyIsRegisteredWithTheRedactor(t *testing.T) {
	const odd = "plainword-Zq81mXk20Lr"
	exe, _ := os.Executable()
	begin(t, Options{
		Spec: keySpec("gemini"), Cwd: acptest.SessionDir(t), Environ: []string{}, Key: odd,
		Command: &Command{Path: exe, Args: []string{acptest.ArgAgent}},
	})
	if got := record.Redact("GEMINI_API_KEY=" + odd + " " + odd); strings.Contains(got, odd) {
		t.Errorf("Redact left the key: %q", got)
	}
	// An agent with no key variable registers nothing.
	const unrelated = "plainword-Unrelated9988"
	begin(t, Options{
		Spec: agent.Spec{ID: "x", Runner: agent.RunnerACP, ACP: agent.ACPSpec{Adapter: "gemini"}},
		Cwd:  acptest.SessionDir(t), Environ: []string{}, Key: unrelated,
		Command: &Command{Path: exe, Args: []string{acptest.ArgAgent}},
	})
	if got := record.Redact(unrelated); got != unrelated {
		t.Errorf("a key for an agent with no slot was registered: %q", got)
	}
}

// An agent that wants its key through an authenticate call (Gemini CLI) is
// authenticated once with the entry's KeyMethod when a key is stored, and is
// otherwise left in the login state.
func TestAKeyAnswersALoginRequestOnlyWhenTheEntryNamesTheMethod(t *testing.T) {
	for _, tc := range []struct {
		name   string
		method string
		key    string
		ready  bool
	}{
		{"key and method", "api-key", testKey, true},
		{"no key", "api-key", "", false},
		{"no method", "", testKey, false},
		{"method the agent did not list", "gemini-api-key", testKey, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := fakeOptions(t, acptest.WithAuth("new"))
			o.Spec = keySpec("gemini")
			o.Spec.ACP.KeyMethod = tc.method
			o.Key = tc.key
			o.Environ = []string{}
			r := begin(t, o)
			want := PhaseSetup
			if tc.ready {
				want = PhaseReady
			}
			until(t, r, "the "+string(want)+" phase", func(s Snapshot) bool { return s.Phase == want })
		})
	}
}
