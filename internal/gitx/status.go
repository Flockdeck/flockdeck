package gitx

import (
	"strconv"
	"strings"
	"sync"
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
	out, err := run(dir, "status", "--porcelain=v2", "--branch", "--untracked-files=normal")
	if err != nil {
		return st
	}

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
			}
		case strings.HasPrefix(line, "? "):
			st.Untracked++
		case strings.HasPrefix(line, "1 "), strings.HasPrefix(line, "2 "), strings.HasPrefix(line, "u "):
			st.Dirty++
		}
	}
	return st
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
		branches = append(branches, Branch{
			Name:      parts[0],
			Upstream:  parts[1],
			Current:   parts[2] == "*",
			CheckedIn: parts[3],
		})
	}
	return branches, nil
}

// ListDetailed returns every worktree with its status filled in.
//
// Each worktree needs its own status call, so they are run concurrently: a
// repository with several worktrees would otherwise make the panel wait for
// them one after another.
func ListDetailed(dir string) ([]Worktree, error) {
	wts, err := List(dir)
	if err != nil {
		return nil, err
	}
	var wg sync.WaitGroup
	for i := range wts {
		if wts[i].Bare {
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			wts[i].Status = StatusOf(wts[i].Path)
		}(i)
	}
	wg.Wait()
	return wts, nil
}

// Prune removes administrative records for worktrees whose directories have
// been deleted behind git's back.
func Prune(repoDir string) error {
	_, err := run(repoDir, "worktree", "prune")
	return err
}

// AddFrom creates a worktree at path.
//
// When branch names an existing local branch it is checked out; otherwise a new
// branch is created, starting at base when one is given and at the current HEAD
// when it is not.
func AddFrom(repoDir, path, branch, base string) error {
	args := []string{"worktree", "add"}
	switch {
	case branch == "":
		args = append(args, "--detach", path)
		if base != "" {
			args = append(args, base)
		}
	case branchExists(repoDir, branch):
		args = append(args, path, branch)
	default:
		args = append(args, "-b", branch, path)
		if base != "" {
			args = append(args, base)
		}
	}
	_, err := run(repoDir, args...)
	return err
}

// DefaultBase returns a sensible starting point for a new branch: the current
// branch of the main worktree.
func DefaultBase(repoDir string) string {
	if b := CurrentBranch(repoDir); b != "" {
		return b
	}
	return "HEAD"
}
