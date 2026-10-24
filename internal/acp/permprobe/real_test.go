package permprobe

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRealOpencodeFixtures runs the check against a real opencode in fixture
// projects. It does nothing unless FLOCKDECK_REAL_OPENCODE is the absolute path
// of the opencode program. It gives the program a home, config, data, cache and
// state folder of its own under the test's temporary directory, passes it no
// credentials, sends no prompt and starts no session: `opencode debug agent
// build` only prints the rules.
func TestRealOpencodeFixtures(t *testing.T) {
	bin := os.Getenv("FLOCKDECK_REAL_OPENCODE")
	if bin == "" {
		t.Skip("FLOCKDECK_REAL_OPENCODE is not set")
	}
	if !filepath.IsAbs(bin) {
		t.Fatal("FLOCKDECK_REAL_OPENCODE must be an absolute path")
	}
	const plainSetting = `{"permission":{"bash":"ask","edit":"ask"}}`
	args := []string{"agent", "list"}

	// newEnv is a fresh isolated environment, and the user's own config file.
	newEnv := func(t *testing.T, userConfig string) []string {
		iso := t.TempDir()
		env := []string{"PATH=" + os.Getenv("PATH"), "CI=1"}
		for k, v := range map[string]string{
			"HOME": "home", "USERPROFILE": "home", "XDG_CONFIG_HOME": "config", "XDG_DATA_HOME": "data",
			"XDG_CACHE_HOME": "cache", "XDG_STATE_HOME": "state", "APPDATA": "appdata", "LOCALAPPDATA": "localappdata", "TMPDIR": "tmp",
		} {
			dir := filepath.Join(iso, v)
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			env = append(env, k+"="+dir)
		}
		if userConfig != "" {
			dir := filepath.Join(iso, "config", "opencode")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "opencode.json"), []byte(userConfig), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return env
	}
	project := func(t *testing.T, files map[string]string) string {
		dir := t.TempDir()
		for name, body := range files {
			p := filepath.Join(dir, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}

	for name, tc := range map[string]struct {
		user    string
		files   map[string]string
		unasked bool
		keeps   []string // pieces the setting must hold
		denies  []string // commands or tools the final rules must still deny
	}{
		"an empty project": {},
		"the user's granular shell and denied edits": {
			user:   `{"permission":{"bash":{"*":"allow","git *":"allow","rm *":"deny"},"edit":"deny"}}`,
			keeps:  []string{`"rm *":"deny"`, `"edit":"deny"`},
			denies: []string{"bash:rm *", "edit:*"},
		},
		"the project's shell allow": {
			files: map[string]string{"opencode.json": `{"permission":{"bash":"allow","edit":"allow"}}`},
		},
		"the project's denials": {
			files:  map[string]string{"opencode.json": `{"permission":{"bash":{"*":"ask","curl *":"deny"},"edit":{"*":"ask","*.env":"deny"}}}`},
			keeps:  []string{`"curl *":"deny"`, `"*.env":"deny"`},
			denies: []string{"bash:curl *", "edit:*.env"},
		},
		"an agent-level allow in opencode.json": {
			files:   map[string]string{"opencode.json": `{"agent":{"build":{"permission":{"bash":"allow","edit":"allow"}}}}`},
			unasked: true,
		},
		"a default agent with an allow": {
			files:   map[string]string{"opencode.json": `{"default_agent":"x","agent":{"x":{"mode":"primary","permission":{"bash":"allow"}}}}`},
			unasked: true,
		},
		"a default agent in a file with an allow": {
			files: map[string]string{
				"opencode.json":        `{"default_agent":"x"}`,
				".opencode/agent/x.md": "---\ndescription: x\nmode: primary\npermission:\n  bash: allow\n---\nx\n",
			},
			unasked: true,
		},
		"a subagent the task tool starts with an allow": {
			files:   map[string]string{"opencode.json": `{"agent":{"general":{"permission":{"bash":"allow"}}}}`},
			unasked: true,
		},
		"a subagent with an edit allow": {
			files:   map[string]string{"opencode.json": `{"agent":{"explore":{"permission":{"edit":"allow"}}}}`},
			unasked: true,
		},
		"an agent that denies": {
			files: map[string]string{"opencode.json": `{"agent":{"x":{"mode":"primary","permission":{"bash":"deny","edit":"deny"}}}}`},
		},
		"an agent file with an allow": {
			files:   map[string]string{".opencode/agent/build.md": "---\ndescription: build\npermission:\n  bash: allow\n  edit: allow\n---\nbuild things\n"},
			unasked: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			env := newEnv(t, tc.user)
			dir := project(t, tc.files)
			res := Check(context.Background(), Exec, bin, args, dir, env, "OPENCODE_CONFIG_CONTENT", plainSetting, "build", "bash", "edit")
			t.Logf("setting %s; unasked %v; %s", res.Inline, res.Unasked, res.Note)
			if !strings.HasPrefix(res.Note, "checked") {
				t.Fatalf("the check did not run cleanly: %s", res.Note)
			}
			if res.Unasked != tc.unasked {
				t.Errorf("unasked = %v, want %v", res.Unasked, tc.unasked)
			}
			for _, k := range tc.keeps {
				if !strings.Contains(res.Inline, k) {
					t.Errorf("the setting lost %s: %s", k, res.Inline)
				}
			}
			// The rules the agent ends with still deny.
			out, err := Exec(context.Background(), bin, args, dir, append(append([]string(nil), env...), "OPENCODE_CONFIG_CONTENT="+res.Inline))
			if err != nil {
				t.Fatal(err)
			}
			agents, err := ParseAgents(out)
			if err != nil {
				t.Fatal(err)
			}
			var rules []Rule
			for _, a := range agents {
				if a.Name == "build" {
					rules = a.Rules
				}
			}
			for _, d := range tc.denies {
				tool, pattern, _ := strings.Cut(d, ":")
				found := false
				for _, r := range For(rules, tool) {
					if r.Pattern == pattern && r.Action == "deny" {
						found = true
					}
				}
				if !found {
					t.Errorf("no deny for %s in the final rules", d)
				}
			}
		})
	}
}
