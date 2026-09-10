package agent

import (
	"slices"
	"testing"
)

// TestBuildArgvClaudeParity is the promise that nothing regresses for somebody
// who only ever runs Claude. The expectations below are exactly what
// session.ClaudeArgs produced before an agent was a choice, written out here
// rather than compared against it, because session will come to import this
// package and a test cannot import it back.
func TestBuildArgvClaudeParity(t *testing.T) {
	const id = "11111111-2222-3333-4444-555555555555"
	claude := claudeSpec()

	tests := []struct {
		name   string
		resume bool
		tokens Tokens
		want   []string
	}{
		{
			name:   "fresh pane with a settings file",
			tokens: Tokens{Session: id, Settings: "/tmp/s.json"},
			want:   []string{"claude", "--session-id", id, "--settings", "/tmp/s.json"},
		},
		{
			name:   "fresh pane with an opening task",
			tokens: Tokens{Session: id, Settings: "/tmp/s.json", Prompt: "fix the tests"},
			want:   []string{"claude", "--session-id", id, "--settings", "/tmp/s.json", "fix the tests"},
		},
		{
			name:   "an opening task that begins with a dash is guarded",
			tokens: Tokens{Session: id, Prompt: "-p is not what I meant"},
			want:   []string{"claude", "--session-id", id, "--", "-p is not what I meant"},
		},
		{
			name:   "no settings file, no task",
			tokens: Tokens{Session: id},
			want:   []string{"claude", "--session-id", id},
		},
		{
			name:   "a chosen model joins the rest",
			tokens: Tokens{Session: id, Settings: "/tmp/s.json", Model: "opus"},
			want:   []string{"claude", "--session-id", id, "--settings", "/tmp/s.json", "--model", "opus"},
		},
		{
			name:   "restored pane resumes its conversation",
			resume: true,
			tokens: Tokens{Session: id, Settings: "/tmp/s.json"},
			want:   []string{"claude", "--resume", id, "--settings", "/tmp/s.json"},
		},
		{
			name:   "a restored pane is never given the opening task again",
			resume: true,
			tokens: Tokens{Session: id, Settings: "/tmp/s.json", Prompt: "fix the tests"},
			want:   []string{"claude", "--resume", id, "--settings", "/tmp/s.json"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildArgv(claude, tt.resume, tt.tokens)
			if !slices.Equal(got, tt.want) {
				t.Errorf("argv\n got %q\nwant %q", got, tt.want)
			}
		})
	}
}

// TestBuildArgvGroups covers the rule that keeps an option with its value: a
// token with no value takes the flag away with it, or the flag is left behind
// to swallow whatever comes next as its value.
func TestBuildArgvGroups(t *testing.T) {
	spec := Spec{
		ID: "demo", Runner: RunnerCLI, Exe: "demo",
		Args: []Arg{
			Group("model", "--model", "{{model}}"),
			Group("cwd", "--dir", "{{cwd}}"),
			Lit("--always"),
			Lit("{{prompt}}"),
		},
	}

	tests := []struct {
		name   string
		tokens Tokens
		want   []string
	}{
		{
			name:   "every token has a value",
			tokens: Tokens{Model: "m", Cwd: "/w", Prompt: "go"},
			want:   []string{"demo", "--model", "m", "--dir", "/w", "--always", "go"},
		},
		{
			name:   "an unset token removes its whole group",
			tokens: Tokens{Cwd: "/w"},
			want:   []string{"demo", "--dir", "/w", "--always"},
		},
		{
			name:   "nothing set at all still starts the program",
			tokens: Tokens{},
			want:   []string{"demo", "--always"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildArgv(spec, false, tt.tokens)
			if !slices.Equal(got, tt.want) {
				t.Errorf("argv\n got %q\nwant %q", got, tt.want)
			}
		})
	}
}

// TestBuildArgvDashDash pins where the guard is and is not put. It exists only
// for a bare positional task, and an agent that takes the task after a flag of
// its own must not get one.
func TestBuildArgvDashDash(t *testing.T) {
	tests := []struct {
		name string
		spec Spec
		want []string
	}{
		{
			name: "positional task beginning with a dash",
			spec: Spec{Exe: "x", Args: []Arg{Lit("{{prompt}}")}},
			want: []string{"x", "--", "-not an option"},
		},
		{
			name: "the agent says it does not want one",
			spec: Spec{Exe: "x", NoDashDash: true, Args: []Arg{Lit("{{prompt}}")}},
			want: []string{"x", "-not an option"},
		},
		{
			// The guard looks only at the argument, not at what precedes it,
			// so a task handed to a flag is guarded too -- and "-i -- -task"
			// is not what any of these tools mean. That is why every catalog
			// entry that passes the task as a flag's value sets NoDashDash.
			name: "the guard does not know the task is a flag's value",
			spec: Spec{Exe: "x", Args: []Arg{Group("prompt", "-i", "{{prompt}}")}},
			want: []string{"x", "-i", "--", "-not an option"},
		},
		{
			name: "which is what NoDashDash is for",
			spec: Spec{Exe: "x", NoDashDash: true, Args: []Arg{Group("prompt", "-i", "{{prompt}}")}},
			want: []string{"x", "-i", "-not an option"},
		},
		{
			name: "an argument that merely contains the token",
			spec: Spec{Exe: "x", Args: []Arg{Lit("task={{prompt}}")}},
			want: []string{"x", "task=-not an option"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildArgv(tt.spec, false, Tokens{Prompt: "-not an option"})
			if !slices.Equal(got, tt.want) {
				t.Errorf("argv\n got %q\nwant %q", got, tt.want)
			}
		})
	}
}

// TestBuildArgvAPIRunnerLeavesTheProgramToTheCaller: only the running process
// knows where its own executable is, so an API runner's argv starts at the
// first flag and workspace puts `perch chat` in front of it.
func TestBuildArgvAPIRunnerLeavesTheProgramToTheCaller(t *testing.T) {
	spec := anthropicAPISpec()
	got := BuildArgv(spec, false, Tokens{Session: "s", Model: "claude-sonnet-5", Prompt: "hello"})
	want := []string{"--agent", "anthropic", "--model", "claude-sonnet-5", "--session", "s", "hello"}
	if !slices.Equal(got, want) {
		t.Errorf("argv\n got %q\nwant %q", got, want)
	}
	if len(got) > 0 && got[0] == spec.Exe && spec.Exe != "" {
		t.Error("an API runner must not name a program of its own")
	}
}

// TestTokensValue covers the names an `if` may use, including the spellings
// that reach it from a hand-written agents.json.
func TestTokensValue(t *testing.T) {
	tokens := Tokens{
		Session: "sess", Model: "mod", Settings: "set",
		Prompt: "task", Cwd: "dir", Pane: "pane",
	}
	tests := []struct{ name, want string }{
		{"session", "sess"},
		{"{{model}}", "mod"},
		{" settings ", "set"},
		{"prompt", "task"},
		{"cwd", "dir"},
		{"pane", "pane"},
		{"nonsense", ""},
	}
	for _, tt := range tests {
		if got := tokens.Value(tt.name); got != tt.want {
			t.Errorf("Value(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
	if got := tokens.Expand("{{pane}} in {{cwd}} running {{model}}"); got != "pane in dir running mod" {
		t.Errorf("Expand = %q", got)
	}
}
