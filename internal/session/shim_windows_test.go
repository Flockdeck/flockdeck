//go:build windows

package session

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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

// TestAnNpmShimIsNotRunThroughCmd covers the batch files npm writes for the
// agents it installs, which is how Codex and Gemini CLI arrive on Windows. Run
// through cmd.exe, a briefing and a task together passed the 8191 characters
// cmd.exe accepts and the pane died with "The command line is too long"; all
// the shim does is start node on a script, so node is started on it directly.
func TestAnNpmShimIsNotRunThroughCmd(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "node_modules", "agent", "bin", "agent.js")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	js := "process.stdout.write('ARGV:' + JSON.stringify(process.argv.slice(2)) + ':END\\n')\n"
	if err := os.WriteFile(script, []byte(js), 0o644); err != nil {
		t.Fatal(err)
	}
	// What npm writes today, in the part that matters.
	shim := filepath.Join(dir, "agent.cmd")
	body := "@ECHO off\r\nGOTO start\r\n:find_dp0\r\nSET dp0=%~dp0\r\nEXIT /b\r\n:start\r\nSETLOCAL\r\nCALL :find_dp0\r\n" +
		"IF EXIST \"%dp0%\\node.exe\" (\r\n  SET \"_prog=%dp0%\\node.exe\"\r\n) ELSE (\r\n  SET \"_prog=node\"\r\n)\r\n" +
		"endLocal & goto #_undefined_# 2>NUL || title %COMSPEC% & \"%_prog%\"  \"%dp0%\\node_modules\\agent\\bin\\agent.js\" %*\r\n"
	if err := os.WriteFile(shim, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	task := "<flockdeck-context>\n" + strings.Repeat("a briefing & \"quoted\" 100% ", 500) + "\n</flockdeck-context>\n\nfix the parser"
	s, err := Start(Config{ID: "npm", Kind: KindAgent, Cwd: dir, Argv: []string{shim, task}, Env: Env(), Cols: maxCols, Rows: 50})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	id, replay, out := s.Subscribe()
	t.Cleanup(func() { s.Unsubscribe(id) })
	got, _ := collect(t, out, replay, ":END", 30*time.Second)

	m := regexp.MustCompile(`(?s)ARGV:(.*?):END`).FindStringSubmatch(strings.ReplaceAll(stripANSI([]byte(got)), "\n", ""))
	if m == nil {
		t.Fatalf("the agent behind the shim printed nothing readable:\n%s", tail(stripANSI([]byte(got)), 400))
	}
	var argv []string
	if err := json.Unmarshal([]byte(m[1]), &argv); err != nil {
		t.Fatalf("unreadable argv: %v", err)
	}
	if len(argv) != 1 || argv[0] != task {
		t.Errorf("the agent was given %d arguments, want the %d-character task exactly as written", len(argv), len(task))
	}
}

// TestOnlyAnNpmShimIsStartedAsNode covers a batch file that names a script
// beside it without being npm's: one running it through Windows Script Host
// must still be run as the batch file it is, not handed to node.
func TestOnlyAnNpmShimIsStartedAsNode(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tool.js"), []byte("WScript.Echo('hi')\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wsh := filepath.Join(dir, "tool.cmd")
	if err := os.WriteFile(wsh, []byte("@cscript //nologo \"%~dp0\\tool.js\" %*\r\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := npmScript(wsh); ok {
		t.Error("a batch file running its script through cscript was taken for an npm shim")
	}

	npm := filepath.Join(dir, "agent.cmd")
	if err := os.WriteFile(npm, []byte("@IF EXIST \"%~dp0\\node.exe\" (\r\n  \"%~dp0\\node.exe\" \"%~dp0\\tool.js\" %*\r\n) ELSE (\r\n  node \"%~dp0\\tool.js\" %*\r\n)\r\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, script, ok := npmScript(npm); !ok || script != filepath.Join(dir, "tool.js") {
		t.Errorf("npm's older shim form was not recognised: %q, %v", script, ok)
	}
}

// printedArgs starts a batch file of the shape the helper answers through and
// returns the arguments the program behind it parsed.
func printedArgs(t *testing.T, args ...string) []string {
	t.Helper()
	dir := t.TempDir()
	shim := filepath.Join(dir, "agent.cmd")
	body := "@\"" + os.Args[0] + "\" \"-test.run=^TestHelperPrintArgs$\" -- %*\r\n"
	if err := os.WriteFile(shim, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := Start(Config{
		ID: "shim", Kind: KindAgent, Cwd: dir,
		Argv: append([]string{shim}, args...),
		Env:  append(Env(), "FLOCKDECK_TEST_PRINT_ARGS=1"),
		Cols: maxCols, Rows: 50,
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
		t.Fatalf("the program behind the batch file printed nothing readable:\n%s", tail(text, 400))
	}
	var argv []string
	if err := json.Unmarshal([]byte(m[1]), &argv); err != nil {
		t.Fatalf("unreadable argv: %v", err)
	}
	return argv
}

// TestALongBriefingGivesWayToTheTask covers a batch file that is not an npm
// shim, which still goes through cmd.exe and its 8191 characters. A briefing
// and a task together pass that, and cmd.exe answered "The command line is too
// long" and ran nothing. The briefing gives way, section by section, and the
// task arrives whole.
func TestALongBriefingGivesWayToTheTask(t *testing.T) {
	var brief strings.Builder
	brief.WriteString("# Where you are running\n\nYou are one agent inside Flockdeck.")
	for _, section := range []string{"This pane", "The other agents", "Starting agents of your own", "What Flockdeck can do"} {
		brief.WriteString("\n\n## " + section + "\n\n" + strings.Repeat("Some words about it & more, 100% sure. ", 130))
	}
	task := `fix the parser & say "done", 100% of it`
	prompt := contextOpen + brief.String() + contextClose + "\n\n" + task
	if len(prompt) < 20000 {
		t.Fatalf("the prompt is only %d characters", len(prompt))
	}

	argv := printedArgs(t, prompt)
	if len(argv) != 1 {
		t.Fatalf("the agent was given %d arguments, want one", len(argv))
	}
	if !strings.HasSuffix(argv[0], task) {
		t.Errorf("the task did not arrive whole; the prompt ends %q", tail(argv[0], 80))
	}
	if !strings.Contains(argv[0], "## This pane") || !strings.Contains(argv[0], "shortened") {
		t.Errorf("want the first sections kept and a note that the briefing was shortened; got %q…", tail(argv[0], 300))
	}
}

// TestATaskTooLongOnItsOwnIsCutAndSaysSo covers a task with no briefing to give
// way, too long for cmd.exe by itself: it is cut, with a note, and the agent
// still starts.
func TestATaskTooLongOnItsOwnIsCutAndSaysSo(t *testing.T) {
	task := strings.Repeat("do this, then that. ", 450)
	argv := printedArgs(t, task)
	if len(argv) != 1 || !strings.HasPrefix(task, strings.TrimSuffix(argv[0], strings.TrimSpace(cutNote))[:100]) {
		t.Fatalf("argv = %q", argv)
	}
	if !strings.HasSuffix(argv[0], strings.TrimSpace(cutNote)) {
		t.Errorf("a cut task should say it was cut; it ends %q", tail(argv[0], 120))
	}
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
