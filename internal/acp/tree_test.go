package acp

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// Close ends everything the agent started, not just the agent. The other close
// tests count goroutines; this one looks at processes.
func TestCloseEndsWhatTheAgentStarted(t *testing.T) {
	r := newRecorder()
	c, sid := start(t, r)
	prompt(t, c, sid, "grandchild")
	text := r.text()
	pid, err := strconv.Atoi(strings.TrimPrefix(text, "pid="))
	if err != nil || pid <= 0 {
		t.Fatalf("the agent reported %q, want pid=N", text)
	}
	t.Cleanup(func() { endProcess(pid) })
	if !alive(pid) {
		t.Fatalf("the process the agent started (%d) is not running before Close", pid)
	}

	_ = c.Close()

	deadline := time.Now().Add(10 * time.Second)
	for alive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("process %d, started by the agent, is still running after Close", pid)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
