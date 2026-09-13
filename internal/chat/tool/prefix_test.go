package tool

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// "Always" for `cmd /c` or `sh -c` would agree to every command there is while
// looking like agreement to one, so a shell or an interpreter told to run what
// follows offers no standing permission at all.
func TestNoStandingPermissionIsOfferedForAShell(t *testing.T) {
	tl := &runCommand{root: newRoot(t)}
	for _, c := range []struct{ command, want string }{
		{"go test ./...", "go test"},
		{"git status", "git status"},
		{"cmd /c del notes.txt", ""},
		{"CMD.EXE /C dir", ""},
		{"powershell -Command Get-ChildItem", ""},
		{`bash -c "rm -rf build"`, ""},
		{`python -c "print(1)"`, ""},
		{"node -e 1", ""},
		{"python script.py", "python script.py"},
		// An option before the code, or a program that runs whatever follows.
		{`bash -lc "rm -rf build"`, ""},
		{`sh -ec "rm -rf build"`, ""},
		{"powershell -NoProfile -Command Remove-Item x", ""},
		{`powershell Get-ChildItem "; Remove-Item x"`, ""},
		{`wsl ls "; rm -rf build"`, ""},
		{"env FOO=1 rm -rf build", ""},
		{"sudo rm -rf build", ""},
		{"python -m http.server", ""},
		{"deno eval 1", ""},
		{"bash build.sh", "bash build.sh"},
		{"node server.js", "node server.js"},
		// An option in the second place names no subcommand.
		{`git -c core.pager="rm -rf build" log`, ""},
		{"make -C sub clean", ""},
		{"git log --oneline", "git log"},
	} {
		if got := tl.Prefix(rawArgs(t, map[string]any{"command": c.command})); got != c.want {
			t.Errorf("Prefix(%q) = %q, want %q", c.command, got, c.want)
		}
	}
}

// Windows drops a trailing dot or space from a file name, so cmd.exe. and
// "cmd.exe " are cmd.exe, and a program found by one of those spellings is a
// shell all the same. conhost and forfiles run the command line they are
// given, and npx runs any package's program.
func TestAShellSpelledAnotherWayOffersNoStandingPermission(t *testing.T) {
	tl := &runCommand{root: newRoot(t)}
	for _, command := range []string{
		"cmd.exe. /c del notes.txt",
		`"cmd.exe " /c del notes.txt`,
		"cmd.exe.. /c del notes.txt",
		"powershell.exe. Remove-Item x",
		"CMD.COM /c del notes.txt",
		"conhost cmd /c del notes.txt",
		"forfiles /c del",
		"npx rimraf build",
	} {
		if got := tl.Prefix(rawArgs(t, map[string]any{"command": command})); got != "" {
			t.Errorf("Prefix(%q) = %q, want no standing permission", command, got)
		}
	}
}

// What a program is called in the command line need not say what it is: a
// link in the pane can be named anything, and on Windows a short name such as
// POWERS~1.EXE is a longer one. The program is judged by the file it leads to.
func TestAShellReachedThroughALinkOffersNoStandingPermission(t *testing.T) {
	root := newRoot(t)
	shell, name := "env", "helper"
	if runtime.GOOS == "windows" {
		shell, name = "cmd", "helper.exe"
	}
	target, err := exec.LookPath(shell)
	if err != nil {
		t.Skipf("no %s here: %v", shell, err)
	}
	if err := os.Symlink(target, filepath.Join(root.Dir(), name)); err != nil {
		t.Skipf("cannot make a link here: %v", err)
	}
	tl := &runCommand{root: root}
	command := "./" + name + " /c del notes.txt"
	if got := tl.Prefix(rawArgs(t, map[string]any{"command": command})); got != "" {
		t.Errorf("Prefix(%q) = %q, want no standing permission for %s under another name", command, got, target)
	}
}

// A command whose first word is an empty quoted string names no program, and
// is neither a question nor a standing permission worth failing over.
func TestAnEmptyProgramNameIsNotAPanic(t *testing.T) {
	tl := &runCommand{root: newRoot(t)}
	args := rawArgs(t, map[string]any{"command": `"" x`})
	tl.Approval(args)
	tl.Prefix(args)
}
