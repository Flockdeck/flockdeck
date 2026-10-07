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

func TestAnAcceptedSetupThatShrinksIsNotAskedAbout(t *testing.T) {
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
	// And the record came down with it: putting it back is new.
	setConfig(t, repo, "credential.helper", "store")
	edit(t, repo, "three\n")
	if w, _ := answers(t, func(c *controlClient) { srv.commitChanges(c, repo, "three", false, nil, nil, 0, gitAnswer{}) }); w == nil {
		t.Fatal("a setting put back after it was removed was not asked about")
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

func TestACheckThatFailsLetsGitRunAndSaysSo(t *testing.T) {
	isolateGit(t)
	srv, _, repo := newRepoServer(t)
	was := scanPrograms
	t.Cleanup(func() { scanPrograms = was })
	scanPrograms = func(string) (gitx.Report, error) { return gitx.Report{}, errors.New("git timed out\nsecond line") }
	edit(t, repo, "one\n")
	warn, notes := answers(t, func(c *controlClient) { srv.commitChanges(c, repo, "one", false, nil, nil, 0, gitAnswer{}) })
	if warn != nil {
		t.Fatalf("a failed check stopped the commit: %+v", warn)
	}
	if commits(t, repo) != 2 {
		t.Fatalf("the commit did not go ahead: %+v", notes)
	}
	said := false
	for _, n := range notes {
		if strings.Contains(n.Text, "could not check") && strings.Contains(n.Text, "git timed out") &&
			strings.Contains(n.Text, "Going ahead without the check") && !strings.Contains(n.Text, "second line") {
			said = true
		}
	}
	if !said {
		t.Errorf("the window was not told the check was skipped: %+v", notes)
	}
}

func TestAStateFileThatCannotBeReadIsAFailedCheckToo(t *testing.T) {
	isolateGit(t)
	srv, _, repo := newRepoServer(t)
	was := loadGitSeen
	t.Cleanup(func() { loadGitSeen = was })
	setConfig(t, repo, "core.sshCommand", "ssh -i planted")
	loadGitSeen = func(string) (store.GitSeen, bool, error) { return store.GitSeen{}, false, errors.New("disk on fire") }
	edit(t, repo, "one\n")
	warn, notes := answers(t, func(c *controlClient) { srv.commitChanges(c, repo, "one", false, nil, nil, 0, gitAnswer{}) })
	if warn != nil || commits(t, repo) != 2 {
		t.Fatalf("warn=%+v commits=%d notes=%+v", warn, commits(t, repo), notes)
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
