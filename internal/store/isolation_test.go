package store

import (
	"os"
	"testing"

	"github.com/jmwri/flockdeck/internal/testiso/iso"
)

// TestMain keeps these tests off the real config directory. This package
// cannot use internal/testiso (it imports this one), so it arms the guard
// itself. See docs/testing.md.
func TestMain(m *testing.M) {
	DirGuard = iso.DirGuard
	os.Exit(iso.Main(m))
}
