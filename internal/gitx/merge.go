package gitx

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The merge of a reviewed branch into its base (docs/changes-api.md, section 7).
// Nothing here takes a branch name to merge: the caller names the commit, so a
// branch that moves after it was approved is not the thing merged.

var (
	// ErrWrongBranch is returned when the checkout is not on the branch the
	// merge was asked to go into.
	ErrWrongBranch = errors.New("the checkout is not on the base branch")
	// ErrNotClean is returned when the checkout has changes to tracked files, or
	// is in the middle of a merge, rebase, cherry-pick or revert.
	ErrNotClean = errors.New("the base checkout has uncommitted changes or an operation in progress")
	// ErrNotFastForward is returned when the commit is not a fast-forward of the
	// base and does not merge into it cleanly either.
	ErrNotFastForward = errors.New("the commit does not merge into the base cleanly")
)

// IsAncestor says whether commit anc is reachable from desc.
func IsAncestor(ctx context.Context, dir, anc, desc string) (bool, error) {
	_, _, err := runCapture(ctx, commandTimeout, dir, "merge-base", "--is-ancestor", anc, desc)
	if err == nil {
		return true, nil
	}
	if exitCode(err) == 1 {
		return false, nil
	}
	return false, err
}

// CountBetween is the number of commits reachable from head and not from base.
func CountBetween(ctx context.Context, dir, base, head string) (int, error) {
	out, _, err := runCapture(ctx, commandTimeout, dir, "rev-list", "--count", head, "^"+base)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

// MergesCleanly says whether commits a and b merge without a conflict, worked out
// in memory under a scratch object store, so that no ref, index or working tree
// is touched. known is false when git cannot say (too old, or the command failed):
// callers must not treat that as a conflict.
func MergesCleanly(ctx context.Context, dir, a, b string) (clean, known bool) {
	if ok, _, known := MergeTreeSupport(); !known || !ok {
		return false, false
	}
	common, err := CommonDir(dir)
	if err != nil {
		return false, false
	}
	s, err := NewScratch(common)
	if err != nil {
		return false, false
	}
	defer s.Close()
	var buf bytes.Buffer
	_, err = runToEnv(ctx, commandTimeout, dir, s.env, nil, &buf, "merge-tree", "--write-tree", "--no-messages", a, b)
	switch {
	case err == nil:
		return true, true
	case exitCode(err) == 1 && buf.Len() > 0:
		return false, true
	}
	return false, false
}

// CheckoutOf is the path of the worktree that has branch checked out, if one does.
func CheckoutOf(ctx context.Context, dir, branch string) (string, bool) {
	list, err := listCtx(ctx, dir)
	if err != nil {
		return "", false
	}
	for _, w := range list {
		if !w.Bare && !w.Detached && !w.Prunable && w.Branch == branch {
			return w.Path, true
		}
	}
	return "", false
}

// MergeInto merges commit sha into the branch base, which dir must have checked
// out, and returns the base's new tip. It fast-forwards when it can and makes a
// merge commit with message when it cannot, and it never forces, resets or
// deletes anything. The repository's hooks do not run. A conflict is aborted and
// reported as ErrNotFastForward, leaving the checkout as it was.
func MergeInto(ctx context.Context, dir, base, sha, message string) (string, error) {
	cur, _, err := runCapture(ctx, commandTimeout, dir, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil || strings.TrimSpace(cur) != base {
		return "", ErrWrongBranch
	}
	gitDir, _, err := runCapture(ctx, commandTimeout, dir, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", err
	}
	if _, serr := os.Stat(filepath.Join(strings.TrimSpace(gitDir), "index.lock")); serr == nil || midOperation(strings.TrimSpace(gitDir)) != "" {
		return "", ErrNotClean
	}
	dirty, _, err := runCapture(ctx, commandTimeout, dir, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(dirty) != "" {
		return "", ErrNotClean
	}
	if _, _, err := runCapture(ctx, commandTimeout, dir, "rev-parse", "--verify", "-q", sha+"^{commit}"); err != nil {
		return "", errors.New("the commit is not in the repository")
	}
	tip, _, err := runCapture(ctx, commandTimeout, dir, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "", err
	}
	tip = strings.TrimSpace(tip)
	ff, err := IsAncestor(ctx, dir, tip, sha)
	if err != nil {
		return "", err
	}
	if ff {
		_, _, err = runCapture(ctx, networkTimeout, dir, "merge", "--ff-only", "--end-of-options", sha)
	} else {
		if clean, known := MergesCleanly(ctx, dir, tip, sha); known && !clean {
			return "", ErrNotFastForward
		}
		_, _, err = runCapture(ctx, networkTimeout, dir, "merge", "--no-ff", "--no-edit", "-m", message, "--end-of-options", sha)
		if err != nil {
			// Whatever the merge left half done is undone; its own failure is the answer.
			if _, serr := os.Stat(filepath.Join(strings.TrimSpace(gitDir), "MERGE_HEAD")); serr == nil {
				_, _, _ = runCapture(ctx, commandTimeout, dir, "merge", "--abort")
				return "", ErrNotFastForward
			}
		}
	}
	if err != nil {
		return "", err
	}
	out, _, err := runCapture(ctx, commandTimeout, dir, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}
