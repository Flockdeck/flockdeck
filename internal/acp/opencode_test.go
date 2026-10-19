//go:build acp_opencode

package acp

import (
	"os"
	"os/exec"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp/acptest"
)

// TestOpencodeStartsASession runs a real agent, so it is behind the
// acp_opencode build tag and also needs FLOCKDECK_ACP_OPENCODE=1 and opencode
// on PATH. It does initialize and session/new only: no prompt is sent, so
// nothing reaches a model.
//
// The spike found that opencode ignores a redirected home and creates empty
// directories under the real profile (.config, .local, .cache). Run it
// knowing that, and never from a CI job.
func TestOpencodeStartsASession(t *testing.T) {
	if os.Getenv("FLOCKDECK_ACP_OPENCODE") != "1" {
		t.Skip("set FLOCKDECK_ACP_OPENCODE=1 to run a real opencode")
	}
	path, err := exec.LookPath("opencode")
	if err != nil {
		t.Skip("opencode is not installed")
	}
	r := newRecorder()
	c, err := Start(Config{Path: path, Args: []string{"acp"}, Dir: acptest.SessionDir(t), Env: os.Environ(), Handler: r})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if _, err := c.Initialize(ctxT(t)); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if _, err := c.NewSession(ctxT(t), acptest.SessionDir(t), nil); err != nil {
		t.Fatalf("session/new: %v", err)
	}
}
