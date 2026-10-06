package gitx

import (
	"context"
	"sort"
	"strconv"
	"strings"
)

// Since lists the files the commits on HEAD changed since HEAD left base, with
// line counts, and says how many commits that is. It is the committed half of
// what a branch has done; Changes is the uncommitted half. The comparison is
// against the merge base, so work that landed on base after the branch was cut
// is not counted as the branch's.
//
// A file's Status and Label are left empty: only the line counts are read.
//
// base is a branch name an agent or a repository chose, so it is passed after
// --end-of-options: one that starts with a dash is a name, never an option.
func Since(dir, base string) (files []FileChange, commits int, err error) {
	ctx := context.Background()
	counts, err := numstat(ctx, dir, "--end-of-options", base+"...HEAD")
	if err != nil {
		return nil, 0, err
	}
	for path, n := range counts {
		files = append(files, FileChange{Path: path, Added: n.added, Removed: n.removed})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	out, err := runUntil(ctx, dir, "rev-list", "--count", "--end-of-options", base+"..HEAD")
	if err != nil {
		return files, 0, err
	}
	commits, _ = strconv.Atoi(strings.TrimSpace(out))
	return files, commits, nil
}
