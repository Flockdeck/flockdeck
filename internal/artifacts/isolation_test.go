package artifacts

import (
	"os"
	"testing"

	"github.com/jmwri/flockdeck/internal/testiso"
)

// TestMain keeps these tests off the real config directory, the stored API
// keys and the real network. See docs/testing.md. Every file these tests make
// is under t.TempDir; none reads a real home or ~/.claude.
func TestMain(m *testing.M) { os.Exit(testiso.Main(m)) }
