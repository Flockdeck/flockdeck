package gitx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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

// ErrPullRemote is a pull whose remote is not a single named remote with a URL.
var ErrPullRemote = &gitError{"this branch pulls from something that is not a single named remote (a URL, or several remotes), so a pull of it is not run from here"}

// ErrPullMerge is a pull of a branch with more than one branch.<name>.merge.
var ErrPullMerge = &gitError{"this branch has more than one branch.<name>.merge setting, so a pull of it is not run from here: git would merge all of them"}

// ErrPinOption is a pinned value that git would read as an option.
var ErrPinOption = &gitError{"a remote, URL or branch name here starts with '-', which git would read as an option, so it is not run from here"}

// ErrPinHook is a push of a repository that has a pre-push hook or uses Git LFS.
// A pinned push runs no hooks, so the owner's check would be skipped and LFS
// would send pointers without their content.
var ErrPinHook = &gitError{"this repository has a pre-push hook or Git LFS: push from the desk"}

// ErrPinURL is a pinned URL that git would rewrite again when it is given it.
var ErrPinURL = &gitError{"a URL here is rewritten again by a url.<base>.insteadOf or pushInsteadOf rule, so where it connects is not the URL shown, and it is not run from here"}

// ErrPinProgram is a setting in the repository's own configuration that runs a
// program or reroutes the connection.
type ErrPinProgram struct{ Key string }

func (e *ErrPinProgram) Error() string {
	return "the repository's own git configuration sets " + e.Key + ", which runs a program or reroutes the connection, so a push or pull approved from elsewhere is not run. Run it from a terminal, or remove the setting"
}

// optionLike reports whether any of vals would be read by git as an option.
func optionLike(vals ...string) bool {
	for _, v := range vals {
		if strings.HasPrefix(v, "-") {
			return true
		}
	}
	return false
}

// localProgramKeys are the settings of the repository's own configuration that a
// pinned push or pull refuses to run beside: the ones that start a program or
// send the connection elsewhere. The pinned run cannot blank them without also
// blanking the user's own (a credential helper or ssh command from the global
// configuration), so a repository that sets one is refused instead.
// core.hooksPath is not here: the pinned run points it at nothing itself.
var localProgramKeys = regexp.MustCompile(`^(core\.(sshcommand|askpass|gitproxy|alternaterefscommand)|gpg\.(.+\.)?program|gpg\.ssh\.defaultkeycommand|credential\.(.+\.)?helper|remote\..+\.(uploadpack|receivepack|vcs|proxy)|http\.(.+\.)?(proxy|curloptresolve|sslcainfo|sslcapath|sslverify|proxyauthmethod|proxyssl[a-z]*|extraheader|cookiefile|sslcert[a-z]*|sslkey[a-z]*|followredirects)|ssh\.variant|url\..+\.(insteadof|pushinsteadof)|hook\..+\.command|lfs\.customtransfer\..+\.path)$`)

// rewriteKeys are the url.<base>.insteadOf and pushInsteadOf keys. An empty value
// is not skipped for these: it matches every URL.
var rewriteKeys = regexp.MustCompile(`^url\..+\.(insteadof|pushinsteadof)$`)

// strayRemote reports whether key is a remote.<name>.<variable> setting for a
// name that is not a remote the repository really has (one with a url), or that
// is spelt like a URL or a path. git push and pull read the URL they are given as
// a remote name first, so such a setting is a way to send them elsewhere.
func strayRemote(key string, real map[string]bool) bool {
	rest, ok := strings.CutPrefix(key, "remote.")
	if !ok {
		return false
	}
	i := strings.LastIndex(rest, ".")
	if i <= 0 {
		return false
	}
	name := rest[:i]
	return strings.ContainsAny(name, ":/\\") || !real[name]
}

// repoProgramSetting returns the first setting in the repository's own
// configuration (local, worktree, and what they include) that would run a
// program or reroute the connection for a push or pull, or "". A pull also
// refuses a filter driver, which the checkout it does would run.
func repoProgramSetting(dir string, pull bool) (string, error) {
	out, err := run(dir, "config", "--list", "--show-scope", "-z")
	if err != nil {
		return "", errConfigRead
	}
	parts := strings.Split(out, "\x00")
	real := map[string]bool{}
	for i := 0; i+1 < len(parts); i += 2 {
		key, _, _ := strings.Cut(parts[i+1], "\n")
		if name, ok := strings.CutPrefix(strings.ToLower(key), "remote."); ok && strings.HasSuffix(name, ".url") {
			real[strings.TrimSuffix(name, ".url")] = true
		}
	}
	for i := 0; i+1 < len(parts); i += 2 {
		if scope := parts[i]; scope != "local" && scope != "worktree" && scope != "command" {
			continue
		}
		key, val, _ := strings.Cut(parts[i+1], "\n")
		key = strings.ToLower(key)
		val = strings.TrimSpace(val)
		if val == "" && !rewriteKeys.MatchString(key) {
			continue
		}
		switch {
		case strayRemote(key, real):
			return key, nil
		case localProgramKeys.MatchString(key):
			return key, nil
		case key == "protocol.ext.allow" && !strings.EqualFold(val, "never"):
			return key, nil
		case key == "protocol.allow" && strings.EqualFold(val, "always"):
			return key, nil
		case pull && strings.HasPrefix(key, "filter.") && runsProgram(key, val):
			return key, nil
		case pull && strings.HasPrefix(key, "submodule.") && strings.HasSuffix(key, ".update") && strings.HasPrefix(val, "!"):
			return key, nil
		}
	}
	return "", nil
}

