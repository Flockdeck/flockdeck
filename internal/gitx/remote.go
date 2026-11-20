package gitx

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
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
	upstream, uerr := upstreamAsked(dir)
	if uerr != nil {
		// Not knowing is not the same as having none: going on would set an
		// upstream, and repoint a branch that tracks something else.
		return "", uerr
	}
	if upstream == "" {
		var err error
		if remote, err = pushRemote(dir, branch); err != nil {
			return "", err
		}
		// The full ref: a tag of the same name makes the short one ambiguous,
		// and git refuses it as a refspec that "matches more than one".
		args = append(args, "--set-upstream", remote, "refs/heads/"+branch)
	}
	// A commit that records a submodule's new commit, pushed while that
	// commit is on no remote of the submodule's, points everyone who fetches
	// it at a commit they cannot get. git checks for that only when asked,
	// so it is asked -- unless the user has said what they want in
	// push.recurseSubmodules themselves.
	if _, cerr := run(dir, "config", "--get", "push.recurseSubmodules"); cerr != nil {
		args = append(args, "--recurse-submodules=check")
	}
	out, err := runVerbose(dir, args...)
	if err != nil && strings.Contains(err.Error(), "not be found on any remote") {
		return "", &gitError{"this push records a submodule's commits that are not on the submodule's own remote yet, " +
			"so nobody fetching it could get them. Push the submodule first -- from its own folder -- then push here."}
	}
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
	// A branch tracking an upstream of another name -- set up in a terminal,
	// or started from origin/main before new branches stopped tracking their
	// base -- is refused by the default push.default, and git's explanation
	// was cut off before the line saying what to do. Asked only once git has
	// refused, so a setting that pushes to the upstream anyway is left alone.
	if err != nil && upstream != "" {
		r, name := splitUpstream(dir, upstream)
		if name != "" && name != branch {
			return "", &gitError{fmt.Sprintf("%s tracks %s, a branch of another name, so git will not push to it here. "+
				"Give it one of its own by pushing once from a terminal: git push -u %s %s", branch, upstream, r, branch)}
		}
		// An upstream with no remote in front of it is a branch of this
		// repository -- `git checkout -b feature --track main` makes one --
		// and git's way out, "git push . HEAD:main", pushes into the local
		// repository rather than anywhere else.
		if name == "" {
			if remote, perr := pushRemote(dir, branch); perr == nil {
				return "", &gitError{fmt.Sprintf("%s tracks the local branch %s, not anything on a remote, so there is nowhere to push it. "+
					"Give it an upstream of its own by pushing once from a terminal: git push -u %s %s", branch, upstream, remote, branch)}
			}
		}
	}
	return out, err
}

// splitUpstream parts "origin/feature/x" into its remote and its branch, by the
// remotes the repository has -- a branch name is free to hold a slash, so the
// first one is not the place to cut. name is "" when no remote matches.
func splitUpstream(dir, upstream string) (remote, name string) {
	for _, r := range Remotes(dir) {
		if rest, ok := strings.CutPrefix(upstream, r+"/"); ok && len(r) > len(remote) {
			remote, name = r, rest
		}
	}
	return remote, name
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
	up, _ := upstreamAsked(dir)
	return up
}

// upstreamAsked is UpstreamOf with the one failure that is not an answer kept:
// git given up on at its deadline. git saying there is no upstream, or that the
// one configured is gone from the remote, is an answer of "".
func upstreamAsked(dir string) (string, error) {
	out, err := run(dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return "", fmt.Errorf("could not tell which remote branch this one tracks, so nothing was changed: %w", err)
		}
		return "", nil // no upstream configured
	}
	return strings.TrimSpace(out), nil
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
	if up, uerr := upstreamAsked(dir); uerr != nil {
		return "", uerr
	} else if up == "" {
		return "", &gitError{"this branch does not track anything on the remote yet, so there is nothing to pull; push it first to set that up"}
	}
	args := []string{"pull", "--ff-only"}
	// A pull that brings in a teammate's bump of a submodule moves the commit
	// recorded for it, and without this leaves the submodule's own checkout
	// where it was. The file list then shows the submodule as changed, and
	// the next Commit -- made for something else entirely -- staged the old
	// checkout and quietly undid the bump. With it, the submodules are
	// checked out at what was pulled; unless the user has said otherwise in
	// submodule.recurse, which git then follows.
	if _, cerr := run(dir, "config", "--get", "submodule.recurse"); cerr != nil {
		args = append(args, "--recurse-submodules")
	}
	return pullRun(dir, args)
}

