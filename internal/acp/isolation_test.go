package acp

import (
	"os"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp/acptest"
	"github.com/jmwri/flockdeck/internal/testiso"
)

// TestMain keeps these tests off the real config directory, the stored API
// keys and the real network (see docs/testing.md). The tests start only the
// fake agent, which is this test binary run again: when it was started as the
// agent it runs as one and exits without the isolation.
func TestMain(m *testing.M) {
	if acptest.RunIfAgent() {
		return
	}
	os.Exit(testiso.Main(m))
}