func checkProgramSetting(dir string, pull bool) error {
	key, err := repoProgramSetting(dir, pull)
	if err != nil {
		return err
	}
	if key != "" {
		return &ErrPinProgram{key}
	}
	return nil
}

// ResolvePin reads where a push (pull false) or a pull (pull true) of the
// checked-out branch would go. Any part that cannot be read is an error: a pin
// with a hole in it holds to nothing. A value git would read as an option, a
// pull that is not from one named remote and one branch, and a repository
// setting that runs a program are refused here, so they are refused at the ask
// and again at the run.
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
		if optionLike(remote, dest) {
			return RemotePin{}, ErrPinOption
		}
		if UpstreamOf(dir) != "" && dest != branch {
			return RemotePin{}, ErrPushName
		}
		url, err := run(dir, "remote", "get-url", "--push", "--", remote)
		if err != nil || strings.TrimSpace(url) == "" {
			return RemotePin{}, errors.New("the push URL could not be read")
		}
		p.Remote, p.Dest, p.PushURL = remote, dest, strings.TrimSpace(url)
		if optionLike(p.PushURL) {
			return RemotePin{}, ErrPinOption
		}
		if err := checkProgramSetting(dir, false); err != nil {
			return RemotePin{}, err
		}
		if !urlStable(dir, p.PushURL) {
			return RemotePin{}, ErrPinURL
		}
		if hook, err := prePushOrLFS(dir); err != nil || hook {
			if err != nil {
				return RemotePin{}, errConfigRead
			}
			return RemotePin{}, ErrPinHook
		}
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
	if len(remote) > 1 {
		return RemotePin{}, ErrPullRemote
	}
	if len(merge) > 1 {
		return RemotePin{}, ErrPullMerge
	}
	p.PullRemote, p.Merge = remote[0], merge[0]
	if optionLike(p.PullRemote, p.Merge) {
		return RemotePin{}, ErrPinOption
	}
	if err := checkProgramSetting(dir, true); err != nil {
		return RemotePin{}, err
	}
	// A branch that pulls from this repository itself ('.') has no remote to pin
	// a URL for, so it fails closed.
	if p.PullRemote == "." {
		return RemotePin{}, ErrPullRemote
	}
	// A branch whose remote is a URL has no remote.<name>.url to read.
	if urls, uerr := configValues(dir, "remote."+p.PullRemote+".url"); uerr != nil || len(urls) == 0 {
		return RemotePin{}, ErrPullRemote
	}
	url, err := run(dir, "remote", "get-url", "--", p.PullRemote)
	if err != nil || strings.TrimSpace(url) == "" {
		return RemotePin{}, errors.New("the fetch URL could not be read")
	}
	p.FetchURL = strings.TrimSpace(url)
	if optionLike(p.FetchURL) {
		return RemotePin{}, ErrPinOption
	}
	if !urlStable(dir, p.FetchURL) {
		return RemotePin{}, ErrPinURL
	}
	return p, nil
}

// PushExpect is what the branch must be at when a pinned push starts.
type PushExpect struct {
	// Head is the commit the branch was at when the push was approved.
	Head string
	// AfterCommit is set when the request made a commit before pushing: the
	// branch must then be at exactly Sha.
	AfterCommit bool
	// Sha is the commit the commit step made, as NewCommit returned it.
	Sha string
}

// headMatches reports whether HEAD is what e expects, and returns it. After a
// commit it is exact: HEAD must be the commit that was made, so an amend or a
// merge written between the commit and the push is refused.
func headMatches(dir string, e PushExpect) (string, bool) {
	head := HeadSHA(dir)
	if head == "" {
		return "", false
	}
	if !e.AfterCommit {
		return head, head == e.Head
	}
	return head, e.Sha != "" && head == e.Sha
}

