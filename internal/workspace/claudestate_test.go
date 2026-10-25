package workspace

import (
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/claudestate"
	"github.com/jmwri/flockdeck/internal/session"
)

func pick(e *claudestate.Entry, pid int) (claudestate.Entry, bool) {
	if e == nil || e.Pid != pid {
		return claudestate.Entry{}, false
	}
	return *e, true
}

// A pane whose file disagrees with its hooks is brought into line, in both
// directions; one with no file, or whose file agrees, is left alone.
func TestClaudeStateServiceBringsAPaneIntoLineWithItsFile(t *testing.T) {
	s := idleShellSession(t)
	if s.Pid() == 0 {
		t.Skip("the pane has no process id")
	}
	time.Sleep(30 * time.Millisecond)
	at := time.Now().Add(-5 * time.Millisecond)
	var e *claudestate.Entry
	svc := &claudeStateService{
		panes:   func() []claudeStatePane { return []claudeStatePane{{Sess: s, Conversation: "conv"}} },
		readPid: func(pid int) (claudestate.Entry, bool) { return pick(e, pid) },
		readAll: func() []claudestate.Entry { return nil },
		started: func(int) (time.Time, bool) { return time.Time{}, false },
		settle:  0,
	}
	status := func() session.Status { st, _ := s.Status(); return st }
	file := func(busy bool, when time.Time) {
		e = &claudestate.Entry{Pid: s.Pid(), SessionID: "conv", Busy: busy, Updated: when}
	}

	svc.tick() // no file: left to the hooks
	if status() != session.StatusIdle {
		t.Fatalf("a pane with no file changed to %v", status())
	}
	file(false, at)
	svc.tick() // agrees
	if status() != session.StatusIdle {
		t.Fatalf("a pane whose file agrees changed to %v", status())
	}
	file(true, at)
	svc.tick() // hooks say idle, the agent says busy
	if status() != session.StatusWorking {
		t.Fatalf("busy in the file but %v in Flockdeck", status())
	}
	time.Sleep(30 * time.Millisecond)
	file(false, time.Now().Add(-5*time.Millisecond))
	svc.tick() // hooks say working, the agent says idle
	if status() != session.StatusIdle {
		t.Fatalf("idle in the file but %v in Flockdeck", status())
	}
}

// A file is used only when it is about this pane's conversation and the process
// that started when it says it did.
func TestClaudeStateFileMustBeThePanesOwn(t *testing.T) {
	s := idleShellSession(t)
	if s.Pid() == 0 {
		t.Skip("the pane has no process id")
	}
	started := time.Now().Add(-time.Hour)
	base := claudestate.Entry{Pid: s.Pid(), SessionID: "conv", Busy: true, Updated: time.Now(), Started: started}
	var all []claudestate.Entry
	cur := base
	svc := &claudeStateService{
		readAll: func() []claudestate.Entry { return all },
		started: func(int) (time.Time, bool) { return started, true },
		readPid: func(pid int) (claudestate.Entry, bool) { return pick(&cur, pid) },
	}
	pane := claudeStatePane{Sess: s, Conversation: "conv"}
	allFn := func() []claudestate.Entry { return all }

	if _, ok := svc.entryFor(pane, allFn); !ok {
		t.Fatal("the pane's own file was refused")
	}
	cur = base
	cur.SessionID = "another"
	if _, ok := svc.entryFor(pane, allFn); ok {
		t.Error("a file for another conversation was used")
	}
	cur = base
	cur.Started = started.Add(10 * time.Minute)
	if _, ok := svc.entryFor(pane, allFn); ok {
		t.Error("a file for a process that started at another time (a reused pid) was used")
	}
	// Where the start times cannot be compared, a file last changed long before the
	// pane began is a stale one on a reused pid.
	svc.started = func(int) (time.Time, bool) { return time.Time{}, false }
	cur = base
	cur.Started = time.Time{}
	cur.Updated = s.StartedAt().Add(-time.Hour)
	if _, ok := svc.entryFor(pane, allFn); ok {
		t.Error("a file last changed before the pane began was used")
	}
	cur.Updated = time.Now()
	if _, ok := svc.entryFor(pane, allFn); !ok {
		t.Error("a file changed since the pane began was refused")
	}
	// No file for the pid: the conversation finds it, if there is one.
	svc.readPid = func(int) (claudestate.Entry, bool) { return claudestate.Entry{}, false }
	all = []claudestate.Entry{{Pid: 4242, SessionID: "conv", Updated: time.Now()}}
	if e, ok := svc.entryFor(pane, allFn); !ok || e.Pid != 4242 {
		t.Errorf("the conversation fallback gave %+v, %v", e, ok)
	}
	if _, ok := svc.entryFor(claudeStatePane{Sess: s, Conversation: "none"}, allFn); ok {
		t.Error("a conversation with no file found one")
	}
}

