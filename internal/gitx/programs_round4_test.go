package gitx

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// blackhole is an address nothing answers (TEST-NET-1, reserved for
// documentation). Anything that opens a share here waits for a connection that
// never comes; the checks below are that it is never opened.
const blackhole = `\\192.0.2.1\x`

func quickly(t *testing.T, what string, f func()) {
	t.Helper()
	start := time.Now()
	f()
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("%s took %s: something was opened on the share", what, d)
	}
}

func hasNetworkItem(rep Report) bool {
	for _, p := range rep.Items {
		if p.Kind == "unscannable" && strings.Contains(p.Value, "network share") {
			return true
		}
	}
	return false
}

func windowsOnly(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("only Windows connects to a share to look at it")
	}
}

// linkToShare makes a directory junction (which needs no privilege) and has the
// link-reading step report that it leads to a share. Making a real link to a
// share needs a privilege and itself connects to the share.
func linkToShare(t *testing.T, link string) {
	t.Helper()
	target := t.TempDir()
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Skipf("a junction cannot be made here: %v: %s", err, out)
	}
	was := readLink
	t.Cleanup(func() { readLink = was })
	readLink = func(p string) (string, error) {
		// By name: the scan spells the path as the file system does (long names where
		// the temporary folder has short ones), and only a link is ever asked.
		if strings.EqualFold(filepath.Base(p), filepath.Base(link)) {
			return blackhole + `\target`, nil
		}
		return was(p)
	}
}

// noEval fails a test if a share, or a path that leads to one, is resolved: that
// is the call that connects to it.
func noEval(t *testing.T) {
	t.Helper()
	was := evalSymlinks
	t.Cleanup(func() { evalSymlinks = was })
	evalSymlinks = func(p string) (string, error) {
		if isNetworkPath(p) || tfs().guard(p) != nil {
			t.Errorf("EvalSymlinks was called on %s, which is or leads to a share", p)
		}
		return was(p)
	}
}

func TestAHooksPathOnAShareIsNeverOpened(t *testing.T) {
	windowsOnly(t)
	noEval(t)
	repo := newRepo(t)
	gitRun(t, repo, "config", "core.hooksPath", blackhole+`\hooks`)
	var rep Report
	quickly(t, "a scan with core.hooksPath on a share", func() { rep = scan(t, repo) })
	if !hasNetworkItem(rep) {
		t.Errorf("no item says the share was not touched: %+v", rep.Items)
	}
}

func TestAHooksPathThroughALinkToAShareIsNeverOpened(t *testing.T) {
	windowsOnly(t)
	noEval(t)
	repo := newRepo(t)
	link := filepath.Join(t.TempDir(), "hooks")
	linkToShare(t, link)
	gitRun(t, repo, "config", "core.hooksPath", filepath.ToSlash(link))
	var rep Report
	quickly(t, "a scan with a hooks path that links to a share", func() { rep = scan(t, repo) })
	if !hasNetworkItem(rep) {
		t.Errorf("no item says the share was not touched: %+v", rep.Items)
	}
}

