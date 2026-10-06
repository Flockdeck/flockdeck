package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
)

func TestMoreCloudSwitchesAndFoundryAreGateways(t *testing.T) {
	isolateEnv(t)
	for _, name := range []string{"CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX"} {
		t.Setenv(name, "1")
		if got := BatonProvider(claudeSpec); got != "" {
			t.Errorf("%s=1 left claude %q", name, got)
		}
		t.Setenv(name, "")
	}
	for _, name := range []string{"ANTHROPIC_FOUNDRY_BASE_URL", "ANTHROPIC_FOUNDRY_RESOURCE", "ANTHROPIC_BEDROCK_BASE_URL", "ANTHROPIC_VERTEX_BASE_URL"} {
		t.Setenv(name, "my-resource.example")
		if got := BatonProvider(claudeSpec); got != "" {
			t.Errorf("%s left claude %q", name, got)
		}
		t.Setenv(name, "")
	}
	t.Setenv("GOOGLE_GENAI_USE_VERTEXAI", "true")
	if got := BatonProvider(geminiSpec); got != "" {
		t.Errorf("GOOGLE_GENAI_USE_VERTEXAI=true left gemini %q", got)
	}
	t.Setenv("GOOGLE_GENAI_USE_VERTEXAI", "")
	t.Setenv("GOOGLE_VERTEX_BASE_URL", "https://v.example")
	if got := BatonProvider(geminiSpec); got != "" {
		t.Errorf("GOOGLE_VERTEX_BASE_URL left gemini %q", got)
	}
}

