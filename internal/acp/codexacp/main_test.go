package codexacp

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/testiso"
	"github.com/jmwri/flockdeck/internal/testiso/depth"
)

// TestMain keeps these tests off the real config directory and the network
// (see docs/testing.md). The test binary is also the fake codex: when it was
// started as one it runs as that and exits without the isolation. The chain is
// the test, then the fake codex, which starts nothing.
func TestMain(m *testing.M) {
	depth.Guard()
	if os.Getenv(envFake) == "1" {
		runFake()
		return
	}
	if os.Getenv(envAdapt) == "1" {
		// the adapter, for the interop test: it starts the fake codex
		exe, _ := os.Executable()
		var env []string
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, envAdapt+"=") {
				env = append(env, kv)
			}
		}
		if err := Run(context.Background(), os.Stdin, os.Stdout, Options{CodexPath: exe, Env: append(env, envFake+"=1"), Stderr: os.Stderr}); err != nil {
			os.Exit(1)
		}
		return
	}
	os.Exit(testiso.Main(m))
}
