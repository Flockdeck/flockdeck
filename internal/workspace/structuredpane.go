package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jmwri/flockdeck/internal/acp/bridge"
	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/session/transcript"
	"github.com/jmwri/flockdeck/internal/spend"
	"github.com/jmwri/flockdeck/internal/structured"
	"github.com/jmwri/flockdeck/internal/team"
)

// A structured pane is an agent Flockdeck talks to over the Agent Client
// Protocol (agent.RunnerACP) in place of a terminal. It has no session: its
// state is told by internal/structured and read through the PaneRuntime this
// file implements. Every consumer that asked a pane for its status, activity,
// waiting-on or last reply gets them from the runtime; nothing here types into
// a terminal, because there is none.
//
// It is opt-in. A catalog entry with RunnerACP is only usable when the
// structuredPanes preference is on (agent.Catalog.Structured), and a pane whose
// agent is not usable reports that in place, as any pane with an agent that is
// not configured does. Turning the preference off takes effect when the catalog
// is read again (ReloadAgents) or a pane is restarted; a running pane is not
// ended by it.

// maxPaneHistory bounds the entries a restarted structured pane keeps from the
// sessions before it.
var maxPaneHistory = 2000

// ErrStructuredPane is what an operation that needs a terminal says when asked
// of a structured pane.
var ErrStructuredPane = errors.New("that pane is a structured agent pane and has no terminal")

// structuredRuntime is the PaneRuntime of a structured pane.
type structuredRuntime struct {
	w  *Workspace
	p  *Pane
	rt *structured.Runtime
	// unasked is the manifest's word that the agent runs without asking.
	unasked bool

	started time.Time
	// prior is what the sessions before this one showed, oldest first. A restart
	// starts a fresh session and keeps the earlier entries as history.
	prior []transcript.Entry

	mu     sync.Mutex
	status session.Status
	detail string
	since  time.Time
	// sent is when Flockdeck last sent the agent a prompt of its own: a message
	// from the graph or a wake. The wake service leaves the pane alone for a
	// moment after it.
	sent time.Time

	stop     chan struct{}
	stopOnce sync.Once
}

// startStructured starts the agent of a structured pane and sets the pane's
// runtime. It reports a failure to start in p.Err, as startPane does for a
// terminal.
func (w *Workspace) startStructured(p *Pane, spec agent.Spec) {
	// The history stays on the pane until the new session has started. A start
	// that fails keeps it, with a line saying so, for the next restart to show.
	prior := p.history
	failed := func(err error) {
		p.Err = err
		if len(prior) == 0 {
			return
		}
		// The notice is named by a count of failures that trimming never lowers, so
		// two are never given one id. A failure straight after a failure takes its
		// place, so a pane that cannot start does not push the conversation out.
		p.failedSeq++
		note := transcript.Entry{ID: fmt.Sprintf("restart-failed-%d", p.failedSeq), Kind: transcript.KindNotice,
			TS: time.Now().UTC().Format(time.RFC3339), Text: "The agent could not be started again: " + err.Error()}
		keep := prior
		if n := len(keep); n > 0 && strings.HasPrefix(keep[n-1].ID, "restart-failed-") {
			keep = keep[:n-1]
		}
		p.history = append(keep[:len(keep):len(keep)], note)
		if n := len(p.history); n > maxPaneHistory {
			p.history = p.history[n-maxPaneHistory:]
		}
	}
	// Kept for the layout even if the agent cannot start, so a restore still
	// knows what the pane was.
	w.mu.Lock()
	p.savedTransport = TransportStructured
	w.mu.Unlock()
	if !w.agents().Structured {
		failed(fmt.Errorf("%s is a structured agent, and structured panes are turned off. Turn on structuredPanes in prefs.json and restart the pane, or close it", spec.Name))
		return
	}
	if _, err := os.Stat(p.Cwd); err != nil {
		failed(fmt.Errorf("the directory this pane works in, %s, is not there: %w. Put it back and restart the pane, or close it", p.Cwd, err))
		return
	}
	w.mu.RLock()
	cmdFor := w.structuredCommand
	w.mu.RUnlock()
	opts := structured.Options{Spec: spec, Cwd: p.Cwd}
	if cmdFor != nil {
		opts.Command = cmdFor(spec)
	}
	rt, err := structured.Start(context.Background(), opts)
	if err != nil {
		failed(fmt.Errorf("%w (working directory %s)", err, p.Cwd))
		return
	}
	p.history = nil
	sr := &structuredRuntime{w: w, p: p, rt: rt, unasked: spec.RunsWithoutAsking(), started: time.Now(), prior: prior,
		status: session.StatusStarting, since: time.Now(), stop: make(chan struct{})}
	w.mu.Lock()
	p.Sess = nil
	p.Err = nil
	w.mu.Unlock()
	runtimeMu.Lock()
	p.rt = sr
	runtimeMu.Unlock()
	// The task the pane was started with is its first prompt, with the briefing
	// an agent that reports nothing of its own is given in front of it.
	if prompt := w.OpeningPrompt(p.ID, p.initial, spec.Caps.Context); strings.TrimSpace(prompt) != "" {
		_ = rt.Send(prompt)
	}
	p.initial = ""
	go sr.watch()
}

