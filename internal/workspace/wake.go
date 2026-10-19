package workspace

import (
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/team"
)

// The wake service types one fixed line into an idle agent's pane to tell it
// that it has mail, for an agent that has no hook to answer while it sits at its
// prompt. It reverses an old rule on purpose (a pane used to be typed into only
// by the user), so everything about it is narrow: one line, never a sender or
// message text, only at a prompt a tested built-in rule recognises, and only
// while nobody is at the keyboard.
//
// Settings for the service (global, per agent type, per pane) are B6's. The
// service reads them through WakeSettings and, until that exists, everything is
// on for an agent whose rule is tested, which none is yet.

// Conditions and limits. All conditions must hold.
const (
	// wakeTick is how often panes with mail are looked at.
	wakeTick = 2 * time.Second
	// wakeQuiet is how long the tail of the pane's output must have been
	// unchanged.
	wakeQuiet = 5 * time.Second
	// wakeUserQuiet is how long nobody may have typed into the pane.
	wakeUserQuiet = 30 * time.Second
	// wakeHookQuiet is how long after a hook notice the agent is left alone.
	wakeHookQuiet = time.Minute
	// wakePaneEvery is the least time between two typed lines in one pane.
	wakePaneEvery = time.Minute
	// wakeProjectHour is the default ceiling of typed lines per project in an
	// hour.
	wakeProjectHour = 20
	// wakeStreakMax is how many lines in a row may be typed into a pane with no
	// `inbox read` between them.
	wakeStreakMax = 3
)

// wakeLine is exactly what is typed: a fixed sentence and the digits of the
// newest unread message's id. Never a sender, never message text.
func wakeLine(id int) string {
	return fmt.Sprintf("Flockdeck: new mail, message %d. Run: flockdeck inbox read %d", id, id)
}

// WakeSettings says whether the wake service may type into a pane. B6 supplies
// the real one; the default allows everything.
type WakeSettings interface {
	WakeEnabled(agentID, paneID string) bool
}

type wakeAllOn struct{}

func (wakeAllOn) WakeEnabled(string, string) bool { return true }

// wakeSession is what the service needs of a pane's session. *session.Session
// is one; a test supplies another.
type wakeSession interface {
	WakeReading() session.WakeReading
	LastUserInput() time.Time
	Status() (session.Status, string)
	Exited() bool
	WriteStringFrom(src session.InputSource, text string) error
}

// wakeMail is what the service needs of the inboxes. *team.Store is one.
type wakeMail interface {
	List(pane string, all bool) []team.Message
	NewestUnread(pane string) int
	MarkWoken(pane string, ids ...int)
}

// wakeNotices is what it needs of the hook notifier. *team.Notifier is one.
type wakeNotices interface {
	LastNotice(pane string) time.Time
}

// wakeCandidate is one live agent pane the service may consider.
type wakeCandidate struct {
	ID    string
	Agent string
	Root  string
	Sess  wakeSession
}

// wakeService decides, for each agent pane with unread mail, whether to type
// the line, and types it.
type wakeService struct {
	panes    func() []wakeCandidate
	mail     wakeMail
	notices  wakeNotices
	settings func() WakeSettings
	record   func(team.Event)

	now   func() time.Time
	sleep func(d time.Duration, stop <-chan struct{}) bool
	// settle is the pause between the line and its Enter.
	settle time.Duration
	// projectHour is the per-project ceiling per hour.
	projectHour int

	mu       sync.Mutex
	state    map[string]*wakePaneState
	projects map[string][]time.Time

	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// wakePaneState is what the service remembers about one pane.
type wakePaneState struct {
	lastWake time.Time
	// streak is how many lines were typed since the agent last read mail, and
	// streakFrom when the first of them was.
	streak     int
	streakFrom time.Time
	inFlight   bool
}

func newWakeService(panes func() []wakeCandidate, mail wakeMail, notices wakeNotices,
	settings func() WakeSettings, record func(team.Event)) *wakeService {
	return &wakeService{
		panes: panes, mail: mail, notices: notices, settings: settings, record: record,
		now:         time.Now,
		sleep:       realSleep,
		settle:      pasteSettle,
		projectHour: wakeProjectHour,
		state:       map[string]*wakePaneState{},
		projects:    map[string][]time.Time{},
		stop:        make(chan struct{}),
	}
}

// realSleep waits d, and reports false if stop closed first.
func realSleep(d time.Duration, stop <-chan struct{}) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-stop:
		return false
	}
}

// run looks at the panes every wakeTick until Stop.
func (s *wakeService) run() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		t := time.NewTicker(wakeTick)
		defer t.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-t.C:
				s.tick()
			}
		}
	}()
}

// Stop ends the service and waits for any line in flight to finish or give up.
func (s *wakeService) Stop() {
	s.stopOnce.Do(func() { close(s.stop) })
	s.wg.Wait()
}

// tick considers every live agent pane once.
func (s *wakeService) tick() {
	seen := map[string]bool{}
	for _, c := range s.panes() {
		seen[c.ID] = true
		s.consider(c)
	}
	// What is remembered of a pane that has closed goes with it.
	s.mu.Lock()
	for id := range s.state {
		if !seen[id] && !s.state[id].inFlight {
			delete(s.state, id)
		}
	}
	s.mu.Unlock()
}

