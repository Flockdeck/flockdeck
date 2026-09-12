package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// writeConfig lays down a Claude Code configuration for the test to work on.
func writeConfig(t *testing.T, dir string, cfg map[string]any) string {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".claude.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestInheritTrustOnlyCarriesAnAnswerAlreadyGiven is the important one: this
// writes to a security-relevant setting, so it must never invent trust.
func TestInheritTrustOnlyCarriesAnAnswerAlreadyGiven(t *testing.T) {
	dir := t.TempDir()
	trusted := filepath.Clean(filepath.Join(dir, "repo"))
	untrusted := filepath.Clean(filepath.Join(dir, "elsewhere"))
	target := filepath.Clean(filepath.Join(dir, "repo-worktree"))

	writeConfig(t, dir, map[string]any{
		"numStartups": 7,
		"projects": map[string]any{
			trusted:   map[string]any{"hasTrustDialogAccepted": true, "allowedTools": []any{"Bash"}},
			untrusted: map[string]any{"hasTrustDialogAccepted": false},
		},
	})

	if !IsTrusted(trusted) {
		t.Fatal("the trusted directory should be reported as trusted")
	}
	if IsTrusted(untrusted) {
		t.Fatal("a directory with the answer 'no' is not trusted")
	}
	if IsTrusted(target) {
		t.Fatal("an unknown directory is not trusted")
	}

	// Inheriting from something that is not itself trusted must be refused.
	if err := InheritTrust(untrusted, target); err == nil {
		t.Error("expected inheriting from an untrusted directory to be refused")
	}
	if IsTrusted(target) {
		t.Fatal("the refusal must not have written anything")
	}

	// Inheriting from a trusted directory carries the answer over.
	if err := InheritTrust(trusted, target); err != nil {
		t.Fatalf("inherit: %v", err)
	}
	if !IsTrusted(target) {
		t.Error("the worktree should now be trusted")
	}
}

// TestInheritTrustPreservesTheRestOfTheFile matters because this file belongs
// to Claude Code, not to us.
func TestInheritTrustPreservesTheRestOfTheFile(t *testing.T) {
	dir := t.TempDir()
	trusted := filepath.Clean(filepath.Join(dir, "repo"))
	target := filepath.Clean(filepath.Join(dir, "wt"))

	path := writeConfig(t, dir, map[string]any{
		"numStartups":   42,
		"installMethod": "native",
		"tipsHistory":   map[string]any{"a": float64(1)},
		"projects": map[string]any{
			trusted: map[string]any{
				"hasTrustDialogAccepted": true,
				"allowedTools":           []any{"Bash", "Edit"},
				"mcpServers":             map[string]any{"x": "y"},
			},
		},
	})

	if err := InheritTrust(trusted, target); err != nil {
		t.Fatalf("inherit: %v", err)
	}

	var got map[string]any
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("the configuration is no longer valid JSON: %v", err)
	}
	if got["numStartups"] != float64(42) {
		t.Errorf("numStartups = %v, want it untouched", got["numStartups"])
	}
	if got["installMethod"] != "native" {
		t.Errorf("installMethod = %v, want it untouched", got["installMethod"])
	}
	projects := got["projects"].(map[string]any)
	orig := projects[trusted].(map[string]any)
	if len(orig["allowedTools"].([]any)) != 2 {
		t.Error("the original project's settings were damaged")
	}
	if _, ok := orig["mcpServers"]; !ok {
		t.Error("unrelated project fields were dropped")
	}
}

// TestInheritedTrustIsWhereClaudeLooks covers Windows, where Claude Code keys
// its projects by the path with forward slashes. An answer written under the
// backslashed path is never read, and every child of a fan-out stopped on the
// trust question the dialog had offered to answer for it.
func TestInheritedTrustIsWhereClaudeLooks(t *testing.T) {
	dir := t.TempDir()
	trusted := filepath.Clean(filepath.Join(dir, "repo"))
	target := filepath.Clean(filepath.Join(dir, "repo-wt"))
	path := writeConfig(t, dir, map[string]any{
		"projects": map[string]any{filepath.ToSlash(trusted): map[string]any{"hasTrustDialogAccepted": true}},
	})

	if err := InheritTrust(trusted, target); err != nil {
		t.Fatalf("inherit: %v", err)
	}
	var got map[string]any
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	want := target
	if runtime.GOOS == "windows" {
		want = filepath.ToSlash(target)
	}
	entry, _ := got["projects"].(map[string]any)[want].(map[string]any)
	if ok, _ := entry["hasTrustDialogAccepted"].(bool); !ok {
		t.Errorf("no trust recorded under %q, where Claude Code looks; projects = %v", want, got["projects"])
	}
}

// TestInheritTrustCarriesTheExternalImportsAnswer covers the other question a
// fresh checkout is asked: a project whose CLAUDE.md imports files from
// outside it is asked whether to allow that, and a worktree is a project
// Claude Code has never seen, so every child of a fan-out stopped on it.
func TestInheritTrustCarriesTheExternalImportsAnswer(t *testing.T) {
	dir := t.TempDir()
	answered := filepath.Clean(filepath.Join(dir, "answered"))
	unasked := filepath.Clean(filepath.Join(dir, "unasked"))
	path := writeConfig(t, dir, map[string]any{
		"projects": map[string]any{
			answered: map[string]any{
				"hasTrustDialogAccepted":                  true,
				"hasClaudeMdExternalIncludesApproved":     false,
				"hasClaudeMdExternalIncludesWarningShown": true,
			},
			unasked: map[string]any{"hasTrustDialogAccepted": true},
		},
	})
	fromAnswered := filepath.Join(dir, "answered-wt")
	fromUnasked := filepath.Join(dir, "unasked-wt")
	for from, to := range map[string]string{answered: fromAnswered, unasked: fromUnasked} {
		if err := InheritTrust(from, to); err != nil {
			t.Fatalf("inherit %s: %v", from, err)
		}
	}

	var got map[string]any
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	projects := got["projects"].(map[string]any)
	child, _ := projects[claudeProjectKey(fromAnswered)].(map[string]any)
	// A "no" is carried as a "no": the child is not asked, and does not load
	// the imports either, exactly as the project does not.
	if child["hasClaudeMdExternalIncludesApproved"] != false || child["hasClaudeMdExternalIncludesWarningShown"] != true {
		t.Errorf("the worktree's entry is %v, want the project's answer to the imports question", child)
	}
	other, _ := projects[claudeProjectKey(fromUnasked)].(map[string]any)
	if _, asked := other["hasClaudeMdExternalIncludesWarningShown"]; asked {
		t.Errorf("an answer was invented for a project never asked: %v", other)
	}
}

// TestInheritTrustIsIdempotent covers fanning out twice into the same worktree.
func TestInheritTrustIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	trusted := filepath.Clean(filepath.Join(dir, "repo"))
	target := filepath.Clean(filepath.Join(dir, "wt"))

	writeConfig(t, dir, map[string]any{
		"projects": map[string]any{trusted: map[string]any{"hasTrustDialogAccepted": true}},
	})

	for i := 0; i < 2; i++ {
		if err := InheritTrust(trusted, target); err != nil {
			t.Fatalf("inherit %d: %v", i, err)
		}
	}
	if !IsTrusted(target) {
		t.Error("the worktree should be trusted")
	}
}

// TestIsTrustedWithoutAConfiguration covers a machine where Claude has not run.
func TestIsTrustedWithoutAConfiguration(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(t.TempDir(), "missing"))
	if IsTrusted(t.TempDir()) {
		t.Error("nothing is trusted when there is no configuration")
	}
}
