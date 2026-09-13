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

// "Always" for two words is agreed to for what those two words do. A later
// option that writes a file wherever it says or runs a program it names goes
// further, and so does a subcommand whose later words say what it runs: none
// is offered standing permission, and a call answering "" is asked about even
// after "always" for its first two words.
func TestAStandingPermissionDoesNotCoverWhatGoesFurther(t *testing.T) {
	tl := &runCommand{root: newRoot(t)}
	for _, c := range []struct{ command, want string }{
		// git options that write where they say or run what they name.
		{"git log --output=../elsewhere/log.txt", ""},
		{"git log --output=log.txt", ""},
		{"git log --out=../elsewhere/log.txt", ""},
		{"git format-patch --output-directory ../elsewhere HEAD~1", ""},
		{"git format-patch -o ../elsewhere HEAD~1", ""},
		{"git format-patch -o../elsewhere HEAD~1", ""},
		{"git format-patch -ko ../elsewhere HEAD~1", ""},
		{"git format-patch -o .git/hooks HEAD~1", ""},
		{"git fetch --upload-pack=touch .", ""},
		{"git push --receive-pack=touch origin", ""},
		{"git difftool --extcmd=touch", ""},
		{"git grep -Otouch needle", ""},
		{"git grep --open-files-in-pager=touch needle", ""},
		// git subcommands whose later words say what runs.
		{"git config core.fsmonitor touch", ""},
		{"git submodule foreach touch x", ""},
		{"git rebase -x touch main", ""},
		{"git bisect run touch", ""},
		{"git clone -u touch . copy", ""},
		// go flags that run what they name, even set for later.
		{"go test -exec=touch ./...", ""},
		{"go test -toolexec touch ./...", ""},
		{"go vet --vettool=touch ./...", ""},
		{"go env -w GOFLAGS=-toolexec=touch", ""},
		// npm and docker subcommands that run anything.
		{"npm exec touch", ""},
		{"npm x touch", ""},
		{"npm exe touch", ""},
		{"docker run -v /:/host alpine", ""},
		{"docker exec box sh", ""},
		{"docker container run alpine", ""},
		{"docker compose run web sh", ""},
		// What goes no further keeps its standing permission.
		{"git log --oneline", "git log"},
		{"git format-patch -o patches HEAD~1", "git format-patch"},
		{"git grep -o needle", "git grep"},
		{"git commit -o main.go -m tidy", "git commit"},
		{"git log -- --output", "git log"},
		{"go test -run TestExec ./...", "go test"},
		{"go build ./cmd/foo-exec", "go build"},
		{"npm run build", "npm run"},
		{"docker ps", "docker ps"},
		{"docker compose up", "docker compose"},
	} {
		if got := tl.Prefix(rawArgs(t, map[string]any{"command": c.command})); got != c.want {
			t.Errorf("Prefix(%q) = %q, want %q", c.command, got, c.want)
		}
	}
}
