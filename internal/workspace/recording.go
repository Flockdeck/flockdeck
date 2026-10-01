package workspace

import (
	"encoding/json"
	"path/filepath"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/record"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
)

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

// SetPaneRecording turns a pane's recording on or off -- see Pane.Recording --
// and reports whether the pane was found. Turning it on opens a new session
// file at once, so a failure to is reported rather than found later; an error
// leaves the pane as it was. Only an agent pane records: a shell reports no
// events, so asking for one is refused the same way as a pane that is gone.
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
		if _, err := w.rec.Start(meta, "turned on"); err != nil {
			return true, err
		}
	} else {
		w.rec.Stop(meta, "turned off")
	}
	w.mu.Lock()
	if p := w.panes[id]; p != nil {
		p.Recording = on
	}
	w.mu.Unlock()
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

// recordEvent writes what a hook event says to the pane's transcript. The
// event is already known to belong to a pane that is recording.
func (w *Workspace) recordEvent(meta record.Meta, ev hooks.Event) {
	e, ok := entryFor(ev)
	if !ok {
		return
	}
	if !w.rec.Record(meta, e) {
		w.recordingCapped(meta)
	}
}

// recordStatus writes the status change an event brought about, if it brought
// one. Only changes the hook events cause are seen: a status reached by the
// terminal going quiet, or a pane's process ending, is not one.
func (w *Workspace) recordStatus(meta record.Meta, before session.Status, sess *session.Session) {
	after, detail := sess.Status()
	if after == before {
		return
	}
	e := record.Entry{Type: record.TypeStatus, Status: after.String(), Previous: before.String(), Detail: detail}
	if !w.rec.Record(meta, e) {
		w.recordingCapped(meta)
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
	w.rec.Stop(meta, "size cap reached")
	w.wake()
	if fn := w.onRecordingEnded.Load(); fn != nil && *fn != nil {
		(*fn)(meta.Pane, "reached its size cap")
	}
}

// entryFor turns a hook event into a transcript line. It reports false for
// the events a transcript has no line for.
func entryFor(ev hooks.Event) (record.Entry, bool) {
	d := ev.Detail
	if d == nil {
		d = &hooks.Detail{}
	}
	e := record.Entry{Subagent: ev.AgentID, Tool: ev.Tool, ToolUseID: d.ToolUseID}
	if len(d.Input) > 0 {
		e.Input = decodeInput(d.Input)
	}
	switch ev.Event {
	case "SessionStart":
		e.Type, e.Source, e.Text = record.TypeSession, ev.Source, "start"
	case "SessionEnd":
		e.Type, e.Text = record.TypeSession, "end"
	case "UserPromptSubmit":
		e.Type, e.Text = record.TypePrompt, d.Prompt
		if e.Text == "" {
			e.Text = ev.Prompt
		}
	case "PreToolUse":
		e.Type = record.TypeToolCall
	case "PermissionRequest":
		e.Type = record.TypePermission
	case "PermissionDenied":
		e.Type, e.Outcome = record.TypeOutcome, record.OutcomeDenied
	case "PostToolUse":
		e.Type, e.Output = record.TypeToolResult, d.Result
		e.Input = nil
	case "PostToolUseFailure":
		e.Type, e.Output, e.IsError = record.TypeToolResult, d.Error, true
		e.Input = nil
	case hooks.Interrupted:
		e.Type, e.Output, e.IsError, e.Interrupted = record.TypeToolResult, d.Error, true, true
		e.Input = nil
	case "Stop", "SubagentStop", "StopFailure":
		if d.Message == "" {
			return e, false
		}
		e.Type, e.Text, e.Tool = record.TypeAssistant, d.Message, ""
		if ev.Event == "StopFailure" {
			e.Reason = "the turn ended on an error"
		}
	default:
		return e, false
	}
	return e, true
}

// decodeInput reads a tool input's JSON for the transcript, which writes it as
// the object it was rather than as a string of one.
func decodeInput(raw []byte) any {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return string(raw)
	}
	return v
}