// consider checks every condition for one pane and, if all hold, types the
// line. It returns why not, or "" when a line was started, for the tests.
func (s *wakeService) consider(c wakeCandidate) string {
	id := s.mail.NewestUnread(c.ID)
	if id == 0 {
		return "no unread mail"
	}
	if c.Sess == nil || c.Sess.Exited() {
		return "no running process"
	}
	if !s.settings().WakeEnabled(c.Agent, c.ID) {
		return "switched off"
	}
	now := s.now()
	if st, _ := c.Sess.Status(); st != session.StatusIdle {
		// Waiting on anything, working, starting: not at an empty prompt.
		return "not idle"
	}
	if at := s.notices.LastNotice(c.ID); !at.IsZero() && now.Sub(at) < wakeHookQuiet {
		return "told by a hook a moment ago"
	}
	if at := c.Sess.LastUserInput(); !at.IsZero() && now.Sub(at) < wakeUserQuiet {
		return "the user is typing"
	}
	r := c.Sess.WakeReading()
	if r.Verdict != agent.WakeOK {
		return "the rule does not allow it"
	}
	if now.Sub(r.Tail.ChangedAt) < wakeQuiet {
		return "output changed a moment ago"
	}

	s.mu.Lock()
	st := s.state[c.ID]
	if st == nil {
		st = &wakePaneState{}
		s.state[c.ID] = st
	}
	if st.inFlight {
		s.mu.Unlock()
		return "a line is being typed"
	}
	if st.streak > 0 && s.readSince(c.ID, st.streakFrom) {
		st.streak = 0
	}
	switch {
	case !st.lastWake.IsZero() && now.Sub(st.lastWake) < wakePaneEvery:
		s.mu.Unlock()
		return "one line a minute"
	case st.streak >= wakeStreakMax:
		s.mu.Unlock()
		return "waiting for the agent to read"
	}
	recent := s.projects[c.Root][:0:0]
	for _, at := range s.projects[c.Root] {
		if now.Sub(at) < time.Hour {
			recent = append(recent, at)
		}
	}
	if len(recent) >= s.projectHour {
		s.projects[c.Root] = recent
		s.mu.Unlock()
		return "project limit for the hour"
	}
	s.projects[c.Root] = append(recent, now)
	st.lastWake = now
	if st.streak == 0 {
		st.streakFrom = now
	}
	st.streak++
	st.inFlight = true
	s.mu.Unlock()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.fire(c, id, r)
	}()
	return ""
}

// readSince reports whether the pane's agent ran `inbox read` on any message
// since t.
func (s *wakeService) readSince(pane string, t time.Time) bool {
	for _, m := range s.mail.List(pane, true) {
		if m.ReadAt.After(t) {
			return true
		}
	}
	return false
}

// fire types the line and its Enter. The user is looked for again just before
// the line, and again before the Enter: someone starting to type in the pause
// is not sent an Enter that would submit their half-written text with ours.
func (s *wakeService) fire(c wakeCandidate, id int, r session.WakeReading) {
	defer func() {
		s.mu.Lock()
		if st := s.state[c.ID]; st != nil {
			st.inFlight = false
		}
		s.mu.Unlock()
	}()
	typing := func() bool {
		at := c.Sess.LastUserInput()
		return !at.IsZero() && s.now().Sub(at) < wakeUserQuiet
	}
	if typing() {
		s.undo(c.ID)
		return
	}
	// The pane may have changed since it was judged.
	if again := c.Sess.WakeReading(); again.Verdict != r.Verdict || !again.Tail.ChangedAt.Equal(r.Tail.ChangedAt) {
		s.undo(c.ID)
		return
	}
	line := wakeLine(id)
	if err := c.Sess.WriteStringFrom(session.InputFlockdeck, line); err != nil {
		s.undo(c.ID)
		return
	}
	typedAt := s.now()
	submitted := false
	if s.sleep(s.settle, s.stop) {
		if at := c.Sess.LastUserInput(); at.IsZero() || !at.After(typedAt) {
			submitted = c.Sess.WriteStringFrom(session.InputFlockdeck, "\r") == nil
		}
	}
	_ = submitted
	s.mail.MarkWoken(c.ID, id)
	if s.record != nil {
		s.record(team.Event{
			Kind: team.EventWake, ID: id, Time: typedAt, Pane: c.ID, Agent: c.Agent,
			Rule: r.Version, RuleSource: r.Source, Prompt: cleanPromptLine(r.Prompt), Line: line,
		})
	}
}

// undo takes back the limits a line that was never typed counted.
func (s *wakeService) undo(pane string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st := s.state[pane]; st != nil {
		st.lastWake = time.Time{}
		if st.streak > 0 {
			st.streak--
		}
	}
}

// cleanPromptLine makes the matched prompt line fit for the event log: control
// characters removed and cut to team.MaxPromptLine characters.
func cleanPromptLine(line string) string {
	var b strings.Builder
	n := 0
	for _, r := range line {
		if unicode.IsControl(r) {
			continue
		}
		if n == team.MaxPromptLine {
			break
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

// startWake starts the wake service on the workspace's panes.
func (w *Workspace) startWake() {
	t := &w.team
	if t.inbox == nil {
		return
	}
	s := newWakeService(w.wakeCandidates, t.inbox, t.notifier,
		func() WakeSettings { return w.wakeSettings }, w.recordEvent)
	w.wakeSvc = s
	s.run()
}

// stopWake stops the service. It is safe to call twice.
func (w *Workspace) stopWake() {
	if w.wakeSvc != nil {
		w.wakeSvc.Stop()
	}
}

// wakeCandidates lists the agent panes that have a running session.
func (w *Workspace) wakeCandidates() []wakeCandidate {
	w.mu.RLock()
	defer w.mu.RUnlock()
	var out []wakeCandidate
	for _, p := range w.panes {
		if p.Kind != session.KindAgent || p.Sess == nil {
			continue
		}
		out = append(out, wakeCandidate{ID: p.ID, Agent: p.Agent, Root: p.Root, Sess: p.Sess})
	}
	return out
}
