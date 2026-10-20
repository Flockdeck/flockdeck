package workspace

import (
	"fmt"
	"sync"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/team"
)

// The wake service types one fixed line into an idle agent's pane to tell it
// that it has mail, for an agent that has no hook to answer while it sits at its
// prompt. It reverses an old rule on purpose (a pane used to be typed into only
// by the user), so everything about it is narrow: one line, never a sender or
// message text, only at a prompt a tested built-in rule recognises, and only
// while nobody is at the keyboard.
//
// Three off switches (global, per agent type, per pane) are kept in the
// preferences and read through WakeSettings. Everything is on until one is
// switched off, and still only for an agent whose rule is tested.

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

// WakeSettings says whether the wake service may type into a pane.
type WakeSettings interface {
	WakeEnabled(agentID, paneID string) bool
	// Invalidate drops anything kept, so the next WakeEnabled reads the
	// switches afresh. The service calls it just before it types, and the
	// window command that sets a switch calls it so that a click takes effect
	// at once.
	Invalidate()
}

type wakeAllOn struct{}

func (wakeAllOn) WakeEnabled(string, string) bool { return true }
func (wakeAllOn) Invalidate()                     {}

// wakeSettingsTTL is how long the preferences are kept before they are read
// again. The service asks about every pane on every tick, and a switch changed
// by `flockdeck wake`, which is another process, takes effect within this. A
// wake already past its checks types its line, and then its Enter, only after
// reading the switches afresh each time, so one that is switched off between
// is stopped there, and the line may stay typed.
const wakeSettingsTTL = time.Second

// prefsWake reads the off switches from the preferences file. A file that
// cannot be read, or is damaged anywhere in its wake part, turns wake off,
// because the switches the user set are not known and typing into a pane
// against one of them is the worse mistake; the last good reading is not used,
// since it may be the one that has just changed. It reads with store.ReadWake,
// not ReadPrefs: ReadPrefs gives the defaults for a damaged file, which turn
// wake on, after moving the file aside.
type prefsWake struct {
	mu    sync.Mutex
	read  func() (store.Prefs, error)
	now   func() time.Time
	at    time.Time
	prefs store.Prefs
	err   error
}

func newPrefsWake() *prefsWake {
	return &prefsWake{read: store.ReadWake, now: time.Now}
}

func (p *prefsWake) Invalidate() {
	p.mu.Lock()
	p.at = time.Time{}
	p.mu.Unlock()
}

func (p *prefsWake) WakeEnabled(agentID, paneID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if now := p.now(); p.at.IsZero() || now.Sub(p.at) >= wakeSettingsTTL {
		p.prefs, p.err = p.read()
		p.at = now
	}
	return p.err == nil && p.prefs.WakeEnabled(agentID, paneID)
}

