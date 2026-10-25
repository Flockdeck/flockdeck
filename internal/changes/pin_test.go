package changes

import "testing"

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
