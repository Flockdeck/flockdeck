package tool

import (
	"runtime"
	"strings"
	"testing"
)

// On Windows a model is told up front how cmd.exe's own commands are run,
// rather than learning it from a failed call for each one it tries.
func TestRunCommandSaysHowToRunCmdCommandsOnWindows(t *testing.T) {
	d := (&runCommand{root: newRoot(t)}).Describe().Description
	if runtime.GOOS == "windows" && !strings.Contains(d, "cmd /c dir") {
		t.Errorf("the description does not say how to run dir:\n%s", d)
	}
	if runtime.GOOS != "windows" && strings.Contains(d, "cmd /c") {
		t.Errorf("the description talks about cmd.exe off Windows:\n%s", d)
	}
}
