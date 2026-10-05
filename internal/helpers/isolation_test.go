package helpers

import (
	"os"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/testiso"
)

// TestMain keeps these tests off the real config directory, the stored API
// keys and the real network. See docs/testing.md.
//
// It is also where the fake helper (this binary run again as a child, see
// fakehelper_test.go) writes out its environment, before testiso adds
// variables of its own, so a test can see exactly what the supervisor passed.
func TestMain(m *testing.M) {
	if args := fakeArgs(); args != nil {
		for i, a := range args {
			if a == "--dump-env" && i+1 < len(args) {
				_ = os.WriteFile(args[i+1], []byte(strings.Join(os.Environ(), "\n")), 0o600)
			}
		}
	}
	os.Exit(testiso.Main(m))
}
