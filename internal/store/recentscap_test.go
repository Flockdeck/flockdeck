package store

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// The cap on the recent list lets go of the project used longest ago, not of
// whichever comes last in the order the picker shows. Projects placed by hand
// are shown ahead of every other, however long ago they were used, so cut in
// that order a full list lost the project the user was in a moment ago --
// and the name they had given it -- as soon as they opened another.
func TestRecentsCapLetsGoOfTheLeastRecentlyUsed(t *testing.T) {
	isolateConfig(t)

	long := time.Now().Add(-30 * 24 * time.Hour)
	var list []Project
	for i := 1; i < maxRecents; i++ {
		list = append(list, Project{Root: fmt.Sprintf("/repo/placed%02d", i), LastUsed: long.Add(time.Duration(i) * time.Minute), Order: i})
	}
	list = append(list, Project{Root: "/repo/current", Name: "Mine", LastUsed: time.Now().Add(-time.Minute)})
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if err := writeRecentsFile(filepath.Join(dir, recentsFile), list); err != nil {
		t.Fatal(err)
	}

	if err := TouchRecent("/repo/new"); err != nil {
		t.Fatalf("touch: %v", err)
	}

	got, err := Recents()
	if err != nil {
		t.Fatalf("recents: %v", err)
	}
	if len(got) != maxRecents {
		t.Fatalf("remembered %d projects, want %d", len(got), maxRecents)
	}
	var current, fresh, oldest bool
	for _, p := range got {
		switch {
		case sameRoot(p.Root, "/repo/current"):
			current = true
			if p.Name != "Mine" {
				t.Errorf("the project in use kept the name %q, want %q", p.Name, "Mine")
			}
		case sameRoot(p.Root, "/repo/new"):
			fresh = true
		case sameRoot(p.Root, "/repo/placed01"):
			oldest = true
		}
	}
	if !current {
		t.Errorf("the project used a minute ago was dropped from the recent list: %v", roots(got))
	}
	if !fresh {
		t.Errorf("the project just opened is not in the recent list: %v", roots(got))
	}
	if oldest {
		t.Errorf("the project used longest ago was kept over one used a minute ago: %v", roots(got))
	}
}

func roots(list []Project) []string {
	out := make([]string, len(list))
	for i, p := range list {
		out[i] = p.Root
	}
	return out
}
