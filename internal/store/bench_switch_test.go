package store

import (
	"path/filepath"
	"testing"
)

// BenchmarkTouchRecentAlternating measures the cost of TouchRecent as
// Workspace.SelectProject calls it: once for every project switch, in the
// pattern a person actually produces by flipping between two open projects
// (A, B, A, B, ...) rather than always re-touching the same one already at
// the front of the list, which TouchRecents already short-circuits.
//
// SelectProject runs this on the single goroutine that owns the workspace
// and serializes every other command and PTY event, so whatever this costs
// is a stall felt by the whole window, not just the switch that triggered it.
func BenchmarkTouchRecentAlternating(b *testing.B) {
	dir := b.TempDir()
	b.Setenv("APPDATA", dir)
	b.Setenv("XDG_CONFIG_HOME", dir)
	b.Setenv("HOME", dir)

	a := filepath.Join(dir, "project-a")
	c := filepath.Join(dir, "project-b")
	if err := TouchRecent(a); err != nil {
		b.Fatalf("seed: %v", err)
	}
	if err := TouchRecent(c); err != nil {
		b.Fatalf("seed: %v", err)
	}

	roots := [2]string{a, c}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := TouchRecent(roots[i%2]); err != nil {
			b.Fatalf("touch: %v", err)
		}
	}
}
