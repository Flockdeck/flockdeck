package workspace

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/record"
	"github.com/jmwri/flockdeck/internal/session/transcript"
	"github.com/jmwri/flockdeck/internal/store"
)

// A pane's transcript is made from the conversation its agent stored, and from
// nothing else: recording follows that file as it grows and exporting reads it
// once, through the same record.Sync, with the identity record.MetaFor works
// out from the conversation. What the agent says to hooks is not part of it, so
// a transcript does not depend on whether recording was on while the
// conversation happened, or on the pane's name or model.

// recSettle is how long after the last hook event a recording looks at the
// stored conversation again. An agent writes its record a moment after it
// reports what it did, so the last entries of a turn are not there yet when
// the event that ends it arrives. Each look that finds nothing new waits twice
// as long as the one before, and after recIdleLooks of them the recording stops
// looking until the agent reports another event.
const (
	recSettle    = 1500 * time.Millisecond
	recIdleLooks = 3
)

// followerFor is a follower as the workspace uses it: as it is, unless a test has
// put something between it and what it reads to make it fail.
func (w *Workspace) followerFor(f transcript.Follower) transcript.Follower {
	if w.wrapFollower != nil {
		return w.wrapFollower(f)
	}
	return f
}

// recSettleFor is the interval a Workspace waits before its first further look.
// It is a field, set before anything runs and never after, and not a package
// variable, because the timers of a Workspace outlive the code that made it and
// read it from their own goroutines.
func (w *Workspace) recSettleFor() time.Duration {
	if w.recSettle > 0 {
		return w.recSettle
	}
	return recSettle
}

// recActivity counts the looks under way and lets a caller wait for none to be.
// It is not a sync.WaitGroup because looks begin from timers at any time, and a
// WaitGroup may not have an Add from zero race with a Wait.
type recActivity struct {
	mu   sync.Mutex
	cond *sync.Cond
	n    int
}

func (a *recActivity) c() *sync.Cond {
	if a.cond == nil {
		a.cond = sync.NewCond(&a.mu)
	}
	return a.cond
}

func (a *recActivity) begin() {
	a.mu.Lock()
	a.n++
	a.mu.Unlock()
}

func (a *recActivity) end() {
	a.mu.Lock()
	a.n--
	a.c().Broadcast()
	a.mu.Unlock()
}

// wait returns when no look is under way.
func (a *recActivity) wait() {
	a.mu.Lock()
	for a.n > 0 {
		a.c().Wait()
	}
	a.mu.Unlock()
}

// paneRecorder follows one pane's conversation into its transcript.
type paneRecorder struct {
	// work serialises the reading and writing, which can take a while for a
	// long conversation.
	work     sync.Mutex
	conv     string
	meta     record.Meta
	mk       func() record.Meta
	follower transcript.Follower
	// synced says the conversation has been read once, so a transcript already
	// over the cap when recording was turned on is told apart from one that
	// grew into it.
	synced bool
	// fails and readFails count the looks in a row that could not write, and
	// that could not read what is stored.
	fails, readFails int
	// fresh makes a follower of the conversation from its start.
	fresh func() transcript.Follower
	// looks counts the times the conversation has been read.
	looks int

	// done is closed when the recorder has been stopped and is finished with: a
	// recorder stays where it is found until then, so that a pane turned on again
	// waits for it rather than writing beside it.
	done     chan struct{}
	doneOnce sync.Once

	// ctl guards the rest, and is only ever held briefly.
	ctl     sync.Mutex
	timer   *time.Timer
	idle    int
	pending bool
	running bool
	stopped bool
	// failing says a look failed and is to be tried again when the timer fires,
	// and that events are not to bring it forward.
	failing bool
}

func newPaneRecorder() *paneRecorder { return &paneRecorder{done: make(chan struct{})} }

func (r *paneRecorder) finish() { r.doneOnce.Do(func() { close(r.done) }) }

// metaNow is whose lines the transcript has: the one it was opened with, or, if
// nothing is written yet, what the conversation says of itself now. It is never
// empty once there is a follower.
func (r *paneRecorder) metaNow(w *Workspace) record.Meta {
	if r.meta.Pane != "" && w.rec.Active(r.meta.Pane) {
		return r.meta
	}
	r.meta = r.mk()
	return r.meta
}

