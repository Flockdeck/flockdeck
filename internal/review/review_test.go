package review

import (
	"encoding/json"
	"testing"
)

// A read-only command is allowed, and says why -- the reason a permission
// decision carries back to the agent.
func TestDecideAllowsAReadOnlyCommand(t *testing.T) {
	for _, cmd := range []string{
		"ls -la", "git status", "git status --short", "cat internal/review/review.go",
		"go env GOPATH", "git log -5", "pwd", "grep -rn TODO .",
	} {
		d := Decide("Bash", `{"command":`+quote(cmd)+`}`)
		if !d.Allow {
			t.Errorf("Decide(Bash, %q).Allow = false, want true", cmd)
		}
		if d.Reason == "" {
			t.Errorf("Decide(Bash, %q).Reason is empty, want an explanation", cmd)
		}
	}
}

// Anything that writes, deletes, sends network traffic or chains into
// something else is left to ask, exactly as it always has been -- including a
// safe verb smuggling something else in behind it.
func TestDecideAsksForAnythingElse(t *testing.T) {
	for _, cmd := range []string{
		"rm -rf /tmp/x",
		"git commit -m x",
		"git push",
		"git branch -D main",
		"curl http://example.com",
		"echo hi > out.txt",
		"cat foo && rm bar",
		"cat foo; rm bar",
		"cat foo | rm bar",
		"cat $(rm bar)",
		"cat `rm bar`",
		"(rm -rf /)",
		"cat {a,b}",
		"npm install",
		"chmod +x a.sh",
		"sudo ls",
	} {
		d := Decide("Bash", `{"command":`+quote(cmd)+`}`)
		if d.Allow {
			t.Errorf("Decide(Bash, %q).Allow = true, want false", cmd)
		}
		if d.Reason != "" {
			t.Errorf("Decide(Bash, %q).Reason = %q, want empty", cmd, d.Reason)
		}
	}
}

// Only Bash is judged: a file write is exactly the request auto-review must
// never wave through, and an unrecognised tool is unknown territory.
func TestDecideAsksForEveryToolButBash(t *testing.T) {
	for _, tc := range []struct{ tool, input string }{
		{"Write", `{"filePath":"a.go","content":"package a"}`},
		{"Edit", `{"filePath":"a.go","oldString":"a","newString":"b"}`},
		{"MultiEdit", `{"filePath":"a.go"}`},
		{"AskUserQuestion", `{"questions":[]}`},
		{"mcp__example__tool", `{}`},
	} {
		if d := Decide(tc.tool, tc.input); d.Allow {
			t.Errorf("Decide(%s, ...).Allow = true, want false", tc.tool)
		}
	}
}

// Nothing to read -- no tool input at all, or input that is not the JSON
// object Decide expects -- is asked rather than misread as safe.
func TestDecideAsksWhenThereIsNothingToRead(t *testing.T) {
	for _, in := range []string{"", "not json", `{}`, `{"command":""}`, `{"command":123}`} {
		if d := Decide("Bash", in); d.Allow {
			t.Errorf("Decide(Bash, %q).Allow = true, want false", in)
		}
	}
}

// quote renders s as a JSON string literal, so a command holding a quote, a
// backslash or a backtick still builds a tool_input Decide can parse.
func quote(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}
