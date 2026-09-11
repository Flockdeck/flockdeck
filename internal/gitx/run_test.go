package gitx

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestDeadlineHoldsWhenAChildKeepsTheOutputOpen is about what git starts
// rather than git itself. The deadline kills git; a hook or an ssh it left
// running still holds the pipes git wrote to, and waiting for those to close
// used to outlast the deadline by as long as the child cared to run.
func TestDeadlineHoldsWhenAChildKeepsTheOutputOpen(t *testing.T) {
	if !Available() {
		t.Skip("git is not installed")
	}
	start := time.Now()
	_, _, err := runCapture(context.Background(), time.Second, t.TempDir(),
		"-c", "alias.hang=!sleep 15", "hang")
	if err == nil || !strings.Contains(err.Error(), "gave up") {
		t.Fatalf("err = %v, want the deadline reported", err)
	}
	if took := time.Since(start); took > 8*time.Second {
		t.Fatalf("returned after %s, long past a 1s deadline", took)
	}
}