// conversationOf is the id of the conversation a pane is in, for a transcript.
func paneConversation(p *Pane) string {
	if p.Conversation != "" {
		return p.Conversation
	}
	return p.ID
}

// transcriptSourceLocked is what a pane's transcript is made from: the agent's
// spec, and who stores its conversations. It is false for a pane whose agent
// keeps no conversation Flockdeck can read, which can neither record nor be
// exported. It is called with w.mu held.
func (w *Workspace) transcriptSourceLocked(p *Pane) (agent.Spec, transcript.Exporter, bool) {
	agentID := p.Agent
	if agentID == "" {
		agentID = w.defaultAgentFor(p.Root)
	}
	spec, ok := w.agents().Find(agentID)
	if !ok {
		return agent.Spec{}, nil, false
	}
	ex, ok := transcript.ExporterFor(spec)
	return spec, ex, ok
}

func specLabel(spec agent.Spec) string {
	switch {
	case spec.Name != "":
		return spec.Name
	case spec.ID != "":
		return spec.ID
	}
	return "this agent"
}

// PaneTranscriptSupported reports whether a pane's agent stores a conversation
// Flockdeck can turn into a transcript, and the agent's name for saying so.
func (w *Workspace) PaneTranscriptSupported(id string) (name string, ok bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	p := w.panes[id]
	if p == nil || !p.IsAgent() {
		return "", false
	}
	spec, _, ok := w.transcriptSourceLocked(p)
	return specLabel(spec), ok
}

// SetPaneRecording turns a pane's recording on or off -- see Pane.Recording --
// and reports whether the pane was found. Turning it on starts writing the
// conversation so far, from its first message, in the background, and then
// follows it; turning it off ends the transcript. Only an agent pane records: a
// shell has no conversation, so asking for one is refused the same way as a pane
// that is gone. An agent that stores no conversation Flockdeck can read has
// nothing to record, and turning it on is an error saying so.
func (w *Workspace) SetPaneRecording(id string, on bool) (found bool, err error) {
	w.mu.Lock()
	p := w.panes[id]
	if p == nil || !p.IsAgent() {
		w.mu.Unlock()
		return false, nil
	}
	if p.Recording == on {
		w.mu.Unlock()
		return true, nil
	}
	conv := ""
	if on {
		if spec, _, ok := w.transcriptSourceLocked(p); !ok {
			w.mu.Unlock()
			return true, fmt.Errorf("%s stores no conversation Flockdeck can read, so there is nothing to record", specLabel(spec))
		}
		conv = paneConversation(p)
	}
	w.mu.Unlock()
	if on {
		if owner := w.conversationOwner(conv); owner != "" && owner != id {
			return true, errors.New("another pane is already recording this conversation, and a conversation is one file")
		}
	}

	if on {
		// The folder is made now, so that a state directory that cannot be
		// written to is reported here and not found later.
		if _, err := record.EnsureRoot(store.Dir); err != nil {
			return true, err
		}
	}
	if on {
		// A recorder still finishing the transcript this one turns on again is
		// waited for, not written beside.
		w.awaitStopped(id)
	}
	// The pane stops being a recording one before its recorder is stopped, so
	// that an event arriving in between cannot make a second recorder for it.
	w.mu.Lock()
	if p := w.panes[id]; p != nil {
		p.Recording = on
	}
	w.mu.Unlock()
	if on {
		w.kickRecording(id, true)
	} else {
		w.stopRecorderAsync(id, true)
	}
	w.wake()
	return true, nil
}

// PaneRecording reports whether the pane named id is being recorded.
func (w *Workspace) PaneRecording(id string) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	p := w.panes[id]
	return p != nil && p.Recording
}

// RecordingsDir is the folder recordings are kept under, made if it is not
// there yet, for a window to open.
func (w *Workspace) RecordingsDir() (string, error) {
	return record.EnsureRoot(store.Dir)
}