// NewCommit is read straight after the commit step. It returns the sha the
// branch is at and checks that it is a new commit with one parent, the commit
// approved (before), or a root commit when before is "" (a branch with no
// commits yet). Anything else, a merge or a commit on top of another, is
// ErrPinMoved. The commit and this read are two steps, so a commit written in
// the gap is the one returned; what the push later holds to is this sha.
func NewCommit(dir, before string) (string, error) {
	head := HeadSHA(dir)
	if head == "" || head == before {
		return "", ErrPinMoved
	}
	out, err := run(dir, "rev-list", "--parents", "-n", "1", head)
	if err != nil {
		return "", ErrPinMoved
	}
	f := strings.Fields(out)
	if len(f) < 1 || f[0] != head {
		return "", ErrPinMoved
	}
	parents := f[1:]
	switch {
	case before == "" && len(parents) == 0:
	case before != "" && len(parents) == 1 && parents[0] == before:
	default:
		return "", ErrPinMoved
	}
	return head, nil
}

// pushOwnConfig is what a pinned push is run with so that git's settings cannot
// add anything to the one ref pushed: no push.default (the refspec is explicit),
// no tags, no push options, submodules only checked.
var pushOwnConfig = []string{
	"-c", "push.default=nothing",
	"-c", "push.followTags=false",
	"-c", "push.pushOption=",
	"-c", "push.recurseSubmodules=check",
	"-c", "push.gpgSign=false",
}

// runOwnConfig is added to a pinned push or pull so that the repository's hooks
// (pre-push, post-merge and the rest) do not run, and the ext:: transport, which
// runs a command, is off.
var runOwnConfig = append(append([]string{}, noHooks...), "-c", "protocol.ext.allow=never")

// programRefusal passes on the one resolve error that says why, and reads any
// other as the pin having moved.
func programRefusal(err error) error {
	var pe *ErrPinProgram
	if errors.As(err, &pe) || errors.Is(err, ErrPinHook) || errors.Is(err, ErrPinURL) || errors.Is(err, ErrPullRemote) {
		return err
	}
	return ErrPinMoved
}

// pinRemoteName is a remote name made for one run. git reads a URL argument as a
// remote name first, so the pinned URL is given as the url of a remote nothing in
// the repository can have set anything for.
func pinRemoteName() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "flockdeck-pin-" + hex.EncodeToString(b)
}

// PushPinned sends the commit the branch is at to the pinned destination and to
// nothing else: git push -- <pinned URL> <sha>:refs/heads/<dest>, with the
// settings that would widen a push set aside and the repository's hooks off.
// The pin and the head are looked at again first, immediately before git
// starts; what is pushed is the sha read there, and the URL is the pinned one,
// so a remote or refspec setting written since the approval is not consulted.
// The URL is also checked to be one git does not rewrite again (git applies
// url.<base>.insteadOf and pushInsteadOf to a URL argument as well), and
// repository rewrite rules are refused outright. Programs set in the user's own
// configuration still run.
func PushPinned(dir string, pin RemotePin, expect PushExpect) (string, error) {
	now, err := ResolvePin(dir, false)
	if err != nil {
		return "", programRefusal(err)
	}
	if now != pin {
		return "", ErrPinMoved
	}
	head, ok := headMatches(dir, expect)
	if !ok {
		return "", ErrPinMoved
	}
	name := pinRemoteName()
	args := append(append(append([]string{}, runOwnConfig...), pushOwnConfig...), "-c", "remote."+name+".url="+pin.PushURL, "push", "--", name, head+":refs/heads/"+pin.Dest)
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
	// A push by URL does not move the tracking ref, so it is set from what was
	// sent: created, with the upstream, the first time; otherwise moved when it
	// exists, so ahead and behind count from what the remote now has.
	tracking := "refs/remotes/" + pin.Remote + "/" + pin.Dest
	_, missing := run(dir, "rev-parse", "--verify", "-q", tracking)
	if UpstreamOf(dir) == "" {
		if _, e := run(dir, "update-ref", tracking, head); e == nil {
			_, _ = run(dir, "branch", "--set-upstream-to="+pin.Remote+"/"+pin.Dest, pin.Branch)
		}
	} else if missing == nil {
		_, _ = run(dir, "update-ref", tracking, head)
	}
	return out, nil
}

// urlStable reports whether git, given url as a repository argument, uses it
// unchanged. The URL a pin holds has had url.<base>.insteadOf applied once by git
// remote get-url; a second pass that changes it means the connection goes
// somewhere the pin does not show.
func urlStable(dir, url string) bool {
	out, err := run(dir, "ls-remote", "--get-url", "--", url)
	return err == nil && strings.TrimSpace(out) == url
}

