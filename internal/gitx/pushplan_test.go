package gitx

import (
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
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo := newRepo(t)
			gitRun(t, repo, "remote", "add", "origin", "https://example.com/t/one.git")
			gitRun(t, repo, "remote", "add", "fork", "https://example.com/t/two.git")
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
