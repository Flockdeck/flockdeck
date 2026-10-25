package changes

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHashChecksFollowsWhatWouldRun(t *testing.T) {
	a := []CheckDef{{Name: "unit", Cmd: "go test ./..."}, {Name: "lint", Cmd: "go vet ./..."}}
	if HashChecks(a) != HashChecks(append([]CheckDef(nil), a...)) {
		t.Error("the same commands hash differently")
	}
	for name, other := range map[string][]CheckDef{
		"a command": {{Name: "unit", Cmd: "go test ./... && curl x"}, a[1]},
		"a name":    {{Name: "units", Cmd: a[0].Cmd}, a[1]},
		"the order": {a[1], a[0]},
		"an entry":  {a[0]},
	} {
		if HashChecks(other) == HashChecks(a) {
			t.Errorf("a change to %s kept the hash", name)
		}
	}
	// A name and a command cannot be moved across the boundary to the same hash.
	if HashChecks([]CheckDef{{Name: "a", Cmd: "b c"}}) == HashChecks([]CheckDef{{Name: "a b", Cmd: "c"}}) {
		t.Error("the boundary between name and command is not part of the hash")
	}
	if len(HashChecks(nil)) != 64 || HashChecks(nil) != HashChecks([]CheckDef{}) {
		t.Errorf("an empty list: %q", HashChecks(nil))
	}
}

func TestAPinIsKeptAndADamagedOneIsNoPin(t *testing.T) {
	dir := t.TempDir()
	p := PinPath(filepath.Join(dir, "proj-1234abcd.json"))
	if filepath.Base(p) != "proj-1234abcd.pin.json" {
		t.Fatalf("PinPath = %q", p)
	}
	if LoadPin(p) != "" {
		t.Error("a missing pin is a pin")
	}
	h := HashChecks([]CheckDef{{Name: "unit", Cmd: "go test"}})
	if err := SavePin(p, h); err != nil {
		t.Fatal(err)
	}
	if LoadPin(p) != h {
		t.Errorf("LoadPin = %q, want %q", LoadPin(p), h)
	}
	for _, body := range []string{"{", `{"hash":"nope"}`, `{"hash":"` + h[:63] + `"}`, `{"hash":"` + h + `0"}`, ""} {
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := LoadPin(p); got != "" {
			t.Errorf("%q gave the pin %q", body, got)
		}
	}
	if err := SavePin(p, "nope"); err == nil {
		t.Error("a pin that is not a hash was saved")
	}
}
