//go:build !windows

package proctree

import (
	"testing"
	"time"
)

// TestEndWithoutAReapedChannelSignalsOnlyTheProcess covers a caller that does
// not know whether the process was reaped. Its id could be another process's,
// so End must not signal the process group or the session.
func TestEndWithoutAReapedChannelSignalsOnlyTheProcess(t *testing.T) {
	s := startParent(t)
	s.tree.End(s.cmd.Process, nil, time.Second)
	waitGone(t, "End with a nil reaped", s.cmd.Process.Pid)
	if !alive(s.child) {
		t.Fatal("End with a nil reaped signalled the process's session")
	}
}