// SetRecordingEndedHook installs the function told when a recording stopped by
// itself, which is only the size cap being reached.
func (w *Workspace) SetRecordingEndedHook(fn func(paneID, why string)) {
	w.onRecordingEnded.Store(&fn)
}

// recorder is a pane's recorder. With create it is made if there is none, but
// only for a pane that is recording, so a late event or timer for a pane that has
// stopped, or been closed, does not leave one behind; and none is ever made once
// the Workspace is closing, which is what keeps a late look from writing to a
// transcript that has been finished.
func (w *Workspace) recorder(id string, create bool) *paneRecorder {
	w.recMu.Lock()
	defer w.recMu.Unlock()
	if w.recClosed {
		return nil
	}
	r := w.recorders[id]
	if r == nil && create {
		// Whether the pane is recording is read with recMu held, so that it is not
		// read, and then acted on, either side of the pane being switched off and
		// its recorder removed (which takes recMu after switching it off): that
		// would leave a recorder nothing ever stops. The order is recMu, then w.mu,
		// and nothing takes them the other way round.
		w.mu.RLock()
		p := w.panes[id]
		recording := p != nil && p.Recording
		w.mu.RUnlock()
		if recording {
			if w.recorders == nil {
				w.recorders = map[string]*paneRecorder{}
			}
			r = newPaneRecorder()
			w.recorders[id] = r
		}
	}
	return r
}

// claimConversation records that pane id is the one recording a conversation,
// and reports false if another pane already is: a conversation is one file, and
// two writers to it would interleave and repeat its lines.
func (w *Workspace) claimConversation(conv, id string) bool {
	w.recMu.Lock()
	defer w.recMu.Unlock()
	if owner, ok := w.recOwner[conv]; ok && owner != id {
		return false
	}
	if w.recOwner == nil {
		w.recOwner = map[string]string{}
	}
	w.recOwner[conv] = id
	return true
}

func (w *Workspace) releaseConversation(conv, id string) {
	w.recMu.Lock()
	defer w.recMu.Unlock()
	if w.recOwner[conv] == id {
		delete(w.recOwner, conv)
	}
}

// conversationOwner is the pane recording a conversation, if one is.
func (w *Workspace) conversationOwner(conv string) string {
	w.recMu.Lock()
	defer w.recMu.Unlock()
	return w.recOwner[conv]
}

// kickRecording has a recording pane's transcript brought up to date with the
// stored conversation, off the goroutine that asked: it is called from the hook
// server for every event and from the control path, neither of which can wait
// on a long conversation being read. A kick while one is under way is folded
// into a further look when it ends. fromEvent says the agent just reported
// something, which is when a recording that had stopped looking starts again --
// but not one that is waiting out a failure, which would be tried at once, for
// every event, and give up in seconds.
func (w *Workspace) kickRecording(id string, fromEvent bool) {
	r := w.recorder(id, true)
	if r == nil {
		return
	}
	r.ctl.Lock()
	if r.stopped || (fromEvent && r.failing) {
		r.ctl.Unlock()
		return
	}
	if fromEvent {
		r.idle = 0
	}
	r.pending = true
	if r.running {
		r.ctl.Unlock()
		return
	}
	r.running = true
	// Counted while r.ctl is held and r is known not to be stopped, so that
	// stopRecorders, which stops r under the same lock before it waits, sees it.
	w.recAct.begin()
	r.ctl.Unlock()

	go func() {
		defer w.recAct.end()
		for {
			r.ctl.Lock()
			if !r.pending || r.stopped {
				r.running = false
				r.ctl.Unlock()
				return
			}
			r.pending = false
			r.ctl.Unlock()
			w.syncRecording(id, r)
		}
	}()
}

const (
	recMaxFails     = 8
	recMaxReadFails = 20
	recMaxBackoff   = 5 // doublings of the settle interval
)

