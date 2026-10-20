//go:build !next

package webui

import (
	"io/fs"
	"slices"
	"strings"
	"testing"
)

// The default build serves exactly the old tree's files and no others: no file
// of the new shell, and nothing marked as it.
func TestDefaultFSIsExactlyTheOldTree(t *testing.T) {
	if Variant != "default" {
		t.Fatalf("Variant = %q", Variant)
	}
	got, want := files(t, FS()), files(t, oldTree())
	if !slices.Equal(got, want) {
		t.Errorf("FS lists %d files, assets has %d; first difference: %s", len(got), len(want), firstDiff(got, want))
	}
	for _, name := range got {
		b, err := fs.ReadFile(FS(), name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), nextShellMarker) {
			t.Errorf("%s carries the new shell's marker", name)
		}
	}
}

func firstDiff(a, b []string) string {
	for _, n := range a {
		if !slices.Contains(b, n) {
			return n + " (only in FS)"
		}
	}
	for _, n := range b {
		if !slices.Contains(a, n) {
			return n + " (only in assets)"
		}
	}
	return "none"
}
