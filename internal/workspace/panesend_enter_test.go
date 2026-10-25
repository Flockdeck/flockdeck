package workspace

import (
	"sync"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
)

// sinkWrite is one write a fakeSink saw.
type sinkWrite struct {
	data string
	at   time.Time
}

// fakeSink stands in for the terminal and notes what is written to it and when.
type fakeSink struct {
	bracketed bool
	mu        sync.Mutex
	writes    []sinkWrite
}

func (f *fakeSink) BracketedPaste() bool { return f.bracketed }

func (f *fakeSink) WriteStringFrom(_ session.InputSource, s string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, sinkWrite{s, time.Now()})
	return nil
}

// The Enter that submits a prompt is a write of its own, a moment after the
// text, whether the text was typed or pasted. An agent that reads text and Enter
// arriving together as one paste takes the Enter for a line break and leaves the
// text in its prompt, which is the bug this is the guard for.
func TestEnterIsAWriteOfItsOwnAfterTheText(t *testing.T) {
	cases := []struct {
		name      string
		text      string
		bracketed bool
		wantText  string
		// gap is the least wait the test accepts, written out so changing the
		// settle times cannot change what is asked of them.
		gap time.Duration
	}{
		{"one line, no paste", "Great. Full review please", false, "Great. Full review please", 150 * time.Millisecond},
		{"one line, program takes pastes", "Great. Full review please", true, "Great. Full review please", 150 * time.Millisecond},
		{"two lines, program takes pastes", "one\ntwo", true, "\x1b[200~one\rtwo\x1b[201~", pasteSettle},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeSink{bracketed: c.bracketed}
			typeIntoSink(f, session.InputFlockdeck, c.text, true)
			if len(f.writes) != 2 {
				t.Fatalf("%d writes, want the text and then the Enter: %+v", len(f.writes), f.writes)
			}
			if f.writes[0].data != c.wantText || f.writes[1].data != "\r" {
				t.Errorf("writes %q, want %q then \"\r\"", []string{f.writes[0].data, f.writes[1].data}, c.wantText)
			}
			if gap := f.writes[1].at.Sub(f.writes[0].at); gap < c.gap {
				t.Errorf("the Enter came %v after the text, want at least %v", gap, c.gap)
			}
		})
	}
	f := &fakeSink{}
	typeIntoSink(f, session.InputFlockdeck, "draft", false)
	if len(f.writes) != 1 || f.writes[0].data != "draft" {
		t.Errorf("a draft is typed without an Enter: %+v", f.writes)
	}
}

func idleShellSession(t *testing.T) *session.Session {
	t.Helper()
	isolateConfig(t)
	root := t.TempDir()
	ws, err := New(Options{Root: root})
	if err != nil {
		t.Fatalf("new workspace: %v", err)
	}
	t.Cleanup(ws.Close)
	tab := ws.NewTab(session.KindShell, root, "shell")
	p := ws.Pane(tab.Tree.Panes()[0])
	if p == nil || !p.Alive() {
		t.Fatal("the shell pane did not start")
	}
	p.Sess.SetStatus(session.StatusIdle, "")
	return p.Sess
}

// An agent that starts a turn straight after the Enter is not sent another.
func TestSubmittedTextIsNotSentASecondEnter(t *testing.T) {
	s := idleShellSession(t)
	go func() {
		time.Sleep(60 * time.Millisecond)
		s.SetStatus(session.StatusWorking, "Read")
	}()
	if err := confirmSubmitted(s, 400*time.Millisecond); err != nil {
		t.Fatalf("a turn that started was reported as not sent: %v", err)
	}
	if !s.LastFlockdeckInput().IsZero() {
		t.Error("an Enter was sent to an agent that had started")
	}
}

// An agent that has not started after the wait is sent one more Enter, and if it
// starts then, the send is not reported as failed.
func TestAnAgentThatStartsOnTheSecondEnterIsNotReportedUnsent(t *testing.T) {
	s := idleShellSession(t)
	go func() {
		time.Sleep(500 * time.Millisecond)
		s.SetStatus(session.StatusWorking, "Read")
	}()
	if err := confirmSubmitted(s, 400*time.Millisecond); err != nil {
		t.Fatalf("a turn that started on the second Enter was reported as not sent: %v", err)
	}
	if s.LastFlockdeckInput().IsZero() {
		t.Error("no second Enter was sent")
	}
}

// One that never starts is reported, after exactly one more Enter, and not
// silently.
func TestAnAgentThatNeverStartsIsReportedUnsent(t *testing.T) {
	s := idleShellSession(t)
	start := time.Now()
	err := confirmSubmitted(s, 200*time.Millisecond)
	if err != ErrSendNotSubmitted {
		t.Fatalf("err = %v, want ErrSendNotSubmitted", err)
	}
	if took := time.Since(start); took < 400*time.Millisecond {
		t.Errorf("gave up after %v, want two waits of 200ms", took)
	}
	if s.LastFlockdeckInput().IsZero() {
		t.Error("no second Enter was sent before giving up")
	}
}
