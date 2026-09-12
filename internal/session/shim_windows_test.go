//go:build windows

package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestHelperPrintArgs is not a test of its own. Run by the batch file below, it
// stands for the program behind an npm shim and prints the arguments it parsed.
func TestHelperPrintArgs(t *testing.T) {
	if os.Getenv("FLOCKDECK_TEST_PRINT_ARGS") != "1" {
		t.Skip("run as a helper process only")
	}
	args := os.Args
	if i := slices.Index(args, "--"); i >= 0 {
		args = args[i+1:]
	}
	out, _ := json.Marshal(args)
	fmt.Printf("ARGV:%s:END\n", out)
	os.Exit(0)
}

// TestATaskReachesAnAgentBehindABatchShim covers an agent installed with npm,
// which on Windows is a batch file run by cmd.exe. cmd.exe ended the command at
// the first line break, so an agent's briefing and task arrived as their first
// line alone, and it took a quote in the task as the end of the argument, so
// what followed ran as commands of its own.
func TestATaskReachesAnAgentBehindABatchShim(t *testing.T) {
	dir := t.TempDir()
	shim := filepath.Join(dir, "agent.cmd")
	// The shape of an npm shim: run the real program, passing everything on.
	body := "@\"" + os.Args[0] + "\" \"-test.run=^TestHelperPrintArgs$\" -- %*\r\n"
	if err := os.WriteFile(shim, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	tasks := []string{
		`a" & echo INJECTED & "b`,
		`100% sure %PATH% stays as written`,
		"<flockdeck-context>\nYou are the pane named \"api\".\n</flockdeck-context>\n\nfix the parser",
		`C:\a path\ending in a slash\`,
		`tabs	and ^carets | pipes > redirects ! bangs`,
	}
	s, err := Start(Config{
		ID:   "shim",
		Kind: KindAgent,
		Cwd:  dir,
		Argv: append([]string{shim}, tasks...),
		Env:  append(Env(), "FLOCKDECK_TEST_PRINT_ARGS=1"),
		Cols: maxCols,
		Rows: 50,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	id, replay, out := s.Subscribe()
	t.Cleanup(func() { s.Unsubscribe(id) })
	got, _ := collect(t, out, replay, ":END", 30*time.Second)
	text := stripANSI([]byte(got))

	m := regexp.MustCompile(`(?s)ARGV:(.*?):END`).FindStringSubmatch(strings.ReplaceAll(text, "\n", ""))
	if m == nil {
		t.Fatalf("the program behind the shim printed nothing readable:\n%s", text)
	}
	var argv []string
	if err := json.Unmarshal([]byte(m[1]), &argv); err != nil {
		t.Fatalf("unreadable argv %q: %v", m[1], err)
	}
	want := make([]string, len(tasks))
	for i, task := range tasks {
		want[i] = strings.ReplaceAll(task, "\n", " ")
	}
	if !slices.Equal(argv, want) {
		t.Errorf("the program behind the shim was given\n %q\nwant\n %q", argv, want)
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "INJECTED") {
			t.Errorf("text from a task ran as a command: %q", line)
		}
	}
}
