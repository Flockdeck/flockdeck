package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
)

var (
	claudeSpec = agent.Spec{ID: "claude", Name: "Claude Code", Exe: "claude"}
	codexSpec  = agent.Spec{ID: "codex", Name: "Codex", Exe: "codex"}
	geminiSpec = agent.Spec{ID: "gemini", Name: "Gemini", Exe: "gemini"}
)

// isolateEnv clears the settings that could say a CLI goes through a gateway, and
// puts Claude Code's user settings in an empty folder, so a test sets only what it
// means to.
func isolateEnv(t *testing.T) string {
	t.Helper()
	for _, name := range []string{
		"ANTHROPIC_BASE_URL", "ANTHROPIC_BEDROCK_BASE_URL", "ANTHROPIC_VERTEX_BASE_URL", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX",
		"OPENAI_BASE_URL", "OPENAI_API_BASE", "AZURE_OPENAI_ENDPOINT", "CODEX_OSS_BASE_URL",
		"GOOGLE_GEMINI_BASE_URL", "GEMINI_API_BASE_URL", "GOOGLE_VERTEX_BASE_URL",
	} {
		t.Setenv(name, "")
	}
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	t.Setenv("CODEX_HOME", t.TempDir())
	oldManaged := managedSettingsPaths
	managedSettingsPaths = func() []string { return nil }
	t.Cleanup(func() { managedSettingsPaths = oldManaged })
	return dir
}

func TestAGatewayIsOnlyOneThatAffectsThatCLI(t *testing.T) {
	isolateEnv(t)
	t.Setenv("OPENAI_BASE_URL", "https://gateway.example/v1")
	if got := BatonProvider(claudeSpec); got != "anthropic" {
		t.Errorf("an OpenAI variable changed claude: %q", got)
	}
	if got := BatonProvider(geminiSpec); got != "gemini" {
		t.Errorf("an OpenAI variable changed gemini: %q", got)
	}
	if got := BatonProvider(codexSpec); got != "" {
		t.Errorf("OPENAI_BASE_URL did not make codex unknown: %q", got)
	}
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("ANTHROPIC_BASE_URL", "https://gateway.example")
	if got := BatonProvider(codexSpec); got != "openai" {
		t.Errorf("an Anthropic variable changed codex: %q", got)
	}
	if got := BatonProvider(claudeSpec); got != "" {
		t.Errorf("ANTHROPIC_BASE_URL did not make claude unknown: %q", got)
	}
	t.Setenv("ANTHROPIC_BASE_URL", "")
	t.Setenv("GOOGLE_GEMINI_BASE_URL", "https://gateway.example")
	if got := BatonProvider(geminiSpec); got != "" {
		t.Errorf("GOOGLE_GEMINI_BASE_URL did not make gemini unknown: %q", got)
	}
	if got := BatonProvider(claudeSpec); got != "anthropic" {
		t.Errorf("a Google variable changed claude: %q", got)
	}
}

func TestEmptyZeroFalseAndTheOfficialHostAreNotAGateway(t *testing.T) {
	isolateEnv(t)
	for _, v := range []string{"", "0", "false", "FALSE", "off", "  ", "https://api.anthropic.com", "https://api.anthropic.com/v1", "api.anthropic.com"} {
		t.Setenv("ANTHROPIC_BASE_URL", v)
		if got := BatonProvider(claudeSpec); got != "anthropic" {
			t.Errorf("ANTHROPIC_BASE_URL=%q made claude %q", v, got)
		}
	}
	t.Setenv("ANTHROPIC_BASE_URL", "")
	for _, v := range []string{"0", "false", ""} {
		t.Setenv("CLAUDE_CODE_USE_BEDROCK", v)
		if got := BatonProvider(claudeSpec); got != "anthropic" {
			t.Errorf("CLAUDE_CODE_USE_BEDROCK=%q made claude %q", v, got)
		}
	}
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "1")
	if got := BatonProvider(claudeSpec); got != "" {
		t.Errorf("CLAUDE_CODE_USE_BEDROCK=1 left claude %q", got)
	}
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "")
	t.Setenv("OPENAI_BASE_URL", "https://api.openai.com/v1")
	if got := BatonProvider(codexSpec); got != "openai" {
		t.Errorf("the official OpenAI host made codex %q", got)
	}
	// A look-alike host is not the official one.
	t.Setenv("OPENAI_BASE_URL", "https://api.openai.com.evil.example/v1")
	if got := BatonProvider(codexSpec); got != "" {
		t.Errorf("a look-alike host was taken for the official one: %q", got)
	}
}

func TestTheAgentsOwnEnvBlockAndClaudeSettingsAreRead(t *testing.T) {
	cfg := isolateEnv(t)
	own := claudeSpec
	own.Env = []string{"ANTHROPIC_BASE_URL=https://proxy.example"}
	if got := BatonProvider(own); got != "" {
		t.Errorf("the agent's own env block was not read: %q", got)
	}
	// Claude Code's user settings.
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(cfg, "settings.json"), `{"env": {"ANTHROPIC_BASE_URL": "https://corp-gateway.example/anthropic"}}`)
	p, why := BatonProviderDetail(claudeSpec, "")
	if p != "" || !strings.Contains(why, "corp-gateway.example") {
		t.Errorf("user settings: provider %q, why %q", p, why)
	}
	// Not for another vendor's CLI.
	if got := BatonProvider(codexSpec); got != "openai" {
		t.Errorf("Claude Code's settings changed codex: %q", got)
	}
	if err := os.Remove(filepath.Join(cfg, "settings.json")); err != nil {
		t.Fatal(err)
	}
	// The project's.
	proj := t.TempDir()
	write(filepath.Join(proj, ".claude", "settings.local.json"), `{"env": {"CLAUDE_CODE_USE_VERTEX": "1"}}`)
	if p, _ := BatonProviderDetail(claudeSpec, proj); p != "" {
		t.Errorf("the project's local settings were not read: %q", p)
	}
	if got := BatonProvider(claudeSpec); got != "anthropic" {
		t.Errorf("another project's settings reached this one: %q", got)
	}
	// A settings file that is not JSON may hold a gateway: the company is not known.
	write(filepath.Join(proj, ".claude", "settings.local.json"), `not json`)
	if p, why := BatonProviderDetail(claudeSpec, proj); p != "" || !strings.Contains(why, "could not be read") {
		t.Errorf("a damaged settings file did not make the company unknown: %q %q", p, why)
	}
}

// An endpoint is said by its host, never with the user, password, path or query.
func TestAProviderIsShownByHostOnly(t *testing.T) {
	api := agent.Spec{ID: "gw", Runner: agent.RunnerAPI, API: agent.APISpec{Wire: "openai", BaseURL: "https:" + "//user:sekret@gw.example:8443/v1?key=abc"}}
	got := BatonProviderShown(api, "")
	if got != "openai via gw.example" {
		t.Errorf("BatonProviderShown = %q", got)
	}
	for _, bad := range []string{"sekret", "user", "8443", "abc", "/v1"} {
		if strings.Contains(got, bad) {
			t.Errorf("%q leaks %q", got, bad)
		}
	}
	if h := HostOf("not a url at all://"); h != "" {
		t.Errorf("HostOf(garbage) = %q", h)
	}
}
