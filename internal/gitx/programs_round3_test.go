package gitx

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fileURL spells a folder as a file:// URL, with every "-" written %2D as git
// reads it back.
func fileURL(dir string, host string) string {
	p := filepath.ToSlash(dir)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return "file://" + host + strings.ReplaceAll(p, "-", "%2D")
}

func TestFileURLPath(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"file:///srv/a%2Db", filepath.FromSlash("/srv/a-b"), true},
		{"file://localhost/srv/a", filepath.FromSlash("/srv/a"), true},
		{"FILE://LOCALHOST/srv/a", filepath.FromSlash("/srv/a"), true},
		{"file:///C:/x/y", filepath.FromSlash("C:/x/y"), true},
		{"file://localhost/C:/x", filepath.FromSlash("C:/x"), true},
		{"file://other-host/srv/a", "", false},
		{"file:///srv/%zz", "", false},
		{"/plain/%2D/path", filepath.FromSlash("/plain/%2D/path"), true},
	}
	for _, c := range cases {
		got, ok := fileURLPath(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("fileURLPath(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func bareWithHook(t *testing.T, repo, dir string) string {
	t.Helper()
	bare := filepath.Join(dir, "r-x.git")
	gitRun(t, repo, "init", "--bare", "-q", bare)
	writeHook(t, filepath.Join(bare, "hooks"), "pre-receive", "#!/bin/sh\n")
	return bare
}

func TestAPercentEncodedOrLocalhostFileUrlDoesNotHideTheRemotesHooks(t *testing.T) {
	repo := newRepo(t)
	bare := bareWithHook(t, repo, t.TempDir())
	for _, host := range []string{"", "localhost"} {
		gitRun(t, repo, "config", "remote.origin.url", fileURL(bare, host))
		if _, ok := find(scan(t, repo), "hook", "pre-receive"); !ok {
			t.Errorf("host %q: the remote's hook was not found through %s", host, fileURL(bare, host))
		}
	}
}

func TestARemoteReachedByARewriteIsLookedAtToo(t *testing.T) {
	repo := newRepo(t)
	parent := t.TempDir()
	bare := filepath.Join(parent, "repo")
	gitRun(t, repo, "init", "--bare", "-q", bare)
	writeHook(t, filepath.Join(bare, "hooks"), "update", "#!/bin/sh\n")
	// An ordinary-looking address that a rewrite sends to a folder.
	gitRun(t, repo, "remote", "add", "origin", "gh:repo")
	gitRun(t, repo, "config", "url."+filepath.ToSlash(parent)+"/.pushInsteadOf", "gh:")
	rep := scan(t, repo)
	if _, ok := find(rep, "hook", "update"); !ok {
		t.Errorf("the hook of the folder a pushInsteadOf rewrite leads to was not found: %+v", rep.Items)
	}
	if _, ok := find(rep, "setting", "remote.origin.url"); !ok {
		t.Error("the remote that is rewritten to a folder was not listed")
	}
}

func TestARemoteThatIsALinkedWorktreeRunsTheMainRepositorysHooks(t *testing.T) {
	repo := newRepo(t)
	remote := newRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	gitRun(t, remote, "worktree", "add", "-q", "-b", "other", wt)
	writeHook(t, filepath.Join(remote, ".git", "hooks"), "pre-receive", "#!/bin/sh\n")
	gitRun(t, repo, "remote", "add", "origin", filepath.ToSlash(wt))
	h, ok := find(scan(t, repo), "hook", "pre-receive")
	if !ok || !strings.HasSuffix(slash(h.Where), "/.git/hooks") {
		t.Fatalf("hook = %+v ok=%v, want the main repository's", h, ok)
	}
}

func TestALocalRemoteThatCannotBeWorkedOutIsAnItemAndOneThatIsNotThereIsNot(t *testing.T) {
	repo := newRepo(t)
	empty := t.TempDir()
	gitRun(t, repo, "remote", "add", "origin", filepath.ToSlash(empty))
	if _, ok := find(scan(t, repo), "unscannable", ""); !ok {
		t.Error("a folder that is there and is not a repository Flockdeck can read was passed over")
	}
	gitRun(t, repo, "remote", "set-url", "origin", filepath.ToSlash(filepath.Join(empty, "nowhere")))
	rep := scan(t, repo)
	if _, ok := find(rep, "unscannable", ""); ok || len(rep.Items) != 1 {
		t.Errorf("a folder that is not there = %+v, want only the setting", rep.Items)
	}
	gitRun(t, repo, "remote", "set-url", "origin", "file://some-other-host/srv/x.git")
	if _, ok := find(scan(t, repo), "unscannable", ""); !ok {
		t.Error("a file URL for another host was passed over")
	}
}

func TestANetworkSharePathIsNeverTouched(t *testing.T) {
	cases := map[string]bool{
		`\\server\share\r.git`: true,
		`//server/share/r.git`: true,
		`\\?\UNC\server\s`:     true,
		`\\?\C:\repos\r.git`:   false,
		`C:\repos\r.git`:       false,
		`/srv/git/r.git`:       false,
	}
	for p, want := range cases {
		got := isNetworkPath(p)
		if runtime.GOOS != "windows" {
			want = false
		}
		if got != want {
			t.Errorf("isNetworkPath(%q) = %v, want %v", p, got, want)
		}
	}
	if runtime.GOOS != "windows" {
		return
	}
	repo := newRepo(t)
	gitRun(t, repo, "config", "remote.origin.url", `\\flockdeck-test-no-such-host\share\r.git`)
	if u, ok := find(scan(t, repo), "unscannable", ""); !ok || !strings.Contains(u.Value, "network share") {
		t.Errorf("a share = %+v ok=%v, want an item that says it was not touched", u, ok)
	}
	gitRun(t, repo, "config", "core.hooksPath", `\\flockdeck-test-no-such-host\share\hooks`)
	n := 0
	for _, p := range scan(t, repo).Items {
		if p.Kind == "unscannable" && strings.Contains(p.Value, "network share") {
			n++
		}
	}
	if n != 2 {
		t.Errorf("%d items about shares, want 2 (the remote and the hooks path)", n)
	}
}

func TestAGitThatPrintsTooMuchIsAnItemNotAHang(t *testing.T) {
	if !Available() {
		t.Skip("git is not installed")
	}
	var b cappedBuf
	if _, err := b.Write(make([]byte, maxScanOutput)); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write([]byte("x")); err == nil || !b.over {
		t.Fatal("output past the cap was kept")
	}

	dir := t.TempDir()
	cfg := filepath.Join(dir, "big")
	var buf bytes.Buffer
	buf.WriteString("[a]\n")
	for i := 0; buf.Len() < maxScanOutput+1<<20; i++ {
		fmt.Fprintf(&buf, "\tk%d = %s\n", i, strings.Repeat("v", 60))
	}
	if err := os.WriteFile(cfg, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &scanner{ctx: context.Background(), budget: maxHashBudget}
	if _, err := s.git(dir, "config", "--file", cfg, "--list"); err != errTooMuchOutput {
		t.Errorf("err = %v, want it to say git printed too much", err)
	}
}

func TestBackgroundGitRunsNoHooksAndCommitsStillDo(t *testing.T) {
	repo := newRepo(t)
	marker := filepath.Join(repo, "hook-ran.txt")
	body := "#!/bin/sh\necho ran >> \"" + filepath.ToSlash(marker) + "\"\n"
	hooks := filepath.Join(repo, ".git", "hooks")
	for _, name := range []string{"post-index-change", "reference-transaction", "post-commit"} {
		writeHook(t, hooks, name, body)
	}
	ran := func() bool { _, err := os.Stat(marker); return err == nil }

	// Without Flockdeck's setting, this git runs them, or there is nothing to prove.
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	plain := exec.Command("git", "add", "--all")
	plain.Dir = repo
	if out, err := plain.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}
	if !ran() {
		t.Skip("this git ran no post-index-change hook, so there is nothing to stop")
	}
	os.Remove(marker)
	gitRun(t, repo, "reset", "-q")
	os.Remove(marker)

	if _, err := run(repo, "add", "--all"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(repo, "update-ref", "refs/heads/probe", "HEAD"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(repo, "update-ref", "-d", "refs/heads/probe"); err != nil {
		t.Fatal(err)
	}
	if err := runUpdateIndex(repo); err != nil {
		t.Fatal(err)
	}
	if ran() {
		t.Error("a background git command ran a hook the repository holds")
	}

	// A commit is one of the things the hooks are for.
	if err := CommitAll(repo, "with hooks"); err != nil {
		t.Fatal(err)
	}
	if !ran() {
		t.Error("a commit no longer runs the repository's hooks")
	}
}

func TestConfigForBlanksHooksOnlyWhereNoneAreWanted(t *testing.T) {
	has := func(args ...string) bool {
		for _, a := range configFor(args) {
			if strings.HasPrefix(a, "core.hooksPath=") {
				return true
			}
		}
		return false
	}
	for _, c := range []string{"status", "diff", "update-index", "add", "update-ref", "rev-parse", "config"} {
		if !has(c) {
			t.Errorf("git %s keeps the repository's hooks", c)
		}
	}
	for _, c := range []string{"commit", "push", "pull", "fetch", "worktree"} {
		if has(c) {
			t.Errorf("git %s has its hooks blanked", c)
		}
	}
}
