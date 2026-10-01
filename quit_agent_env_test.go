package main

import (
	"bytes"
	"github.com/jmwri/flockdeck/internal/testiso/iso"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// mainHelper is the copy's side of TestQuitIgnoresTheStartAgent: it runs main
// with the arguments it was given, as the program would be.
func mainHelper() {
	os.Args = append([]string{"flockdeck"}, strings.Fields(os.Getenv("FLOCKDECK_TEST_MAIN_ARGS"))...)
	main()
	os.Exit(0)
}

// -quit stops an instance and starts no pane, but it was refused, with exit
// status 2, whenever the agent new panes start as -- -agent, or the
// FLOCKDECK_START_AGENT a service's environment carries -- was not in the
// catalog, so a stale value there kept the service from being stopped.
func TestQuitIgnoresTheStartAgent(t *testing.T) {
	if os.Getenv("FLOCKDECK_TEST_MAIN_ARGS") != "" {
		mainHelper()
	}
	dir := t.TempDir()
	for _, args := range []string{"-quit", "-agent nosuchagent -quit"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestQuitIgnoresTheStartAgent$")
		cmd.Env = []string{
			"FLOCKDECK_TEST_MAIN_ARGS=" + args,
			"FLOCKDECK_START_AGENT=nosuchagent",
			"APPDATA=" + dir, "LOCALAPPDATA=" + dir, "XDG_CONFIG_HOME=" + dir,
			"HOME=" + dir, "USERPROFILE=" + dir,
			"SystemRoot=" + os.Getenv("SystemRoot"), "PATH=" + os.Getenv("PATH"),
		}
		cmd.Env = append(cmd.Env, iso.ChildEnv()...)
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Run(); err != nil {
			t.Errorf("flockdeck %s: %v; output:\n%s", args, err, out.String())
			continue
		}
		if !strings.Contains(out.String(), "nothing is running") {
			t.Errorf("flockdeck %s said %q, want that nothing is running", args, out.String())
		}
	}
}
