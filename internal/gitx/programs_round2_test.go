package gitx

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestAScanThatTimesOutIsAnItemNotAnError(t *testing.T) {
	repo := newRepo(t)
	was := scanTimeout
	t.Cleanup(func() { scanTimeout = was })
	scanTimeout = time.Nanosecond
	rep, err := ScanPrograms(repo)
	if err != nil {
		t.Fatalf("a scan that ran out of time was an error, which callers let through: %v", err)
	}
	u, ok := find(rep, "unscannable", "")
	if !ok || !strings.Contains(u.Value, "did not answer") {
		t.Fatalf("items = %+v, want an unscannable one saying git did not answer", rep.Items)
	}
	if v := Judge(rep, nil); !v.Warn {
		t.Error("a scan that timed out was let through on first sight")
	}
	// Nothing accepted before covers it.
	if v := Judge(rep, &Known{IDs: rep.IDs()}); !v.Warn {
		t.Error("a scan that timed out was let through because its own ID had been accepted")
	}
	for _, id := range rep.AcceptableIDs() {
		if id == u.ID() {
			t.Error("an unscannable item can be remembered")
		}
	}
}

func TestOnlyNoRepositoryOrNoGitIsAnError(t *testing.T) {
	if !Available() {
		t.Skip("git is not installed")
	}
	if _, err := ScanPrograms(t.TempDir()); !errors.Is(err, ErrNotRepository) {
		t.Errorf("err = %v, want ErrNotRepository", err)
	}
	if _, err := ScanPrograms(""); !errors.Is(err, ErrNotRepository) {
		t.Errorf("err = %v, want ErrNotRepository", err)
	}
}

func TestAConfigFileFarPastTheSaneSizeIsAnItem(t *testing.T) {
	repo := newRepo(t)
	cfg := filepath.Join(repo, ".git", "config")
	f, err := os.OpenFile(cfg, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("# padding\n", 1000)
	for i := 0; i < (maxConfigBytes/len(line))+2; i++ {
		if _, err := f.WriteString(line); err != nil {
			t.Fatal(err)
		}
	}
	f.WriteString("[core]\n\tsshCommand = ssh -i planted\n")
	f.Close()
	rep := scan(t, repo)
	if l, ok := find(rep, "limit", ""); !ok || !strings.HasSuffix(slash(l.Where), "/.git/config") {
		t.Errorf("a configuration file past the size limit left no trace: %+v", rep.Items)
	}
	if v := Judge(rep, nil); !v.Warn {
		t.Error("not asked about a padded configuration file")
	}
}

func TestAMergeDriverIsReported(t *testing.T) {
	repo := newRepo(t)
	gitRun(t, repo, "config", "merge.evil.driver", "/tmp/m %O %A %B")
	gitRun(t, repo, "config", "merge.evil.recursive", "evil")
	rep := scan(t, repo)
	if _, ok := find(rep, "setting", "merge.evil.driver"); !ok {
		t.Error("a merge driver was not reported")
	}
	if _, ok := find(rep, "setting", "merge.evil.recursive"); ok {
		t.Error("merge.<name>.recursive only names another driver, and was reported as a program")
	}
	for _, k := range []string{"merge.tool", "mergetool.x.cmd"} {
		if runsProgram(k, "x") {
			t.Errorf("%s is run only by git mergetool, which Flockdeck never runs", k)
		}
	}
}

func TestAHookDotExeIsFoundOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only git for Windows starts <hook>.exe")
	}
	repo := newRepo(t)
	writeHook(t, filepath.Join(repo, ".git", "hooks"), "pre-commit.exe", "MZ")
	if _, ok := find(scan(t, repo), "hook", "pre-commit.exe"); !ok {
		t.Fatal("pre-commit.exe, which git for Windows runs as the pre-commit hook, was not found")
	}
	// Also from core.hooksPath.
	other := t.TempDir()
	writeHook(t, other, "pre-push.exe", "MZ")
	gitRun(t, repo, "config", "core.hooksPath", filepath.ToSlash(other))
	if _, ok := find(scan(t, repo), "hook", "pre-push.exe"); !ok {
		t.Error("a .exe hook in core.hooksPath was not found")
	}
}

