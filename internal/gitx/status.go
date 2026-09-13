package gitx

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Status summarises a working tree: what is checked out, whether it has
// uncommitted work, and how it stands against its upstream.
type Status struct {
	Branch   string
	Head     string // short commit id
	Detached bool
	Upstream string
	Ahead    int
	Behind   int
	// Unborn marks a branch with no commits on it yet, where Head is empty
	// because there is nothing to point at.
	Unborn bool
	// Operation names what a detached checkout is in the middle of --
	// "rebasing" or "bisecting" -- when Branch was read from that operation's
	// state rather than from HEAD, which names no branch while it runs. A
	// rebase started from a detached HEAD has no branch to read, and is
	// "rebasing" with Branch empty.
	Operation string
	// Dirty counts tracked files with changes; Untracked counts new files.
	Dirty     int
	Untracked int
}

// HasChanges reports whether anything is uncommitted.
func (s Status) HasChanges() bool { return s.Dirty > 0 || s.Untracked > 0 }

// StatusOf reads a working tree's state in a single git call.
//
// `--porcelain=v2 --branch` reports the branch, upstream and ahead/behind
// counts alongside the file entries, which avoids the several invocations the
// same information would otherwise take.
func StatusOf(dir string) Status {
	st, _ := statusWithin(dir, commandTimeout)
	return st
}

// StatusWithin is StatusOf for a caller that gives git less time and needs to
// know when it ran out: the pane headers, which are refreshed every few
// seconds and would rather say a checkout did not answer than wait on it. The
// whole of the read, the extra call for a detached checkout included, has
// timeout to finish in. When it does not, git is stopped, the Status is empty,
// and errors.Is(err, context.DeadlineExceeded) holds.
//
// It does not look inside submodules. Finding work left uncommitted in one
// means a git status of its own inside every submodule, every refresh, and
// that work cannot be committed from here anyway; the review panel, which
// reads StatusOf, still says so. A submodule moved to another commit is this
// repository's change, and is still counted.
func StatusWithin(dir string, timeout time.Duration) (Status, error) {
	return statusWithin(dir, timeout, "--ignore-submodules=dirty")
}

// statusWithin is StatusWithin with extra options for git status.
func statusWithin(dir string, timeout time.Duration, opts ...string) (Status, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var st Status
	// --untracked-files=all matters for the number, not the listing: "normal"
	// collapses a new directory into one entry, so a pane header claiming one
	// untracked file sat above a review panel -- which asks for "all" --
	// listing the thirty inside it.
	args := append([]string{"status", "--porcelain=v2", "--branch", "--untracked-files=all"}, opts...)
	out, _, err := runCapture(ctx, timeout, dir, args...)
	if err != nil {
		return st, err
	}

	var counted bool // git said how far ahead and behind the upstream is
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "# branch.oid "):
			// A repository with no commits yet reports "(initial)" here, which
			// must not be shortened into a commit-id-looking "(initia".
			oid := strings.TrimPrefix(line, "# branch.oid ")
			if oid == "(initial)" {
				st.Unborn = true
				break
			}
			if len(oid) > 7 {
				oid = oid[:7]
			}
			st.Head = oid
		case strings.HasPrefix(line, "# branch.head "):
			head := strings.TrimPrefix(line, "# branch.head ")
			if head == "(detached)" {
				st.Detached = true
			} else {
				st.Branch = head
			}
		case strings.HasPrefix(line, "# branch.upstream "):
			st.Upstream = strings.TrimPrefix(line, "# branch.upstream ")
		case strings.HasPrefix(line, "# branch.ab "):
			// Format: "# branch.ab +2 -3"
			fields := strings.Fields(strings.TrimPrefix(line, "# branch.ab "))
			if len(fields) == 2 {
				st.Ahead, _ = strconv.Atoi(strings.TrimPrefix(fields[0], "+"))
				st.Behind, _ = strconv.Atoi(strings.TrimPrefix(fields[1], "-"))
				counted = true
			}
		case strings.HasPrefix(line, "? "):
			st.Untracked++
		case strings.HasPrefix(line, "1 AD "), strings.HasPrefix(line, "2 CD "):
			// Added -- or copied -- and then deleted, which leaves nothing to
			// commit; the file list leaves it out, and the count agrees with it.
		case strings.HasPrefix(line, "1 "), strings.HasPrefix(line, "2 "), strings.HasPrefix(line, "u "):
			st.Dirty++
		}
	}
	// An upstream git names but cannot count against is one that is gone --
	// deleted on the remote and pruned here -- and read as 0 and 0 it was
	// shown as "level with" a branch that no longer exists, over commits
	// that were never pushed. It is no upstream, as UpstreamOf already said,
	// and Push sets up another.
	if !counted {
		st.Upstream = ""
	}
	if st.Detached && st.Branch == "" {
		st.Branch, st.Operation = operationBranch(ctx, dir)
	}
	return st, nil
}