// wakeSession is what the service needs of a pane's session. *session.Session
// is one; a test supplies another.
type wakeSession interface {
	WakeReading() session.WakeReading
	LastUserInput() time.Time
	LastFlockdeckInput() time.Time
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
	// Claim takes the pane's send flag, the one SendToPane holds while it types
	// a graph message, and says false if someone has it. Release gives it back.
	// A line and its Enter are written under the claim, so a send of Flockdeck's
	// own cannot land between them. Nil means no coordination, for a test.
	Claim   func() bool
	Release func()
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
	// stuck is set when a line was typed and its Enter was not, so it may still
	// be in the input box. Nothing more is typed until the user has typed in
	// the pane since typedAt.
	stuck   bool
	typedAt time.Time
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
	if at := c.Sess.LastFlockdeckInput(); !at.IsZero() && now.Sub(at) < wakeQuiet {
		return "Flockdeck typed into the pane a moment ago"
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
	if st.stuck {
		if at := c.Sess.LastUserInput(); at.After(st.typedAt) {
			st.stuck = false
		} else {
			s.mu.Unlock()
			return "an earlier line may still be in the input box"
		}
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
	if c.Claim != nil && !c.Claim() {
		s.projects[c.Root] = recent
		s.mu.Unlock()
		return "a send is in flight"
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
		s.fire(c, id, r, now)
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
//
// A message is marked woken, and the wake recorded as done, only when the Enter
// was written. When it was not, the line may still be in the input box. It is
// not taken back, because a key that clears the box would clear the user's text
// as well. The event says the line was unsubmitted, the message stays unread
// and unwoken, and nothing more is typed until the user has typed in the pane.
// at is the time the limits were counted at.
func (s *wakeService) fire(c wakeCandidate, id int, r session.WakeReading, at time.Time) {
	defer func() {
		if c.Release != nil {
			c.Release()
		}
		s.mu.Lock()
		if st := s.state[c.ID]; st != nil {
			st.inFlight = false
		}
		s.mu.Unlock()
	}()
	// off is a read of the switches that is not the cached one.
	off := func() bool {
		set := s.settings()
		set.Invalidate()
		return !set.WakeEnabled(c.Agent, c.ID)
	}
	typing := func() bool {
		t := c.Sess.LastUserInput()
		return !t.IsZero() && s.now().Sub(t) < wakeUserQuiet
	}
	if typing() || off() {
		s.undo(c, at, false)
		return
	}
	// The pane may have changed since it was judged.
	if again := c.Sess.WakeReading(); again.Verdict != r.Verdict || !again.Tail.ChangedAt.Equal(r.Tail.ChangedAt) {
		s.undo(c, at, false)
		return
	}
	line := wakeLine(id)
	if err := c.Sess.WriteStringFrom(session.InputFlockdeck, line); err != nil {
		// Backed off, so a pane that cannot be written to is not tried every tick.
		s.undo(c, at, true)
		return
	}
	typedAt := s.now()
	submitted := false
	if s.sleep(s.settle, s.stop) {
		if t := c.Sess.LastUserInput(); (t.IsZero() || !t.After(typedAt)) && !off() {
			submitted = c.Sess.WriteStringFrom(session.InputFlockdeck, "\r") == nil
		}
	}
	if submitted {
		s.mail.MarkWoken(c.ID, id)
	} else {
		s.mu.Lock()
		if st := s.state[c.ID]; st != nil {
			st.stuck, st.typedAt = true, typedAt
		}
		s.mu.Unlock()
	}
	if s.record != nil {
		s.record(team.Event{
			Kind: team.EventWake, ID: id, Time: typedAt, Pane: c.ID, Agent: c.Agent,
			Rule: r.Version, RuleSource: r.Source, Prompt: cleanPromptLine(r.Prompt), Line: line,
			Unsubmitted: !submitted,
		})
	}
}

// undo takes back what an attempt that typed nothing counted: its place in the
// project's hour, and in the pane's streak. With backoff the pane still waits
// out the minute, for a write that failed.
func (s *wakeService) undo(c wakeCandidate, at time.Time, backoff bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	times := s.projects[c.Root]
	for i, t := range times {
		if t.Equal(at) {
			s.projects[c.Root] = append(times[:i:i], times[i+1:]...)
			break
		}
	}
	if st := s.state[c.ID]; st != nil {
		if !backoff {
			st.lastWake = time.Time{}
		}
		if st.streak > 0 {
			st.streak--
		}
	}
}

// cleanPromptLine makes the matched prompt line fit for the event log.
func cleanPromptLine(line string) string { return team.CleanLine(line, team.MaxPromptLine) }

// WakeSettingsChanged drops the cached wake switches, so a switch just set is
// in force at once and not within the next second.
func (w *Workspace) WakeSettingsChanged() {
	if w.wakeSettings != nil {
		w.wakeSettings.Invalidate()
	}
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
		p := p
		out = append(out, wakeCandidate{ID: p.ID, Agent: p.Agent, Root: p.Root, Sess: p.Sess,
			Claim: func() bool { return w.claimSend(p) }, Release: func() { w.releaseSend(p) }})
	}
	return out
}