// watch follows the runtime until it reaches a final phase, keeping the time
// the status began and telling the workspace and the windows of each change.
func (s *structuredRuntime) watch() {
	ch, unsubscribe := s.rt.Subscribe()
	defer unsubscribe()
	for {
		s.refresh()
		s.w.wake()
		s.w.paneChanged(s.p.ID)
		if !s.Alive() {
			return
		}
		select {
		case <-ch:
		case <-s.stop:
			return
		}
	}
}

// refresh reads the runtime's status and notes when it changed.
func (s *structuredRuntime) refresh() {
	st, detail := statusOfSnapshot(s.rt.Snapshot())
	s.mu.Lock()
	if st != s.status {
		s.status, s.since = st, time.Now()
	}
	s.detail = detail
	s.mu.Unlock()
}

// statusOfSnapshot maps the runtime's phase and status onto the status every
// consumer already reads. A waiting pane is one with a permission request open;
// a pane that needs the person to set it up reads as blocked, with nothing to
// answer; a pane whose agent has gone reads as exited.
func statusOfSnapshot(sn structured.Snapshot) (session.Status, string) {
	switch sn.Phase {
	case structured.PhaseStarting:
		return session.StatusStarting, ""
	case structured.PhaseSetup:
		return session.StatusBlocked, "needs setup"
	case structured.PhaseReady:
		switch sn.Status {
		case bridge.StatusWaiting:
			if sn.Pending != nil {
				return session.StatusWaiting, sn.Pending.Tool
			}
			return session.StatusWaiting, ""
		case bridge.StatusWorking:
			return session.StatusWorking, sn.Activity
		}
		return session.StatusIdle, ""
	}
	return session.StatusExited, ""
}

// SpendReporter is a runtime that can say what its agent reported of its spend
// through the protocol, as opposed to the hook server's reports a terminal pane
// makes. Only a structured pane has it.
type SpendReporter interface {
	ReportedSpend() (spend.Context, bool)
}

func (s *structuredRuntime) Transport() Transport { return TransportStructured }

// ReportedSpend is what the agent last said about its context and cost through
// usage_update, and whether it said anything yet. See spend.Context for what it
// leaves out.
func (s *structuredRuntime) ReportedSpend() (spend.Context, bool) {
	u := s.rt.Snapshot().Usage
	c := spend.Context{Used: u.Used, Size: u.Size, Cost: u.Cost, Currency: u.Currency}
	return c, c.Used != 0 || c.Size != 0 || c.Cost != 0
}

func (s *structuredRuntime) Caps() PaneCaps {
	return PaneCaps{Structured: true, AnswerPermission: true, RunsUnasked: s.unasked}
}

func (s *structuredRuntime) Status() (session.Status, string) {
	s.refresh()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status, s.detail
}

func (s *structuredRuntime) StatusSince() time.Time {
	s.refresh()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.since
}

func (s *structuredRuntime) StartedAt() time.Time { return s.started }

// Alive says the agent has not reached a final phase.
func (s *structuredRuntime) Alive() bool {
	switch s.rt.Snapshot().Phase {
	case structured.PhaseExited, structured.PhaseFailed, structured.PhaseClosed:
		return false
	}
	return true
}

// Failed says the agent went away or could not run, and why. A pane that was
// closed on purpose did not fail.
func (s *structuredRuntime) Failed() (string, bool) {
	sn := s.rt.Snapshot()
	switch sn.Phase {
	case structured.PhaseExited, structured.PhaseFailed:
		if sn.Err == "" {
			return "the agent ended", true
		}
		return sn.Err, true
	}
	return "", false
}

