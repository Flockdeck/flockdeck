package gitx

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// git push -- <url> reads <url> as a remote name first. A repository that names a
// remote after the pinned URL and gives it a pushurl sends the push there, and
// urlStable does not see it (it prints the first url). The setting is refused,
// and the push goes to a remote name that cannot be set in advance.
func TestAPushIsNotRedirectedByARemoteNamedAfterThePinnedURL(t *testing.T) {
	repo, bare := pinnedRepo(t)
	evil := t.TempDir()
	gitRun(t, evil, "init", "-q", "--bare")
	pin, err := ResolvePin(repo, false)
	if err != nil {
		t.Fatal(err)
	}
	before := HeadSHA(repo)
	commitFile(t, repo, "a.txt")
	made, err := NewCommit(repo, before)
	if err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "config", "remote."+pin.PushURL+".pushurl", evil)

	var pe *ErrPinProgram
	if _, err := ResolvePin(repo, false); !errors.As(err, &pe) {
		t.Errorf("ResolvePin with a remote named after the URL: %v", err)
	}
	if _, err := PushPinned(repo, pin, PushExpect{Head: before, AfterCommit: true, Sha: made}); err == nil {
		t.Error("the push went ahead")
	}
	if out := gitRun(t, evil, "for-each-ref"); strings.TrimSpace(out) != "" {
		t.Errorf("the other repository received %q", out)
	}
	if got := strings.TrimSpace(gitRun(t, bare, "rev-parse", "main")); got == made {
		t.Errorf("the push was not refused (it should be) but reached the pinned URL")
	}
}

// An empty url.<base>.insteadOf matches every URL.
func TestResolvePinRefusesAnEmptyRewriteRule(t *testing.T) {
	for _, key := range []string{"insteadOf", "pushInsteadOf"} {
		repo, _ := pinnedRepo(t)
		gitRun(t, repo, "config", "url.https://x.example/."+key, "")
		var pe *ErrPinProgram
		if _, err := ResolvePin(repo, false); !errors.As(err, &pe) {
			t.Errorf("%s push: %v", key, err)
		}
		if _, err := ResolvePin(repo, true); !errors.As(err, &pe) {
			t.Errorf("%s pull: %v", key, err)
		}
	}
}

func TestResolvePinRefusesMoreProgramAndTransportSettings(t *testing.T) {
	for _, kv := range [][2]string{
		{"gpg.program", "x"},
		{"gpg.ssh.program", "x"},
		{"gpg.x509.program", "x"},
		{"gpg.ssh.defaultKeyCommand", "x"},
		{"http.extraHeader", "X: y"},
		{"http.cookieFile", "/tmp/c"},
		{"http.sslCert", "/tmp/c"},
		{"http.sslKey", "/tmp/k"},
		{"http.proxySSLCert", "/tmp/c"},
		{"http.followRedirects", "true"},
	} {
		repo, _ := pinnedRepo(t)
		gitRun(t, repo, "config", kv[0], kv[1])
		var pe *ErrPinProgram
		if _, err := ResolvePin(repo, false); !errors.As(err, &pe) {
			t.Errorf("%s push: %v", kv[0], err)
		}
		if _, err := ResolvePin(repo, true); !errors.As(err, &pe) {
			t.Errorf("%s pull: %v", kv[0], err)
		}
	}
}

// submodule.recurse makes a pull fetch and update the submodules, reading the
// configuration in each submodule's own git directory, which is never checked.
// A pinned pull leaves them alone.
func TestAPinnedPullDoesNotRecurseIntoSubmodules(t *testing.T) {
	bare := func() string {
		dir := t.TempDir()
		cmd := exec.Command("git", "init", "-q", "--bare", "--initial-branch=main")
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("bare init failed: %v: %s", err, out)
		}
		return dir
	}
	subOrigin, outerOrigin := bare(), bare()
	inner := newRepo(t)
	gitRun(t, inner, "remote", "add", "origin", subOrigin)
	gitRun(t, inner, "push", "-q", "-u", "origin", "main")

	mine := newRepo(t)
	gitRun(t, mine, "config", "protocol.file.allow", "always")
	gitRun(t, mine, "remote", "add", "origin", outerOrigin)
	gitRun(t, mine, "-c", "protocol.file.allow=always", "submodule", "add", "-q", subOrigin, "sub")
	gitRun(t, mine, "commit", "-qm", "add sub")
	gitRun(t, mine, "push", "-q", "-u", "origin", "main")

	theirs := t.TempDir()
	cmd := exec.Command("git", "-c", "protocol.file.allow=always", "clone", "-q", "--recurse-submodules", outerOrigin, theirs)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone: %v: %s", err, out)
	}
	tsub := filepath.Join(theirs, "sub")
	gitRun(t, tsub, "-c", "user.email=o@x", "-c", "user.name=o", "commit", "-q", "--allow-empty", "-m", "their bump")
	gitRun(t, tsub, "push", "-q", "origin", "HEAD:main")
	gitRun(t, theirs, "-c", "user.email=o@x", "-c", "user.name=o", "commit", "-qam", "bump sub")
	gitRun(t, theirs, "push", "-q")

	// The file transport is for a submodule's own fetch too, as a user who
	// allows it would have it; only the recursion is what is tested.
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "protocol.file.allow")
	t.Setenv("GIT_CONFIG_VALUE_0", "always")
	gitRun(t, mine, "config", "submodule.recurse", "true")
	gitRun(t, mine, "config", "fetch.recurseSubmodules", "on-demand")
	pin, err := ResolvePin(mine, true)
	if err != nil {
		t.Fatal(err)
	}
	subBefore := strings.TrimSpace(gitRun(t, filepath.Join(mine, "sub"), "rev-parse", "HEAD"))
	if _, err := PullPinned(mine, pin, HeadSHA(mine)); err != nil {
		t.Fatalf("pull: %v", err)
	}
	if got := strings.TrimSpace(gitRun(t, filepath.Join(mine, "sub"), "rev-parse", "HEAD")); got != subBefore {
		t.Errorf("the pull updated the submodule: %s, was %s", got, subBefore)
	}
}
