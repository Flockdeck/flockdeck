package creds

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
)

const fakeJev = "FAKE-jev-key-for-tests-0000"

func TestJevKeySetReplaceClear(t *testing.T) {
	isolateConfig(t)
	if HasJevKey() || JevKey() != "" {
		t.Fatal("a key on a fresh store")
	}
	if had, err := ClearJevKey(); err != nil || had {
		t.Errorf("clearing nothing: %v, %v", had, err)
	}
	if err := SetJevKey("  "); err == nil {
		t.Error("an empty key was accepted")
	}
	if err := SetJevKey(" " + fakeJev + "\n"); err != nil {
		t.Fatal(err)
	}
	if JevKey() != fakeJev || !HasJevKey() {
		t.Error("the key was not stored, trimmed")
	}
	if err := SetJevKey("FAKE-second"); err != nil || JevKey() != "FAKE-second" {
		t.Errorf("replace: %v", err)
	}
	if had, err := ClearJevKey(); err != nil || !had || HasJevKey() {
		t.Errorf("clear: %v, %v", had, err)
	}
}

// The key shares the agent keys' file but is not an agent's: it is not listed,
// no agent's Set or Clear can reach it, and no Spec resolves it.
func TestJevKeyIsNotAnAgentsKey(t *testing.T) {
	isolateConfig(t)
	if err := Set("openai", "FAKE-openai"); err != nil {
		t.Fatal(err)
	}
	if err := SetJevKey(fakeJev); err != nil {
		t.Fatal(err)
	}
	names, err := Names()
	if err != nil || len(names) != 1 || names[0] != "openai" {
		t.Errorf("names = %v, %v", names, err)
	}
	if err := Set(JevID, "FAKE-x"); err == nil {
		t.Error("Set took the reserved id")
	}
	if had, _ := Clear(JevID); had || !HasJevKey() {
		t.Error("Clear reached the TypeSafe key")
	}
	// Clearing the TypeSafe key leaves the agent's.
	if _, err := ClearJevKey(); err != nil || !Has("openai") {
		t.Errorf("clearing the TypeSafe key took another: %v", err)
	}
	// And no Spec resolves to it: neither one named for it nor one given the
	// reserved id itself in agents.json.
	if err := SetJevKey(fakeJev); err != nil {
		t.Fatal(err)
	}
	st := StatusAll([]agent.Spec{apiSpec("typesafe", "TYPESAFE_API_KEY")})
	if len(st) != 1 || st[0].Set && st[0].Source == SourceStore {
		t.Errorf("an agent named typesafe resolved the key: %+v", st)
	}
	for _, id := range []string{JevID, " " + JevID, "@other"} {
		spec := apiSpec(id, "FLOCKDECK_TEST_NO_SUCH_KEY")
		if k := Resolve(spec); k.Set() || k.Secret() != "" {
			t.Errorf("a spec with id %q resolved a stored key: %v", id, k)
		}
		if env := Env(spec); len(env) != 0 {
			t.Errorf("a spec with id %q was handed a key in its environment", id)
		}
		if Has(id) {
			t.Errorf("Has(%q) is true", id)
		}
	}
	if !HasJevKey() || JevKey() != fakeJev {
		t.Error("the TypeSafe key is no longer read by JevKey")
	}
}

// Nothing printable about a status, and no error, carries the value; the file
// is 0600 where the system has modes.
func TestJevKeyNeverEchoed(t *testing.T) {
	isolateConfig(t)
	if err := SetJevKey(fakeJev); err != nil {
		t.Fatal(err)
	}
	for _, s := range StatusAll([]agent.Spec{apiSpec("openai", "OPENAI_API_KEY")}) {
		data, _ := json.Marshal(s)
		if strings.Contains(string(data)+s.Describe(), fakeJev) {
			t.Errorf("status carries the key: %s", data)
		}
	}
	// A corrupt store is refused, and the parse error does not quote it.
	if err := os.WriteFile(storePath(t), []byte(`{"@typesafe": "`+fakeJev+`" oops`), 0o600); err != nil {
		t.Fatal(err)
	}
	err := SetJevKey("FAKE-other")
	if err == nil || strings.Contains(err.Error(), fakeJev) {
		t.Errorf("error = %v", err)
	}
	if JevKey() != "" {
		t.Error("an unreadable store gave a key")
	}
}
