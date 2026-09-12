package chat

import (
	"encoding/json"
	"testing"
)

// A call is drawn as what it acts on. As JSON, a write's line was the file it
// was writing, escaped onto one line.
func TestACallIsDrawnAsWhatItActsOn(t *testing.T) {
	tests := []struct {
		name, args, want string
	}{
		{"write_file", `{"path":"src/a.go","content":"package main\n\nfunc main() {}\n"}`, "write_file src/a.go"},
		{"run_command", `{"command":"go test ./...","timeout_seconds":60}`, "run_command go test ./..."},
		{"grep", `{"pattern":"TODO","path":"internal"}`, `grep "TODO" in internal`},
		{"glob", `{"pattern":"**/*.go"}`, `glob "**/*.go"`},
		{"list_dir", `{}`, "list_dir"},
		{"mystery", `{"x":1}`, `mystery {"x":1}`},
	}
	for _, tc := range tests {
		got := describeCall(ToolCall{Name: tc.name, Args: json.RawMessage(tc.args)}, 80)
		if got != tc.want {
			t.Errorf("describeCall(%s %s) = %q, want %q", tc.name, tc.args, got, tc.want)
		}
	}
}
