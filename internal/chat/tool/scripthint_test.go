package tool

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// On Windows a script run as though it were a program is answered with how to
// run it, whether Windows said it was not found or did not exist.
func TestAScriptOnWindowsIsAnsweredWithHowToRunIt(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("scripts are run as programs everywhere else")
	}
	if err := notFoundHint("build.ps1", &exec.Error{Name: "build.ps1", Err: exec.ErrNotFound}); !strings.Contains(err.Error(), "powershell -File build.ps1") {
		t.Errorf("a .ps1 not found: %v", err)
	}
	if err := notFoundHint("./deploy.sh", &exec.Error{Name: "./deploy.sh", Err: os.ErrNotExist}); !strings.Contains(err.Error(), "bash ./deploy.sh") {
		t.Errorf("a .sh with a path: %v", err)
	}
	// An ordinary program that is not there still gets the ordinary answer.
	plain := &exec.Error{Name: "frobnicate", Err: exec.ErrNotFound}
	if err := notFoundHint("frobnicate", plain); !errors.Is(err, plain) {
		t.Errorf("an ordinary missing program: %v", err)
	}

	// And through the tool, as a model would run it.
	root := newRoot(t)
	write(t, root, "build.ps1", "Write-Output hi")
	if _, err := call(t, &runCommand{root: root}, map[string]any{"command": "build.ps1"}); err == nil ||
		!strings.Contains(err.Error(), "powershell -File build.ps1") {
		t.Errorf("run_command build.ps1: %v", err)
	}
}
