package tool

import "testing"

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
	} {
		if got := tl.Prefix(rawArgs(t, map[string]any{"command": c.command})); got != c.want {
			t.Errorf("Prefix(%q) = %q, want %q", c.command, got, c.want)
		}
	}
}
