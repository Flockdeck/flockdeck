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
	// A merge driver in the repository's own configuration would run on a conflict.
	// Without being sure there is none, git is not asked.
	if UnsafeConfig(ctx, dir) != nil {
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

func fullHex(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
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

// ErrTipUnknown is returned when the merge was made but the base's new tip could
// not be read afterwards.
var ErrTipUnknown = errors.New("the merge was made, but the new tip of the base could not be read")

// PartlyUpdatedError is returned when a merge failed after it may have begun to
// write the base checkout, and git left it neither as it was nor finished.
type PartlyUpdatedError struct{ Err error }

func (e *PartlyUpdatedError) Error() string {
	return "the merge did not finish and the base checkout may be partly updated: " + e.Err.Error()
}
func (e *PartlyUpdatedError) Unwrap() error { return e.Err }

// hiddenFromStatus says whether the checkout has a file that status does not
// compare: one marked assume-unchanged or skip-worktree, which a merge may
// overwrite without it showing as a change.
func hiddenFromStatus(ctx context.Context, dir string) (bool, error) {
	out, err := ReadOnlyCapped(ctx, dir, 64<<20, "ls-files", "-v", "-z")
	if err != nil {
		return false, err
	}
	for _, entry := range strings.Split(out, "\x00") {
		if entry == "" {
			continue
		}
		// The tag is one letter: H for an ordinary file, S for skip-worktree, and a
		// lower-case letter for assume-unchanged.
		if t := entry[0]; t == 'S' || (t >= 'a' && t <= 'z') {
			return true, nil
		}
	}
	return false, nil
}

// MergeInto merges commit sha into the branch base, which dir must have checked
// out, and returns the base's new tip. It fast-forwards when it can and makes a
// merge commit with message when it cannot, and it never forces, resets or
// deletes anything.
//
// What runs: git itself. The repository's hooks do not run, the commit is not
// signed, and the merge is refused (an *UnsafeConfigError) while the repository's
// own configuration names a merge driver, a filter other than Git LFS's, a signing
// program or an include. A conflict is aborted and reported as ErrNotFastForward,
// leaving the checkout as it was. It is refused (ErrNotClean) while the checkout has
// changes to tracked files, files git is told not to compare, or a merge, rebase,
// cherry-pick or revert in progress.
//
// The merge itself is given its own deadline and not ctx's: a merge stopped part
// way through writing files leaves the checkout worse than one that is let finish.
// If it fails, the base is looked at again: a commit that is in the base after all
// is a merge that was made.
func MergeInto(ctx context.Context, dir, base, sha, message string) (string, error) {
	// Only a full commit id: a name, an abbreviation or anything git could read as an
	// option is not a commit that was approved.
	if !fullHex(sha) {
		return "", errors.New("the commit must be a full commit id")
	}
	cur, _, err := runCapture(ctx, commandTimeout, dir, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil || strings.TrimSpace(cur) != base {
		return "", ErrWrongBranch
	}
	if err := UnsafeConfig(ctx, dir); err != nil {
		return "", err
	}
	gitDir, _, err := runCapture(ctx, commandTimeout, dir, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", err
	}
	gitDir = strings.TrimSpace(gitDir)
	if _, serr := os.Stat(filepath.Join(gitDir, "index.lock")); serr == nil || midOperation(gitDir) != "" {
		return "", ErrNotClean
	}
	dirty, _, err := runCapture(ctx, commandTimeout, dir, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(dirty) != "" {
		return "", ErrNotClean
	}
	if hidden, err := hiddenFromStatus(ctx, dir); err != nil {
		return "", err
	} else if hidden {
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
	if !ff {
		if clean, known := MergesCleanly(ctx, dir, tip, sha); known && !clean {
			return "", ErrNotFastForward
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	// From here on the checkout is written to.
	mctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), networkTimeout)
	defer cancel()
	if ff {
		_, _, err = runCapture(mctx, networkTimeout, dir, "merge", "--ff-only", "--no-gpg-sign", "--end-of-options", sha)
	} else {
		_, _, err = runCapture(mctx, networkTimeout, dir, "merge", "--no-ff", "--no-edit", "--no-gpg-sign", "-m", message, "--end-of-options", sha)
	}
	// Looked at on a fresh deadline: the one the merge ran under may be what ran out.
	fctx, fcancel := context.WithTimeout(context.WithoutCancel(ctx), commandTimeout)
	defer fcancel()
	if err != nil {
		if _, serr := os.Stat(filepath.Join(gitDir, "MERGE_HEAD")); serr == nil {
			_, _, _ = runCapture(fctx, commandTimeout, dir, "merge", "--abort")
			return "", ErrNotFastForward
		}
		if merged, aerr := IsAncestor(fctx, dir, sha, "HEAD"); aerr == nil && merged {
			err = nil // it was made
		} else {
			if _, serr := os.Stat(filepath.Join(gitDir, "index.lock")); serr == nil {
				return "", &PartlyUpdatedError{Err: err}
			}
			if left, _, derr := runCapture(fctx, commandTimeout, dir, "status", "--porcelain", "--untracked-files=no"); derr != nil || strings.TrimSpace(left) != "" {
				return "", &PartlyUpdatedError{Err: err}
			}
			return "", err
		}
	}
	out, _, err := runCapture(fctx, commandTimeout, dir, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "", ErrTipUnknown
	}
	return strings.TrimSpace(out), nil
}