// syncRecording reads what the agent has stored since the last look and writes
// it to the pane's transcript.
func (w *Workspace) syncRecording(id string, r *paneRecorder) {
	w.mu.RLock()
	p := w.panes[id]
	if p == nil || !p.Recording {
		w.mu.RUnlock()
		return
	}
	conv := paneConversation(p)
	spec, ex, ok := w.transcriptSourceLocked(p)
	w.mu.RUnlock()
	if !ok {
		// Nothing to follow, as for a pane restored or spawned recording whose
		// agent stores no conversation: it is not recording, and does not say so.
		w.mu.Lock()
		if p := w.panes[id]; p != nil {
			p.Recording = false
		}
		w.mu.Unlock()
		w.wake()
		return
	}

	r.work.Lock()
	defer r.work.Unlock()
	r.looks++
	r.ctl.Lock()
	stopped := r.stopped
	r.ctl.Unlock()
	if stopped {
		return
	}
	meta := func() record.Meta { return r.metaNow(w) }
	if r.follower != nil && r.conv != conv {
		// The agent went on in a conversation of its own (/clear): the one
		// before ends here, and the new one is a transcript of its own.
		if err := w.finalSync(r); err != nil {
			w.rec.Abandon(r.meta)
			w.note(id, "The transcript of the earlier conversation was left unfinished: "+err.Error())
		} else {
			w.finished(id, w.rec.Finish(r.meta))
		}
		w.releaseConversation(r.conv, id)
		r.follower, r.synced, r.meta = nil, false, record.Meta{}
	}
	if r.follower == nil {
		if !w.claimConversation(conv, id) {
			w.endRecordingAsync(id, "was not started: another pane is already recording this conversation")
			return
		}
		r.fresh = func() transcript.Follower { return w.followerFor(ex.Follow(spec, conv)) }
		r.conv, r.follower = conv, r.fresh()
		r.mk = func() record.Meta { return record.MetaFor(spec, ex, conv) }
	}
	res, err := record.Sync(w.rec, meta, r.follower)
	if errors.Is(err, transcript.ErrReplaced) {
		// What is stored is not what was read: write the transcript again from
		// the start, which is what it would be if it had been made now.
		w.rec.Abandon(r.meta)
		res, err = record.Sync(w.rec, meta, r.follower)
	}
	switch {
	case res.Full:
		why := "reached its size cap"
		if !r.synced {
			why = "was already longer than a transcript can be (16 MiB), so it was cut there and recording ended"
		}
		w.endRecordingAsync(id, why)
		return
	case errors.Is(err, transcript.ErrRead):
		// What is stored could not be read for now, a file another program has
		// locked. Nothing was passed over, so it is tried again, later each time.
		r.readFails++
		if r.readFails >= recMaxReadFails {
			w.endRecordingAsync(id, "could not be read ("+err.Error()+"), so recording ended")
			return
		}
		w.scheduleAfterFailure(id, r, r.readFails)
		return
	case err != nil && !errors.Is(err, transcript.ErrNoTranscript):
		// Something could not be written. Nothing is carried on from a file that
		// is missing what it could not take: it is made again from the start at a
		// later look, and if that keeps failing recording ends and says why.
		w.rec.Abandon(r.meta)
		r.follower, r.synced = r.fresh(), false
		r.fails++
		if r.fails >= recMaxFails {
			w.endRecordingAsync(id, "could not be written ("+err.Error()+"), so recording ended")
			return
		}
		w.scheduleAfterFailure(id, r, r.fails)
		return
	}
	r.fails, r.readFails = 0, 0
	r.synced = r.synced || err == nil
	r.ctl.Lock()
	r.failing = false
	r.ctl.Unlock()
	w.scheduleRecording(id, r, res.Events > 0)
}

// finalSync reads what the agent has stored since the last look, for a
// transcript that is about to be finished. It reports an error if the file could
// not be made complete -- a write that failed is redone from the start once, a
// read that failed is tried again once -- since a closing line on a file with
// gaps makes it look whole.
func (w *Workspace) finalSync(r *paneRecorder) error {
	meta := func() record.Meta { return r.metaNow(w) }
	sync := func() error {
		_, err := record.Sync(w.rec, meta, r.follower)
		if errors.Is(err, transcript.ErrReplaced) {
			w.rec.Abandon(r.meta)
			_, err = record.Sync(w.rec, meta, r.follower)
		}
		return err
	}
	err := sync()
	if errors.Is(err, transcript.ErrRead) {
		time.Sleep(100 * time.Millisecond)
		err = sync()
	} else if err != nil && !errors.Is(err, transcript.ErrNoTranscript) {
		w.rec.Abandon(r.meta)
		r.follower = r.fresh()
		err = sync()
	}
	if errors.Is(err, transcript.ErrNoTranscript) {
		if r.synced {
			return fmt.Errorf("the stored conversation can no longer be found, and what was stored since the last look may be missing: %w", err)
		}
		return nil // nothing was ever stored, so nothing is missing
	}
	return err
}

