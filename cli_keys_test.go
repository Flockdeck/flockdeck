package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jmwri/perch/internal/creds"
)

// isolateKeys points the state directory at one of this test's own, so nothing
// here can read or write the keys of whoever is running it.
func isolateKeys(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)         // Windows
	t.Setenv("XDG_CONFIG_HOME", dir) // Linux
	t.Setenv("HOME", dir)            // macOS and fallback
	// The built-in API agents read these, and a key exported by the machine
	// running the tests would otherwise show up in the listing as set.
	for _, s := range keysAgents() {
		for _, name := range s.API.KeyEnv {
			t.Setenv(name, "")
		}
	}
}

// runKeysCmd drives the subcommand with nobody at the terminal, which is how a
// script uses it, and returns everything it printed.
func runKeysCmd(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := keysCmd(args, keysIO{in: strings.NewReader(stdin), out: &out})
	return out.String(), err
}

func TestKeysSetReadsStdin(t *testing.T) {
	tests := []struct {
		name  string
		stdin string
		want  string
	}{
		{name: "a line", stdin: "sk-typed\n", want: "sk-typed"},
		{name: "no trailing newline", stdin: "sk-piped", want: "sk-piped"},
		{name: "surrounding space", stdin: "  sk-padded  \r\n", want: "sk-padded"},
		{name: "only the first line", stdin: "sk-first\nsk-second\n", want: "sk-first"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateKeys(t)
			out, err := runKeysCmd(t, tt.stdin, "set", "anthropic")
			if err != nil {
				t.Fatalf("keys set: %v", err)
			}
			if strings.Contains(out, tt.want) {
				t.Errorf("the key was printed back: %q", out)
			}
			got := creds.Resolve(keysAgents()[0])
			if got.Secret() != tt.want {
				t.Errorf("stored %q, want %q", got.Secret(), tt.want)
			}
			if got.Source != creds.SourceStore {
				t.Errorf("source = %q, want the store", got.Source)
			}
		})
	}
}

func TestKeysSetRefusesNothing(t *testing.T) {
	isolateKeys(t)
	if _, err := runKeysCmd(t, "   \n", "set", "anthropic"); err == nil {
		t.Fatal("an empty line was accepted as a key")
	}
	if creds.Has("anthropic") {
		t.Error("an empty key was stored anyway")
	}
}

func TestKeysSetNeedsAnAgent(t *testing.T) {
	isolateKeys(t)
	if _, err := runKeysCmd(t, "sk-x", "set"); err == nil {
		t.Fatal("keys set with no agent named succeeded")
	}
}

// The listing is the one place a person looks to find out where a key is
// coming from, and it must answer that without ever showing one.
func TestKeysListSaysWhereWithoutSaying(t *testing.T) {
	isolateKeys(t)
	t.Setenv("OPENAI_API_KEY", "sk-exported")
	if _, err := runKeysCmd(t, "sk-stored\n", "set", "anthropic"); err != nil {
		t.Fatalf("keys set: %v", err)
	}
	// A key left behind by an agent nobody has in their catalog any more is
	// listed too, because otherwise there is no way to be told it is there.
	if err := creds.Set("an-old-agent", "sk-orphan"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	out, err := runKeysCmd(t, "", "list")
	if err != nil {
		t.Fatalf("keys list: %v", err)
	}
	for _, secret := range []string{"sk-exported", "sk-stored", "sk-orphan"} {
		if strings.Contains(out, secret) {
			t.Errorf("the listing printed %s:\n%s", secret, out)
		}
	}
	for _, want := range []string{"anthropic", "openai", "google", "an-old-agent", "OPENAI_API_KEY"} {
		if !strings.Contains(out, want) {
			t.Errorf("the listing does not mention %s:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "not set") {
		t.Errorf("the listing never says an agent has no key:\n%s", out)
	}
}

func TestKeysClear(t *testing.T) {
	isolateKeys(t)
	if _, err := runKeysCmd(t, "sk-stored\n", "set", "anthropic"); err != nil {
		t.Fatalf("keys set: %v", err)
	}
	out, err := runKeysCmd(t, "", "clear", "anthropic")
	if err != nil {
		t.Fatalf("keys clear: %v", err)
	}
	if !strings.Contains(out, "anthropic") {
		t.Errorf("clearing did not say what it cleared: %q", out)
	}
	if creds.Has("anthropic") {
		t.Error("the key survived being cleared")
	}
	out, err = runKeysCmd(t, "", "clear", "anthropic")
	if err != nil {
		t.Fatalf("clearing a key that is not there failed: %v", err)
	}
	if !strings.Contains(out, "no stored key") {
		t.Errorf("clearing nothing said %q", out)
	}
}

func TestKeysUsage(t *testing.T) {
	isolateKeys(t)
	for _, args := range [][]string{nil, {"-h"}, {"help"}} {
		out, err := runKeysCmd(t, "", args...)
		if err != nil {
			t.Fatalf("keys %v: %v", args, err)
		}
		if !strings.Contains(out, "perch keys") {
			t.Errorf("keys %v printed no usage: %q", args, out)
		}
	}
	if out, err := runKeysCmd(t, "", "frobnicate"); err == nil {
		t.Errorf("an unknown command succeeded: %q", out)
	}
}
