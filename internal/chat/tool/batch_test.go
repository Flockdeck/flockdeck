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

// A batch file named without its extension -- .\gradlew for gradlew.bat, the
// way its own README says to run it -- is still a batch file: Windows finds it
// by adding the extension and hands it to cmd.exe all the same. So is one
// named with a trailing dot, which Windows drops. Either is refused when an
// argument carries cmd.exe syntax, whatever directory the chat itself was
// started in, and is never run without a question.
func TestABatchFileNamedWithoutItsExtensionIsNotHandedCmdSyntax(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("batch files are a Windows matter")
	}
	root := newRoot(t)
	if err := os.WriteFile(filepath.Join(root.Dir(), "gradlew.bat"), []byte("@echo args: %*\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tl := &runCommand{root: root}
	check := func(where string) {
		for _, program := range []string{`.\gradlew`, `./gradlew`, `.\gradlew.bat.`, `.\gradlew.bat `} {
			command := `"` + program + `" test 'x"&echo INJECTED'`
			q := tl.Approval(rawArgs(t, map[string]any{"command": command}))
			got, err := call(t, tl, map[string]any{"command": command})
			if strings.Contains(got, "INJECTED") || err == nil {
				t.Errorf("%s, %s: ran (asked %q) and returned %q, %v", where, program, q, got, err)
			}
		}
		got, err := call(t, tl, map[string]any{"command": `.\gradlew test plain`})
		if err != nil || !strings.Contains(got, "args: test plain") {
			t.Errorf("%s: a plain argument: %q, %v", where, got, err)
		}
	}
	check("started elsewhere")
	t.Chdir(root.Dir())
	check("started in the working directory")
}
