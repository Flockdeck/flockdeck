//go:build !windows

package manifest

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// leaveSession makes c start in a session of its own, out of the reach of the
// tree that is ended with its parent.
func leaveSession(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

// A descendant that has made its own session is not ended with the adapter,
// and it still holds the adapter's stdout and stderr. Handshake has to return
// anyway: it closes its read ends rather than wait for them to reach EOF.
func TestHandshakeDoesNotWaitForADescendantThatEscapedTheTree(t *testing.T) {
	cfg := fakeAdapter(t, "escape")
	started := time.Now()
	res, err := handshakeWithin(t, cfg, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.ProtocolVersion != 1 {
		t.Errorf("result = %+v", res)
	}
	if took := time.Since(started); took > 10*time.Second {
		t.Errorf("took %v; it waited for the escaped descendant, which lives 20s", took)
	}
}
