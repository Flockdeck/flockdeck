package agent

import "testing"

// TestBuiltinsPassTheirOwnChecks is the gap Merge's own comment names:
// checkTokens, checkRunner and checkTiers only ever run over a built-in when a
// user's agents.json happens to define that same agent id (Merge calls them
// on the merged entry) -- Builtins() on its own goes through normalizeAll and
// nothing else. So a hand-edit to builtin.go that writes a bad token, an
// unknown tier or a malformed runner currently passes `go test ./...` and
// every other check in this package, and is only discovered live, in a pane.
//
// This is the fast, no-external-dependencies half of the compatibility check
// described in flockdeck-planning/13-ci-agent-model-compatibility.md: it runs
// on every PR as part of the ordinary Go test suite (.github/workflows/test.yml)
// and catches Flockdeck's own mistakes in the catalog before anything is
// installed or called live.
func TestBuiltinsPassTheirOwnChecks(t *testing.T) {
	for _, spec := range Builtins() {
		t.Run(spec.ID, func(t *testing.T) {
			s := spec
			for _, problem := range checkRunner(&s) {
				t.Error(problem)
			}
			for _, problem := range checkTokens(&s) {
				t.Error(problem)
			}
			for _, problem := range checkTiers(&s) {
				t.Error(problem)
			}
		})
	}
}
