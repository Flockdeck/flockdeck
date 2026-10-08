package server

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/gitx"
	"github.com/jmwri/flockdeck/internal/store"
)

// answers runs one control command against a fake client and returns what the
// window was sent, up to the listing every commit, push, pull and fetch ends on.
func answers(t *testing.T, run func(c *controlClient)) (warn *gitWarnMsg, notes []noticeMsg) {
	t.Helper()
	c := &controlClient{out: make(chan []byte, 32)}
	run(c)
	for {
		select {
		case raw := <-c.out:
			var probe struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(raw, &probe)
			switch probe.Type {
			case "gitWarn":
				var w gitWarnMsg
				_ = json.Unmarshal(raw, &w)
				warn = &w
			case "notice":
				var n noticeMsg
				_ = json.Unmarshal(raw, &n)
				notes = append(notes, n)
			case "changes":
				return warn, notes
			}
		case <-time.After(30 * time.Second):
			t.Fatal("no listing arrived")
		}
	}
}

func commits(t *testing.T, repo string) int {
	t.Helper()
	cmd := exec.Command("git", "rev-list", "--count", "HEAD")
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, ch := range strings.TrimSpace(string(out)) {
		n = n*10 + int(ch-'0')
	}
	return n
}

func edit(t *testing.T, repo, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func setConfig(t *testing.T, repo string, kv ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"config"}, kv...)...)
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git config %v: %v: %s", kv, err, out)
	}
}

