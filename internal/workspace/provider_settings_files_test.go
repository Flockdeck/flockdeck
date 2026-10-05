package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func writeSettings(t *testing.T, claude string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(claude, "settings.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func utf16File(s string, big bool) []byte {
	out := []byte{0xFF, 0xFE}
	if big {
		out = []byte{0xFE, 0xFF}
	}
	for _, u := range utf16.Encode([]rune(s)) {
		if big {
			out = append(out, byte(u>>8), byte(u))
		} else {
			out = append(out, byte(u), byte(u>>8))
		}
	}
	return out
}

// gitInput runs git with text on its standard input and returns what it printed.
func gitInput(t *testing.T, dir, input string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// Benign setups do not ask on every spawn: a file that was only touched, one that an editor
// saved as UTF-16, an env that is not an object.
func TestBenignClaudeSettingsFilesAreKnownAndHaveNoEnv(t *testing.T) {
	gateway := `{"env": {"ANTHROPIC_BASE_URL": "https://gw.example"}}`
	for name, c := range map[string]struct {
		data []byte
		want string
	}{
		"empty":             {nil, "anthropic"},
		"whitespace":        {[]byte(" \n\t\r\n"), "anthropic"},
		"utf16 little":      {utf16File(`{"env": {}}`, false), "anthropic"},
		"utf16 big":         {utf16File(`{}`, true), "anthropic"},
		"utf16 gateway":     {utf16File(gateway, false), ""},
		"utf16 big gateway": {utf16File(gateway, true), ""},
		"env list":          {[]byte(`{"env": []}`), "anthropic"},
		"env null":          {[]byte(`{"env": null}`), "anthropic"},
		"env number":        {[]byte(`{"env": 5}`), "anthropic"},
		"env string":        {[]byte(`{"env": "x"}`), "anthropic"},
		"unparseable":       {[]byte(`{"env": `), ""},
	} {
		isolateEnv(t)
		repo, claude := repoWith(t)
		writeSettings(t, claude, c.data)
		if p, why := BatonProviderDetail(claudeSpec, repo); p != c.want {
			t.Errorf("%s: provider %q (%s), want %q", name, p, why, c.want)
		}
	}
}

// The notice names the whole path, so that the file of the user, the project's and the
// managed one can be told apart.
func TestTheNoticeNamesTheFullPathOfTheFileThatMadeTheCompanyUnknown(t *testing.T) {
	isolateEnv(t)
	repo, claude := repoWith(t)
	writeSettings(t, claude, []byte(`{"env": `))
	_, why := BatonProviderDetail(claudeSpec, repo)
	if !strings.Contains(why, filepath.Join(claude, "settings.json")) {
		t.Errorf("why %q does not hold the full path %q", why, filepath.Join(claude, "settings.json"))
	}
}

// What an editor leaves in the drop-in folder is not settings.
func TestEditorFilesInTheManagedDropInFolderAreIgnored(t *testing.T) {
	isolateEnv(t)
	dir := t.TempDir()
	managedSettingsPaths = func() []string { return []string{filepath.Join(dir, "managed-settings.json")} }
	d := filepath.Join(dir, "managed-settings.d")
	if err := os.MkdirAll(d, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{".10-gw.json", "10-gw.json.swp", "10-gw.json~", "10-gw.json.tmp"} {
		if err := os.WriteFile(filepath.Join(d, n), []byte(`{"env": `), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if p, why := BatonProviderDetail(claudeSpec, ""); p != "anthropic" {
		t.Errorf("provider %q, %q: a file of an editor counted", p, why)
	}
}

// A link to a regular file that is just over the limit is too large, and is not read.
func TestAClaudeSettingsLinkToAFileOverTheLimitIsTooLarge(t *testing.T) {
	isolateEnv(t)
	repo, claude := repoWith(t)
	target := filepath.Join(t.TempDir(), "big.json")
	if err := os.WriteFile(target, []byte(`{"env": {"X": "`+strings.Repeat("a", settingsFileMax)+`"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(claude, "settings.json")); err != nil {
		t.Skipf("no link: %v", err)
	}
	unknownBecause(t, repo, "is too large")
}

// A settings file committed in a branch that is a link, over the limit, or that git cannot
// give is not known, which is not the same as there being none.
func TestBranchSettingsThatCannotBeReadAreUnknown(t *testing.T) {
	isolateConfig(t)
	repo := commitRepo(t)
	ws := newTestWorkspace(t, repo)
	// none at all: known, nothing
	if blobs, unknown := ws.BranchSettings(repo, "nope"); len(blobs) != 0 || unknown != "" {
		t.Errorf("no settings: %q %q", blobs, unknown)
	}
	// over the limit
	if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	big := []byte(`{"env": {"X": "` + strings.Repeat("a", settingsFileMax) + `"}}`)
	if err := os.WriteFile(filepath.Join(repo, ".claude", "settings.json"), big, 0o600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-m", "big")
	if blobs, unknown := ws.BranchSettings(repo, "nope"); len(blobs) != 0 || !strings.Contains(unknown, "too large") {
		t.Errorf("a blob over the limit: %d blobs, %q", len(blobs), unknown)
	}
	// a link committed in its place (git can hold one on any system)
	linkBlob := gitInput(t, repo, "/dev/zero", "hash-object", "-w", "--stdin")
	gitIn(t, repo, "update-index", "--add", "--cacheinfo", "120000,"+linkBlob+",.claude/settings.json")
	gitIn(t, repo, "commit", "-m", "link")
	if blobs, unknown := ws.BranchSettings(repo, "nope"); len(blobs) != 0 || !strings.Contains(unknown, "not a regular file") {
		t.Errorf("a link: %d blobs, %q", len(blobs), unknown)
	}
}

// A repository where git cannot say what HEAD holds: an error is unknown, not an empty answer.
func TestAGitErrorReadingBranchSettingsIsUnknown(t *testing.T) {
	isolateConfig(t)
	repo := t.TempDir()
	gitIn(t, repo, "init", "--initial-branch=main")
	ws := newTestWorkspace(t, repo)
	if _, unknown := ws.BranchSettings(repo, "new-one"); unknown == "" {
		t.Error("a repository with no commits gave a known, empty answer")
	}
}

// A new branch is cut from HEAD, so the worktree holds the settings of HEAD and not those of
// the working copy: either one saying another company asks.
func TestANewBranchIsJudgedByTheCommitItIsCutFromAndTheWorkingCopyBoth(t *testing.T) {
	isolateEnv(t)
	gateway := `{"env": {"ANTHROPIC_BASE_URL": "https://gw.example"}}`
	judge := func(repo string) string {
		ws := newTestWorkspace(t, repo)
		blobs, unknown := ws.BranchSettings(repo, "brand-new")
		planned := filepath.Join(t.TempDir(), "001-brand-new")
		p, _ := BatonProviderWith(claudeSpec, planned, Sources{Dirs: []string{repo}, Settings: blobs, Unknown: unknown})
		return p
	}
	commit := func(repo, body string) {
		if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, ".claude", "settings.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		gitIn(t, repo, "add", ".")
		gitIn(t, repo, "commit", "-m", "settings")
	}
	// HEAD has the gateway; the working copy has had it taken out and not committed.
	repo := commitRepo(t)
	commit(repo, gateway)
	writeSettings(t, filepath.Join(repo, ".claude"), []byte(`{}`))
	if p := judge(repo); p != "" {
		t.Errorf("the gateway in HEAD, removed from the working copy: provider %q", p)
	}
	// HEAD has none; the working copy adds one.
	repo2 := commitRepo(t)
	commit(repo2, `{}`)
	writeSettings(t, filepath.Join(repo2, ".claude"), []byte(gateway))
	if p := judge(repo2); p != "" {
		t.Errorf("a gateway only in the working copy: provider %q", p)
	}
	// Neither.
	repo3 := commitRepo(t)
	commit(repo3, `{}`)
	if p := judge(repo3); p != "anthropic" {
		t.Errorf("no gateway anywhere: provider %q", p)
	}
}