func TestSettingsValuesThatAreNumbersAndBooleansCount(t *testing.T) {
	cfg := isolateEnv(t)
	for _, body := range []string{
		`{"env": {"CLAUDE_CODE_USE_BEDROCK": 1}}`, `{"env": {"CLAUDE_CODE_USE_BEDROCK": true}}`, `{"env": {"CLAUDE_CODE_USE_BEDROCK": "1"}}`,
	} {
		if err := os.WriteFile(filepath.Join(cfg, "settings.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := BatonProvider(claudeSpec); got != "" {
			t.Errorf("%s left claude %q", body, got)
		}
	}
	if err := os.WriteFile(filepath.Join(cfg, "settings.json"), []byte(`{"env": {"CLAUDE_CODE_USE_BEDROCK": 0}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := BatonProvider(claudeSpec); got != "anthropic" {
		t.Errorf("a zero switch made claude %q", got)
	}
}

func TestSettingsWithABOMCommentsAndTrailingCommasAreRead(t *testing.T) {
	cfg := isolateEnv(t)
	body := "\xef\xbb\xbf{\n  // the gateway\n  \"env\": {\n    \"ANTHROPIC_BASE_URL\": \"https://corp.example/x\", /* proxy */\n  },\n}\n"
	if err := os.WriteFile(filepath.Join(cfg, "settings.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	p, why := BatonProviderDetail(claudeSpec, "")
	if p != "" || !strings.Contains(why, "corp.example") {
		t.Errorf("provider %q, why %q", p, why)
	}
}

// A settings file that cannot be read may hold a gateway: the company is not known,
// and the notice says why, and does not look away.
func TestASettingsFileThatCannotBeReadMakesTheCompanyUnknownAndSaysSo(t *testing.T) {
	cfg := isolateEnv(t)
	if err := os.WriteFile(filepath.Join(cfg, "settings.json"), []byte(`{"env": {`), 0o600); err != nil {
		t.Fatal(err)
	}
	p, why := BatonProviderDetail(claudeSpec, "")
	if p != "" || !strings.Contains(why, "could not be read") || !strings.Contains(why, "settings.json") {
		t.Errorf("provider %q, why %q", p, why)
	}
	// It does not touch another vendor's CLI.
	if got := BatonProvider(codexSpec); got != "openai" {
		t.Errorf("codex: %q", got)
	}
}

func TestProjectSettingsAreReadFromParentsUpToTheGitRoot(t *testing.T) {
	isolateEnv(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claude", "settings.json"), []byte(`{"env": {"CLAUDE_CODE_USE_VERTEX": "1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(root, "services", "api", "cmd")
	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Fatal(err)
	}
	if p, _ := BatonProviderDetail(claudeSpec, deep); p != "" {
		t.Errorf("settings at the git root were not read from a folder below it: %q", p)
	}
	// Past the git root they are not: a folder above the repository has its own.
	above := t.TempDir()
	if err := os.MkdirAll(filepath.Join(above, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(above, ".claude", "settings.json"), []byte(`{"env": {"CLAUDE_CODE_USE_VERTEX": "1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(above, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if p, _ := BatonProviderDetail(claudeSpec, repo); p != "anthropic" {
		t.Errorf("settings above the git root reached the project: %q", p)
	}
}

// A worktree that is not there yet is judged by the checkout it is cut from.
func TestAWorktreeNotMadeYetIsJudgedByTheCheckoutItIsCutFrom(t *testing.T) {
	isolateEnv(t)
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(source, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, ".claude", "settings.local.json"), []byte(`{"env": {"ANTHROPIC_BASE_URL": "https://corp.example"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	planned := filepath.Join(t.TempDir(), "repo-wt")
	if p, _ := BatonProviderDetail(claudeSpec, planned); p != "anthropic" {
		t.Errorf("a path with no settings: %q", p)
	}
	if p, _ := BatonProviderDetail(claudeSpec, planned, source); p != "" {
		t.Errorf("the checkout's settings were not read for the worktree: %q", p)
	}
}

func TestCodexConfigWithAnotherProviderOrBaseURLIsAGateway(t *testing.T) {
	isolateEnv(t)
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("model = \"gpt-5\"\n")
	if got := BatonProvider(codexSpec); got != "openai" {
		t.Errorf("a plain config: %q", got)
	}
	write("model_provider = \"openai\"\n")
	if got := BatonProvider(codexSpec); got != "openai" {
		t.Errorf("openai as the provider: %q", got)
	}
	write("model_provider = \"azure\"\n[model_providers.azure]\nbase_url = \"https://x.openai.azure.com/openai\"\n")
	if p, why := BatonProviderDetail(codexSpec, ""); p != "" || why == "" {
		t.Errorf("another provider: %q, %q", p, why)
	}
	write("[model_providers.p]\nbase_url = \"https://api.openai.com/v1\"\n")
	if got := BatonProvider(codexSpec); got != "openai" {
		t.Errorf("the official base_url: %q", got)
	}
	write("model_provider = \"p\"\n[model_providers.p]\nbase_url = \"https://litellm.example/v1\"\n")
	if got := BatonProvider(codexSpec); got != "" {
		t.Errorf("a gateway base_url: %q", got)
	}
}

// A wrapper that runs Claude Code under another name is still Claude Code, by its id.
func TestAWrappedClaudeIsScopedByItsIDNotItsProgram(t *testing.T) {
	isolateEnv(t)
	wrapped := agent.Spec{ID: "claude-code", Name: "Claude Code", Exe: "node"}
	if got := BatonProvider(wrapped); got != "anthropic" {
		t.Errorf("a wrapper called claude-code: %q", got)
	}
	t.Setenv("ANTHROPIC_BASE_URL", "https://gw.example")
	if got := BatonProvider(wrapped); got != "" {
		t.Errorf("a gateway did not reach the wrapper: %q", got)
	}
	other := agent.Spec{ID: "mytool", Name: "Mine", Exe: "node"}
	if got := BatonProvider(other); got != "node" {
		t.Errorf("an agent that is nobody's: %q", got)
	}
}

func TestATrailingDotOnTheOfficialHostIsStillOfficial(t *testing.T) {
	isolateEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", "https://api.anthropic.com./v1")
	if got := BatonProvider(claudeSpec); got != "anthropic" {
		t.Errorf("a trailing dot made the official host a gateway: %q", got)
	}
}

// An agent's own env entry that is empty does not hide the same setting in the
// environment. (Documented: the entry is not an override of it.)
func TestAnEmptyEntryInTheAgentsEnvDoesNotMaskTheEnvironment(t *testing.T) {
	isolateEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", "https://gw.example")
	own := claudeSpec
	own.Env = []string{"ANTHROPIC_BASE_URL="}
	if got := BatonProvider(own); got != "" {
		t.Errorf("an empty entry masked the environment: %q", got)
	}
}
