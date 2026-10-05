// Package radar predicts which of the panes working in one repository will
// conflict when their work is merged, while they are still working.
//
// The prediction is git's own: each pane's checkout is snapshotted as a commit
// (gitx.Snapshot), and two snapshots are merged in memory (gitx.MergeTree), so
// what is reported is what git would report at merge time, including work not
// yet committed. It says nothing about whether the merged result builds or
// passes its tests, and nothing is shown for a pair git would merge cleanly.
package radar

import (
	"context"
	"strings"

	"github.com/jmwri/flockdeck/internal/gitx"
)

// Predict merges the snapshot commits a and b under scratch, in dir, a
// checkout of their repository. It returns the paths git reports as
// conflicted, and clean when there are none.
func Predict(ctx context.Context, dir string, s *gitx.Scratch, a, b string) (paths []string, clean bool, err error) {
	out, conflict, err := gitx.MergeTree(ctx, dir, s, a, b)
	if err != nil {
		return nil, false, err
	}
	if !conflict {
		return nil, true, nil
	}
	return parseConflicts(out, a, b), false, nil
}

// parseConflicts reads merge-tree's -z output: the merged tree's id, then each
// conflicted path, every one ended by a NUL.
//
// A file that conflicts with a directory of the same name is listed as
// foo~<commit>, naming the side that holds it by its commit. The suffix is taken
// off, so the path is the one in the repository.
func parseConflicts(out string, commits ...string) []string {
	fields := strings.Split(out, "\x00")
	var paths []string
	seen := map[string]bool{}
	for _, f := range fields[1:] {
		for _, c := range commits {
			f = strings.TrimSuffix(f, "~"+c)
		}
		if f != "" && !seen[f] {
			seen[f] = true
			paths = append(paths, f)
		}
	}
	return paths
}
