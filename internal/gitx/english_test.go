package gitx

import (
	"sync"
	"testing"
)

// The commands that retryIndex asks again match git's English index-open message,
// so each of them runs with LANGUAGE=C and LC_MESSAGES=C, even when the process
// was started in another language.
func TestRetriedIndexCommandsRunWithEnglishMessages(t *testing.T) {
	repo, _ := staleRepo(t, 3)
	t.Setenv("LANGUAGE", "de")
	t.Setenv("LC_MESSAGES", "de_DE.UTF-8")

	var mu sync.Mutex
	seen := map[string][]string{}
	envHook = func(args, env []string) {
		mu.Lock()
		defer mu.Unlock()
		if len(args) > 0 {
			seen[args[0]] = env
		}
	}
	t.Cleanup(func() { envHook = nil })

	if _, err := StatusWithin(repo, commandTimeout); err != nil {
		t.Fatal(err)
	}
	if _, err := Changes(repo); err != nil {
		t.Fatal(err)
	}
	if _, err := Diff(repo, "README.md"); err != nil {
		t.Fatal(err)
	}

	last := func(env []string, key string) string {
		v := "<unset>"
		for _, kv := range env {
			if len(kv) > len(key) && kv[:len(key)+1] == key+"=" {
				v = kv[len(key)+1:]
			}
		}
		return v
	}
	for _, cmd := range []string{"status", "diff"} {
		env, ok := seen[cmd]
		if !ok {
			t.Fatalf("git %s was not run", cmd)
		}
		if got := last(env, "LANGUAGE"); got != "C" {
			t.Errorf("git %s: LANGUAGE = %q, want C", cmd, got)
		}
		if got := last(env, "LC_MESSAGES"); got != "C" {
			t.Errorf("git %s: LC_MESSAGES = %q, want C", cmd, got)
		}
	}
}
