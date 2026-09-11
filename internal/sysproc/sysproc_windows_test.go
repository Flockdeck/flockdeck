//go:build windows

package sysproc

import (
	"os/exec"
	"syscall"
	"testing"
)

// The flag is the whole fix, and a caller that had already asked for something
// of its own -- a process group, a hidden start -- must not lose it to this.
func TestNoWindow(t *testing.T) {
	const newProcessGroup = 0x00000200 // CREATE_NEW_PROCESS_GROUP
	cases := []struct {
		name      string
		attr      *syscall.SysProcAttr
		wantFlags uint32
		wantHide  bool
	}{
		{"nothing set yet", nil, createNoWindow, false},
		{"flags of its own", &syscall.SysProcAttr{CreationFlags: newProcessGroup},
			newProcessGroup | createNoWindow, false},
		{"other fields of its own", &syscall.SysProcAttr{HideWindow: true},
			createNoWindow, true},
		{"already marked", &syscall.SysProcAttr{CreationFlags: createNoWindow},
			createNoWindow, false},
	}
	for _, c := range cases {
		cmd := exec.Command("git", "--version")
		cmd.SysProcAttr = c.attr
		NoWindow(cmd)
		if cmd.SysProcAttr == nil {
			t.Errorf("%s: SysProcAttr is still nil", c.name)
			continue
		}
		if got := cmd.SysProcAttr.CreationFlags; got != c.wantFlags {
			t.Errorf("%s: CreationFlags = %#x, want %#x", c.name, got, c.wantFlags)
		}
		if got := cmd.SysProcAttr.HideWindow; got != c.wantHide {
			t.Errorf("%s: HideWindow = %v, want %v", c.name, got, c.wantHide)
		}
	}
}

// A console program started this way still runs and still hands back its
// output: hiding the window must not be the same as taking the console away.
func TestNoWindowStillRuns(t *testing.T) {
	// The words go separately: Go quotes an argument containing a space on
	// Windows, and echo would print the quotes back.
	cmd := exec.Command("cmd", "/c", "echo", "still", "here")
	NoWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("cmd /c echo: %v", err)
	}
	if got := string(out); got != "still here\r\n" {
		t.Errorf("output = %q, want %q", got, "still here\r\n")
	}
}
