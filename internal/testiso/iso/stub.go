package iso

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// StubNames are the command names of the agents Flockdeck launches by name, the
// ones whose spec in internal/agent has an Exe. A test that opens a pane with
// one of them would start whatever of that name is installed on the machine:
// on a developer's, the real agent, writing its first-run files into the test
// home and talking to its service. Main puts a stub of each first on PATH
// instead. A test in internal/testiso checks the list against the catalog, so
// a new agent cannot be added without a stub.
var StubNames = []string{"claude", "codex", "gemini", "aider", "opencode", "cursor-agent"}

// stubDirName is the directory the stubs are in, inside the test home.
const stubDirName = "agent-stubs"

// stubLogEnv, when set to a file name, makes a stub append a line there for
// each run: its name, arguments, working directory and home. It has no
// FLOCKDECK_ prefix, which Main clears.
const stubLogEnv = "ISO_STUB_LOG"

// stubIdleLimit is the longest a stub idles with nobody reading from it, so one
// whose pane was never closed does not outlive the run for long.
const stubIdleLimit = 10 * time.Minute

// liveAgentsEnv is the switch the live agent tests are gated on. With it set
// Main leaves the real binaries alone.
const liveAgentsEnv = "FLOCKDECK_LIVE_AGENT_TESTS"

// IsStub reports whether path is one of the stubs Main installed.
func IsStub(path string) bool {
	return filepath.Base(filepath.Dir(path)) == stubDirName && stubName(filepath.Base(path)) != ""
}

// stubName is the agent a program name stands for, or "" for any other. The
// name is the program's file name, without the .exe a Windows binary has.
func stubName(program string) string {
	name := strings.TrimSuffix(strings.ToLower(program), ".exe")
	for _, n := range StubNames {
		if name == n {
			return n
		}
	}
	return ""
}

// installStubs makes the stub directory under home and returns it. Each stub is
// a link to this test binary (or a copy where a link cannot be made), which
// Main recognises by the name it was started under.
func installStubs(home string) (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, stubDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	for _, n := range StubNames {
		dst := filepath.Join(dir, n+ext)
		if os.Link(self, dst) == nil {
			continue
		}
		if err := copyExecutable(self, dst); err != nil {
			return "", err
		}
	}
	return dir, nil
}

func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o700)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// prependPath puts dir first on PATH.
func prependPath(dir string) {
	os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// runStub is what a stub does when it is started: it answers the questions an
// installed agent is asked about itself and otherwise idles, as the agent in a
// pane would, until its input ends. It returns the exit code.
func runStub(name string, args []string) int {
	if path := os.Getenv(stubLogEnv); path != "" {
		if f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			wd, _ := os.Getwd()
			home, _ := os.UserHomeDir()
			fmt.Fprintf(f, "%s args=%q wd=%s home=%s\n", name, args, wd, home)
			f.Close()
		}
	}
	for _, a := range args {
		switch a {
		case "--version", "-v", "-V", "version":
			fmt.Printf("0.0.0 (flockdeck test stub for %s)\n", name)
			return 0
		case "--help", "-h", "help":
			fmt.Printf("flockdeck test stub for %s: not the real program\n", name)
			return 0
		}
	}
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(stubIdleLimit):
	}
	return 0
}
