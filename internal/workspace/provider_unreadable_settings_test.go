package workspace

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// repoWith is a folder that is a git repository with a .claude folder in it.
func repoWith(t *testing.T) (repo, claude string) {
	t.Helper()
	repo = t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	claude = filepath.Join(repo, ".claude")
	if err := os.MkdirAll(claude, 0o700); err != nil {
		t.Fatal(err)
	}
	return repo, claude
}

func unknownBecause(t *testing.T, repo, mention string) {
	t.Helper()
	p, why := BatonProviderDetail(claudeSpec, repo)
	if p != "" || !strings.Contains(why, "could not be read") || !strings.Contains(why, mention) {
		t.Errorf("provider %q, why %q, want the company unknown and %q said", p, why, mention)
	}
}

// A settings file that cannot be opened is not "no settings": it may hold a gateway.
func TestAClaudeSettingsFileThatCannotBeOpenedMakesTheCompanyUnknown(t *testing.T) {
	isolateEnv(t)
	repo, claude := repoWith(t)
	// A folder where the file should be.
	if err := os.MkdirAll(filepath.Join(claude, "settings.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	unknownBecause(t, repo, "is a folder")
}

func TestAClaudeSettingsFileThatIsRefusedMakesTheCompanyUnknown(t *testing.T) {
	isolateEnv(t)
	repo, claude := repoWith(t)
	f := filepath.Join(claude, "settings.local.json")
	if err := os.WriteFile(f, []byte(`{"env": {"ANTHROPIC_BASE_URL": "https://gw.example"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	makeUnreadable(t, f)
	unknownBecause(t, repo, "settings.local.json")
}

// Over 1 MB is not read at all, and the company is not known.
func TestAClaudeSettingsFileOverAMegabyteIsNotReadAndMakesTheCompanyUnknown(t *testing.T) {
	isolateEnv(t)
	repo, claude := repoWith(t)
	big := `{"env": {"X": "` + strings.Repeat("a", 1<<20+10) + `"}}`
	if err := os.WriteFile(filepath.Join(claude, "settings.json"), []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	unknownBecause(t, repo, "settings.json is too large")
}

// A file where a folder should be is a place with no settings, and not an unknown.
func TestAClaudeFolderThatIsAFileHasNoSettings(t *testing.T) {
	isolateEnv(t)
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".claude"), []byte("not a folder"), 0o600); err != nil {
		t.Fatal(err)
	}
	if p, why := BatonProviderDetail(claudeSpec, repo); p != "anthropic" {
		t.Errorf("provider %q, why %q", p, why)
	}
}

func TestManagedDropInSettingsAreReadAndFailClosed(t *testing.T) {
	isolateEnv(t)
	dir := t.TempDir()
	managed := filepath.Join(dir, "managed-settings.json")
	managedSettingsPaths = func() []string { return []string{managed} }
	d := filepath.Join(dir, "managed-settings.d")
	if err := os.MkdirAll(d, 0o700); err != nil {
		t.Fatal(err)
	}
	// Nothing in the folder: not an override.
	if p, why := BatonProviderDetail(claudeSpec, ""); p != "anthropic" {
		t.Errorf("an empty drop-in folder: %q, %q", p, why)
	}
	// A drop-in with a gateway.
	f := filepath.Join(d, "10-gateway.json")
	if err := os.WriteFile(f, []byte(`{"env": {"ANTHROPIC_BASE_URL": "https://gw.example"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if p, _ := BatonProviderDetail(claudeSpec, ""); p != "" {
		t.Errorf("a drop-in gateway was not seen: %q", p)
	}
	// A drop-in that cannot be read as JSON.
	if err := os.WriteFile(f, []byte(`{"env": `), 0o600); err != nil {
		t.Fatal(err)
	}
	if p, why := BatonProviderDetail(claudeSpec, ""); p != "" || !strings.Contains(why, "10-gateway.json") {
		t.Errorf("a broken drop-in: %q, %q", p, why)
	}
}

func TestAManagedDropInFolderThatCannotBeListedMakesTheCompanyUnknown(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a folder cannot be made unreadable to this user here")
	}
	isolateEnv(t)
	dir := t.TempDir()
	managedSettingsPaths = func() []string { return []string{filepath.Join(dir, "managed-settings.json")} }
	d := filepath.Join(dir, "managed-settings.d")
	if err := os.MkdirAll(d, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(d, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(d, 0o700) })
	if p, why := BatonProviderDetail(claudeSpec, ""); p != "" || !strings.Contains(why, "managed-settings.d") {
		t.Errorf("provider %q, why %q", p, why)
	}
}
