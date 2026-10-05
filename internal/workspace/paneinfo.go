package workspace

import (
	"github.com/jmwri/flockdeck/internal/record"
	"github.com/jmwri/flockdeck/internal/session/transcript"
	"github.com/jmwri/flockdeck/internal/store"
)

// PaneDetails is what identifies one pane to the files, logs and commands that
// name it. It carries nothing secret: no environment, no tokens. It is built on
// request for the window on this machine and is not part of the state every
// window is sent.
type PaneDetails struct {
	ID    string
	Agent bool
	Name  string
	Cwd   string
	// Root is the pane's project, as the project is open; Branch is the
	// checkout's branch as last read.
	Root   string
	Branch string
	// TabID and Tab are where the pane is drawn.
	TabID string
	Tab   string
	// AgentID and Model are what the pane runs, empty for a shell.
	AgentID string
	Model   string
	// Conversation is the agent's own id for its conversation, which is the
	// pane's id until the agent starts another (after /clear). Empty for a
	// shell.
	Conversation string
	// Pid is the pane's process, zero when it has none.
	Pid       int
	PeerName  string
	Status    string
	Recording bool
	Locked    bool
	// Transcribable says the agent stores a conversation Flockdeck can read, so
	// the pane can be recorded and exported. False for a shell, and for an agent
	// that stores none Flockdeck can read.
	Transcribable bool
	// StoredPath is the file the agent keeps the conversation in, empty when
	// the agent stores none Flockdeck can find or the file is not there yet.
	StoredPath string
	// TranscriptPath is the file of the open recording or the latest export,
	// empty when there is none.
	TranscriptPath string
}

// PaneDetailsOf describes a pane, or reports false if it is not open. Looking
// for its files costs a read of the disk, so it is done only when files is set.
// It reads
// the tab and project lists, so it must run on the workspace goroutine.
func (w *Workspace) PaneDetailsOf(id string, files bool) (PaneDetails, bool) {
	p := w.Pane(id)
	if p == nil {
		return PaneDetails{}, false
	}
	d := PaneDetails{
		ID:       p.ID,
		Agent:    p.IsAgent(),
		Name:     p.Name,
		Cwd:      p.Cwd,
		Branch:   p.Branch,
		Root:     w.rootOf(id),
		Locked:   p.Locked,
		PeerName: w.PeerNameOf(p),
	}
	if t := w.tabOf(id); t != nil {
		d.TabID, d.Tab = t.ID, t.Title
		if d.Root == "" {
			d.Root = t.Root
		}
	}
	st, _ := p.Status()
	d.Status = st.String()
	if p.Sess != nil {
		d.Pid = p.Sess.Pid()
	}
	if !d.Agent {
		return d, true
	}

	w.mu.RLock()
	d.AgentID, d.Model, d.Recording = p.Agent, p.Model, p.Recording
	conv := paneConversation(p)
	spec, ex, ok := w.transcriptSourceLocked(p)
	w.mu.RUnlock()
	d.Conversation = conv
	d.Transcribable = ok
	if d.AgentID == "" {
		d.AgentID = spec.ID
	}
	if !files {
		return d, true
	}
	// The stored conversation is wherever the agent's reader says, which
	// is a real file for Claude Code and for Flockdeck's own API agents, and ""
	// for an agent with no reader or while the file is not there yet. It does
	// not need an exporter: only the transcript rows below do.
	d.StoredPath = transcript.For(spec).Path(spec, conv)
	if !ok {
		return d, true
	}
	if d.Recording {
		d.TranscriptPath = w.rec.Path(conv)
	}
	if d.TranscriptPath == "" {
		if path, err := record.FindExport(store.Dir, record.MetaFor(spec, ex, conv)); err == nil {
			d.TranscriptPath = path
		}
	}
	return d, true
}

// AllPaneDetails describes every pane in every open project, in tab order,
// without looking for files.
func (w *Workspace) AllPaneDetails() []PaneDetails {
	var out []PaneDetails
	for _, t := range w.Tabs {
		for _, id := range t.Tree.Panes() {
			if d, ok := w.PaneDetailsOf(id, false); ok {
				out = append(out, d)
			}
		}
	}
	return out
}