func TestBranchAndPushDefaultRemotesThatAreFoldersAreListedWithTheirHooks(t *testing.T) {
	repo := newRepo(t)
	for _, key := range []string{"branch.main.remote", "branch.main.pushRemote", "remote.pushDefault"} {
		bare := filepath.Join(t.TempDir(), "r.git")
		gitRun(t, repo, "init", "--bare", "-q", bare)
		writeHook(t, filepath.Join(bare, "hooks"), "pre-receive", "#!/bin/sh\n")
		gitRun(t, repo, "config", key, filepath.ToSlash(bare))
		rep := scan(t, repo)
		if _, ok := find(rep, "setting", strings.ToLower(key)); !ok {
			t.Errorf("%s: the setting was not listed: %+v", key, rep.Items)
		}
		if _, ok := find(rep, "hook", "pre-receive"); !ok {
			t.Errorf("%s: the remote's hook was not listed", key)
		}
		gitRun(t, repo, "config", "--unset", key)
	}
	// A name of a configured remote is looked at under that remote.
	gitRun(t, repo, "remote", "add", "origin", "https://example.com/a.git")
	gitRun(t, repo, "config", "branch.main.remote", "origin")
	gitRun(t, repo, "config", "branch.other.remote", ".")
	if rep := scan(t, repo); len(rep.Items) != 0 {
		t.Errorf("a branch's remote that names a configured remote, or this one, was listed: %+v", rep.Items)
	}
}

func TestALocalRemotesOwnHooksPathIsFollowed(t *testing.T) {
	repo := newRepo(t)
	bare := filepath.Join(t.TempDir(), "r.git")
	gitRun(t, repo, "init", "--bare", "-q", bare)
	elsewhere := t.TempDir()
	writeHook(t, elsewhere, "update", "#!/bin/sh\n")
	gitRun(t, bare, "config", "core.hooksPath", filepath.ToSlash(elsewhere))
	writeHook(t, filepath.Join(bare, "hooks"), "pre-receive", "#!/bin/sh\n")
	gitRun(t, repo, "remote", "add", "origin", filepath.ToSlash(bare))
	rep := scan(t, repo)
	if _, ok := find(rep, "hook", "update"); !ok {
		t.Errorf("the hook in the remote's own core.hooksPath was not found: %+v", rep.Items)
	}
	if _, ok := find(rep, "hook", "pre-receive"); ok {
		t.Error("the remote's unused default hooks were listed")
	}
	gitRun(t, bare, "config", "core.hooksPath", "~someoneelse/hooks")
	if _, ok := find(scan(t, repo), "unscannable", ""); !ok {
		t.Error("a ~user hooks path on the remote left no trace")
	}
}

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	if got, ok := expandPath("~/hooks"); !ok || got != filepath.Join(home, "hooks") {
		t.Errorf("~/hooks = %q, %v", got, ok)
	}
	for _, p := range []string{"~someone/hooks", "%(prefix)/hooks"} {
		if _, ok := expandPath(p); ok {
			t.Errorf("%q was worked out; git expands it in ways this does not follow", p)
		}
	}
	if got, ok := expandPath(".githooks"); !ok || got != ".githooks" {
		t.Errorf(".githooks = %q, %v", got, ok)
	}
}

func TestAHooksPathThatCannotBeWorkedOutIsAnItem(t *testing.T) {
	repo := newRepo(t)
	gitRun(t, repo, "config", "core.hooksPath", "~someone/hooks")
	if _, ok := find(scan(t, repo), "unscannable", ""); !ok {
		t.Error("a ~user hooks path left no trace")
	}
}

func TestAGlobalRelativeHooksPathInsideTheTreeIsTheRepositorys(t *testing.T) {
	repo := newRepo(t)
	global := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(global, []byte("[core]\n\thooksPath = .githooks\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	writeHook(t, filepath.Join(repo, ".githooks"), "pre-commit", "#!/bin/sh\n")
	rep := scan(t, repo)
	h, ok := find(rep, "hook", "pre-commit")
	if !ok || h.Machine {
		t.Fatalf("hook = %+v ok=%v, want one that is the repository's", h, ok)
	}
	if v := Judge(rep, nil); !v.Warn {
		t.Error("an in-tree hook reached through a global relative hooksPath was not asked about on first sight")
	}
}

func TestAGuardRunsAfterStagingAndStopsTheCommit(t *testing.T) {
	repo := newRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	staged := false
	err := CommitAllGuarded(repo, "msg", func() error {
		out := gitRun(t, repo, "diff", "--cached", "--name-only")
		staged = strings.Contains(out, "new.txt")
		return errors.New("stop")
	})
	if err == nil || err.Error() != "stop" {
		t.Fatalf("err = %v, want the guard's", err)
	}
	if !staged {
		t.Error("the guard ran before the files were staged")
	}
	if n := strings.TrimSpace(gitRun(t, repo, "rev-list", "--count", "HEAD")); n != "1" {
		t.Errorf("%s commits, want the guard to have stopped the second", n)
	}
	if err := CommitAllGuarded(repo, "msg", func() error { return nil }); err != nil {
		t.Fatalf("a guard that agrees stopped the commit: %v", err)
	}
}