func TestAHookThatLinksToAShareIsNeverOpened(t *testing.T) {
	windowsOnly(t)
	noEval(t)
	repo := newRepo(t)
	hooks := filepath.Join(repo, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	linkToShare(t, filepath.Join(hooks, "pre-commit"))
	var rep Report
	quickly(t, "a scan with a hook that links to a share", func() { rep = scan(t, repo) })
	if !hasNetworkItem(rep) {
		t.Errorf("no item says the share was not touched: %+v", rep.Items)
	}
}

func TestASubmodulesGitFileOnAShareIsNeverOpened(t *testing.T) {
	windowsOnly(t)
	noEval(t)
	outer := newRepo(t)
	mod := addSubmodule(t, outer)
	if err := os.WriteFile(filepath.Join(mod, ".git"), []byte("gitdir: //192.0.2.1/x/sub.git\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var rep Report
	quickly(t, "a scan with a submodule whose git directory is on a share", func() { rep = scan(t, outer) })
	if !hasNetworkItem(rep) {
		t.Errorf("no item says the share was not touched: %+v", rep.Items)
	}
}

func TestARemotesHooksPathOnAShareIsNeverOpened(t *testing.T) {
	windowsOnly(t)
	noEval(t)
	repo := newRepo(t)
	bare := filepath.Join(t.TempDir(), "r.git")
	gitRun(t, repo, "init", "--bare", "-q", bare)
	gitRun(t, bare, "config", "core.hooksPath", blackhole+`\hooks`)
	gitRun(t, repo, "remote", "add", "origin", filepath.ToSlash(bare))
	var rep Report
	quickly(t, "a scan whose remote has its hooks on a share", func() { rep = scan(t, repo) })
	if !hasNetworkItem(rep) {
		t.Errorf("no item says the share was not touched: %+v", rep.Items)
	}
}

func TestCanonicalLeavesAShareAloneAndFollowsNoLinkToOne(t *testing.T) {
	windowsOnly(t)
	noEval(t)
	if _, err := tfs().Canonical(blackhole + `b`); !errors.Is(err, errNetworkPath) {
		t.Errorf("fsCanonical on a share = %v, want errNetworkPath", err)
	}
	link := filepath.Join(t.TempDir(), "l")
	linkToShare(t, link)
	if _, err := tfs().Canonical(filepath.Join(link, "deeper")); !errors.Is(err, errNetworkPath) {
		t.Errorf("fsCanonical through a link to a share = %v, want errNetworkPath", err)
	}
}

func TestALocalRemotesOwnConfigIsReadToo(t *testing.T) {
	repo := newRepo(t)
	remote := newRepo(t)
	// A push that updates the remote's checked-out branch runs its fsmonitor
	// command and its filters.
	gitRun(t, remote, "config", "receive.denyCurrentBranch", "updateInstead")
	gitRun(t, remote, "config", "core.fsmonitor", "/tmp/fsm")
	gitRun(t, remote, "config", "filter.evil.smudge", "/tmp/s")
	gitRun(t, repo, "remote", "add", "origin", filepath.ToSlash(remote))
	rep := scan(t, repo)
	f, ok := find(rep, "setting", "core.fsmonitor")
	if !ok || !strings.HasSuffix(slash(f.Where), "/.git/config") || !strings.Contains(slash(f.Where), slash(filepath.Base(remote))) {
		t.Fatalf("the remote's fsmonitor = %+v ok=%v, want it listed from the remote's own config", f, ok)
	}
	if _, ok := find(rep, "setting", "filter.evil.smudge"); !ok {
		t.Error("the remote's filter was not listed")
	}
	// An edit to it changes the report.
	gitRun(t, remote, "config", "core.fsmonitor", "/tmp/other")
	f2, _ := find(scan(t, repo), "setting", "core.fsmonitor")
	if f.ID() == f2.ID() {
		t.Error("an edit to the remote's configuration did not change it")
	}
	// The repository's own fsmonitor is blanked by Flockdeck and is not listed.
	gitRun(t, repo, "config", "core.fsmonitor", "/tmp/own")
	own := 0
	for _, p := range scan(t, repo).Items {
		if p.Name == "core.fsmonitor" && !strings.Contains(slash(p.Where), slash(filepath.Base(remote))) {
			own++
		}
	}
	if own != 0 {
		t.Error("the repository's own core.fsmonitor was listed, though Flockdeck blanks it")
	}
}

func TestFsmonitorCommand(t *testing.T) {
	for v, want := range map[string]bool{"/tmp/f": true, "true": false, "false": false, "": false, "ON": false, "echo hi": true} {
		if got := fsmonitorCommand("core.fsmonitor", v); got != want {
			t.Errorf("fsmonitorCommand(%q) = %v, want %v", v, got, want)
		}
	}
	if fsmonitorCommand("core.pager", "/tmp/x") {
		t.Error("another key counted")
	}
}

func TestTheLongestMatchingRewriteWins(t *testing.T) {
	rules := []rewriteRule{
		{prefix: "gh:", base: "/short/"},
		{prefix: "gh:org/", base: "https://long.example/"},
		{prefix: "gh:org/sub/", base: "/longest/"},
	}
	cases := map[string]string{
		"gh:thing":         "/short/thing",
		"gh:org/repo":      "https://long.example/repo",
		"gh:org/sub/repo":  "/longest/repo",
		"other:org/repo":   "other:org/repo",
		"gh:orgs/repo.git": "/short/orgs/repo.git",
	}
	for in, want := range cases {
		if got := apply(in, rules); got != want {
			t.Errorf("apply(%q) = %q, want %q", in, got, want)
		}
	}
	// Order in the file does not matter.
	rev := []rewriteRule{rules[2], rules[1], rules[0]}
	if got := apply("gh:org/repo", rev); got != "https://long.example/repo" {
		t.Errorf("reversed rules: apply = %q", got)
	}
}

func TestTheLongerRewriteDecidesWhetherARemoteIsLocal(t *testing.T) {
	repo := newRepo(t)
	parent := t.TempDir()
	bare := filepath.Join(parent, "repo")
	gitRun(t, repo, "init", "--bare", "-q", bare)
	writeHook(t, filepath.Join(bare, "hooks"), "update", "#!/bin/sh\n")
	gitRun(t, repo, "remote", "add", "origin", "gh:org/repo")
	// The short rule leads to a folder, the longer one to the network.
	gitRun(t, repo, "config", "url."+filepath.ToSlash(parent)+"/.pushInsteadOf", "gh:org/")
	gitRun(t, repo, "config", "url.https://example.com/.pushInsteadOf", "gh:org/rep")
	if _, ok := find(scan(t, repo), "hook", "update"); ok {
		t.Error("the shorter rule was applied though a longer one matches")
	}
}