// operationBranch names the branch a rebase or a bisect in progress will return
// to, and which of the two it is.
//
// Both are done on a detached HEAD, so a checkout in the middle of one reports
// no branch at all: an agent that stopped on a conflict, or that is halving
// its way to a bad commit, shows in its pane header and in the review panel as
// "detached" -- which is true of HEAD and useless to the person looking at it,
// who has not stopped thinking of it as their branch. git keeps the name it
// will go back to in the state directory, and reads it back for its own
// "rebasing topic" and "bisecting".
//
// This costs an extra call, so it is only made for a checkout that has already
// said it is detached, which is rare and stays that way for as long as the
// operation does.
func operationBranch(ctx context.Context, dir string) (branch, operation string) {
	out, err := runUntil(ctx, dir, "rev-parse", "--git-dir")
	if err != nil {
		return "", ""
	}
	base := strings.TrimSpace(out)
	if base == "" {
		return "", ""
	}
	// rev-parse answers relative to the directory it was asked from.
	if !filepath.IsAbs(base) {
		base = filepath.Join(dir, base)
	}
	// rebase-merge belongs to the default backend; rebase-apply to `--apply`
	// and to git old enough to have had no other.
	for _, state := range []string{"rebase-merge", "rebase-apply"} {
		data, err := os.ReadFile(filepath.Join(base, state, "head-name"))
		if err != nil {
			continue
		}
		// Only a branch's own ref: a rebase started from a detached HEAD
		// writes the words "detached HEAD" here, which were shown as the
		// branch's name and offered as the base for a new worktree -- one
		// git then refused as no reference at all. That rebase is still a
		// rebase, with no branch to go back to.
		if ref := strings.TrimSpace(string(data)); strings.HasPrefix(ref, "refs/heads/") {
			return strings.TrimPrefix(ref, "refs/heads/"), "rebasing"
		}
		return "", "rebasing"
	}
	// A bisect writes down where it started: a branch's name, or a commit
	// id when it was started from a detached HEAD, which is no branch.
	if data, err := os.ReadFile(filepath.Join(base, "BISECT_START")); err == nil {
		if start := strings.TrimSpace(string(data)); start != "" && !isCommitID(start) {
			return strings.TrimPrefix(start, "refs/heads/"), "bisecting"
		}
	}
	return "", ""
}

// isCommitID reports whether s is a full commit id rather than a name.
func isCommitID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	return strings.Trim(s, "0123456789abcdef") == ""
}

// submoduleWork names the submodules -- among paths, or all of them when none
// are given -- with work inside them that is not committed there: files changed
// or new in the submodule's own working tree, while it still points at the
// commit this repository records. That work belongs to the submodule's
// repository, and nothing done in this one can commit it.
//
// porcelain v2 says so in an entry's third field, which for a submodule is
// "S" and three flags: its commit changed, it has changes, it has new files.
//
// -z, so a name comes back as it is: without it git quotes and escapes any
// name that is not plain ASCII, and a commit refused for work inside the
// submodule "süb" said the work was inside "s\303\274b".
func submoduleWork(dir string, paths ...string) []string {
	args := []string{"status", "--porcelain=v2", "--ignore-submodules=none", "-z"}
	if len(paths) > 0 {
		args = append(args, "--")
		for _, p := range paths {
			args = append(args, pathspec(p))
		}
	}
	out, err := run(dir, args...)
	if err != nil {
		return nil
	}
	var names []string
	records := strings.Split(out, "\x00")
	for i := 0; i < len(records); i++ {
		f := strings.SplitN(records[i], " ", 9)
		if len(f) > 0 && f[0] == "2" {
			i++ // a rename's old name follows as a record of its own
			continue
		}
		if len(f) < 9 || f[0] != "1" || len(f[2]) != 4 {
			continue
		}
		if sub := f[2]; sub[0] == 'S' && sub[1] == '.' && (sub[2] == 'M' || sub[3] == 'U') {
			names = append(names, f[8])
		}
	}
	return names
}

// Branch is a local branch and where it stands against its upstream.
type Branch struct {
	Name      string
	Upstream  string
	Current   bool
	CheckedIn string // path of the worktree that has it checked out, if any
}

// Branches lists local branches, most recently used first, so the worktree
// picker can offer them without the user typing a name.
func Branches(dir string) ([]Branch, error) {
	out, err := run(dir, "for-each-ref",
		"--sort=-committerdate",
		"--format=%(refname:short)%09%(upstream:short)%09%(HEAD)%09%(worktreepath)",
		"refs/heads/")
	if err != nil {
		return nil, err
	}
	var branches []Branch
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 4 {
			continue
		}
		// worktreepath comes back with forward slashes, like every other path
		// git prints; clean it so it can be compared with a Worktree.Path.
		checkedIn := parts[3]
		if checkedIn != "" {
			// And spelled as the repository was asked about, as List spells
			// every worktree, where a link lies on the way.
			checkedIn = respeller(dir)(filepath.Clean(checkedIn))
		}
		branches = append(branches, Branch{
			Name:      parts[0],
			Upstream:  parts[1],
			Current:   parts[2] == "*",
			CheckedIn: checkedIn,
		})
	}
	return branches, nil
}
