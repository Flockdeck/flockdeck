package gitx

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func commitFile(t *testing.T, repo, name string) {
	t.Helper()
	write(t, repo, name, name+"\n")
	gitRun(t, repo, "add", name)
	gitRun(t, repo, "commit", "-q", "-m", name)
}

// A push is shown by the commit it would send and how many that is.
func TestPushAheadCountsWhatAPushWouldSend(t *testing.T) {
	repo := newRepo(t)
	gitRun(t, repo, "remote", "add", "origin", "https://example.com/t/one.git")
	if got := HeadSHA(repo); got != strings.TrimSpace(gitRun(t, repo, "rev-parse", "HEAD")) || len(got) != 40 {
		t.Fatalf("HeadSHA = %q", got)
	}
	// Nothing of this remote is known: every commit would go.
	if n, err := PushAhead(repo); err != nil || n != 1 {
		t.Fatalf("no upstream, nothing on the remote: ahead = %d, %v, want 1", n, err)
	}
	gitRun(t, repo, "update-ref", "refs/remotes/origin/main", "HEAD")
	if n, err := PushAhead(repo); err != nil || n != 0 {
		t.Fatalf("everything is on the remote: ahead = %d, %v, want 0", n, err)
	}
	commitFile(t, repo, "a.txt")
	commitFile(t, repo, "b.txt")
	if n, err := PushAhead(repo); err != nil || n != 2 {
		t.Fatalf("two commits since: ahead = %d, %v, want 2", n, err)
	}
	// With an upstream it counts against that.
	gitRun(t, repo, "config", "branch.main.remote", "origin")
	gitRun(t, repo, "config", "branch.main.merge", "refs/heads/main")
	if n, err := PushAhead(repo); err != nil || n != 2 {
		t.Fatalf("with an upstream: ahead = %d, %v, want 2", n, err)
	}
	old := HeadSHA(repo)
	commitFile(t, repo, "c.txt")
	if HeadSHA(repo) == old {
		t.Fatal("HeadSHA did not move with a commit")
	}
}

func TestPushSettingsRiskSaysWhenAPushWouldSendMore(t *testing.T) {
	cases := []struct {
		name string
		set  [][]string
		risk bool
	}{
		{"plain", nil, false},
		{"push.default simple", [][]string{{"push.default", "simple"}}, false},
		{"push.default current", [][]string{{"push.default", "current"}}, false},
		{"push.default matching", [][]string{{"push.default", "matching"}}, true},
		{"push refspec for this branch", [][]string{{"remote.origin.push", "refs/heads/main:refs/heads/main"}}, false},
		{"push refspec for another branch", [][]string{{"remote.origin.push", "refs/heads/other:refs/heads/other"}}, true},
		{"push refspec of everything", [][]string{{"remote.origin.push", "refs/heads/*:refs/heads/*"}}, true},
		{"push refspec of tags", [][]string{{"remote.origin.push", "+refs/tags/*:refs/tags/*"}}, true},
		{"mirror", [][]string{{"remote.origin.mirror", "true"}}, true},
		{"pushRemote elsewhere", [][]string{{"branch.main.pushRemote", "fork"}}, true},
		{"pushDefault elsewhere", [][]string{{"remote.pushDefault", "fork"}}, true},
		{"follow tags", [][]string{{"push.followTags", "true"}}, true},
		{"push option", [][]string{{"push.pushOption", "ci.skip"}}, true},
		{"push option cleared", [][]string{{"push.pushOption", ""}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo := newRepo(t)
			gitRun(t, repo, "remote", "add", "origin", "https://example.com/t/one.git")
			gitRun(t, repo, "remote", "add", "fork", "https://example.com/t/two.git")
			// With an upstream, so a push goes by git's settings.
			gitRun(t, repo, "update-ref", "refs/remotes/origin/main", "HEAD")
			gitRun(t, repo, "config", "branch.main.remote", "origin")
			gitRun(t, repo, "config", "branch.main.merge", "refs/heads/main")
			for _, kv := range c.set {
				gitRun(t, repo, "config", "--add", kv[0], kv[1])
			}
			got := PushSettingsRisk(repo)
			if (got != "") != c.risk {
				t.Fatalf("risk = %q, want risk %v", got, c.risk)
			}
			if strings.ContainsAny(got, "\r\n") {
				t.Fatalf("the reason holds a line break: %q", got)
			}
		})
	}
}

// With no upstream the push names its remote and refspec, so push.default, a push
// refspec, pushRemote and pushDefault do not decide anything and are not refused.
func TestPushSettingsRiskSkipsWhatAnExplicitPushIgnores(t *testing.T) {
	repo := newRepo(t)
	gitRun(t, repo, "remote", "add", "origin", "https://example.com/t/one.git")
	gitRun(t, repo, "remote", "add", "fork", "https://example.com/t/two.git")
	gitRun(t, repo, "config", "push.default", "matching")
	gitRun(t, repo, "config", "remote.origin.push", "refs/heads/other:refs/heads/other")
	gitRun(t, repo, "config", "branch.main.pushRemote", "fork")
	if got := PushSettingsRisk(repo); got != "" {
		t.Fatalf("risk with no upstream = %q, want none", got)
	}
	gitRun(t, repo, "config", "remote.origin.mirror", "true")
	if got := PushSettingsRisk(repo); !strings.Contains(got, "mirror") {
		t.Fatalf("a mirror remote: %q, want the setting named", got)
	}
}

