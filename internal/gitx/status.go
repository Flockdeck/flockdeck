package gitx

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	var st Status
	// --untracked-files=all matters for the number, not the listing: "normal"
	// collapses a new directory into one entry, so a pane header claiming one
	// untracked file sat above a review panel -- which asks for "all" --
	// listing the thirty inside it.
	out, err := run(dir, "status", "--porcelain=v2", "--branch", "--untracked-files=all")
	if err != nil {
		return st
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
		case strings.HasPrefix(line, "1 AD "):
			// Added and then deleted, which leaves nothing to commit; the
			// file list leaves it out, and the count agrees with it.
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
		st.Branch = rebasingBranch(dir)
	}
	return st
}

// rebasingBranch names the branch a rebase in progress will return to.
//
// Replaying commits is done on a detached HEAD, so a checkout in the middle of
// a rebase reports no branch at all: an agent that stopped on a conflict shows
// in its pane header, and in the review panel, as "detached" -- which is true
// of HEAD and useless to the person looking at it, who has not stopped
// thinking of it as their branch. git keeps the name it will go back to in the
// state directory, and reads it back for its own "rebasing topic".
//
// This costs an extra call, so it is only made for a checkout that has already
// said it is detached, which is rare and stays that way for as long as the
// rebase does.
func rebasingBranch(dir string) string {
	out, err := run(dir, "rev-parse", "--git-dir")
	if err != nil {
		return ""
	}
	base := strings.TrimSpace(out)
	if base == "" {
		return ""
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
		if ref := strings.TrimSpace(string(data)); ref != "" {
			return strings.TrimPrefix(ref, "refs/heads/")
		}
	}
	return ""
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
			checkedIn = filepath.Clean(checkedIn)
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
