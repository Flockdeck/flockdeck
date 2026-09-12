package tool

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// A model told only "executable file not found" for `dir` tries `dir` again;
// told it is part of cmd.exe, it runs `cmd /c dir`. The hints are asked for
// directly rather than by running the commands, because Git for Windows can put
// programs of these names on PATH.
func TestAProgramThatIsNotThereIsExplainedOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the hints are for Windows")
	}
	for _, c := range []struct{ program, want string }{
		{"dir", "cmd /c dir"},
		{"DIR", "cmd /c DIR"},
		{"ls", "list_dir"},
	} {
		if err := notFoundHint(c.program, exec.ErrNotFound); !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error = %v, want one suggesting %q", c.program, err, c.want)
		}
	}
	if err := notFoundHint("flockdeck-nothing", exec.ErrNotFound); err != exec.ErrNotFound {
		t.Errorf("an unknown program's error was replaced with %v", err)
	}
}
