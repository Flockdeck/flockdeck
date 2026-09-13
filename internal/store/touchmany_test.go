package store

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestTouchRecentsPutsProjectsFirstInTheOrderGiven checks recording several
// projects at once leaves them at the front of the list as given, each once,
// and writes nothing when they are there already.
func TestTouchRecentsPutsProjectsFirstInTheOrderGiven(t *testing.T) {
	isolateConfig(t)
	base := t.TempDir()
	a, b, c, d := filepath.Join(base, "a"), filepath.Join(base, "b"), filepath.Join(base, "c"), filepath.Join(base, "d")
	roots := func() []string {
		list, err := Recents()
		if err != nil {
			t.Fatalf("recents: %v", err)
		}
		var out []string
		for _, p := range list {
			out = append(out, filepath.Base(p.Root))
		}
		return out
	}

	if err := TouchRecents(a, b, c); err != nil {
		t.Fatalf("touch: %v", err)
	}
	if err := TouchRecent(d); err != nil {
		t.Fatalf("touch: %v", err)
	}
	// Named twice, the second time untidily: still one project.
	if err := TouchRecents(b, c, b+string(filepath.Separator)); err != nil {
		t.Fatalf("touch: %v", err)
	}
	if got, want := fmt.Sprint(roots()), "[b c d a]"; got != want {
		t.Errorf("recents = %s, want %s", got, want)
	}

	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, recentsFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := TouchRecents(b, c); err != nil {
		t.Fatalf("touch: %v", err)
	}
	if after, _ := os.ReadFile(filepath.Join(dir, recentsFile)); !bytes.Equal(before, after) {
		t.Errorf("recording projects already at the front rewrote the list")
	}
	if err := TouchRecents(c, b); err != nil {
		t.Fatalf("touch: %v", err)
	}
	if got, want := fmt.Sprint(roots()), "[c b d a]"; got != want {
		t.Errorf("recents after reordering = %s, want %s", got, want)
	}
	if err := TouchRecents(a, ""); err == nil {
		t.Error("an empty path was taken for a project")
	}
}

// BenchmarkRecordingReopenedProjects is what a start that reopens five
// projects spends on the recent list: recorded one at a time, as it was, and
// all at once. The list is disturbed before each, so that both have writing to
// do, as a start after a day of switching between other projects does.
func BenchmarkRecordingReopenedProjects(b *testing.B) {
	dir := b.TempDir()
	b.Setenv("APPDATA", dir)
	b.Setenv("XDG_CONFIG_HOME", dir)
	b.Setenv("HOME", dir)
	var reopened []string
	for i := 0; i < 5; i++ {
		reopened = append(reopened, filepath.Join(dir, fmt.Sprintf("project-%d", i)))
	}
	other := filepath.Join(dir, "elsewhere")
	disturb := func() {
		b.StopTimer()
		if err := TouchRecent(other); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
	}
	b.Run("one-at-a-time", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			disturb()
			for _, root := range reopened {
				if err := TouchRecent(root); err != nil {
					b.Fatal(err)
				}
			}
		}
	})
	b.Run("together", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			disturb()
			if err := TouchRecents(reopened...); err != nil {
				b.Fatal(err)
			}
		}
	})
}
