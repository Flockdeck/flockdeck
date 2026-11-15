package structured

import (
	"errors"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// The conversations a pane's folder has had, and moving the pane to one of
// them. The list is what the agent says through session/list for the pane's
// folder, which only agents that advertise it have (Flockdeck's Claude adapter
// does; its Codex adapter does not). The pane is moved by restarting its agent
// on the chosen session, as a restored pane is (see workspace.restartStructured),
// so the replay is the one session/load already gives.
//
// Everything the agent says here is its own word. Ids and titles are cleaned and
// bounded, a session that names another folder is dropped, and a session is
// resumed only if a fresh list for this pane holds it. The window never names
// an id the agent did not list a moment before.

// SessionsMax is the most sessions listed.
const SessionsMax = 30

// SessionTitleCap is the most runes of a session's title that are kept.
const SessionTitleCap = 120

// sessionIDMax bounds the length of an id that is not a UUID.
const sessionIDMax = 128

var (
	// ErrSessionsNotOffered: the agent does not list its sessions.
	ErrSessionsNotOffered = errors.New("structured: this agent does not list its past conversations")
	// ErrSessionsFailed: the agent did not answer the list.
	ErrSessionsFailed = errors.New("structured: the agent could not list its conversations")
	// ErrNoSession: the id is not one of this folder's conversations as the agent
	// lists them now.
	ErrNoSession = errors.New("structured: that conversation is not one of this folder's")
	// ErrSessionCurrent: the pane is on that conversation already.
	ErrSessionCurrent = errors.New("structured: the pane is on that conversation already")
	// ErrSessionBusy: a turn is running or prompts are waiting, and the caller did
	// not say it is fine to end them.
	ErrSessionBusy = errors.New("structured: a turn is running or prompts are waiting")
)

// SessionItem is one conversation of the pane's folder, as a window is shown it.
type SessionItem struct {
	ID    string
	Title string
	// Updated is when the agent says it last changed, RFC 3339, "" if it said
	// nothing that parses.
	Updated string
	// Current says the pane is on this conversation.
	Current bool
}

// SessionsOffered says the agent can list its conversations and the pane can be
// moved to one: it lists them and can reopen one with session/load or resume.
func (r *Runtime) SessionsOffered() bool {
	r.mu.Lock()
	c, phase := r.client, r.phase
	r.mu.Unlock()
	if c == nil || phase != PhaseReady {
		return false
	}
	caps := c.Capabilities()
	return caps.List && (caps.LoadSession || caps.Resume)
}

// Sessions lists the pane's folder's conversations, newest first, at most
// SessionsMax. It asks the agent.
func (r *Runtime) Sessions() ([]SessionItem, error) {
	r.mu.Lock()
	c, phase, cur := r.client, r.phase, r.sessionIDLocked()
	r.mu.Unlock()
	if c == nil || phase != PhaseReady {
		return nil, ErrNotRunning
	}
	caps := c.Capabilities()
	if !caps.List || !(caps.LoadSession || caps.Resume) {
		return nil, ErrSessionsNotOffered
	}
	infos, err := c.ListSessions(r.ctx, r.opts.Cwd)
	if err != nil {
		if r.ctx.Err() != nil {
			return nil, ErrNotRunning
		}
		return nil, ErrSessionsFailed
	}
	own := r.ownClaudeAdapterSpec()
	type item struct {
		SessionItem
		at time.Time
	}
	var items []item
	seen := map[string]bool{}
	for _, in := range infos {
		id := in.ID
		switch {
		case id == "" || len(id) > sessionIDMax || id != clean(id, sessionIDMax) || strings.ContainsAny(id, " \"'`\\"):
			continue
		case own:
			// The id of a Claude session ends up in the saved layout and on a
			// command line, so it is a UUID in its canonical spelling or nothing.
			if u, err := uuid.Parse(id); err != nil || u.String() != id {
				continue
			}
		}
		if seen[id] || !sameDir(in.Cwd, r.opts.Cwd) {
			continue
		}
		seen[id] = true
		it := item{SessionItem: SessionItem{ID: id, Title: clean(in.Title, SessionTitleCap), Current: id == cur}}
		if t, err := time.Parse(time.RFC3339, in.UpdatedAt); err == nil {
			it.at, it.Updated = t, t.UTC().Format(time.RFC3339)
		}
		items = append(items, it)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].at.After(items[j].at) })
	if len(items) > SessionsMax {
		items = items[:SessionsMax]
	}
	out := make([]SessionItem, 0, len(items))
	for _, it := range items {
		out = append(out, it.SessionItem)
	}
	return out, nil
}

// ownClaudeAdapterSpec says the entry names Flockdeck's Claude adapter, the one
// whose session ids are known to be UUIDs.
func (r *Runtime) ownClaudeAdapterSpec() bool {
	a, ok := r.opts.Spec.ACPAdapter()
	return ok && a.ID == ownClaudeAdapter
}

// PickSession checks that id is one of this folder's conversations as the agent
// lists them now and that the pane is not on it, and returns the id to resume
// by. With confirm false it also refuses, with ErrSessionBusy, while a turn is
// running, a prompt is waiting or a rewind is out: ending those is the person's
// call. The caller restarts the pane on the id.
func (r *Runtime) PickSession(id string, confirm bool) (string, error) {
	if !confirm && r.Busy() {
		return "", ErrSessionBusy
	}
	items, err := r.Sessions()
	if err != nil {
		return "", err
	}
	for _, it := range items {
		if it.ID != id {
			continue
		}
		if it.Current {
			return "", ErrSessionCurrent
		}
		return it.ID, nil
	}
	return "", ErrNoSession
}

// Busy says a turn is running, a prompt is waiting, the agent is working on its
// own, or a rewind is out. A pane that is moved to another conversation ends
// all of these.
func (r *Runtime) Busy() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.inTurn || len(r.queue) > 0 || r.rewinding || (r.br != nil && r.br.Background())
}

// sameDir says two folder names are the same place. It compares the cleaned
// spellings, ignoring case where the file system does.
func sameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
