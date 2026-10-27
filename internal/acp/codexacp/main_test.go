package codexacp

import (
	"os"
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
	os.Exit(testiso.Main(m))
}
