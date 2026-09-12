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
	} {
		if got := tl.Prefix(rawArgs(t, map[string]any{"command": c.command})); got != c.want {
			t.Errorf("Prefix(%q) = %q, want %q", c.command, got, c.want)
		}
	}
}