// prePushOrLFS reports whether the repository has a pre-push hook (wherever
// core.hooksPath puts it), a core.hooksPath of its own, an LFS filter, or an
// attributes file that names filter=lfs. A pinned push runs no hooks, so for
// these the owner's check would be skipped and LFS content not uploaded.
func prePushOrLFS(dir string) (bool, error) {
	out, err := run(dir, "config", "--list", "--show-scope", "-z")
	if err != nil {
		return false, errConfigRead
	}
	// git here is run with core.hooksPath pointed at nothing (command scope), so
	// that entry is not the repository's and is skipped.
	hooksPath := ""
	parts := strings.Split(out, "\x00")
	for i := 0; i+1 < len(parts); i += 2 {
		scope := parts[i]
		key, val, _ := strings.Cut(parts[i+1], "\n")
		key, val = strings.ToLower(key), strings.TrimSpace(val)
		local := scope == "local" || scope == "worktree" || scope == "command"
		if key == "core.hookspath" && val != "" && !(scope == "command" && val == os.DevNull) {
			if local {
				return true, nil
			}
			hooksPath = val
		}
		if local && strings.HasPrefix(key, "filter.lfs.") {
			return true, nil
		}
	}
	hooks := filepath.Join(hooksPath, "pre-push")
	if hooksPath == "" {
		common, err := run(dir, "rev-parse", "--git-common-dir")
		if err != nil {
			return false, errConfigRead
		}
		common = strings.TrimSpace(common)
		if !filepath.IsAbs(common) {
			common = filepath.Join(dir, common)
		}
		hooks = filepath.Join(common, "hooks", "pre-push")
	} else if rest, ok := strings.CutPrefix(hooksPath, "~/"); ok {
		if home, herr := os.UserHomeDir(); herr == nil {
			hooks = filepath.Join(home, rest, "pre-push")
		}
	}
	// The disk is looked at through the scan layer, which refuses a network
	// share. A path it will not look at is treated as a hook that may be there.
	sfs := newScanFS(context.Background())
	if _, err := sfs.Stat(hooks); err == nil || blocked(err) {
		return true, nil
	}
	if path, err := run(dir, "rev-parse", "--git-path", "info/attributes"); err == nil && attributesNameLFS(sfs, dir, strings.TrimSpace(path)) {
		return true, nil
	}
	if top, err := run(dir, "rev-parse", "--show-toplevel"); err == nil && attributesNameLFS(sfs, strings.TrimSpace(top), ".gitattributes") {
		return true, nil
	}
	return false, nil
}

func attributesNameLFS(sfs *scanFS, base, path string) bool {
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	b, err := sfs.ReadFile(path)
	return err == nil && strings.Contains(strings.ToLower(string(b)), "filter=lfs")
}

// PullPinned fast-forwards the branch from the pinned URL and ref, and from
// nothing else: git pull --ff-only -- <pinned fetch URL> <pinned ref>, with
// pull.rebase set aside and the repository's hooks off. The pin and the head are
// looked at again immediately before git starts.
func PullPinned(dir string, pin RemotePin, head string) (string, error) {
	now, err := ResolvePin(dir, true)
	if err != nil {
		return "", programRefusal(err)
	}
	if now != pin || HeadSHA(dir) != head {
		return "", ErrPinMoved
	}
	name := pinRemoteName()
	args := append(append([]string{}, runOwnConfig...), "-c", "pull.rebase=false",
		"-c", "fetch.recurseSubmodules=no", "-c", "submodule.recurse=false",
		"-c", "remote."+name+".url="+pin.FetchURL, "pull", "--ff-only")
	args = append(args, "--", name, pin.Merge)
	out, err := pullRun(dir, args)
	if err == nil {
		// A pull by URL leaves refs/remotes/<remote>/<branch> where it was, so
		// ahead and behind read wrong; it is set to what was fetched.
		if b, ok := strings.CutPrefix(pin.Merge, "refs/heads/"); ok && pin.PullRemote != "." {
			tracking := "refs/remotes/" + pin.PullRemote + "/" + b
			if _, e := run(dir, "rev-parse", "--verify", "-q", tracking); e == nil {
				if sha, e := run(dir, "rev-parse", "--verify", "-q", "FETCH_HEAD"); e == nil && strings.TrimSpace(sha) != "" {
					_, _ = run(dir, "update-ref", tracking, strings.TrimSpace(sha))
				}
			}
		}
	}
	return out, err
}

// String is the pin as a person reads it.
func (p RemotePin) String() string {
	if p.Dest != "" {
		return fmt.Sprintf("%s %s (branch %s)", p.Remote, p.PushURL, p.Dest)
	}
	return fmt.Sprintf("%s %s (%s)", p.PullRemote, p.FetchURL, p.Merge)
}
