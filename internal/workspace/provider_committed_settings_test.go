package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const gatewaySettings = `{"env": {"ANTHROPIC_BASE_URL": "https://gw.example"}}`

// commitAt puts content in the index at a path with a mode, without a file in the working
// copy, and commits.
func commitAt(t *testing.T, repo, mode, path, content string) {
	t.Helper()
	blob := gitInput(t, repo, content, "hash-object", "-w", "--stdin")
	gitIn(t, repo, "update-index", "--add", "--cacheinfo", mode+","+blob+","+path)
	gitIn(t, repo, "commit", "-m", "add "+path)
}

// askedFor is the company that a spawn on a branch is judged to go to, as the server works it
// out: the settings in the object store and the working copy of the checkout.
func askedFor(t *testing.T, repo, branch string) (string, string) {
	t.Helper()
	ws := newTestWorkspace(t, repo)
	blobs, unknown := ws.BranchSettings(repo, branch)
	planned := filepath.Join(t.TempDir(), "001-"+branch)
	return BatonProviderWith(claudeSpec, planned, Sources{Dirs: []string{repo}, Settings: blobs, Unknown: unknown})
}

// A settings.local.json that a repository committed is in the worktree, whatever the working
// copy of the checkout says now.
func TestACommittedSettingsLocalFileIsReadFromTheCommit(t *testing.T) {
	isolateEnv(t)
	repo := commitRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(repo, ".claude", "settings.local.json")
	if err := os.WriteFile(local, []byte(gatewaySettings), 0o600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "-f", ".")
	gitIn(t, repo, "commit", "-m", "local settings")
	if err := os.Remove(local); err != nil {
		t.Fatal(err)
	}
	if p, why := askedFor(t, repo, "new-one"); p == "anthropic" {
		t.Errorf("a gateway in a committed settings.local.json was missed (%s)", why)
	}
}

// .claude committed as a link to a folder of the same commit is followed; one that leaves the
// repository, or leads nowhere, is not known.
func TestAClaudeFolderCommittedAsALinkIsFollowedOrUnknown(t *testing.T) {
	isolateEnv(t)
	repo := commitRepo(t)
	commitAt(t, repo, "100644", "shared/claude/settings.json", gatewaySettings)
	commitAt(t, repo, "120000", ".claude", "shared/claude")
	if p, why := askedFor(t, repo, "linked"); p == "anthropic" {
		t.Errorf("a gateway behind a .claude link was missed (%s)", why)
	}
	for name, target := range map[string]string{"outside": "../elsewhere", "absolute": "/etc", "missing": "no/such/folder", "a file": "f.txt"} {
		r := commitRepo(t)
		commitAt(t, r, "120000", ".claude", target)
		ws := newTestWorkspace(t, r)
		if blobs, unknown := ws.BranchSettings(r, "x"); len(blobs) != 0 || unknown == "" {
			t.Errorf("%s: %d blobs, unknown %q", name, len(blobs), unknown)
		}
	}
}

// A branch that is not checked out, with its settings in another case, is read; a repository
// with the exact name and another spelling has both read.
func TestSettingsInAnotherCaseOnAnotherBranchAreRead(t *testing.T) {
	isolateEnv(t)
	repo := commitRepo(t)
	gitIn(t, repo, "checkout", "-q", "-b", "other")
	commitAt(t, repo, "100644", ".Claude/Settings.json", gatewaySettings)
	gitIn(t, repo, "checkout", "-q", "main")
	if p, why := askedFor(t, repo, "other"); p == "anthropic" {
		t.Errorf("a gateway in .Claude/Settings.json was missed (%s)", why)
	}
	if p, _ := askedFor(t, repo, "main"); p != "anthropic" {
		t.Errorf("main has none and was judged %q", p)
	}
	both := commitRepo(t)
	commitAt(t, both, "100644", ".claude/settings.json", `{}`)
	commitAt(t, both, "100644", ".Claude/settings.local.json", gatewaySettings)
	ws := newTestWorkspace(t, both)
	if blobs, _ := ws.BranchSettings(both, "x"); len(blobs) != 2 {
		t.Errorf("both spellings: %d blobs, want 2", len(blobs))
	}
}

// The limits of the object store reads: a file that is a link, or over the limit, in the
// folder that is found.
func TestCommittedSettingsThatAreLinksOrTooLargeAreUnknown(t *testing.T) {
	isolateEnv(t)
	repo := commitRepo(t)
	commitAt(t, repo, "120000", ".claude/settings.local.json", "/dev/zero")
	ws := newTestWorkspace(t, repo)
	if _, unknown := ws.BranchSettings(repo, "x"); !strings.Contains(unknown, "not a regular file") {
		t.Errorf("a link: %q", unknown)
	}
	repo2 := commitRepo(t)
	commitAt(t, repo2, "100644", ".claude/settings.json", `{"env":{"X":"`+strings.Repeat("a", settingsFileMax)+`"}}`)
	ws2 := newTestWorkspace(t, repo2)
	if _, unknown := ws2.BranchSettings(repo2, "x"); !strings.Contains(unknown, "too large") {
		t.Errorf("too large: %q", unknown)
	}
}
