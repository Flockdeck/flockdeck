package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexConfigIsReadAsTablesAndCommentsNotJustLines(t *testing.T) {
	isolateEnv(t)
	dir := os.Getenv("CODEX_HOME")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name, body, want string
	}{
		{"a comment after the value", "model_provider = \"openai\" # the default\n", "openai"},
		{"a commented-out gateway", "# base_url = \"https://gw.example/v1\"\n", "openai"},
		{"a provider after a trailing comment", "model_provider = \"azure\" # work\n", ""},
		{"a top level openai_base_url", "openai_base_url = \"https://gw.example/v1\"\n", ""},
		{"the official openai_base_url", "openai_base_url = \"https://api.openai.com/v1\"\n", "openai"},
		{"a model_providers table", "model_provider = \"corp\"\n[model_providers.corp]\nname = \"corp\"\nbase_url = 'https://gw.example/v1' # proxy\n", ""},
		{"an inline table", "model_provider = \"corp\"\nmodel_providers = { corp = { base_url = \"https://gw.example/v1\" } }\n", ""},
		{"a model_provider inside a table is not the top level one", "[profiles.x]\nmodel_provider = \"azure\"\n", "openai"},
		{"a byte order mark", "\ufeffmodel_provider = \"azure\"\n", ""},
	}
	for _, c := range cases {
		write(c.body)
		if got := BatonProvider(codexSpec); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestACodexConfigOverAMegabyteOrUnreadableMakesTheCompanyUnknown(t *testing.T) {
	isolateEnv(t)
	dir := os.Getenv("CODEX_HOME")
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(strings.Repeat("# x\n", 300000)), 0o600); err != nil {
		t.Fatal(err)
	}
	p, why := BatonProviderDetail(codexSpec, "")
	if p != "" || !strings.Contains(why, "too large") {
		t.Errorf("provider %q, why %q", p, why)
	}
	// A config.toml that is a folder cannot be read.
	_ = os.Remove(filepath.Join(dir, "config.toml"))
	if err := os.Mkdir(filepath.Join(dir, "config.toml"), 0o700); err != nil {
		t.Fatal(err)
	}
	if p, why := BatonProviderDetail(codexSpec, ""); p != "" || !strings.Contains(why, "is a folder") {
		t.Errorf("provider %q, why %q", p, why)
	}
}

func TestWithNoRepositoryOnlyTheFolderItselfIsRead(t *testing.T) {
	isolateEnv(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claude", "settings.json"), []byte(`{"env": {"CLAUDE_CODE_USE_VERTEX": "1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Fatal(err)
	}
	if p, _ := BatonProviderDetail(claudeSpec, deep); p != "anthropic" {
		t.Errorf("a folder above a folder that is in no repository was read: %q", p)
	}
	if p, _ := BatonProviderDetail(claudeSpec, root); p != "" {
		t.Errorf("the folder's own settings were not read: %q", p)
	}
}

func TestManagedSettingsAreRead(t *testing.T) {
	isolateEnv(t)
	f := filepath.Join(t.TempDir(), "managed-settings.json")
	if err := os.WriteFile(f, []byte(`{"env": {"ANTHROPIC_BASE_URL": "https://gw.example"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	managedSettingsPaths = func() []string { return []string{f} }
	if p, _ := BatonProviderDetail(claudeSpec, ""); p != "" {
		t.Errorf("a managed gateway was not seen: %q", p)
	}
}
