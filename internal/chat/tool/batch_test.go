package tool

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Windows hands a batch file's whole command line to cmd.exe, so a quote in
// an argument breaks out of the quoting round it and what follows runs as a
// command of its own. Such an argument is refused, without being asked about.
func TestABatchFileIsNotHandedCmdSyntax(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("batch files are a Windows matter")
	}
	root := newRoot(t)
	bat := filepath.Join(root.Dir(), "tool.cmd")
	if err := os.WriteFile(bat, []byte("@echo args: %*\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tl := &runCommand{root: root}

	for _, arg := range []string{`'x"&echo INJECTED'`, `'x&echo INJECTED'`, `'x%PATH%'`} {
		command := `"` + bat + `" test ` + arg
		if q := tl.Approval(rawArgs(t, map[string]any{"command": command})); q != "" {
			t.Errorf("%s was put to the user: %q", arg, q)
		}
		got, err := call(t, tl, map[string]any{"command": command})
		if err == nil || strings.Contains(got, "INJECTED") {
			t.Errorf("%s: ran and returned %q, %v", arg, got, err)
		}
	}

	got, err := call(t, tl, map[string]any{"command": `"` + bat + `" test plain`})
	if err != nil || !strings.Contains(got, "args: test plain") {
		t.Errorf("a plain argument: %q, %v", got, err)
	}
}
