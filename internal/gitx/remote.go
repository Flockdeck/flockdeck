package gitx

import (
	"fmt"
	"strings"
)

// Push sends the current branch to its remote, setting the upstream the first
// time so a new worktree's branch does not need a separate command.
func Push(dir string) (string, error) {
	branch := CurrentBranch(dir)
	if branch == "" {
		return "", errDetached
	}
	// A branch nobody has committed to yet has nothing on it to send, and git
	// says so as "src refspec main does not match any".
	if !branchExists(dir, branch) {
		return "", &gitError{"nothing to push yet: " + branch + " has no commits; commit something first"}
	}
	args := []string{"push"}
	var remote string
	if UpstreamOf(dir) == "" {
		var err error
		if remote, err = pushRemote(dir, branch); err != nil {
			return "", err
		}
		args = append(args, "--set-upstream", remote, branch)
	}
	out, err := runVerbose(dir, args...)
	// git's own explanation of a rejected push is the part cut from a toast,
	// leaving "failed to push some refs" and nothing about what to do. The
	// "[rejected]" beside the ref is git's marker for it rather than prose.
	if err != nil && strings.Contains(err.Error(), "[rejected]") {
		if remote != "" {
			// A first push, turned away because the name is already taken
			// there. The upstream is set only by a push that succeeds, so
			// "pull them in" sent the reader to a Pull that answered "push it
			// first", and round again.
			return "", &gitError{fmt.Sprintf("push rejected: %s already has a branch called %s, with commits this one does not have. "+
				"Bring them in from a terminal (git pull %s %s), or push this work under another branch name.", remote, branch, remote, branch)}
		}
		return "", &gitError{"push rejected: the remote has commits this branch does not. Pull them in, then push again."}
	}
	return out, err
}

// errDetached is what pushing or pulling without a branch checked out says.
var errDetached = &gitError{"no branch is checked out here (a detached HEAD), so there is nothing to push or pull; check out a branch first"}

// UpstreamOf names the remote branch the checked-out branch tracks, or "" when
// it tracks nothing.
//
// Push is the only thing that needs this, and asking StatusOf made git walk the
// entire working tree to answer it -- 190ms on a checkout with 20,000 untracked
// files, against 44ms here -- for a fact that has nothing to do with what is in
// the working tree. It also meant a status that timed out on a large checkout
// read as "no upstream", and Push would then set one: on a branch tracking
// something other than origin, that quietly repoints it.
func UpstreamOf(dir string) string {
	out, err := run(dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	if err != nil {
		return "" // no upstream configured
	}
	return strings.TrimSpace(out)
}

// pushRemote picks where a branch with no upstream should go: "origin" by
// convention, or the only remote when the repository names it something else.
// More than one and no origin is a choice the user has to make themselves.
//
// Either failure ends with the command that gets past it: the panel has no way
// to add a remote or pick one, and "push manually" left the reader to work out
// how.
func pushRemote(dir, branch string) (string, error) {
	remotes := Remotes(dir)
	switch {
	case len(remotes) == 0:
		return "", &gitError{"this repository has no remote to push to; add one from a terminal: git remote add origin <url>"}
	case len(remotes) == 1:
		return remotes[0], nil
	}
	for _, r := range remotes {
		if r == "origin" {
			return r, nil
		}
	}
	return "", &gitError{fmt.Sprintf("there is no \"origin\" among this repository's remotes (%s), so which to push to is yours "+
		"to choose; push once from a terminal and the panel will follow it: git push -u %s %s",
		strings.Join(remotes, ", "), remotes[0], branch)}
}

// Pull fast-forwards from the upstream. A merge that cannot fast-forward is
// left for the user to resolve deliberately rather than started here.
//
// The two ways that commonly fails are told apart by asking the repository
// rather than by reading git's words, which change with its language: a branch
// with no upstream has nothing to pull from, and one that has moved on as well
// as fallen behind cannot be fast-forwarded. Both used to arrive as git's
// advice, cut down to the lines that did not say what to do.
func Pull(dir string) (string, error) {
	if CurrentBranch(dir) == "" {
		return "", errDetached
	}
	if UpstreamOf(dir) == "" {
		return "", &gitError{"this branch does not track anything on the remote yet, so there is nothing to pull; push it first to set that up"}
	}
	out, err := runVerbose(dir, "pull", "--ff-only")
	if err != nil {
		// Bringing the branch up to date writes the index, which an agent's
		// own git may be holding at that moment; the fetch half is done by
		// then, so trying again only has the rest to do.
		if lock, held := waitForIndex(dir, err); lock != "" {
			if held {
				return "", indexHeld(lock)
			}
			out, err = runVerbose(dir, "pull", "--ff-only")
		}
	}
	if err != nil {
		if st := StatusOf(dir); st.Ahead > 0 && st.Behind > 0 {
			return "", &gitError{fmt.Sprintf("this branch and %s have both moved on (%d commits here, %d there), "+
				"so pulling cannot simply catch up: merge or rebase them in a terminal, then push", st.Upstream, st.Ahead, st.Behind)}
		}
	}
	return out, err
}

// Fetch updates the remote-tracking branches.
func Fetch(dir string) (string, error) {
	return runVerbose(dir, "fetch", "--prune")
}

// HasRemote reports whether the repository has anywhere to push to. A
// substring test for "origin" used to be enough here, but it also matched a
// remote merely named "my-origin" and missed a repository whose only remote is
// called something else entirely.
func HasRemote(dir string) bool { return len(Remotes(dir)) > 0 }

// Remotes lists the configured remote names.
func Remotes(dir string) []string {
	out, err := run(dir, "remote")
	if err != nil {
		return nil
	}
	var names []string
	for _, line := range strings.Split(out, "\n") {
		if name := strings.TrimSpace(line); name != "" {
			names = append(names, name)
		}
	}
	return names
}
