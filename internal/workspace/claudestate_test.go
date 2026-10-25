package workspace

import (
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/claudestate"
	"github.com/jmwri/flockdeck/internal/session"
)

// A pane whose file disagrees with its hooks is brought into line, in both
// directions; one with no file, or whose file agrees, is left alone.
func TestClaudeStateServiceBringsAPaneIntoLineWithItsFile(t *testing.T) {
	s := idleShellSession(t)
	if s.Pid() == 0 {
		t.Skip("the pane has no process id")
	}
	time.Sleep(30 * time.Millisecond)
	at := time.Now().Add(-5 * time.Millisecond)
	var entries []claudestate.Entry
	svc := &claudeStateService{
		panes:  func() []claudeStatePane { return []claudeStatePane{{Sess: s}} },
		read:   func() []claudestate.Entry { return entries },
		settle: 0,
	}
	status := func() session.Status { st, _ := s.Status(); return st }

	svc.tick() // no file: left to the hooks
	if status() != session.StatusIdle {
		t.Fatalf("a pane with no file changed to %v", status())
	}
	entries = []claudestate.Entry{{Pid: s.Pid(), Busy: false, Updated: at}}
	svc.tick() // agrees
	if status() != session.StatusIdle {
		t.Fatalf("a pane whose file agrees changed to %v", status())
	}
	entries = []claudestate.Entry{{Pid: s.Pid(), Busy: true, Updated: at}}
	svc.tick() // hooks say idle, the agent says busy
	if status() != session.StatusWorking {
		t.Fatalf("busy in the file but %v in Flockdeck", status())
	}
	time.Sleep(30 * time.Millisecond)
	entries = []claudestate.Entry{{Pid: s.Pid(), Busy: false, Updated: time.Now().Add(-5 * time.Millisecond)}}
	svc.tick() // hooks say working, the agent says idle
	if status() != session.StatusIdle {
		t.Fatalf("idle in the file but %v in Flockdeck", status())
	}
}
