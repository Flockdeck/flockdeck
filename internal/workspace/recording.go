package workspace

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/record"
	"github.com/jmwri/flockdeck/internal/session/transcript"
	"github.com/jmwri/flockdeck/internal/store"
)

// A pane's transcript is made from the conversation its agent stored, and from
// nothing else: recording follows that file as it grows and exporting reads it
// once, through the same record.Sync. What the agent says to hooks is not part
// of it, so a transcript does not depend on whether recording was on while the
// conversation happened.

// recSettle is how long after the last hook event a recording looks at the
// stored conversation again. An agent writes its record a moment after it
// reports what it did, so the last entries of a turn are not there yet when
// the event that ends it arrives.
const recSettle = 1500 * time.Millisecond

// paneRecorder follows one pane's conversation into its transcript.
type paneRecorder struct {
	mu       sync.Mutex
	conv     string
	follower transcript.Follower
	timer    *time.Timer
	stopped  bool
}

// recMetaLocked says whose lines a pane's recording writes. It reads the pane,
// so it is called with w.mu held.
func (w *Workspace) recMetaLocked(p *Pane) record.Meta {
	// Not the tab's root as a last resort: the tabs belong to the workspace's
	// own goroutine, and this is called from a hook server's.
	root := p.Root
	if root == "" {
		root = p.Cwd
	}
	conv := p.Conversation
	if conv == "" {
		conv = p.ID
	}
	return record.Meta{
		Pane: p.ID, PaneName: p.Name, Project: filepath.Base(root), ProjectRoot: root,
		Agent: p.Agent, Model: p.Model, Conversation: conv,
	}
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
	name = spec.Name
	if name == "" {
		name = spec.ID
	}
	return name, ok
}

// SetPaneRecording turns a pane's recording on or off -- see Pane.Recording --
// and reports whether the pane was found. Turning it on writes the
// conversation so far at once and follows it from there; turning it off ends
// the transcript. Only an agent pane records: a shell has no conversation, so
// asking for one is refused the same way as a pane that is gone. An agent that
// stores no conversation can be set recording and records nothing; see
// PaneTranscriptSupported.
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
	meta := w.recMetaLocked(p)
	w.mu.Unlock()

	if on {
		// The folder is made now, so that a state directory that cannot be
		// written to is reported here and not found later.
		if _, err := record.EnsureRoot(store.Dir); err != nil {
			return true, err
		}
	} else {
		w.stopRecorder(meta, true)
	}
	w.mu.Lock()
	if p := w.panes[id]; p != nil {
		p.Recording = on
	}
	w.mu.Unlock()
	if on {
		w.syncRecording(id)
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

// syncRecording brings a recording pane's transcript up to date with the
// conversation its agent has stored, and sees that it looks again shortly.
// It is called for every event a recording pane's agent reports, which is when
// there is something new to find.
func (w *Workspace) syncRecording(id string) {
	w.mu.RLock()
	p := w.panes[id]
	if p == nil || !p.Recording {
		w.mu.RUnlock()
		return
	}
	meta := w.recMetaLocked(p)
	spec, ex, ok := w.transcriptSourceLocked(p)
	w.mu.RUnlock()
	if !ok {
		return
	}

	w.recMu.Lock()
	if w.recorders == nil {
		w.recorders = map[string]*paneRecorder{}
	}
	r := w.recorders[id]
	if r == nil {
		r = &paneRecorder{}
		w.recorders[id] = r
	}
	w.recMu.Unlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return
	}
	if r.follower != nil && r.conv != meta.Conversation {
		// The agent went on in a conversation of its own (/clear): the one
		// before ends here, and the new one is a transcript of its own.
		old := meta
		old.Conversation = r.conv
		if _, err := record.Sync(w.rec, old, r.follower); err == nil || errors.Is(err, transcript.ErrNoTranscript) {
			w.rec.Finish(old)
		}
		r.follower = nil
	}
	if r.follower == nil {
		r.conv, r.follower = meta.Conversation, ex.Follow(spec, meta.Conversation)
	}
	// A conversation not stored yet is one not begun, and any other failure is
	// one to try again at the next event.
	res, _ := record.Sync(w.rec, meta, r.follower)
	if res.Full {
		go w.recordingCapped(meta)
		return
	}
	if r.timer == nil {
		r.timer = time.AfterFunc(recSettle, func() { w.syncRecording(id) })
	} else {
		r.timer.Reset(recSettle)
	}
}

// stopRecorder ends a pane's transcript. With final it first reads what the
// agent has stored since the last look, and closes the transcript with its
// closing line; without, it only lets go.
func (w *Workspace) stopRecorder(meta record.Meta, final bool) {
	w.recMu.Lock()
	r := w.recorders[meta.Pane]
	delete(w.recorders, meta.Pane)
	w.recMu.Unlock()
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopped = true
	if r.timer != nil {
		r.timer.Stop()
	}
	if r.follower == nil {
		return
	}
	meta.Conversation = r.conv
	if final {
		_, _ = record.Sync(w.rec, meta, r.follower)
	}
	w.rec.Finish(meta)
}

// stopRecorders lets go of every recorder, for a Flockdeck that is quitting.
// Nothing is written: a pane left recording starts again at the next start.
func (w *Workspace) stopRecorders() {
	w.recMu.Lock()
	recorders := w.recorders
	w.recorders = nil
	w.recMu.Unlock()
	for _, r := range recorders {
		r.mu.Lock()
		r.stopped = true
		if r.timer != nil {
			r.timer.Stop()
		}
		r.mu.Unlock()
	}
}

// recordingCapped is what happens to a pane whose transcript has hit its size
// cap: the recording is over, so the pane stops showing that it is on.
func (w *Workspace) recordingCapped(meta record.Meta) {
	w.mu.Lock()
	if p := w.panes[meta.Pane]; p != nil {
		p.Recording = false
	}
	w.mu.Unlock()
	w.stopRecorder(meta, false)
	w.wake()
	if fn := w.onRecordingEnded.Load(); fn != nil && *fn != nil {
		(*fn)(meta.Pane, "reached its size cap")
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
	meta := w.recMetaLocked(p)
	spec, ex, ok := w.transcriptSourceLocked(p)
	w.mu.RUnlock()
	if !ok {
		name := spec.Name
		if name == "" {
			name = spec.ID
		}
		if name == "" {
			name = "this agent"
		}
		return record.ExportResult{}, fmt.Errorf("%s stores no conversation Flockdeck can read, so there is nothing to export", name)
	}
	if path != "" {
		if err := record.CheckExportPath(path, meta.ProjectRoot); err != nil {
			return record.ExportResult{}, err
		}
	}
	res, err := record.Export(store.Dir, meta, ex.Follow(spec, meta.Conversation), record.ExportOptions{Path: path})
	switch {
	case errors.Is(err, transcript.ErrNoTranscript):
		return res, errors.New("the agent has stored no conversation for this pane yet, so there is nothing to export")
	case errors.Is(err, record.ErrNothingToExport):
		return res, errors.New("the stored conversation has nothing in it to export yet")
	}
	return res, err
}
