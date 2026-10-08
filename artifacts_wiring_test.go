package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/remote"
	"github.com/jmwri/flockdeck/internal/server"
)

// The remote manager is both halves of what the artifacts socket needs to
// decide a device may be served: the record of verified keys, and the key a
// device would handshake with. If either method changes shape the build breaks
// here rather than the socket quietly serving nobody.
var (
	_ server.ArtifactVerifier = (*remote.Manager)(nil)
	_ interface {
		E2EDeviceKey(ctx context.Context, deviceID string, origin remote.KeyOrigin) ([]byte, bool)
	} = (*remote.Manager)(nil)
)

// TestMainGivesTheServerItsArtifactVerifier reads main.go: the server is only
// told which devices are verified by a call made there, and a build without it
// compiles and serves no device at all.
func TestMainGivesTheServerItsArtifactVerifier(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "srv.SetArtifactVerifier(remoteAccess)") {
		t.Error("main.go does not call srv.SetArtifactVerifier with the remote manager")
	}
}
