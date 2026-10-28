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
	"github.com/jmwri/flockdeck/internal/creds"
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
	w       *Workspace
	p       *Pane
	rt      *structured.Runtime
	started time.Time
	// gen tells this runtime from the one that came before it in the same pane
	// (see runtimeGen).
	gen uint64
	// prior is what the sessions before this one showed, oldest first. A restart
	// starts a fresh session and keeps the earlier entries as history.
	prior []transcript.Entry

	mu     sync.Mutex
	status session.Status
	detail string
	since  time.Time

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
	gen := runtimeGen.Add(1)
	w.mu.Lock()
	resume := p.savedSession
	// Taken once: a restart of the fork is a new session, not another fork.
	fork := p.forkFrom
	p.forkFrom = nil
	w.mu.Unlock()
	opts := structured.Options{Spec: spec, Cwd: p.Cwd, MCPServers: w.mcpServersFor(p, spec), ResumeSession: resume, Fork: fork,
		Key:            creds.StoredKey(spec),
		OnTurn:         func(e structured.TurnEvent) { w.promptTurn(p.ID, gen, e) },
		OnSessionMoved: func() { w.noteSessionMoved(p.ID) }}
	if cmdFor != nil {
		opts.Command = cmdFor(spec)
	}
	rt, err := structured.Start(context.Background(), opts)
	if err != nil {
		failed(fmt.Errorf("%w (working directory %s)", err, p.Cwd))
		return
	}
	p.history = nil
	sr := &structuredRuntime{w: w, p: p, rt: rt, started: time.Now(), prior: prior, gen: gen,
		status: session.StatusStarting, since: time.Now(), stop: make(chan struct{})}
	w.mu.Lock()
	p.Sess = nil
	p.Err = nil
	p.savedSession = "" // the runtime holds it now
	w.mu.Unlock()
	runtimeMu.Lock()
	p.rt = sr
	runtimeMu.Unlock()
	// The task the pane was started with is its first prompt, with the briefing
	// an agent that reports nothing of its own is given in front of it.
	// A fork has the conversation it was made from and is not told to start one.
	if prompt := w.OpeningPrompt(p.ID, p.initial, spec.Caps.Context); fork == nil && strings.TrimSpace(prompt) != "" {
		_ = rt.SendSystem(prompt)
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
	return PaneCaps{Structured: true, AnswerPermission: true, RunsUnasked: s.rt.Unasked()}
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
	default: // structured.SetupLogin, the only kind
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

// Wake sends message to the agent as a prompt, queued behind a running turn.
// Mail from other panes does not come this way: it is sent tagged, by the prompt
// delivery service.
func (s *structuredRuntime) Wake(message string) error { return s.rt.Send(message) }

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

// ForkLabel is what a forked pane is called. It is fixed: the label is shown
// beside the original in the same tab, and nothing from either conversation goes
// into it.
const ForkLabel = "Fork"

// SpawnFork starts a new structured pane beside the structured pane src, in the
// same directory and with the same agent and model, whose session forks the
// agent's session from. It must run on the workspace goroutine. The agent's part
// (structured.Runtime.ForkSource) is done by the caller before this, off that
// goroutine, since the agent may take a while.
func (w *Workspace) SpawnFork(src string, from structured.ForkFrom) (string, error) {
	p := w.Pane(src)
	if p == nil {
		return "", errors.New("that pane is closed")
	}
	if _, ok := p.setRuntime().(*structuredRuntime); !ok {
		return "", ErrStructuredPane
	}
	w.mu.RLock()
	open := 0
	for _, q := range w.panes {
		if q.forkOf == src {
			open++
		}
	}
	w.mu.RUnlock()
	if open >= maxForks {
		return "", ErrTooManyForks
	}
	id, err := w.Spawn(src, SpawnOptions{Kind: p.Kind, Cwd: p.Cwd, Split: true, Label: ForkLabel,
		Agent: p.Agent, Model: p.Model, Fork: &from})
	if err != nil {
		return "", err
	}
	if q := w.Pane(id); q != nil {
		w.mu.Lock()
		q.forkOf = src
		w.mu.Unlock()
	}
	return id, nil
}

// maxForks is the most forks open beside one pane.
const maxForks = 4

// ErrTooManyForks: the pane already has maxForks forks open.
var ErrTooManyForks = errors.New("that pane already has too many forks open")

// restartStructured ends the pane's agent and starts a fresh session. The
// entries of the old session stay as history, with a line saying where it was
// restarted. A restart is the person asking for a new session, so the agent's
// own conversation is not resumed; a restored pane resumes it (startStructured).
// With resume, the new agent reopens that session instead (a rewind that got no
// answer), and the old entries are not kept since the agent replays them or
// the pane starts empty, as a restored one does.
//
// Closing the old agent waits for it to end, for at most structured's
// closeWait (15 seconds), on the goroutine that called and not under the
// workspace lock. Between the close and the new runtime p.rt is nil, so the
// pane reads as exited for that moment.
func (w *Workspace) restartStructured(p *Pane, old *structuredRuntime, resume string) {
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
	text := "The agent was restarted. This is a new session."
	if resume != "" {
		history = nil
		text = "Going back did not finish, so the agent was restarted with the conversation as it was before."
	}
	history = append(history, transcript.Entry{ID: noteID, Kind: transcript.KindNotice,
		TS: time.Now().UTC().Format(time.RFC3339), Text: text})
	if n := len(history); n > maxPaneHistory {
		history = history[n-maxPaneHistory:]
	}
	runtimeMu.Lock()
	p.rt = nil
	runtimeMu.Unlock()
	p.history = history
	w.mu.Lock()
	p.savedSession = resume
	w.mu.Unlock()
	w.startPane(p, false)
}

// acpSessionOf is the agent session id to save in the layout for the structured
// pane p: the running runtime's, or the one the pane is still to resume.
func (w *Workspace) acpSessionOf(p *Pane) string {
	if sr, ok := p.setRuntime().(*structuredRuntime); ok {
		return sr.rt.SessionID()
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	return p.savedSession
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
