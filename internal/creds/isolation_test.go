package creds

import (
	"os"
	"testing"

	"github.com/jmwri/flockdeck/internal/testiso"
	"github.com/jmwri/flockdeck/internal/testiso/depth"
)

// TestMain keeps these tests off the real config directory, the stored API
// keys and the real network. See docs/testing.md. The test binary is also the
// writer in TestKeysSetFromManyProcessesAreAllKept: started with envWriter set
// it saves its keys and exits, without the suite.
func TestMain(m *testing.M) {
	depth.Guard()
	if os.Getenv(envWriter) != "" {
		os.Exit(runWriter())
	}
	os.Exit(testiso.Main(m))
}
