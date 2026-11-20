package gitx

import (
	"errors"
	"fmt"
	"strings"
)

// RemotePin is where a push or pull goes, read once and then held to. A push or
// pull a person approved is run from it rather than from the repository's
// configuration, which the agent that writes to the repository can change in the
// time between the yes and the git command: the pushed ref and the URL are the
// pinned ones, and git's own settings do not choose them.
type RemotePin struct {
	Branch string // the local branch
	Remote string // the remote a push goes to
	Dest   string // the branch at that remote a push updates
	// PushURL is the remote's push URL after git's rewrites.
	PushURL string

	// For a pull: the remote and branch the checked-out branch tracks, and the
	// URL it fetches from.
	PullRemote string
	Merge      string
	FetchURL   string
}

// ErrPinMoved is a push or pull refused because where it goes, or the commit the
// branch is at, is no longer what was approved.
var ErrPinMoved = &gitError{"the repository changed after this was approved (the branch, remote, URL or last commit), so nothing was sent. Ask again if you still want it"}

// ErrPushName is a branch that tracks a branch of another name.
var ErrPushName = &gitError{"this branch tracks a branch of another name, so a push of it is not run from here"}

// ResolvePin reads where a push (pull false) or a pull (pull true) of the
// checked-out branch would go. Any part that cannot be read is an error: a pin
// with a hole in it holds to nothing.
func ResolvePin(dir string, pull bool) (RemotePin, error) {
	branch := CurrentBranch(dir)
	if branch == "" {
		return RemotePin{}, errDetached
	}
	p := RemotePin{Branch: branch}
	if !pull {
		remote, dest, ok := pushDest(dir)
		if !ok {
			return RemotePin{}, errors.New("no remote to push to")
		}
		if UpstreamOf(dir) != "" && dest != branch {
			return RemotePin{}, ErrPushName
		}
		url, err := run(dir, "remote", "get-url", "--push", remote)
		if err != nil || strings.TrimSpace(url) == "" {
			return RemotePin{}, errors.New("the push URL could not be read")
		}
		p.Remote, p.Dest, p.PushURL = remote, dest, strings.TrimSpace(url)
		return p, nil
	}
	remote, err := configValues(dir, "branch."+branch+".remote")
	if err != nil || len(remote) == 0 || remote[len(remote)-1] == "" {
		return RemotePin{}, errors.New("the remote this branch pulls from could not be read")
	}
	merge, err := configValues(dir, "branch."+branch+".merge")
	if err != nil || len(merge) == 0 || merge[len(merge)-1] == "" {
		return RemotePin{}, errors.New("the branch this branch pulls could not be read")
	}
	p.PullRemote, p.Merge = remote[len(remote)-1], merge[len(merge)-1]
	if p.PullRemote == "." {
		p.FetchURL = "."
		return p, nil
	}
	url, err := run(dir, "remote", "get-url", p.PullRemote)
	if err != nil || strings.TrimSpace(url) == "" {
		return RemotePin{}, errors.New("the fetch URL could not be read")
	}
	p.FetchURL = strings.TrimSpace(url)
	return p, nil
}

// PushExpect is what the branch must be at when a pinned push starts.
type PushExpect struct {
	// Head is the commit the branch was at when the push was approved.
	Head string
	// AfterCommit is set when a commit was made in between: the branch must then
	// be at a commit whose parent is Head ("" for the first commit of a branch).
	AfterCommit bool
}

// headMatches reports whether HEAD is what e expects, and returns it.
func headMatches(dir string, e PushExpect) (string, bool) {
	head := HeadSHA(dir)
	if head == "" {
		return "", false
	}
	if !e.AfterCommit {
		return head, head == e.Head
	}
	parent, err := run(dir, "rev-parse", "--verify", "-q", "HEAD^")
	if err != nil {
		parent = ""
	}
	return head, strings.TrimSpace(parent) == e.Head && head != e.Head
}

// pushOwnConfig is what a pinned push is run with so that git's settings cannot
// add anything to the one ref pushed: no push.default (the refspec is explicit),
// no tags, no push options, submodules only checked.
var pushOwnConfig = []string{
	"-c", "push.default=nothing",
	"-c", "push.followTags=false",
	"-c", "push.pushOption=",
	"-c", "push.recurseSubmodules=check",
}

// PushPinned sends the commit the branch is at to the pinned destination and to
// nothing else: git push <pinned URL> <sha>:refs/heads/<dest>, with the settings
// that would widen a push set aside. The pin and the head are looked at again
// first, immediately before git starts; what is pushed is the sha read there,
// and the URL is the pinned one, so a remote or refspec setting written since the
// approval is not consulted. The one thing the repository can still change after
// the last look is a url.<base>.pushInsteadOf rewrite of the pinned URL, which
// git applies to a URL argument as well.
func PushPinned(dir string, pin RemotePin, expect PushExpect) (string, error) {
	now, err := ResolvePin(dir, false)
	if err != nil || now != pin {
		return "", ErrPinMoved
	}
	head, ok := headMatches(dir, expect)
	if !ok {
		return "", ErrPinMoved
	}
	args := append(append([]string{}, pushOwnConfig...), "push", pin.PushURL, head+":refs/heads/"+pin.Dest)
	out, err := runVerbose(dir, args...)
	if err != nil {
		if strings.Contains(err.Error(), "not be found on any remote") {
			return "", &gitError{"this push records a submodule's commits that are not on the submodule's own remote yet, " +
				"so nobody fetching it could get them. Push the submodule first -- from its own folder -- then push here."}
		}
		if strings.Contains(err.Error(), "[rejected]") {
			return "", &gitError{"push rejected: the remote has commits this branch does not. Pull them in, then push again."}
		}
		return "", err
	}
	// A push by name sets the upstream the first time; one by URL cannot, so the
	// tracking ref and the upstream are set here, from what was sent.
	if UpstreamOf(dir) == "" {
		if _, e := run(dir, "update-ref", "refs/remotes/"+pin.Remote+"/"+pin.Dest, head); e == nil {
			_, _ = run(dir, "branch", "--set-upstream-to="+pin.Remote+"/"+pin.Dest, pin.Branch)
		}
	}
	return out, nil
}

// PullPinned fast-forwards the branch from the pinned URL and ref, and from
// nothing else: git pull --ff-only <pinned fetch URL> <pinned ref>, with
// pull.rebase set aside. The pin and the head are looked at again immediately
// before git starts.
func PullPinned(dir string, pin RemotePin, head string) (string, error) {
	now, err := ResolvePin(dir, true)
	if err != nil || now != pin || HeadSHA(dir) != head {
		return "", ErrPinMoved
	}
	args := []string{"-c", "pull.rebase=false", "pull", "--ff-only"}
	if vals, cerr := configValues(dir, "submodule.recurse"); cerr == nil && len(vals) == 0 {
		args = append(args, "--recurse-submodules")
	}
	args = append(args, pin.FetchURL, pin.Merge)
	return pullRun(dir, args)
}

// String is the pin as a person reads it.
func (p RemotePin) String() string {
	if p.Dest != "" {
		return fmt.Sprintf("%s %s (branch %s)", p.Remote, p.PushURL, p.Dest)
	}
	return fmt.Sprintf("%s %s (%s)", p.PullRemote, p.FetchURL, p.Merge)
}