// scheduleAfterFailure arranges a look after one that failed, later the more
// have: a lock that lasts seconds is waited out and not given up on. Events do
// not bring it forward.
func (w *Workspace) scheduleAfterFailure(id string, r *paneRecorder, failures int) {
	r.ctl.Lock()
	defer r.ctl.Unlock()
	if r.stopped {
		return
	}
	r.failing = true
	delay := w.recSettleFor() << min(failures, recMaxBackoff)
	if r.timer == nil {
		r.timer = time.AfterFunc(delay, func() { w.kickRecording(id, false) })
	} else {
		r.timer.Reset(delay)
	}
}

// finished tells the window when a transcript could not be completed or put in
// its place, which is the one thing about ending one that the user needs to hear.
func (w *Workspace) finished(id string, err error) {
	switch {
	case err == nil:
	case errors.Is(err, record.ErrEarlierKept):
		w.note(id, "The earlier transcript of this conversation was kept: it has lines the new one lacks.")
	default:
		text := err.Error()
		w.note(id, "Could not finish a transcript: "+text+".")
	}
}

// note puts a sentence to the window.
func (w *Workspace) note(id, text string) {
	if fn := w.onRecordingNotice.Load(); fn != nil && *fn != nil {
		(*fn)(id, text)
	}
}

// SetRecordingNoticeHook installs the function told about something a recording
// or an export did that the user needs to hear, as a whole sentence.
func (w *Workspace) SetRecordingNoticeHook(fn func(paneID, text string)) {
	w.onRecordingNotice.Store(&fn)
}

// scheduleRecording arranges the next look, later each time a look finds
// nothing, and none once enough have.
func (w *Workspace) scheduleRecording(id string, r *paneRecorder, found bool) {
	r.ctl.Lock()
	defer r.ctl.Unlock()
	if r.stopped {
		return
	}
	if found {
		r.idle = 0
	} else {
		r.idle++
	}
	if r.idle > recIdleLooks {
		return
	}
	delay := w.recSettleFor() << max(r.idle-1, 0)
	if r.timer == nil {
		r.timer = time.AfterFunc(delay, func() { w.kickRecording(id, false) })
	} else {
		r.timer.Reset(delay)
	}
}

// stopRecorder ends a pane's transcript, and returns when it is ended. With final
// it first reads what the agent has stored since the last look, and closes the
// transcript with its closing line, or leaves it unfinished and says why if it
// could not be made complete; without, it only lets go. The recorder stays where
// it is found until it is done, and a second stop waits for the first.
func (w *Workspace) stopRecorder(id string, final bool) {
	r := w.recorder(id, false)
	if r == nil {
		return
	}
	r.ctl.Lock()
	if r.stopped {
		r.ctl.Unlock()
		<-r.done
		return
	}
	r.stopped = true
	if r.timer != nil {
		r.timer.Stop()
	}
	r.ctl.Unlock()

	r.work.Lock()
	if r.follower != nil {
		w.releaseConversation(r.conv, id)
		if final {
			if err := w.finalSync(r); err != nil {
				w.rec.Abandon(r.meta)
				w.note(id, "A transcript was left unfinished, as it could not be made complete: "+err.Error())
			} else {
				w.finished(id, w.rec.Finish(r.meta))
			}
		} else {
			w.finished(id, w.rec.Finish(r.meta))
		}
	}
	r.work.Unlock()

	w.recMu.Lock()
	if w.recorders[id] == r {
		delete(w.recorders, id)
	}
	w.recMu.Unlock()
	r.finish()
}