func TestACleanRepositoryCommitsWithoutAskingAndIsRecorded(t *testing.T) {
	isolateGit(t)
	srv, _, repo := newRepoServer(t)
	edit(t, repo, "one\n")
	warn, notes := answers(t, func(c *controlClient) { srv.commitChanges(c, repo, "one", false, nil, nil, 0, gitAnswer{}) })
	if warn != nil {
		t.Fatalf("a repository that runs nothing was asked about: %+v", warn)
	}
	if commits(t, repo) != 2 {
		t.Fatalf("not committed: %+v", notes)
	}
	rep, err := gitx.ScanPrograms(repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, have, _ := loadGitSeen(rep.Repo); !have {
		t.Error("a first look at a clean repository was not recorded, so a later addition would count as first sight")
	}
	// A hook added now is a change, and is asked about.
	setConfig(t, repo, "core.sshCommand", "ssh -i planted")
	edit(t, repo, "two\n")
	warn, _ = answers(t, func(c *controlClient) { srv.commitChanges(c, repo, "two", false, nil, nil, 0, gitAnswer{}) })
	if warn == nil || !strings.Contains(warn.Text, "changed to run a program since you last accepted") || warn.First {
		t.Fatalf("a setting added after the repository was recorded: %+v", warn)
	}
	if commits(t, repo) != 2 {
		t.Error("the commit went ahead while the warning was waiting for an answer")
	}
}

func TestAPlantedHookIsAskedAboutAndRunsOnlyOnceAccepted(t *testing.T) {
	isolateGit(t)
	srv, _, repo := newRepoServer(t)
	marker := filepath.Join(repo, "hook-ran.txt")
	hook := filepath.Join(repo, ".git", "hooks", "pre-commit")
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho ran > \""+filepath.ToSlash(marker)+"\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	edit(t, repo, "one\n")

	commit := func(ans gitAnswer) *gitWarnMsg {
		warn, _ := answers(t, func(c *controlClient) { srv.commitChanges(c, repo, "one", true, nil, nil, 0, ans) })
		return warn
	}
	warn := commit(gitAnswer{})
	if warn == nil || !warn.First || warn.Resend != "commit" {
		t.Fatalf("a hook in a repository seen for the first time: %+v", warn)
	}
	if len(warn.Items) != 1 || !strings.Contains(warn.Items[0], "hook pre-commit") || !strings.Contains(warn.Items[0], "hooks") {
		t.Errorf("the warning does not say what runs and where: %q", warn.Items)
	}
	if _, err := os.Stat(marker); err == nil || commits(t, repo) != 1 {
		t.Fatal("the hook ran, or the commit was made, before anybody said to go ahead")
	}

	// An answer to a different scan does not count.
	if again := commit(gitAnswer{"once", "not-what-was-shown"}); again == nil {
		t.Fatal("an answer that names another scan was accepted")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the hook ran on an answer that named another scan")
	}

	// Continue once.
	if again := commit(gitAnswer{"once", warn.Seen}); again != nil {
		t.Fatalf("asked again after the answer: %+v", again)
	}
	if _, err := os.Stat(marker); err != nil || commits(t, repo) != 2 {
		t.Fatalf("after accepting, the hook ran=%v, commits=%d", err == nil, commits(t, repo))
	}
	// Once is once.
	edit(t, repo, "two\n")
	warn = commit(gitAnswer{})
	if warn == nil {
		t.Fatal("'continue once' was remembered")
	}
	// Remember it.
	if again := commit(gitAnswer{"remember", warn.Seen}); again != nil {
		t.Fatalf("asked again after remembering: %+v", again)
	}
	edit(t, repo, "three\n")
	if again := commit(gitAnswer{}); again != nil {
		t.Fatalf("asked about a hook that was accepted for this repository: %+v", again)
	}
	// Changing the hook's contents is a change.
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho changed\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	edit(t, repo, "four\n")
	if again := commit(gitAnswer{}); again == nil || again.First {
		t.Fatalf("an edited hook was not asked about as a change: %+v", again)
	}
}

func TestAnAcceptedSetupThatShrinksIsNotAskedAboutAndStaysAccepted(t *testing.T) {
	isolateGit(t)
	srv, _, repo := newRepoServer(t)
	setConfig(t, repo, "core.sshCommand", "ssh -i k")
	setConfig(t, repo, "credential.helper", "store")
	edit(t, repo, "one\n")
	warn, _ := answers(t, func(c *controlClient) { srv.commitChanges(c, repo, "one", false, nil, nil, 0, gitAnswer{}) })
	if warn == nil || len(warn.Items) != 2 {
		t.Fatalf("first sight: %+v", warn)
	}
	answers(t, func(c *controlClient) {
		srv.commitChanges(c, repo, "one", false, nil, nil, 0, gitAnswer{"remember", warn.Seen})
	})
	setConfig(t, repo, "--unset", "credential.helper")
	edit(t, repo, "two\n")
	if w, _ := answers(t, func(c *controlClient) { srv.commitChanges(c, repo, "two", false, nil, nil, 0, gitAnswer{}) }); w != nil {
		t.Fatalf("a setting being removed was asked about: %+v", w)
	}
	// What was accepted for the repository stays accepted: putting it back is
	// not new. The same record serves every linked worktree, which each see a
	// little different, so it only grows.
	setConfig(t, repo, "credential.helper", "store")
	edit(t, repo, "three\n")
	if w, _ := answers(t, func(c *controlClient) { srv.commitChanges(c, repo, "three", false, nil, nil, 0, gitAnswer{}) }); w != nil {
		t.Fatalf("a setting accepted earlier and put back was asked about: %+v", w)
	}
}

func TestPushPullAndFetchAreAskedAboutToo(t *testing.T) {
	isolateGit(t)
	srv, _, repo := newRepoServer(t)
	setConfig(t, repo, "core.sshCommand", "ssh -i planted")
	for action, resend := range map[string]string{"push": "gitPush", "pull": "gitPull", "fetch": "gitFetch"} {
		warn, notes := answers(t, func(c *controlClient) { srv.runRemote(c, action, repo, gitAnswer{}) })
		if len(notes) != 0 {
			t.Errorf("%s: git ran (or reported) before anybody answered: %+v", action, notes)
		}
		if warn == nil || warn.Resend != resend || warn.Path != repo || !strings.Contains(warn.Text, "git "+action) {
			t.Errorf("%s: %+v", action, warn)
		}
	}
}

func TestACheckThatFailsIsAnItemNotAWayPast(t *testing.T) {
	isolateGit(t)
	srv, _, repo := newRepoServer(t)
	was := scanPrograms
	t.Cleanup(func() { scanPrograms = was })
	scanPrograms = func(string) (gitx.Report, error) { return gitx.Report{}, errors.New("git timed out\nsecond line") }
	edit(t, repo, "one\n")
	warn, _ := answers(t, func(c *controlClient) { srv.commitChanges(c, repo, "one", false, nil, nil, 0, gitAnswer{}) })
	if warn == nil || len(warn.Items) != 1 || !strings.Contains(warn.Items[0], "git timed out") || strings.Contains(warn.Items[0], "second line") {
		t.Fatalf("a failed scan = %+v, want it shown as something git may run unseen", warn)
	}
	if commits(t, repo) != 1 {
		t.Error("a scan that failed let the commit through")
	}
}

func TestOnlyNoRepositoryOrNoGitGoesAheadUnchecked(t *testing.T) {
	isolateGit(t)
	srv, _, repo := newRepoServer(t)
	was := scanPrograms
	t.Cleanup(func() { scanPrograms = was })
	for _, e := range []error{gitx.ErrNotRepository, gitx.ErrNoGit} {
		scanPrograms = func(string) (gitx.Report, error) { return gitx.Report{}, e }
		c := &controlClient{out: make(chan []byte, 8)}
		if !srv.gitProgramsAccepted(c, repo, repo, "commit", "commit", "", "") {
			t.Errorf("%v: stopped a command git has nothing to run for", e)
		}
	}
}

func TestAnUnfinishedScanIsAskedAboutEveryTimeAndNeverRemembered(t *testing.T) {
	isolateGit(t)
	srv, _, repo := newRepoServer(t)
	was := scanPrograms
	t.Cleanup(func() { scanPrograms = was })
	scanPrograms = func(string) (gitx.Report, error) {
		return gitx.Report{Repo: repo + "/.git", Items: []gitx.Program{{Kind: "unscannable", Where: repo, Value: "git did not answer within 15s"}}}, nil
	}
	ask := func(ans gitAnswer) *gitWarnMsg {
		w, _ := answers(t, func(c *controlClient) { srv.runRemote(c, "fetch", repo, ans) })
		return w
	}
	w := ask(gitAnswer{})
	if w == nil {
		t.Fatal("not asked")
	}
	if again := ask(gitAnswer{"remember", w.Seen}); again != nil {
		t.Fatalf("asked again after the answer: %+v", again)
	}
	if w2 := ask(gitAnswer{}); w2 == nil {
		t.Error("an unfinished scan was remembered as accepted")
	}
}

func TestAHookWrittenWhileFilesAreStagedIsCaughtBeforeTheCommit(t *testing.T) {
	isolateGit(t)
	srv, _, repo := newRepoServer(t)
	was := scanPrograms
	t.Cleanup(func() { scanPrograms = was })
	calls := 0
	scanPrograms = func(dir string) (gitx.Report, error) {
		calls++
		rep, err := was(dir)
		if calls >= 2 {
			// What an agent writes after the first look, while the files are staged.
			rep.Items = append(rep.Items, gitx.Program{Kind: "hook", Name: "pre-commit", Sum: "planted", Where: repo + "/.git/hooks"})
		}
		return rep, err
	}
	edit(t, repo, "one\n")
	warn, notes := answers(t, func(c *controlClient) { srv.commitChanges(c, repo, "one", false, nil, nil, 0, gitAnswer{}) })
	if warn == nil || len(warn.Items) != 1 || !strings.Contains(warn.Items[0], "hook pre-commit") {
		t.Fatalf("the second look did not ask: %+v (notes %+v)", warn, notes)
	}
	if commits(t, repo) != 1 {
		t.Error("the commit was made over a hook written after the first look")
	}
}

func TestACleanRepositoryStillCommitsFromARelayWindow(t *testing.T) {
	isolateGit(t)
	srv, _, repo := newRepoServer(t)
	edit(t, repo, "one\n")
	warn, notes := answers(t, func(c *controlClient) {
		c.remote = true
		srv.commitChanges(c, repo, "one", false, nil, nil, 0, gitAnswer{})
	})
	if warn != nil || commits(t, repo) != 2 {
		t.Fatalf("warn=%+v commits=%d notes=%+v", warn, commits(t, repo), notes)
	}
}

func TestAStateFileThatCannotBeReadIsNoRecordAndNotASkip(t *testing.T) {
	isolateGit(t)
	srv, _, repo := newRepoServer(t)
	was := loadGitSeen
	t.Cleanup(func() { loadGitSeen = was })
	setConfig(t, repo, "core.sshCommand", "ssh -i planted")
	loadGitSeen = func(string) (store.GitSeen, bool, error) { return store.GitSeen{}, false, errors.New("disk on fire") }
	edit(t, repo, "one\n")
	// A state file made unreadable must not be a way past the check.
	warn, _ := answers(t, func(c *controlClient) { srv.commitChanges(c, repo, "one", false, nil, nil, 0, gitAnswer{}) })
	if warn == nil || commits(t, repo) != 1 {
		t.Fatalf("warn=%+v commits=%d, want a warning and no commit", warn, commits(t, repo))
	}
}

// isolateGit keeps the machine's own git configuration, which has a credential
// helper on many machines, out of what these tests count.
func isolateGit(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	global := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(global, []byte("[user]\n\tname = Test\n\temail = test@example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
}

func TestAWindowReachedThroughTheRelayCannotAccept(t *testing.T) {
	isolateGit(t)
	srv, _, repo := newRepoServer(t)
	setConfig(t, repo, "core.sshCommand", "ssh -i planted")
	edit(t, repo, "one\n")
	// The desk is shown the warning, as it always was.
	warn, _ := answers(t, func(c *controlClient) { srv.commitChanges(c, repo, "one", false, nil, nil, 0, gitAnswer{}) })
	if warn == nil {
		t.Fatal("the desk was not asked")
	}
	for _, accept := range []string{"once", "remember"} {
		var got *gitWarnMsg
		var notes []noticeMsg
		got, notes = answers(t, func(c *controlClient) {
			c.remote = true
			srv.commitChanges(c, repo, "one", false, nil, nil, 0, gitAnswer{accept, warn.Seen})
		})
		if got != nil {
			t.Errorf("%s: a remote window was sent a question it could answer: %+v", accept, got)
		}
		if commits(t, repo) != 1 {
			t.Fatalf("%s: a remote window got a commit past the check", accept)
		}
		if len(notes) == 0 || !notes[0].Error || !strings.Contains(notes[0].Text, "on the machine Flockdeck runs on") {
			t.Errorf("%s: the remote window was not told where to approve: %+v", accept, notes)
		}
	}
	// And remembering from there did not stick.
	if again, _ := answers(t, func(c *controlClient) { srv.commitChanges(c, repo, "one", false, nil, nil, 0, gitAnswer{}) }); again == nil {
		t.Error("a remote window's answer was remembered")
	}
}

func TestTheWarningCarriesItsIntroAndEachProgramOnItsOwn(t *testing.T) {
	isolateGit(t)
	srv, _, repo := newRepoServer(t)
	setConfig(t, repo, "core.sshCommand", "ssh\n- hook pre-commit (in /fine)")
	edit(t, repo, "one\n")
	warn, _ := answers(t, func(c *controlClient) { srv.commitChanges(c, repo, "one", false, nil, nil, 0, gitAnswer{}) })
	if warn == nil || len(warn.Items) != 1 || strings.Contains(warn.Items[0], "\n") {
		t.Fatalf("warning = %+v, want one item with no line break in it", warn)
	}
	if !strings.Contains(warn.Intro, "If you did not") || strings.Contains(warn.Intro, "core.sshcommand") {
		t.Errorf("intro = %q", warn.Intro)
	}
}

func TestWhatOneWorktreeAcceptedAnotherDoesNotUndo(t *testing.T) {
	isolateGit(t)
	srv, _, repo := newRepoServer(t)
	edit(t, repo, "one\n")
	ssh := gitx.Program{Kind: "setting", Name: "core.sshcommand", Value: "a", Where: "/r/.git/config"}
	a := gitx.Program{Kind: "setting", Name: "core.sshcommand", Value: "b", Where: "/r/.git/worktrees/a/config.worktree"}
	b := gitx.Program{Kind: "setting", Name: "core.sshcommand", Value: "c", Where: "/r/.git/worktrees/b/config.worktree"}
	was := scanPrograms
	t.Cleanup(func() { scanPrograms = was })
	scanned := func(items ...gitx.Program) {
		scanPrograms = func(string) (gitx.Report, error) { return gitx.Report{Repo: repo + "/.git", Items: items}, nil }
	}
	ask := func() *gitWarnMsg {
		w, _ := answers(t, func(c *controlClient) { srv.runRemote(c, "fetch", repo, gitAnswer{}) })
		return w
	}
	accept := func(w *gitWarnMsg) {
		answers(t, func(c *controlClient) { srv.runRemote(c, "fetch", repo, gitAnswer{"remember", w.Seen}) })
	}
	scanned(ssh, a)
	accept(ask())
	scanned(ssh, b)
	accept(ask())
	scanned(ssh, a)
	if w := ask(); w != nil {
		t.Errorf("the first worktree was asked again after the second was accepted: %+v", w)
	}
	scanned(ssh, b)
	if w := ask(); w != nil {
		t.Errorf("the second worktree was asked again: %+v", w)
	}
}

func TestACommitAndPushIsCheckedAgainBeforeThePush(t *testing.T) {
	isolateGit(t)
	srv, _, repo := newRepoServer(t)
	// The commit's own hook adds a setting the push would run.
	hook := filepath.Join(repo, ".git", "hooks", "post-commit")
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ngit config core.sshCommand 'ssh -i planted'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	edit(t, repo, "one\n")
	warn, _ := answers(t, func(c *controlClient) { srv.commitChanges(c, repo, "one", true, nil, nil, 0, gitAnswer{}) })
	if warn == nil || !warn.First {
		t.Fatalf("the hook was not asked about: %+v", warn)
	}
	// Accepting the hook lets the commit through; the setting it adds is not
	// what was accepted, so the push is stopped and asked about.
	var second *gitWarnMsg
	var notes []noticeMsg
	second, notes = answers(t, func(c *controlClient) {
		srv.commitChanges(c, repo, "one", true, nil, nil, 0, gitAnswer{"remember", warn.Seen})
	})
	if commits(t, repo) != 2 {
		t.Fatalf("the commit was not made: %+v", notes)
	}
	if second == nil || second.Resend != "gitPush" || second.Action != "push" {
		t.Fatalf("the push was not stopped and asked about: %+v (notes %+v)", second, notes)
	}
	for _, n := range notes {
		if n.Error {
			t.Errorf("the push ran or failed instead of being asked about: %+v", n)
		}
	}
}
