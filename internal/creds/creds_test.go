package creds

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
)

// isolateConfig points the state directory at a directory of this test's own,
// so nothing here can read or write the keys of whoever is running it.
func isolateConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)         // Windows
	t.Setenv("XDG_CONFIG_HOME", dir) // Linux
	t.Setenv("HOME", dir)            // macOS and fallback
}

// storePath is where the isolated store ended up. It is asked for rather than
// assembled, because the state directory sits somewhere different under each
// of the three operating systems this has to pass on.
func storePath(t *testing.T) string {
	t.Helper()
	p, err := path()
	if err != nil {
		t.Fatalf("locate keys.json: %v", err)
	}
	return p
}

// apiSpec is the shape of Spec this package cares about: an id and the
// variables a key may arrive in.
func apiSpec(id string, env ...string) agent.Spec {
	return agent.Spec{ID: id, Name: id, Runner: agent.RunnerAPI,
		API: agent.APISpec{Wire: "anthropic", KeyEnv: env}}
}

func TestResolveOrder(t *testing.T) {
	spec := apiSpec("anthropic", "FLOCKDECK_TEST_KEY_A", "FLOCKDECK_TEST_KEY_B")

	tests := []struct {
		name       string
		env        map[string]string
		stored     string
		wantSecret string
		wantSource Source
		wantEnv    string
	}{
		{
			name:       "nothing anywhere",
			wantSource: SourceNone,
		},
		{
			name:       "the first name wins",
			env:        map[string]string{"FLOCKDECK_TEST_KEY_A": "first", "FLOCKDECK_TEST_KEY_B": "second"},
			wantSecret: "first",
			wantSource: SourceEnv,
			wantEnv:    "FLOCKDECK_TEST_KEY_A",
		},
		{
			name:       "a later name is still found",
			env:        map[string]string{"FLOCKDECK_TEST_KEY_B": "second"},
			wantSecret: "second",
			wantSource: SourceEnv,
			wantEnv:    "FLOCKDECK_TEST_KEY_B",
		},
		{
			name:       "the environment beats the store",
			env:        map[string]string{"FLOCKDECK_TEST_KEY_A": "exported"},
			stored:     "saved",
			wantSecret: "exported",
			wantSource: SourceEnv,
			wantEnv:    "FLOCKDECK_TEST_KEY_A",
		},
		{
			name:       "the store is the fallback",
			stored:     "saved",
			wantSecret: "saved",
			wantSource: SourceStore,
		},
		{
			name:       "an empty variable is not a key",
			env:        map[string]string{"FLOCKDECK_TEST_KEY_A": "   "},
			stored:     "saved",
			wantSecret: "saved",
			wantSource: SourceStore,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateConfig(t)
			// Both names are cleared first: a value left in the environment by
			// the machine running the tests would decide the answer instead.
			t.Setenv("FLOCKDECK_TEST_KEY_A", "")
			t.Setenv("FLOCKDECK_TEST_KEY_B", "")
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			if tt.stored != "" {
				if err := Set(spec.ID, tt.stored); err != nil {
					t.Fatalf("Set: %v", err)
				}
			}

			got := Resolve(spec)
			if got.Secret() != tt.wantSecret {
				t.Errorf("Secret = %q, want %q", got.Secret(), tt.wantSecret)
			}
			if got.Source != tt.wantSource {
				t.Errorf("Source = %q, want %q", got.Source, tt.wantSource)
			}
			if got.Env != tt.wantEnv {
				t.Errorf("Env = %q, want %q", got.Env, tt.wantEnv)
			}
			if got.Set() != (tt.wantSecret != "") {
				t.Errorf("Set() = %v for secret %q", got.Set(), tt.wantSecret)
			}
		})
	}
}

