package session

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// echoEnv makes this test binary, copied somewhere and run by a shell, print
// what it was given and exit, before any test runs. It is what stands in for
// the status line bridge when a command line is put through a real shell.
const echoEnv = "FLOCKDECK_SESSION_TEST_ECHO"

func init() {
	if os.Getenv(echoEnv) == "" {
		return
	}
	in, _ := io.ReadAll(os.Stdin)
	fmt.Printf("args=%q stdin=%q\n", os.Args[1:], in)
	os.Exit(0)
}

// writeSettings writes the pane's settings with the status line as sl says,
// for a Claude Code whose version does not matter here, and returns the
// statusLine it carries.
func writeSettings(t *testing.T, sl StatusLine) map[string]any {
	t.Helper()
	was := claudeVersion
	t.Cleanup(func() { claudeVersion = was })
	claudeVersion = func(string) string { return "2.1.269 (Claude Code)" }
	path, err := WriteHookSettingsWith("", t.TempDir(), "pane-id", "/bin/flockdeck", "http://127.0.0.1:1/hook", sl)
	if err != nil {
		t.Fatalf("write settings: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got settingsFile
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode settings: %v\n%s", err, raw)
	}
	if len(got.Hooks) == 0 {
		t.Fatalf("the hooks went missing: %s", raw)
	}
	return got.StatusLine
}

// claudeHomeAt points Claude Code's own folder somewhere of the test's.
func claudeHomeAt(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	return home
}

func writeJSON(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// thenOf is the user's command a bridge command line carries.
func thenOf(t *testing.T, command string) string {
	t.Helper()
	_, enc, ok := strings.Cut(command, "--then ")
	if !ok {
		return ""
	}
	enc = strings.Trim(strings.Fields(enc)[0], "'")
	dec, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		t.Fatalf("--then %q does not decode: %v", enc, err)
	}
	return string(dec)
}

// By default the status line is routed only where the user has one of their
// own, which the bridge keeps: there it changes nothing they can see, and
// for somebody with none it would cost Claude Code's footer hints. Off is off,
// and on is on for everybody.
func TestStatusLineOnlyWhereItChangesNothingVisible(t *testing.T) {
	home := claudeHomeAt(t)
	cwd := t.TempDir()
	endpoint := "http://127.0.0.1:1/usage"

	if sl := writeSettings(t, StatusLine{Mode: StatusLineAuto, Endpoint: endpoint, Cwd: cwd}); sl != nil {
		t.Errorf("no status line of the user's, and one was set by default: %v", sl)
	}
	if sl := writeSettings(t, StatusLine{Mode: StatusLineOn, Endpoint: endpoint, Cwd: cwd}); sl == nil {
		t.Error("turned on, and no status line was set")
	} else if cmd, _ := sl["command"].(string); !strings.Contains(cmd, "statusline") || thenOf(t, cmd) != "" {
		t.Errorf("turned on with no status line of the user's, the command is %q", cmd)
	}

	writeJSON(t, filepath.Join(home, "settings.json"),
		`{"model":"opus","statusLine":{"type":"command","command":"~/bin/line.sh --short","padding":2,"refreshInterval":5,"hideVimModeIndicator":true}}`)
	sl := writeSettings(t, StatusLine{Mode: StatusLineAuto, Endpoint: endpoint, Cwd: cwd})
	if sl == nil {
		t.Fatal("the user has a status line, and it was not routed")
	}
	cmd, _ := sl["command"].(string)
	if !strings.Contains(cmd, "statusline") || !strings.Contains(cmd, endpoint) || !strings.Contains(cmd, "pane-id") {
		t.Errorf("command = %q, want the bridge with the endpoint and the pane", cmd)
	}
	// The status line runs after every answer, and a command line is there for
	// anybody on the machine to read: the bridge takes the secret from the
	// pane's environment instead.
	if strings.Contains(cmd, "--token") {
		t.Errorf("command = %q, which carries the secret on its command line", cmd)
	}
	if got := thenOf(t, cmd); got != "~/bin/line.sh --short" {
		t.Errorf("the bridge runs %q, want the user's own command", got)
	}
	// The rest of the user's status line is theirs, and comes with it.
	if sl["type"] != "command" || sl["padding"] != float64(2) || sl["refreshInterval"] != float64(5) || sl["hideVimModeIndicator"] != true {
		t.Errorf("statusLine = %v, want the user's padding, refresh interval and vim setting kept", sl)
	}

	if sl := writeSettings(t, StatusLine{Mode: StatusLineOff, Endpoint: endpoint, Cwd: cwd}); sl != nil {
		t.Errorf("turned off, and a status line was set: %v", sl)
	}
	if sl := writeSettings(t, StatusLine{Mode: StatusLineOn, Cwd: cwd}); sl != nil {
		t.Errorf("with nowhere to report to, a status line was set: %v", sl)
	}
}

// The user's status line is looked for where Claude Code looks, the first to
// set one winning: the project's local settings, the project's, the user's.
func TestUsersStatusLineIsFoundByClaudesPrecedence(t *testing.T) {
	home := claudeHomeAt(t)
	cwd := t.TempDir()
	line := func(cmd string) string { return `{"statusLine":{"type":"command","command":"` + cmd + `"}}` }
	writeJSON(t, filepath.Join(home, "settings.json"), line("from-user"))
	if got := userStatusLine(cwd, home)["command"]; got != "from-user" {
		t.Errorf("got %v, want the user's own", got)
	}
	writeJSON(t, filepath.Join(cwd, ".claude", "settings.json"), line("from-project"))
	if got := userStatusLine(cwd, home)["command"]; got != "from-project" {
		t.Errorf("got %v, want the project's over the user's", got)
	}
	writeJSON(t, filepath.Join(cwd, ".claude", "settings.local.json"), line("from-local"))
	if got := userStatusLine(cwd, home)["command"]; got != "from-local" {
		t.Errorf("got %v, want the local settings over the project's", got)
	}
	// A file that sets something Flockdeck cannot run decides all the same, as
	// it does for Claude Code, rather than letting a file below it through.
	writeJSON(t, filepath.Join(cwd, ".claude", "settings.local.json"), `{"statusLine":{"type":"command","command":""}}`)
	if got := userStatusLine(cwd, home); got != nil {
		t.Errorf("got %v, want none", got)
	}
	// A file that does not parse is passed over.
	writeJSON(t, filepath.Join(cwd, ".claude", "settings.local.json"), `{not json`)
	if got := userStatusLine(cwd, home)["command"]; got != "from-project" {
		t.Errorf("got %v, want the project's past a broken file", got)
	}
}

// A pane whose catalog entry moves Claude Code's folder elsewhere -- which is
// how a second account is run -- reads the user's settings from there, so the
// status line carried is the one that account has, not Flockdeck's own.
func TestAPaneOnAnotherAccountCarriesThatAccountsStatusLine(t *testing.T) {
	own := claudeHomeAt(t)
	other := t.TempDir()
	cwd := t.TempDir()
	line := func(cmd string) string { return `{"statusLine":{"type":"command","command":"` + cmd + `"}}` }
	writeJSON(t, filepath.Join(own, "settings.json"), line("from-flockdecks-account"))
	writeJSON(t, filepath.Join(other, "settings.json"), line("from-the-panes-account"))

	sl := writeSettings(t, StatusLine{Mode: StatusLineAuto, Endpoint: "http://127.0.0.1:1/usage", Cwd: cwd, Home: other})
	if sl == nil {
		t.Fatal("the pane's account has a status line, and it was not routed")
	}
	cmd, _ := sl["command"].(string)
	if got := thenOf(t, cmd); got != "from-the-panes-account" {
		t.Errorf("the bridge runs %q, want the pane's own account's status line", got)
	}
}

// A settings file saved with a byte order mark -- which Notepad and Windows
// PowerShell's Out-File both write -- is one Claude Code reads, taking the mark
// off first. Passing it over as unparseable skipped the status line the user
// had set there: by default the pane then had no limits to show, and with the
// bridge always on, their line was replaced by a lower file's or by nothing.
func TestUsersStatusLineIsReadPastAByteOrderMark(t *testing.T) {
	home := claudeHomeAt(t)
	cwd := t.TempDir()
	bom := string([]byte{0xEF, 0xBB, 0xBF})
	writeJSON(t, filepath.Join(home, "settings.json"), `{"statusLine":{"type":"command","command":"from-user"}}`)
	writeJSON(t, filepath.Join(cwd, ".claude", "settings.json"), bom+`{"statusLine":{"type":"command","command":"from-project"}}`)
	if got := userStatusLine(cwd, home)["command"]; got != "from-project" {
		t.Errorf("got %v, want the project's own, read past its byte order mark", got)
	}
}

// On Windows the command line has to run under Git Bash or PowerShell, and no
// quoting is read alike by the two. A program under a folder with a space is
// named by its short name, which needs none; where the volume keeps no short
// names it is quoted for whichever shell Claude Code will use.
func TestWindowsCommandLineForBothShells(t *testing.T) {
	args := []string{"statusline", "--endpoint", "http://127.0.0.1:5/usage", "--session", "p-1"}
	plain := windowsCommandLine(`C:\Tools\flockdeck.exe`, args, func(string) string { return "" }, true)
	if plain != "C:/Tools/flockdeck.exe statusline --endpoint http://127.0.0.1:5/usage --session p-1" {
		t.Errorf("a plain path: %q", plain)
	}
	spaced := `C:\Users\Jo Smith\AppData\Local\flockdeck\flockdeck.exe`
	short := func(string) string { return `C:\Users\JOSMIT~1\AppData\Local\flockdeck\flockdeck.exe` }
	if got := windowsCommandLine(spaced, args, short, false); !strings.HasPrefix(got, "C:/Users/JOSMIT~1/AppData/Local/flockdeck/flockdeck.exe statusline ") {
		t.Errorf("with a short name: %q", got)
	}
	none := func(string) string { return "" }
	if got := windowsCommandLine(spaced, args, none, true); !strings.HasPrefix(got, "'C:/Users/Jo Smith/AppData/Local/flockdeck/flockdeck.exe' statusline ") {
		t.Errorf("no short name, Git Bash: %q", got)
	}
	if got := windowsCommandLine(spaced, args, none, false); !strings.HasPrefix(got, `& 'C:\Users\Jo Smith\AppData\Local\flockdeck\flockdeck.exe' statusline `) {
		t.Errorf("no short name, PowerShell: %q", got)
	}
	if got := windowsCommandLine(`C:\Users\O'Neil Jo\f.exe`, args, none, false); !strings.HasPrefix(got, `& 'C:\Users\O''Neil Jo\f.exe' `) {
		t.Errorf("a quote in the path, PowerShell: %q", got)
	}
}

// The Windows spike, kept: the bridge's command line, for a program under a
// folder with a space, is put through Git Bash and PowerShell exactly as
// Claude Code 2.1.269 starts them, and the program has to be given its
// arguments and what was written to it.
func TestBridgeCommandLineRunsUnderClaudesShells(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the shells in question are Windows's")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "with space")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "flockdeck.exe")
	if err := os.WriteFile(exe, data, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(echoEnv, "1")

	hasShort := shortPath(exe) != "" && plainForBoth(filepath.ToSlash(shortPath(exe)))
	bash := GitBash()
	line := bridgeCommand("windows", exe, "http://127.0.0.1:5/usage", "p-1", "echo hi")
	want := `args=["statusline" "--endpoint" "http://127.0.0.1:5/usage" "--session" "p-1" "--then" "ZWNobyBoaQ"]`

	type shell struct {
		name string
		argv []string
	}
	var shells []shell
	if bash != "" {
		shells = append(shells, shell{"Git Bash", []string{bash, "-c"}})
	}
	if ps, err := exec.LookPath("powershell"); err == nil && (hasShort || bash == "") {
		// Without a short name the line is written for the shell Claude Code
		// would choose, and only that one is expected to run it.
		shells = append(shells, shell{"PowerShell", []string{ps, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command"}})
	}
	if len(shells) == 0 {
		t.Skip("neither Git Bash nor PowerShell is here")
	}
	for _, sh := range shells {
		cmd := exec.Command(sh.argv[0], append(sh.argv[1:], line)...)
		cmd.Stdin = strings.NewReader(`{"session_id":"s"}`)
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), want) || !strings.Contains(string(out), `stdin="{\"session_id\":\"s\"}"`) {
			t.Errorf("%s ran %q: %v\n%s", sh.name, line, err, out)
		}
	}
}

// The user's own command is run as Claude Code would have run it: a .sh
// script through bash under Git Bash, and Claude Code's variables written the
// way PowerShell reads them.
func TestUsersCommandIsRewrittenAsClaudeRewritesIt(t *testing.T) {
	for line, want := range map[string]string{
		`~/.claude/line.sh`:            `bash ~/.claude/line.sh`,
		`"C:/My Tools/line.sh" --x`:    `bash "C:/My Tools/line.sh" --x`,
		`C:\\tools\\line.sh`:           `bash C:\\tools\\line.sh`,
		`node ~/.claude/line.js`:       `node ~/.claude/line.js`,
		`bash -c 'echo a.sh'`:          `bash -c 'echo a.sh'`,
		`  ./status.sh  `:              `bash   ./status.sh  `,
		`echo "${CLAUDE_PROJECT_DIR}"`: `echo "${CLAUDE_PROJECT_DIR}"`,
	} {
		if got := forGitBash(line); got != want {
			t.Errorf("forGitBash(%q) = %q, want %q", line, got, want)
		}
	}
	if got := forPowerShell(`Write-Output "${CLAUDE_PROJECT_DIR}"`); got != `Write-Output "${env:CLAUDE_PROJECT_DIR}"` {
		t.Errorf("forPowerShell: %q", got)
	}
}

// Git Bash is looked for where Claude Code looks, in its order.
func TestGitBashIsFoundWhereClaudeFindsIt(t *testing.T) {
	exists := func(have ...string) func(string) bool {
		return func(p string) bool {
			for _, h := range have {
				if filepath.Clean(p) == filepath.Clean(h) {
					return true
				}
			}
			return false
		}
	}
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == "CLAUDE_CODE_GIT_BASH_PATH" {
				return v
			}
			return ""
		}
	}
	noGit := func(string) (string, error) { return "", exec.ErrNotFound }
	git := func(string) (string, error) { return `D:\Git\cmd\git.exe`, nil }

	if got := gitBashFor("linux", env(""), exists(), noGit); got != "" {
		t.Errorf("off Windows: %q", got)
	}
	if got := gitBashFor("windows", env(`E:\bash.exe`), exists(`E:\bash.exe`, `C:\Program Files\Git\bin\bash.exe`), noGit); got != `E:\bash.exe` {
		t.Errorf("CLAUDE_CODE_GIT_BASH_PATH: %q", got)
	}
	if got := gitBashFor("windows", env(`E:\zsh.exe`), exists(`E:\zsh.exe`, `C:\Program Files\Git\bin\bash.exe`), noGit); got != `C:\Program Files\Git\bin\bash.exe` {
		t.Errorf("a CLAUDE_CODE_GIT_BASH_PATH that is not bash: %q", got)
	}
	if got := gitBashFor("windows", env(""), exists(`D:\Git\bin\bash.exe`), git); filepath.Clean(got) != filepath.Clean(`D:\Git\bin\bash.exe`) {
		t.Errorf("beside git on PATH: %q", got)
	}
	if got := gitBashFor("windows", env(""), exists(), git); got != "" {
		t.Errorf("no Git Bash anywhere: %q", got)
	}
}