func (s *structuredRuntime) Activity() Activity {
	st, _ := s.Status()
	if _, failed := s.Failed(); failed && st == session.StatusExited {
		return ActivityFailed
	}
	switch st {
	case session.StatusWaiting:
		return ActivityWaiting
	case session.StatusBlocked:
		return ActivityBlocked
	case session.StatusWorking:
		return ActivityWorking
	case session.StatusStarting:
		return ActivityStarting
	case session.StatusExited:
		return ActivityExited
	}
	return ActivityIdle
}

// HasRun says the agent got as far as a session, so a figure for its spend is
// expected.
func (s *structuredRuntime) HasRun() bool {
	return s.rt.Snapshot().Phase != structured.PhaseStarting
}

// Waiting is the open permission request, in the shape the permission view is
// made from.
func (s *structuredRuntime) Waiting() WaitingOn {
	st, detail := s.Status()
	if st != session.StatusWaiting {
		return WaitingOn{Detail: detail}
	}
	c := s.rt.Snapshot().Pending
	if c == nil {
		return WaitingOn{Detail: detail}
	}
	in := struct {
		Command     string `json:"command,omitempty"`
		Description string `json:"description,omitempty"`
		FilePath    string `json:"filePath,omitempty"`
	}{c.Command, c.Summary, c.File}
	b, _ := json.Marshal(in)
	return WaitingOn{Detail: detail, ToolInput: string(b)}
}

// PermissionOptions are the options of the request that is open, as the agent
// named them. The tool is not looked at: the card is the request.
func (s *structuredRuntime) PermissionOptions(string) []PermissionOption {
	out := []PermissionOption{}
	if c := s.rt.Snapshot().Pending; c != nil {
		for _, o := range c.Options {
			out = append(out, PermissionOption{ID: o.OptionID, Name: o.Name, Kind: o.Kind})
		}
	}
	return out
}

// Background is empty: an ACP agent has no background work Flockdeck counts.
func (s *structuredRuntime) Background() BackgroundWork { return BackgroundWork{} }

// LastReply is the newest reply the agent has written.
func (s *structuredRuntime) LastReply() (LastReply, bool) {
	entries := s.rt.Snapshot().Entries
	for i := len(entries) - 1; i >= 0; i-- {
		if e := entries[i]; e.Kind == transcript.KindReply && e.Markdown != "" {
			return LastReply{Text: e.Markdown, TS: e.TS, Kind: e.Kind}, true
		}
	}
	return LastReply{}, false
}

// Setup is what the person has to do before this pane can run, once the runtime
// has stopped for it. The state is final: nothing is retried after the fix, so
// every text ends with the one step to take next.
func (s *structuredRuntime) Setup() (SetupNeeded, bool) {
	sn := s.rt.Snapshot()
	if sn.Phase != structured.PhaseSetup || sn.Setup == nil {
		return SetupNeeded{}, false
	}
	return SetupNeeded{Kind: sn.Setup.Kind, Text: setupText(s.p.Agent, sn.Setup)}, true
}

// setupText is the fix for a setup state, on one line of at most
// team.MaxWaitText characters. The end of it, which says to start the pane
// again, is kept; the front is cut first.
func setupText(agentID string, st *structured.Setup) string {
	name := team.PlainText(agentID, 40)
	if name == "" {
		name = "the agent"
	}
	var head, tail string
	switch st.Kind {
	case structured.SetupNode:
		// The fix already ends by saying to restart Flockdeck, which is what a
		// new Node on PATH needs; a second instruction would only compete.
		head = st.Message
		tail = st.Login
	case structured.SetupInstall:
		head = st.Message
		if st.Login != "" {
			head += ". " + st.Login
		}
		tail = "Then start the pane again."
	default: // structured.SetupLogin
		head = "Sign in to " + name + " with its own tool"
		if len(st.Methods) > 0 {
			var names []string
			for _, m := range st.Methods {
				if m.Name != "" {
					names = append(names, m.Name)
				}
			}
			if len(names) > 0 {
				head += " (" + strings.Join(names, ", ") + ")"
			}
		}
		if st.Login != "" {
			head += ". " + st.Login
		}
		tail = "Flockdeck does not sign in for you. Then start the pane again."
	}
	tail = team.PlainText(tail, team.MaxWaitText)
	room := team.MaxWaitText - len([]rune(tail)) - 1
	if room < 0 {
		return tail
	}
	return strings.TrimSpace(team.PlainText(head, room) + " " + tail)
}