// stopRecorderAsync is stopRecorder off the goroutine that asked, which is the
// control path or the workspace's own and must not wait on a file being finished.
// It is counted, so that closing the Workspace waits for it.
func (w *Workspace) stopRecorderAsync(id string, final bool) {
	w.recAct.begin()
	go func() {
		defer w.recAct.end()
		w.stopRecorder(id, final)
	}()
}

// awaitStopped waits for a pane's recorder to be finished with, if it is being
// stopped, so that turning recording on again does not start one beside it.
func (w *Workspace) awaitStopped(id string) {
	r := w.recorder(id, false)
	if r == nil {
		return
	}
	r.ctl.Lock()
	stopped := r.stopped
	r.ctl.Unlock()
	if stopped {
		<-r.done
	}
}

// stopRecorders lets go of every recorder, for a Flockdeck that is quitting, and
// waits for the looks and the stops under way. Nothing more is written, now or by
// a look that is still to come: a pane left recording starts again at the next
// start.
func (w *Workspace) stopRecorders() {
	w.recMu.Lock()
	w.recClosed = true
	recorders := w.recorders
	w.recorders = nil
	w.recMu.Unlock()
	for _, r := range recorders {
		r.ctl.Lock()
		r.stopped = true
		if r.timer != nil {
			r.timer.Stop()
		}
		r.ctl.Unlock()
	}
	w.recAct.wait()
	for _, r := range recorders {
		r.finish()
	}
}

// endRecordingAsync ends a pane's recording, saying why, off the goroutine that
// found it must end, which is holding the recorder. It is counted, so that
// closing the Workspace waits for it.
func (w *Workspace) endRecordingAsync(id, why string) {
	w.recAct.begin()
	go func() {
		defer w.recAct.end()
		w.recordingCapped(id, why)
	}()
}

// recordingCapped is what happens to a pane whose transcript has hit its size
// cap: the recording is over, so the pane stops showing that it is on, and the
// window is told why.
func (w *Workspace) recordingCapped(id, why string) {
	w.mu.Lock()
	if p := w.panes[id]; p != nil {
		p.Recording = false
	}
	w.mu.Unlock()
	w.stopRecorder(id, false)
	w.wake()
	if fn := w.onRecordingEnded.Load(); fn != nil && *fn != nil {
		(*fn)(id, why)
	}
}

// ExportTranscript writes the conversation a pane's agent has stored as a
// transcript in the recording format, whether or not the pane is being
// recorded, and returns what it wrote. path is where, or empty for the
// exports folder of the pane's project under the recordings folder. It never
// writes into the project: that is refused, as is a file that exists.
func (w *Workspace) ExportTranscript(id, path string) (record.ExportResult, error) {
	w.mu.RLock()
	p := w.panes[id]
	if p == nil || !p.IsAgent() {
		w.mu.RUnlock()
		return record.ExportResult{}, errors.New("only an agent pane has a conversation to export, and that pane is not one or is no longer open")
	}
	conv := paneConversation(p)
	root := p.Root
	if root == "" {
		root = p.Cwd
	}
	spec, ex, ok := w.transcriptSourceLocked(p)
	w.mu.RUnlock()
	if !ok {
		return record.ExportResult{}, fmt.Errorf("%s stores no conversation Flockdeck can read, so there is nothing to export", specLabel(spec))
	}
	meta := record.MetaFor(spec, ex, conv)
	if path != "" {
		for _, project := range []string{meta.ProjectRoot, root} {
			if err := record.CheckExportPath(path, project); err != nil {
				return record.ExportResult{}, err
			}
		}
	}
	res, err := record.Export(store.Dir, meta, ex.Follow(spec, conv), record.ExportOptions{Path: path, MaxBytes: w.recMax})
	switch {
	case errors.Is(err, transcript.ErrNoTranscript):
		return res, errors.New("the agent has stored no conversation for this pane yet, so there is nothing to export")
	case errors.Is(err, record.ErrNothingToExport):
		return res, errors.New("the stored conversation has nothing in it to export yet")
	}
	return res, err
}