// pullRun runs the pull command args and explains the two ways it commonly
// fails.
func pullRun(dir string, args []string) (string, error) {
	out, err := runVerbose(dir, args...)
	if err != nil {
		// Bringing the branch up to date writes the index, which an agent's
		// own git may be holding at that moment; the fetch half is done by
		// then, so trying again only has the rest to do.
		if lock, held := waitForIndex(dir, err); lock != "" {
			if held {
				return "", indexHeld(lock)
			}
			out, err = runVerbose(dir, args...)
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
//
// A bare fetch goes to the current branch's remote, which for a branch
// tracking a local one -- `git checkout -b feature --track main` -- is the
// repository itself: "From . * branch main -> FETCH_HEAD", nothing brought
// down from anywhere, and a toast that read like a fetch. Such a branch
// fetches from the remote a first push would go to instead; every other
// branch keeps git's own choice.
func Fetch(dir string) (string, error) {
	if branch := CurrentBranch(dir); branch != "" {
		if out, err := run(dir, "config", "--get", "branch."+branch+".remote"); err == nil && strings.TrimSpace(out) == "." {
			if remote, rerr := pushRemote(dir, branch); rerr == nil {
				return runVerbose(dir, "fetch", "--prune", remote)
			}
		}
	}
	return runVerbose(dir, "fetch", "--prune")
}

// unknownHost picks the host out of ssh's "No ED25519 host key is known for
// github.com and you have requested strict checking", with a space after it,
// or "the remote's host " when ssh did not name it.
func unknownHost(msg string) string {
	_, rest, ok := strings.Cut(msg, " is known for ")
	if !ok {
		return "the remote's host "
	}
	host, _, ok := strings.Cut(rest, " and ")
	if !ok || host == "" || strings.ContainsAny(host, "\r\n") {
		return "the remote's host "
	}
	return host + " "
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

// pushDest is where a push of the checked-out branch would go: the remote the
// branch tracks, else the one Push would pick, and the branch name there. ok is
// false when there is no branch or no remote.
func pushDest(dir string) (remote, name string, ok bool) {
	branch := CurrentBranch(dir)
	if branch == "" {
		return "", "", false
	}
	if up := UpstreamOf(dir); up != "" {
		remote, name = splitUpstream(dir, up)
	}
	if remote == "" {
		r, err := pushRemote(dir, branch)
		if err != nil {
			return "", "", false
		}
		remote, name = r, branch
	}
	return remote, name, true
}

// PushTarget names where a push of the checked-out branch would go: the remote
// the branch tracks, else the one Push would pick, with its push URL. It is ""
// when there is no branch or no remote. A person is shown it before a push they
// did not start themselves is run (the sealed phone socket).
func PushTarget(dir string) string {
	remote, name, ok := pushDest(dir)
	if !ok {
		return ""
	}
	url, _ := run(dir, "remote", "get-url", "--push", remote)
	return remote + " " + strings.TrimSpace(url) + " (branch " + name + ")"
}

// HeadSHA is the commit the checked-out branch is at, or "" when there is none
// (a branch with no commits yet, or a folder that is not a repository).
func HeadSHA(dir string) string {
	out, err := run(dir, "rev-parse", "--verify", "-q", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// PushAhead counts the commits a push of the checked-out branch would send: those
// not yet on the branch's upstream, or, with none, on any branch of the remote a
// first push would go to. An error means it could not be read. A branch with no
// commit yet (an unborn HEAD) sends none: the commit a request makes is the
// first, and is not counted here.
func PushAhead(dir string) (int, error) {
	if HeadSHA(dir) == "" && CurrentBranch(dir) != "" {
		return 0, nil
	}
	args := []string{"rev-list", "--count", "@{u}..HEAD"}
	if UpstreamOf(dir) == "" {
		remote, _, ok := pushDest(dir)
		if !ok {
			return 0, errors.New("no remote to count against")
		}
		args = []string{"rev-list", "--count", "HEAD", "--not", "--remotes=" + remote}
	}
	out, err := run(dir, args...)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, err
	}
	return n, nil
}

// errConfigRead is a setting that could not be read, as opposed to one that is
// not set.
var errConfigRead = errors.New("a git setting could not be read")

// configValues reads every value of a setting. A setting that is not set is an
// empty answer; git failing for any other reason is an error, so a check made
// on it fails closed rather than reading "no setting" into a failed read.
func configValues(dir, key string) ([]string, error) {
	out, err := run(dir, "config", "--get-all", key)
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			return nil, nil
		}
		return nil, errConfigRead
	}
	var vals []string
	for _, v := range strings.Split(strings.TrimSuffix(strings.TrimSuffix(out, "\n"), "\r"), "\n") {
		vals = append(vals, strings.TrimSpace(v))
	}
	return vals, nil
}

// PushSettingsRisk says, in fixed words that name the setting, why a push here
// would send more than the checked-out branch, or somewhere other than the
// remote PushTarget names: push.default of "matching" (every branch), a
// remote.<name>.push refspec that names something else or a mirror remote, a
// branch.<name>.pushRemote or remote.pushDefault that redirects the push,
// push.followTags (tags go too), push.pushOption (options the remote acts on).
// It is "" when the push is the one shown. The agent that wrote to the
// repository can set any of these between a question and its answer, so the
// check is made at both. A setting that cannot be read is a risk.
//
// A branch with no upstream is pushed with an explicit remote and refspec, which
// push.default, remote.<name>.push, pushRemote and pushDefault do not touch, so
// those are not looked at then.
func PushSettingsRisk(dir string) string {
	branch := CurrentBranch(dir)
	remote, _, ok := pushDest(dir)
	if branch == "" || !ok {
		return ""
	}
	hasUpstream := UpstreamOf(dir) != ""
	var failed bool
	get := func(key string) string {
		vals, err := configValues(dir, key)
		if err != nil {
			failed = true
			return ""
		}
		if len(vals) == 0 {
			return ""
		}
		return vals[len(vals)-1]
	}
	why := func() string {
		if hasUpstream {
			switch strings.ToLower(get("push.default")) {
			case "", "simple", "current", "upstream", "tracking", "nothing":
			default:
				return "push.default would send more than the current branch"
			}
			if r := get("branch." + branch + ".pushRemote"); r != "" && r != remote {
				return "branch.<name>.pushRemote sends the push to another remote"
			}
			if r := get("remote.pushDefault"); r != "" && r != remote && get("branch."+branch+".pushRemote") == "" {
				return "remote.pushDefault sends the push to another remote"
			}
		}
		if strings.EqualFold(get("remote."+remote+".mirror"), "true") {
			return "remote.<name>.mirror makes the push send every ref"
		}
		if v := strings.ToLower(get("push.followTags")); v == "true" || v == "1" || v == "yes" || v == "on" {
			return "push.followTags sends tags with the push"
		}
		if vals, err := configValues(dir, "push.pushOption"); err != nil {
			failed = true
		} else {
			for _, v := range vals {
				if v != "" {
					return "push.pushOption sends options to the remote"
				}
			}
		}
		if hasUpstream {
			vals, err := configValues(dir, "remote."+remote+".push")
			if err != nil {
				failed = true
			}
			for _, spec := range vals {
				switch spec {
				case "", "HEAD", branch, "refs/heads/" + branch, branch + ":" + branch, "refs/heads/" + branch + ":refs/heads/" + branch:
				default:
					return "remote.<name>.push sends something other than the current branch"
				}
			}
		}
		return ""
	}()
	if why != "" {
		return why
	}
	if failed {
		return "a git setting that decides what a push sends could not be read"
	}
	return ""
}
