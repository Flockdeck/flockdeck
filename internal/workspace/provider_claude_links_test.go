package workspace

import (
	"os/exec"
	"strings"
	"testing"
)

// linkOutcome commits a .claude link to target in a repository where the folders given exist
// (path to content), and says what BranchSettings finds.
func linkOutcome(t *testing.T, target string, files map[string]string) (blobs int, unknown string) {
	t.Helper()
	repo := commitRepo(t)
	// paths that are not valid on every system are what is being tested
	gitIn(t, repo, "config", "core.protectNTFS", "false")
	for p, c := range files {
		commitAtMaybe(t, repo, "100644", p, c)
	}
	commitAt(t, repo, "120000", ".claude", target)
	ws := newTestWorkspace(t, repo)
	b, u := ws.BranchSettings(repo, "x")
	return len(b), u
}

// A link target is taken exactly as written. These are read one way by a text reading and
// another by the system, and each hides a gateway if it is read as the benign folder.
func TestAClaudeLinkTargetThatASystemReadsDifferentlyIsNotFollowed(t *testing.T) {
	isolateEnv(t)
	files := map[string]string{" cfg/settings.json": gatewaySettings, "cfg /settings.json": gatewaySettings, "cfg	/settings.json": gatewaySettings, "cfg/settings.json": gatewaySettings, "c:fg/settings.json": gatewaySettings, "cfg/settings.json": `{}`, "deep/er/settings.json": gatewaySettings, "sub/up": "../deep/er", "sub/f": "x"}
	files["cfg"+string(rune(0xa0))+"/settings.json"] = gatewaySettings
	for name, target := range map[string]string{
		"a space in front":  " cfg",
		"a space after":     "cfg ",
		"a tab after":       "cfg\t",
		"two newlines":      "cfg\n\n",
		"a newline inside":  "cfg\nx",
		"through ..":        "sub/up/..",
		"a . part":          "./cfg",
		"a .. part":         "cfg/../cfg",
		"an empty part":     "cfg//x",
		"a control":         "cfg\x01",
		"a backslash":       `cfg\x`,
		"a colon":           "c:fg",
		"a no-break space":  "cfg\u00a0",
		"absolute":          "/cfg",
		"empty":             "",
		"a link in the way": "sub/up/settings.json",
	} {
		blobs, unknown := linkOutcome(t, target, files)
		if blobs != 0 || unknown == "" {
			t.Errorf("%s (%q): %d blobs, unknown %q, want the company unknown", name, target, blobs, unknown)
		}
	}
}

// A link in the middle of the path is not followed through.
func TestAClaudeLinkThroughALinkOnTheWayIsUnknown(t *testing.T) {
	isolateEnv(t)
	repo := commitRepo(t)
	commitAt(t, repo, "100644", "real/cfg/settings.json", gatewaySettings)
	commitAt(t, repo, "120000", "lnk", "real")
	commitAt(t, repo, "120000", ".claude", "lnk/cfg")
	ws := newTestWorkspace(t, repo)
	if blobs, unknown := ws.BranchSettings(repo, "x"); len(blobs) != 0 || unknown == "" {
		t.Errorf("%d blobs, unknown %q", len(blobs), unknown)
	}
}

// A .claude that is a gitlink (a submodule) is not known, not a file with no settings.
func TestAClaudeThatIsASubmoduleIsUnknown(t *testing.T) {
	isolateEnv(t)
	repo := commitRepo(t)
	head := gitIn(t, repo, "rev-parse", "HEAD")
	gitIn(t, repo, "update-index", "--add", "--cacheinfo", "160000,"+head+",.claude")
	gitIn(t, repo, "commit", "-m", "gitlink")
	ws := newTestWorkspace(t, repo)
	if blobs, unknown := ws.BranchSettings(repo, "x"); len(blobs) != 0 || unknown == "" {
		t.Errorf("%d blobs, unknown %q", len(blobs), unknown)
	}
}

// Plain targets are still followed, with a trailing slash or a newline, and into a folder.
func TestPlainClaudeLinkTargetsAreStillFollowed(t *testing.T) {
	isolateEnv(t)
	for _, target := range []string{"cfg", "cfg/", "cfg\n", "cfg/sub", "cfg/sub/"} {
		blobs, unknown := linkOutcome(t, target, map[string]string{"cfg/settings.json": gatewaySettings, "cfg/sub/settings.json": gatewaySettings})
		if blobs != 1 || unknown != "" {
			t.Errorf("%q: %d blobs, unknown %q", target, blobs, unknown)
		}
	}
}

// commitAtMaybe is commitAt for a path that git may refuse on this system (a colon, a space
// at the end of a part): it says whether the path was staged (the next commit takes it).
func commitAtMaybe(t *testing.T, repo, mode, path, content string) bool {
	t.Helper()
	cmd := exec.Command("git", "hash-object", "-w", "--stdin")
	cmd.Dir = repo
	cmd.Stdin = strings.NewReader(content)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	add := exec.Command("git", "update-index", "--add", "--cacheinfo", mode+","+strings.TrimSpace(string(out))+","+path)
	add.Dir = repo
	if add.Run() != nil {
		return false
	}
	return true
}
