package manifest

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// lastActions reads opencode's `debug agent` output and returns, for each tool,
// the action of the last rule that names it with the pattern * (the last
// matching rule is the one that applies). The weekly canary
// (TestCanaryLaunchEnvAsks, built with the acp_canary tag) uses it; it lives
// here, without the tag, so that every test run checks it.
func lastActions(out []byte, tools ...string) (map[string]string, error) {
	i := strings.IndexByte(string(out), '{')
	if i < 0 {
		return nil, os.ErrInvalid
	}
	var agent struct {
		Permission []struct {
			Permission, Action, Pattern string
		} `json:"permission"`
	}
	if err := json.Unmarshal(out[i:], &agent); err != nil {
		return nil, err
	}
	got := map[string]string{}
	for _, r := range agent.Permission {
		for _, tool := range tools {
			if r.Permission == tool && r.Pattern == "*" {
				got[tool] = r.Action
			}
		}
	}
	return got, nil
}

func TestTheCanaryReadsOpencodesResolvedRules(t *testing.T) {
	rule := func(tool, action, pattern string) string {
		return `{"permission":"` + tool + `","action":"` + action + `","pattern":"` + pattern + `"}`
	}
	agent := func(rules ...string) string {
		return `{"name":"build","mode":"primary","permission":[` + strings.Join(rules, ",") + `],"options":{}}`
	}
	for name, tc := range map[string]struct {
		out        string
		bash, edit string
		bad        bool
	}{
		"asks": {out: agent(rule("*", "allow", "*"), rule("bash", "ask", "*"), rule("edit", "ask", "*")), bash: "ask", edit: "ask"},
		"an ask then an allow": {out: agent(rule("*", "allow", "*"), rule("bash", "ask", "*"), rule("edit", "ask", "*"),
			rule("bash", "allow", "*")), bash: "allow", edit: "ask"},
		"an allow then an ask": {out: agent(rule("bash", "allow", "*"), rule("bash", "ask", "*")), bash: "ask"},
		"no rule for the tool": {out: agent(rule("*", "allow", "*"), rule("read", "ask", "*.env")), bash: "", edit: ""},
		"a pattern is not *":   {out: agent(rule("bash", "ask", "git *")), bash: ""},
		"text before the object": {out: "loading config...\n" + agent(rule("bash", "ask", "*"), rule("edit", "ask", "*")),
			bash: "ask", edit: "ask"},
		"no object": {out: "no agent called build", bad: true},
		"junk":      {out: "{not json", bad: true},
	} {
		got, err := lastActions([]byte(tc.out), "bash", "edit")
		if tc.bad {
			if err == nil {
				t.Errorf("%s: no error, got %v", name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got["bash"] != tc.bash || got["edit"] != tc.edit {
			t.Errorf("%s: got %v, want bash %q edit %q", name, got, tc.bash, tc.edit)
		}
	}
}