// Wake sends message to the agent as a prompt. That is a request over the
// protocol and not keystrokes: it is queued behind a running turn.
func (s *structuredRuntime) Wake(message string) error {
	if err := s.rt.Send(message); err != nil {
		return err
	}
	s.mu.Lock()
	s.sent = time.Now()
	s.mu.Unlock()
	return nil
}

func (s *structuredRuntime) Close() error {
	err := s.rt.Close()
	s.stopOnce.Do(func() { close(s.stop) })
	return err
}

// Record is not available yet: a structured pane's traffic is recorded by the
// protocol recorder, which is started with the pane.
func (s *structuredRuntime) Record(bool) error {
	return errors.New("recording a structured pane is not available yet")
}

// Conversation is empty: the conversation is held in memory by the runtime and
// is not stored in a file Flockdeck can read, so it cannot be exported.
func (s *structuredRuntime) Conversation() ConversationSource {
	return ConversationSource{ID: s.p.ID}
}

// Entries are the conversation shown, the history of earlier sessions first.
func (s *structuredRuntime) Entries() []transcript.Entry {
	cur := s.rt.Snapshot().Entries
	out := make([]transcript.Entry, 0, len(s.prior)+len(cur))
	out = append(out, s.prior...)
	return append(out, cur...)
}

// UseStructuredCommand makes structured panes start the program fn returns in
// place of their adapter, and skips the Node check. It is for tests, which start
// the fake agent; the program's environment is still built from the entry.
func (w *Workspace) UseStructuredCommand(fn func(agent.Spec) *structured.Command) {
	w.mu.Lock()
	w.structuredCommand = fn
	w.mu.Unlock()
}

// StructuredRuntime is the runtime of the structured pane named id, nil for any
// other.
func (w *Workspace) StructuredRuntime(id string) *structured.Runtime {
	p := w.Pane(id)
	if p == nil {
		return nil
	}
	if sr, ok := p.setRuntime().(*structuredRuntime); ok {
		return sr.rt
	}
	return nil
}

// StructuredSnapshot is the runtime's snapshot of the structured pane id with
// the entries the pane shows in place of the session's own: the history of the
// sessions before its last restart, then this one's. One runtime is read, once,
// so the history and the session always belong together. The history of a
// runtime never changes after it is made.
func (w *Workspace) StructuredSnapshot(id string) (structured.Snapshot, bool) {
	p := w.Pane(id)
	if p == nil {
		return structured.Snapshot{}, false
	}
	sr, ok := p.setRuntime().(*structuredRuntime)
	if !ok {
		return structured.Snapshot{}, false
	}
	sn := sr.rt.Snapshot()
	entries := make([]transcript.Entry, 0, len(sr.prior)+len(sn.Entries))
	entries = append(entries, sr.prior...)
	sn.Entries = append(entries, sn.Entries...)
	return sn, true
}

// StructuredEntries are the entries a structured pane shows, with the history
// of the sessions before its last restart first.
func (w *Workspace) StructuredEntries(id string) ([]transcript.Entry, bool) {
	p := w.Pane(id)
	if p == nil {
		return nil, false
	}
	sr, ok := p.setRuntime().(*structuredRuntime)
	if !ok {
		return nil, false
	}
	return sr.Entries(), true
}

// restartStructured ends the pane's agent and starts a fresh session. The
// entries of the old session stay as history, with a line saying where it was
// restarted. Resuming the agent's own conversation is not done.
//
// Closing the old agent waits for it to end, for at most structured's
// closeWait (15 seconds), on the goroutine that called and not under the
// workspace lock. Between the close and the new runtime p.rt is nil, so the
// pane reads as exited for that moment.
func (w *Workspace) restartStructured(p *Pane, old *structuredRuntime) {
	// The old session's entries are numbered e1, e2... like the next session's,
	// so they take the generation as a prefix on becoming history.
	cur := old.rt.Snapshot().Entries
	history := append([]transcript.Entry(nil), old.prior...)
	for _, e := range cur {
		e.ID = fmt.Sprintf("r%d-%s", p.generation, e.ID)
		history = append(history, e)
	}
	// The notice is named by the restart it marks, not by the length of the
	// history, which stops growing once it is cut to maxPaneHistory.
	noteID := fmt.Sprintf("restart-%d", p.generation)
	p.generation++
	_ = old.Close()
	history = append(history, transcript.Entry{ID: noteID, Kind: transcript.KindNotice,
		TS: time.Now().UTC().Format(time.RFC3339), Text: "The agent was restarted. This is a new session."})
	if n := len(history); n > maxPaneHistory {
		history = history[n-maxPaneHistory:]
	}
	runtimeMu.Lock()
	p.rt = nil
	runtimeMu.Unlock()
	p.history = history
	w.startPane(p, false)
}

