package tool

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestToolHelperProcess is not a test. It is the program run_command runs in
// the tests below: running the test binary again is the only way to have a
// command that behaves the same on Windows, macOS and Linux without depending
// on anything being installed.
func TestToolHelperProcess(t *testing.T) {
	if os.Getenv("FLOCKDECK_TOOL_HELPER") != "1" {
		return
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	if len(args) == 0 {
		os.Exit(0)
	}
	switch args[0] {
	case "say":
		fmt.Println(strings.Join(args[1:], " "))
	case "fail":
		fmt.Fprintln(os.Stderr, "it went wrong")
		os.Exit(3)
	case "linger":
		time.Sleep(30 * time.Second)
	case "cwd":
		dir, err := os.Getwd()
		if err != nil {
			os.Exit(1)
		}
		fmt.Println(dir)
	}
	os.Exit(0)
}

// helperLine is the command line that runs the helper above with args.
func helperLine(t *testing.T, args ...string) string {
	t.Helper()
	t.Setenv("FLOCKDECK_TOOL_HELPER", "1")
	return fmt.Sprintf("%q -test.run=^TestToolHelperProcess$ -- %s", os.Args[0], strings.Join(args, " "))
}

func TestSplitCommand(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		want    []string
		wantErr string
	}{
		{name: "a plain command", line: "go test ./...", want: []string{"go", "test", "./..."}},
		{name: "extra spaces", line: "  git   status  ", want: []string{"git", "status"}},
		{name: "double quotes group a word", line: `git commit -m "a message here"`, want: []string{"git", "commit", "-m", "a message here"}},
		{name: "single quotes do too", line: `grep 'two words' file`, want: []string{"grep", "two words", "file"}},
		{name: "an empty quoted argument survives", line: `prog "" x`, want: []string{"prog", "", "x"}},
		{name: "a Windows path keeps its backslashes", line: `prog C:\repo\file.go`, want: []string{"prog", `C:\repo\file.go`}},
		{name: "a pipeline is refused", line: "go test ./... | head", wantErr: "shell syntax"},
		{name: "a redirection is refused", line: "go test > out.txt", wantErr: "shell syntax"},
		{name: "a chain is refused", line: "go build && go test", wantErr: "shell syntax"},
		{name: "a second command is refused", line: "cd sub; go test", wantErr: "shell syntax"},
		{name: "a substitution is refused", line: "echo `date`", wantErr: "shell syntax"},
		{name: "more than one line is refused", line: "go build\ngo test", wantErr: "more than one line"},
		{name: "an unclosed quote is refused", line: `prog "unfinished`, wantErr: "unclosed"},
		{name: "nothing at all is refused", line: "   ", wantErr: "empty"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := splitCommand(tt.line)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("got %v, %v; want an error containing %q", got, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("split %q into %q, want %q", tt.line, got, tt.want)
			}
		})
	}
}

func TestCommandPrefix(t *testing.T) {
	tests := []struct {
		argv []string
		want string
	}{
		{nil, ""},
		{[]string{"ls"}, "ls"},
		{[]string{"go", "test", "./..."}, "go test"},
		{[]string{"git", "push", "--force"}, "git push"},
	}
	for _, tt := range tests {
		if got := commandPrefix(tt.argv); got != tt.want {
			t.Errorf("commandPrefix(%q) = %q, want %q", tt.argv, got, tt.want)
		}
	}
}

func TestRunCommand(t *testing.T) {
	root := newRoot(t)
	tl := &runCommand{root: root}

	t.Run("output and a successful exit", func(t *testing.T) {
		got, err := call(t, tl, map[string]any{"command": helperLine(t, "say", "hello")})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, "hello") || !strings.Contains(got, "[exit status 0]") {
			t.Errorf("got:\n%s", got)
		}
	})

	t.Run("a failure is a result, not an error", func(t *testing.T) {
		got, err := call(t, tl, map[string]any{"command": helperLine(t, "fail")})
		if err != nil {
			t.Fatalf("a non-zero exit is something the model should read, not an error: %v", err)
		}
		if !strings.Contains(got, "it went wrong") {
			t.Errorf("standard error should be interleaved with standard output:\n%s", got)
		}
		if !strings.Contains(got, "[exit status 3]") {
			t.Errorf("the exit status should be reported:\n%s", got)
		}
	})

	t.Run("it runs in the pane's directory", func(t *testing.T) {
		got, err := call(t, tl, map[string]any{"command": helperLine(t, "cwd")})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, root.Dir()) {
			t.Errorf("ran in the wrong directory:\n%s", got)
		}
	})

	t.Run("a command that hangs is killed", func(t *testing.T) {
		got, err := call(t, tl, map[string]any{"command": helperLine(t, "linger"), "timeout_seconds": 1})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, "killed after") {
			t.Errorf("got:\n%s", got)
		}
	})

	t.Run("a program that is not there", func(t *testing.T) {
		if _, err := call(t, tl, map[string]any{"command": "flockdeck-no-such-program-exists"}); err == nil {
			t.Error("a command that cannot start is an error")
		}
	})
}

// TestRunCommandAsksAndOffersAPrefix covers the one place standing permission
// is offered, and that it is scoped to a prefix rather than to everything.
func TestRunCommandAsksAndOffersAPrefix(t *testing.T) {
	root := newRoot(t)
	tl := &runCommand{root: root}

	args := rawArgs(t, map[string]any{"command": "go test ./..."})
	if q := tl.Approval(args); !strings.Contains(q, "go test ./...") || !strings.Contains(q, root.Dir()) {
		t.Errorf("the question should say what runs and where: %q", q)
	}
	if got := tl.Prefix(args); got != "go test" {
		t.Errorf("Prefix = %q, want %q", got, "go test")
	}
	if got := tl.Prefix(rawArgs(t, map[string]any{"command": "go build ./..."})); got == "go test" {
		t.Error("`go build` must not share `go test`'s standing permission")
	}
}

// TestRunCommandRefusesShellSyntaxWithoutAsking keeps a command that will not
// run from being put in front of the user as though it would.
func TestRunCommandRefusesShellSyntaxWithoutAsking(t *testing.T) {
	root := newRoot(t)
	tl := &runCommand{root: root}
	args := rawArgs(t, map[string]any{"command": "go test ./... > out.txt"})

	if q := tl.Approval(args); q != "" {
		t.Errorf("a command that cannot run was offered for approval: %q", q)
	}
	if _, err := call(t, tl, map[string]any{"command": "go test ./... > out.txt"}); err == nil {
		t.Error("shell syntax must be refused")
	}
}

func TestClipOutputKeepsBothEnds(t *testing.T) {
	long := "START" + strings.Repeat("m", commandMaxOutput*2) + "END"
	got := clipOutput(long)
	if !strings.HasPrefix(got, "START") || !strings.HasSuffix(got, "END") {
		t.Error("the beginning and the end are what matter and must both survive")
	}
	if !strings.Contains(got, "of output omitted") {
		t.Errorf("the cut should be admitted:\n%s", got[:80])
	}
	if got := clipOutput("short\n"); got != "short\n" {
		t.Errorf("clipOutput(%q) = %q", "short\n", got)
	}
}
