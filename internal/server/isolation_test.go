package server

import (
	"os"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp/acptest"
	"github.com/jmwri/flockdeck/internal/testiso"
)

// TestMain keeps these tests off the real config directory, the stored API
// keys and the real network. See docs/testing.md.
func TestMain(m *testing.M) {
	// The fake agent structured panes are started with is this binary.
	if acptest.RunIfAgent() {
		return
	}
	os.Exit(testiso.Main(m))
}
