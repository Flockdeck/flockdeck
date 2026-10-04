package helpers

import (
	"os"
	"testing"

	"github.com/jmwri/flockdeck/internal/testiso"
)

// TestMain keeps these tests off the real config directory, the stored API
// keys and the real network. See docs/testing.md.
func TestMain(m *testing.M) { os.Exit(testiso.Main(m)) }