// A Spec with no KeyEnv names has nothing to read from the environment, and a
// Spec that is not an API runner is never asked about at all.
func TestResolveWithoutKeyEnv(t *testing.T) {
	isolateConfig(t)
	spec := agent.Spec{ID: "local", Runner: agent.RunnerAPI}
	if got := Resolve(spec); got.Set() {
		t.Errorf("a spec with no key anywhere resolved to %v", got)
	}
	if err := Set("local", "saved"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := Resolve(spec); got.Secret() != "saved" || got.Source != SourceStore {
		t.Errorf("Resolve = %v, want the stored key", got)
	}
}

// Env is the one path a key is allowed to travel, and it carries a key only
// when the process would not already have inherited one.
func TestEnv(t *testing.T) {
	tests := []struct {
		name     string
		exportAs string
		stored   string
		want     []string
	}{
		{name: "no key at all"},
		{
			name:     "already exported, so nothing to add",
			exportAs: "FLOCKDECK_TEST_KEY_A",
			stored:   "saved",
		},
		{
			name:   "a stored key goes under the first name",
			stored: "saved",
			want:   []string{"FLOCKDECK_TEST_KEY_A=saved"},
		},
	}
	spec := apiSpec("anthropic", "FLOCKDECK_TEST_KEY_A", "FLOCKDECK_TEST_KEY_B")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateConfig(t)
			t.Setenv("FLOCKDECK_TEST_KEY_A", "")
			t.Setenv("FLOCKDECK_TEST_KEY_B", "")
			if tt.exportAs != "" {
				t.Setenv(tt.exportAs, "exported")
			}
			if tt.stored != "" {
				if err := Set(spec.ID, tt.stored); err != nil {
					t.Fatalf("Set: %v", err)
				}
			}
			got := Env(spec)
			if strings.Join(got, "\x00") != strings.Join(tt.want, "\x00") {
				t.Errorf("Env = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("nowhere to put it", func(t *testing.T) {
		isolateConfig(t)
		if err := Set("local", "saved"); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if got := Env(agent.Spec{ID: "local", Runner: agent.RunnerAPI}); got != nil {
			t.Errorf("Env invented a variable name: %v", got)
		}
	})
}

func TestStoreRoundTrip(t *testing.T) {
	isolateConfig(t)
	t.Setenv("FLOCKDECK_TEST_KEY_A", "")

	if names, err := Names(); err != nil || len(names) != 0 {
		t.Fatalf("a fresh install listed %v (%v)", names, err)
	}
	if err := Set("openai", "sk-openai"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := Set("anthropic", "sk-anthropic"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	names, err := Names()
	if err != nil {
		t.Fatalf("Names: %v", err)
	}
	if strings.Join(names, ",") != "anthropic,openai" {
		t.Errorf("Names = %v, want them sorted", names)
	}
	if !Has("openai") || Has("gemini") {
		t.Error("Has disagrees with what was stored")
	}

	had, err := Clear("openai")
	if err != nil || !had {
		t.Fatalf("Clear = %v, %v", had, err)
	}
	if had, _ := Clear("openai"); had {
		t.Error("clearing twice reported a second key")
	}
	if Has("openai") {
		t.Error("the cleared key is still there")
	}
	if !Has("anthropic") {
		t.Error("clearing one key took another with it")
	}

	// What ends up on disk is the flat shape the design describes, so a file
	// written by hand and one written here are the same file.
	data, err := os.ReadFile(storePath(t))
	if err != nil {
		t.Fatalf("read keys.json: %v", err)
	}
	var onDisk map[string]string
	if err := json.Unmarshal(data, &onDisk); err != nil {
		t.Fatalf("keys.json is not a flat object: %v", err)
	}
	if onDisk["anthropic"] != "sk-anthropic" || len(onDisk) != 1 {
		t.Errorf("keys.json holds %v", onDisk)
	}
}

// The store carries the only secrets Flockdeck keeps, so it is written 0600 and
// stays that way when it is rewritten.
func TestStoreIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are not how Windows says this")
	}
	isolateConfig(t)
	for _, key := range []string{"first", "second"} {
		if err := Set("anthropic", key); err != nil {
			t.Fatalf("Set: %v", err)
		}
		fi, err := os.Stat(storePath(t))
		if err != nil {
			t.Fatalf("stat keys.json: %v", err)
		}
		if perm := fi.Mode().Perm(); perm != fs.FileMode(0o600) {
			t.Errorf("keys.json is %v after writing %q, want 0600", perm, key)
		}
	}
}

// A store that cannot be parsed reads as no keys, so nothing fails to start
// over it — but it is never rewritten, because that would throw away every
// key in it to fix a stray comma.
func TestDamagedStore(t *testing.T) {
	isolateConfig(t)
	if err := Set("anthropic", "sk-anthropic"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	p := storePath(t)
	if err := os.WriteFile(p, []byte(`{"anthropic": "sk-anthropic",}`), 0o600); err != nil {
		t.Fatalf("damage keys.json: %v", err)
	}

	if Has("anthropic") {
		t.Error("a damaged store answered Has")
	}
	if got := Resolve(apiSpec("anthropic")); got.Set() {
		t.Error("a damaged store resolved a key")
	}
	err := Set("openai", "sk-openai")
	if err == nil {
		t.Fatal("Set overwrote a store it could not read")
	}
	if strings.Contains(err.Error(), "sk-") {
		t.Errorf("the error quotes the file: %v", err)
	}
	data, readErr := os.ReadFile(p)
	if readErr != nil || !strings.Contains(string(data), "sk-anthropic") {
		t.Errorf("the damaged store was not left alone: %q (%v)", data, readErr)
	}
}

// Nothing that formats a Key may print it. The two verbs that reach inside a
// struct are the ones worth pinning down: %v goes through String, and %#v
// through GoString, which is the one that would otherwise print an unexported
// field.
func TestKeyNeverPrintsItself(t *testing.T) {
	k := Key{secret: "sk-super-secret", Source: SourceEnv, Env: "ANTHROPIC_API_KEY"}
	for _, format := range []string{"%v", "%s", "%+v", "%#v", "%q"} {
		if got := fmt.Sprintf(format, k); strings.Contains(got, "sk-super-secret") {
			t.Errorf("%s printed the key: %s", format, got)
		}
	}
	data, err := json.Marshal(k)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(data), "sk-super-secret") {
		t.Errorf("a Key encoded to JSON carries the key: %s", data)
	}
	if got := (Key{}).String(); got != "not set" {
		t.Errorf("an empty Key says %q", got)
	}
}

// TestAStoredKeyAVariableShadowsIsStillReportedStored covers an entry with no
// key variable of its own, such as a gateway given only an address, whose key
// was stored before its wire's usual variable was exported. The variable is
// the key in use, and the keys dialog, which offers Clear only for a stored
// key, had no way left to clear the one Flockdeck still held.
func TestAStoredKeyAVariableShadowsIsStillReportedStored(t *testing.T) {
	isolateConfig(t)
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("FLOCKDECK_API_KEY", "")
	// An address is what makes the wire's usual variable one it may be given.
	gateway := apiSpec("gateway")
	gateway.API.BaseURL = "https://gateway.example"

	if st := StatusOf(gateway); st.Stored {
		t.Errorf("with nothing stored the status says a key is stored: %+v", st)
	}
	if err := Set("gateway", "sk-stored"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if st := StatusOf(gateway); !st.Stored || st.Source != SourceStore {
		t.Errorf("a stored key in use reads %+v, want it stored and the source", st)
	}
	t.Setenv("ANTHROPIC_API_KEY", "exported")
	st := StatusOf(gateway)
	if st.Source != SourceEnv || st.Env != "ANTHROPIC_API_KEY" {
		t.Fatalf("the exported variable is not the key in use: %+v", st)
	}
	if !st.Stored {
		t.Errorf("a stored key the variable shadows reads as not stored, so nothing offers to clear it: %+v", st)
	}

	// An entry with a variable of its own is given that variable ahead of the
	// store, so there the key in use is never the stored one once it is
	// exported: only asking the store says a key is kept there as well.
	t.Setenv("FLOCKDECK_TEST_KEY_A", "")
	own := apiSpec("anthropic", "FLOCKDECK_TEST_KEY_A")
	if err := Set("anthropic", "sk-stored"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	t.Setenv("FLOCKDECK_TEST_KEY_A", "exported")
	st = StatusOf(own)
	if st.Source != SourceEnv || st.Env != "FLOCKDECK_TEST_KEY_A" {
		t.Fatalf("the entry's own variable is not the key in use: %+v", st)
	}
	if !st.Stored {
		t.Errorf("a stored key the entry's own variable shadows reads as not stored: %+v", st)
	}
}

func TestStatus(t *testing.T) {
	isolateConfig(t)
	t.Setenv("FLOCKDECK_TEST_KEY_A", "")
	t.Setenv("FLOCKDECK_TEST_KEY_B", "")

	specs := []agent.Spec{
		apiSpec("anthropic", "FLOCKDECK_TEST_KEY_A"),
		apiSpec("openai", "FLOCKDECK_TEST_KEY_B"),
		{ID: "claude", Name: "Claude Code", Runner: agent.RunnerCLI, Exe: "claude"},
	}
	if err := Set("openai", "sk-openai"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	t.Setenv("FLOCKDECK_TEST_KEY_A", "exported")

	got := StatusAll(specs)
	if len(got) != 2 {
		t.Fatalf("StatusAll returned %d entries, want only the API runners: %+v", len(got), got)
	}
	if got[0].Agent != "anthropic" || !got[0].Set || got[0].Source != SourceEnv || got[0].Env != "FLOCKDECK_TEST_KEY_A" {
		t.Errorf("anthropic status = %+v", got[0])
	}
	if got[1].Agent != "openai" || !got[1].Set || got[1].Source != SourceStore {
		t.Errorf("openai status = %+v", got[1])
	}

	// A Status is what reaches the front end and the CLI, so its encoded form
	// has to be free of the key as well.
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(data), "sk-openai") || strings.Contains(string(data), "exported") {
		t.Errorf("a Status carries the key: %s", data)
	}

	unset := StatusOf(apiSpec("google", "FLOCKDECK_TEST_KEY_C", "FLOCKDECK_TEST_KEY_D"))
	if unset.Set {
		t.Errorf("google reads as set: %+v", unset)
	}
	if d := unset.Describe(); !strings.Contains(d, "FLOCKDECK_TEST_KEY_C") || !strings.Contains(d, "keys set google") {
		t.Errorf("Describe does not say what to do: %q", d)
	}
}
