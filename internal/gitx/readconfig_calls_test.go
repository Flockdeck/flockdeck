package gitx

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Whether a git command compares the working tree with the index, and so runs a clean
// filter, is decided where it is called (see UnsafeReadConfig). A call added without that
// decision would start reading checkouts ungated, so this lists how often each file names
// such a command and fails when a count changes: gate the new call (or decide that it
// needs none, as for a tree-only diff, a worktree add or a commit the person asked for),
// then update the number. Counts are of the quoted words in the non-test files of gitx
// and changes; comparesWorkingTree in changes/read.go names them too.
func TestEveryCallThatComparesTheWorkingTreeIsAccountedFor(t *testing.T) {
	words := []string{"status", "diff", "add", "update-index", "stash", "diff-files", "diff-index", "--modified"}
	want := map[string]map[string]int{
		"changes.go":          {"status": 2, "diff": 2},
		"commit.go":           {"status": 2, "diff": 1, "add": 2},
		"indexrefresh.go":     {"update-index": 1},
		"made.go":             {"status": 1},
		"merge.go":            {"status": 2},
		"snapshot.go":         {"diff": 1, "add": 2, "update-index": 1, "diff-index": 1, "--modified": 1},
		"status.go":           {"status": 2},
		"worktree.go":         {"add": 2},
		"../changes/diff.go":  {"diff": 2, "add": 3},
		"../changes/read.go":  {"status": 1, "diff": 4, "add": 1, "update-index": 1, "stash": 1, "diff-files": 1, "diff-index": 1},
		"../changes/types.go": {"status": 1},
	}
	for _, dir := range []string{".", "../changes"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		sort.Strings(files)
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			key := filepath.ToSlash(f)
			key = strings.TrimPrefix(key, "./")
			for _, w := range words {
				if n := strings.Count(string(b), `"`+w+`"`); n != want[key][w] {
					t.Errorf("%s names %q %d times, the list says %d: gate the new call with UnsafeReadConfig, or decide it needs none, then update the list", key, w, n, want[key][w])
				}
			}
		}
	}
}
