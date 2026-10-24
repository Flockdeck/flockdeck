package boundary

import (
	"os"
	"testing"

	"github.com/jmwri/flockdeck/internal/testiso"
)

// TestMain keeps these tests, and the node processes they start, off the real
// config directory. See docs/testing.md.
func TestMain(m *testing.M) { os.Exit(testiso.Main(m)) }
