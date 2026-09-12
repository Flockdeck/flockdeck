package session

import (
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/jmwri/flockdeck/internal/sysproc"
)

// Claude Code runs a hook or a status line written as one command line through
// a shell, and which shell is its own choice. Flockdeck needs to know it twice
// over: to write the status line bridge's own command line so that the shell
// will run it, and, inside the bridge, to run the user's own status line
// command exactly as Claude Code would have run it without Flockdeck in
// between. Both are read from Claude Code 2.1.269's executable:
//
//   - Elsewhere than Windows, Node's `shell: true`, which is /bin/sh -c.
//   - On Windows, Git Bash where it finds one, as `bash -c`, and PowerShell
//     otherwise, as `-NoProfile -NonInteractive -ExecutionPolicy Bypass
//     -Command`, the policy left alone when
//     CLAUDE_CODE_POWERSHELL_RESPECT_EXECUTION_POLICY is set.
//
// Git Bash is found where CLAUDE_CODE_GIT_BASH_PATH points, if that names a
// bash or sh that exists; then in Git's two usual install folders; then beside
// the git on PATH, two folders up and into bin.

// GitBash returns the Git Bash Claude Code would run a command line through on
// Windows, or "" when it would find none and use PowerShell. It is a variable
// so a test can say which.
var GitBash = func() string { return gitBashFor(runtime.GOOS, os.Getenv, fileExists, exec.LookPath) }

func gitBashFor(goos string, getenv func(string) string, exists func(string) bool, look func(string) (string, error)) string {
	if goos != "windows" {
		return ""
	}
	// Windows paths are taken apart by hand, by either separator, rather than
	// with filepath, which only knows the separators of the platform it is
	// built for -- and the tests of this run everywhere.
	if p := getenv("CLAUDE_CODE_GIT_BASH_PATH"); p != "" {
		switch strings.ToLower(p[strings.LastIndexAny(p, `\/`)+1:]) {
		case "bash.exe", "sh.exe", "bash", "sh":
			if exists(p) {
				return p
			}
		}
	}
	for _, p := range []string{`C:\Program Files\Git\bin\bash.exe`, `C:\Program Files (x86)\Git\bin\bash.exe`} {
		if exists(p) {
			return p
		}
	}
	if git, err := look("git"); err == nil {
		// git.exe is in Git's cmd folder; bash is in bin, beside it.
		root := git
		for range 2 {
			root = root[:max(strings.LastIndexAny(root, `\/`), 0)]
		}
		if p := root + `\bin\bash.exe`; root != "" && exists(p) {
			return p
		}
	}
	return ""
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// ClaudeShellCommand is a command line run the way Claude Code runs a hook or a
// status line written as one: through the same shell, with the same flags and
// the same rewriting of the line for that shell.
//
// Its window is hidden, as Claude Code hides the shell it starts: the output
// goes back to Claude Code through the bridge, and a console window would only
// flash up on every refresh.
func ClaudeShellCommand(line string) *exec.Cmd {
	var cmd *exec.Cmd
	switch bash := GitBash(); {
	case runtime.GOOS != "windows":
		cmd = exec.Command("/bin/sh", "-c", line)
	case bash != "":
		cmd = exec.Command(bash, "-c", forGitBash(line))
	default:
		ps := "powershell"
		// Claude Code asks for pwsh first, the PowerShell a person has chosen
		// to install, and falls back to the one Windows comes with.
		if p, err := exec.LookPath("pwsh"); err == nil {
			ps = p
		} else if p, err := exec.LookPath("powershell"); err == nil {
			ps = p
		}
		args := []string{"-NoProfile", "-NonInteractive"}
		if os.Getenv("CLAUDE_CODE_POWERSHELL_RESPECT_EXECUTION_POLICY") == "" {
			args = append(args, "-ExecutionPolicy", "Bypass")
		}
		cmd = exec.Command(ps, append(args, "-Command", forPowerShell(line))...)
	}
	sysproc.NoWindow(cmd)
	return cmd
}

// forGitBash is how Claude Code rewrites a command line for Git Bash: a line
// whose program is a .sh script is run by bash, since Windows would not know
// what to do with one.
func forGitBash(line string) string {
	if strings.HasSuffix(firstWord(line), ".sh") {
		return "bash " + line
	}
	return line
}

// firstWord is the program a command line names, its quotes taken off and its
// backslash escapes undone, as Claude Code reads it for forGitBash.
func firstWord(line string) string {
	s := strings.TrimSpace(line)
	var b strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '"' || c == '\'':
			end := strings.IndexByte(s[i+1:], c)
			if end < 0 {
				b.WriteString(s[i+1:])
				return b.String()
			}
			b.WriteString(s[i+1 : i+1+end])
			i += end + 2
		case c == '\\' && i+1 < len(s):
			b.WriteByte(s[i+1])
			i += 2
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			return b.String()
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// forPowerShell is how Claude Code rewrites a command line for PowerShell: its
// own variables written ${NAME} would be PowerShell variables that do not
// exist, so they become the environment variables they mean.
func forPowerShell(line string) string {
	for _, name := range []string{"CLAUDE_PROJECT_DIR", "CLAUDE_PLUGIN_ROOT", "CLAUDE_PLUGIN_DATA"} {
		line = strings.ReplaceAll(line, "${"+name+"}", "${env:"+name+"}")
	}
	return line
}