// A setting git fails to read is a risk, not an absence.
func TestPushSettingsRiskFailsClosedWhenAReadFails(t *testing.T) {
	repo := newRepo(t)
	gitRun(t, repo, "remote", "add", "origin", "https://example.com/t/one.git")
	if got := PushSettingsRisk(repo); got != "" {
		t.Fatalf("plain: %q", got)
	}
	old := gitHook
	defer func() { gitHook = old }()
	gitHook = func(_ context.Context, args []string) error {
		if len(args) > 0 && args[0] == "config" {
			return errors.New("config is unreadable")
		}
		return nil
	}
	if got := PushSettingsRisk(repo); got == "" {
		t.Fatal("a config read that failed was read as no risk")
	}
}

// The first commit of a repository has no HEAD yet: nothing to count, not an error.
func TestPushAheadOfAnUnbornBranchIsZero(t *testing.T) {
	repo := t.TempDir()
	gitRun(t, repo, "init", "-q", "-b", "main")
	gitRun(t, repo, "remote", "add", "origin", "https://example.com/t/one.git")
	if n, err := PushAhead(repo); err != nil || n != 0 {
		t.Fatalf("unborn: ahead = %d, %v, want 0", n, err)
	}
}

// The push goes to the pinned URL and ref whatever the repository's settings say
// by the time it runs, and is refused when the pin no longer reads the same.
func TestPushPinnedIgnoresAConfigChangeAfterThePin(t *testing.T) {
	repo := newRepo(t)
	good := t.TempDir()
	evil := t.TempDir()
	gitRun(t, good, "init", "-q", "--bare")
	gitRun(t, evil, "init", "-q", "--bare")
	gitRun(t, repo, "remote", "add", "origin", good)
	pin, err := ResolvePin(repo, false)
	if err != nil {
		t.Fatal(err)
	}
	head := HeadSHA(repo)
	// What the agent writes after the yes: another URL for pushes, a refspec for
	// every branch, tags, and a push.default that matches.
	gitRun(t, repo, "config", "push.default", "matching")
	gitRun(t, repo, "config", "push.followTags", "true")
	gitRun(t, repo, "tag", "v1")
	if _, err := PushPinned(repo, pin, PushExpect{Head: head}); err != nil {
		t.Fatalf("push with only widening settings changed: %v", err)
	}
	if got := strings.TrimSpace(gitRun(t, good, "rev-parse", "refs/heads/main")); got != head {
		t.Fatalf("the pinned remote has %q, want %q", got, head)
	}
	if out := strings.TrimSpace(gitRun(t, good, "tag")); out != "" {
		t.Fatalf("a tag went with the push: %q", out)
	}
	// A pushurl set after the pin is noticed, and nothing is sent to it.
	gitRun(t, repo, "config", "remote.origin.pushurl", evil)
	commitFile(t, repo, "x.txt")
	if _, err := PushPinned(repo, pin, PushExpect{Head: head}); err == nil {
		t.Fatal("a push went on after the push URL changed")
	}
	if out := strings.TrimSpace(gitRun(t, evil, "for-each-ref")); out != "" {
		t.Fatalf("the other remote received refs: %q", out)
	}
	// A new commit under an unchanged pin is refused by the head, not pushed.
	gitRun(t, repo, "config", "--unset", "remote.origin.pushurl")
	if _, err := PushPinned(repo, pin, PushExpect{Head: head}); err == nil {
		t.Fatal("a push of a commit made after the ask went on")
	}
	// A commit the request itself made is pushed: its parent is the pinned head.
	pin2, _ := ResolvePin(repo, false)
	if _, err := PushPinned(repo, pin2, PushExpect{Head: head, AfterCommit: true}); err != nil {
		t.Fatalf("the commit made after the ask: %v", err)
	}
}

// A pull is pinned by the fetch URL and the branch it merges, not only the push URL.
func TestPullPinnedHoldsTheFetchURLAndMergeBranch(t *testing.T) {
	repo := newRepo(t)
	src := t.TempDir()
	gitRun(t, src, "init", "-q", "--bare")
	gitRun(t, repo, "remote", "add", "origin", src)
	gitRun(t, repo, "push", "-q", "-u", "origin", "main")
	pin, err := ResolvePin(repo, true)
	if err != nil {
		t.Fatal(err)
	}
	if pin.FetchURL != src || pin.Merge != "refs/heads/main" || pin.PullRemote != "origin" {
		t.Fatalf("pin = %+v", pin)
	}
	head := HeadSHA(repo)
	for _, kv := range [][]string{{"remote.origin.url", t.TempDir()}, {"branch.main.merge", "refs/heads/other"}} {
		old := strings.TrimSpace(gitRun(t, repo, "config", "--get", kv[0]))
		gitRun(t, repo, "config", kv[0], kv[1])
		if _, err := PullPinned(repo, pin, head); err == nil {
			t.Fatalf("a pull went on after %s changed", kv[0])
		}
		gitRun(t, repo, "config", kv[0], old)
	}
	if _, err := PullPinned(repo, pin, head); err != nil {
		t.Fatalf("an unchanged pin: %v", err)
	}
}
