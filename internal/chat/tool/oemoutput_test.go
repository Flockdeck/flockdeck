package tool

import (
	"runtime"
	"strings"
	"testing"
)

// On Windows, a console program writing its own code page into the pipe --
// cmd.exe, for a file name with an accent in it -- is read as the text it is,
// not as replacement characters the model cannot open a file by.
func TestConsoleOutputInTheOEMCodePageIsDecoded(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows console programs write their own code page into a pipe")
	}
	root := newRoot(t)
	write(t, root, "café.txt", "x")
	tl := &runCommand{root: root}
	for _, command := range []string{"cmd /c dir /b", "cmd /c echo café"} {
		got, err := call(t, tl, map[string]any{"command": command})
		if err != nil {
			t.Fatalf("%s: %v", command, err)
		}
		if !strings.Contains(got, "café") || strings.Contains(got, "�") {
			t.Errorf("%s gave %q", command, got)
		}
	}
}