// reconciledSession is a wake candidate whose status is a real session's, so
// what Reconcile does to it is what the wake service sees.
type reconciledSession struct {
	*wakeFakeSession
	real *session.Session
}

func (r reconciledSession) Status() (session.Status, string) { return r.real.Status() }

// A pane corrected to idle can be woken, and one corrected to working is not
// typed into.
func TestAReconciledStatusDecidesWhetherAPaneIsWoken(t *testing.T) {
	real := idleShellSession(t)
	r := newWakeRig(t)
	r.cand.Sess = reconciledSession{r.sess, real}

	real.SetStatus(session.StatusWorking, "Bash") // the hooks, long out of date
	time.Sleep(30 * time.Millisecond)
	if why := r.try(); why != "not idle" {
		t.Fatalf("a pane shown working was considered: %q", why)
	}
	if !real.Reconcile(false, time.Now().Add(-5*time.Millisecond), 0) {
		t.Fatal("the idle agent was not corrected")
	}
	if why := r.try(); why != "" {
		t.Fatalf("a pane corrected to idle was not woken: %q", why)
	}
	if len(r.sess.written()) != 2 {
		t.Errorf("typed %q, want the line and its Enter", r.sess.written())
	}

	// And the other way: the hooks say idle, the agent is busy.
	r = newWakeRig(t)
	r.cand.Sess = reconciledSession{r.sess, real}
	real.SetStatus(session.StatusIdle, "")
	time.Sleep(30 * time.Millisecond)
	if !real.Reconcile(true, time.Now().Add(-5*time.Millisecond), 0) {
		t.Fatal("the busy agent was not corrected")
	}
	if why := r.try(); why != "not idle" {
		t.Fatalf("a pane corrected to working was considered: %q", why)
	}
	if n := len(r.sess.written()); n != 0 {
		t.Errorf("typed %q into a busy pane", r.sess.written())
	}
}

// What the typed wake acts on is the pane's own pid file and nothing found any
// other way: a file for the conversation with none for the pid is no word, nor is
// the previous process's file left on disk by a restart, nor one from before a
// clear.
func TestTheWakeActsOnlyOnThePanesOwnPidFile(t *testing.T) {
	s := idleShellSession(t)
	if s.Pid() == 0 {
		t.Skip("the pane has no process id")
	}
	var cur *claudestate.Entry
	all := []claudestate.Entry{{Pid: 777, SessionID: "conv", Updated: time.Now()}}
	svc := &claudeStateService{
		readPid: func(pid int) (claudestate.Entry, bool) { return pick(cur, pid) },
		readAll: func() []claudestate.Entry { return all },
		started: func(int) (time.Time, bool) { return time.Time{}, false },
	}
	pane := claudeStatePane{Sess: s, Conversation: "conv"}

	// Only a conversation file: the status check may use it, the wake may not.
	if _, found, ok := svc.ownEntry(pane); found || ok {
		t.Error("a pane with no pid file of its own had one")
	}
	if e, ok := svc.entryFor(pane, func() []claudestate.Entry { return all }); !ok || e.Pid != 777 {
		t.Errorf("the status check lost its conversation fallback: %+v %v", e, ok)
	}
	// The previous process of a restarted pane left its file; this process began later.
	cur = &claudestate.Entry{Pid: s.Pid(), SessionID: "conv", Busy: false, Updated: s.StartedAt().Add(-time.Hour)}
	if _, found, ok := svc.ownEntry(pane); !found || ok {
		t.Errorf("a file from before this process began was taken: found=%v ok=%v", found, ok)
	}
	// A clear changes the session id under the pane.
	cur = &claudestate.Entry{Pid: s.Pid(), SessionID: "before-the-clear", Updated: time.Now()}
	if _, found, ok := svc.ownEntry(pane); !found || ok {
		t.Errorf("a file for another session was taken: found=%v ok=%v", found, ok)
	}
	// And its own is taken.
	cur = &claudestate.Entry{Pid: s.Pid(), SessionID: "conv", Updated: time.Now()}
	if e, found, ok := svc.ownEntry(pane); !found || !ok || e.Pid != s.Pid() {
		t.Errorf("the pane's own file was refused: %+v %v %v", e, found, ok)
	}
}