// noteStructured tells the parent of a structured pane what noteExit and
// noteIdle tell the parent of a terminal one: that the agent ended on its own,
// and that it finished a turn and stayed idle. The status comes from the
// runtime and not from a screen, so there is no hook to wait for.
func (w *Workspace) noteStructured(id string) {
	p := w.Pane(id)
	if p == nil {
		return
	}
	sr, ok := p.setRuntime().(*structuredRuntime)
	if !ok {
		return
	}
	w.mu.RLock()
	parent := p.Parent
	w.mu.RUnlock()
	if parent == "" {
		return
	}
	st, _ := sr.Status()
	switch st {
	case session.StatusExited:
		// Closing the pane is Flockdeck's doing and says nothing.
		why, failed := sr.Failed()
		if !failed {
			return
		}
		w.mu.Lock()
		done := p.structExit == sr
		p.structExit = sr
		w.mu.Unlock()
		if !done {
			w.notifyParent(p, parent, "exited with "+why)
		}
	case session.StatusWorking:
		w.mu.Lock()
		p.turnSeen = true
		w.mu.Unlock()
	case session.StatusIdle:
		w.mu.Lock()
		arm := p.turnSeen && !p.idleArmed
		if arm {
			p.idleArmed = true
		}
		w.mu.Unlock()
		if arm {
			w.armStructuredIdle(p, parent, sr)
		}
	}
}

// armStructuredIdle tells the parent after the dwell if the pane is still idle
// in the same session, as armIdleReport does.
func (w *Workspace) armStructuredIdle(p *Pane, parent string, sr *structuredRuntime) {
	w.team.after(w.team.dwell, func() {
		st, _ := sr.Status()
		same := w.Pane(p.ID) == p && p.setRuntime() == PaneRuntime(sr)
		if !same || st != session.StatusIdle {
			w.mu.Lock()
			p.idleArmed = false
			w.mu.Unlock()
			return
		}
		if time.Since(sr.StatusSince()) < w.team.dwell {
			w.armStructuredIdle(p, parent, sr)
			return
		}
		w.mu.Lock()
		p.idleArmed = false
		report := p.turnSeen
		p.turnSeen = false
		w.mu.Unlock()
		if report {
			w.notifyParent(p, parent, "finished its turn and is idle; its report is in `flockdeck inbox` if it sent one, and `flockdeck task list` shows its task")
		}
	})
}

// wakeStructured is a structured pane as the wake service sees it. Idle is the
// end of a turn and not a prompt on a screen, so there is no rule to match and
// no screen to wait on: the reading always allows, and its quiet period is the
// time the pane has been idle. Nobody types into the pane, so the user's last
// input is never recent; Flockdeck's last input is its last prompt.
type wakeStructured struct{ s *structuredRuntime }

// wakeRuleStructured names the "rule" a structured pane is woken by, in the
// wake event.
const wakeRuleStructured = "structured"

func (a wakeStructured) WakeReading() session.WakeReading {
	return session.WakeReading{
		Verdict: agent.WakeOK, Version: wakeRuleStructured, Source: wakeRuleStructured,
		Tail: session.ScreenTail{ChangedAt: a.s.StatusSince()},
	}
}

func (a wakeStructured) LastUserInput() time.Time { return time.Time{} }

func (a wakeStructured) LastFlockdeckInput() time.Time {
	a.s.mu.Lock()
	defer a.s.mu.Unlock()
	return a.s.sent
}

func (a wakeStructured) Status() (session.Status, string) { return a.s.Status() }
func (a wakeStructured) Exited() bool                     { return !a.s.Alive() }

// WriteStringFrom is not how a structured pane is woken: the service delivers
// through wakeCandidate.Deliver. It refuses so that nothing is ever typed.
func (wakeStructured) WriteStringFrom(session.InputSource, string) error {
	return errors.New("a structured pane takes prompts and not keystrokes")
}
