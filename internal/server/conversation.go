package server

import (
	"sync"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

// This file is the desktop side of the phone's chat view: the
// conversationOpen/conversationOlder/conversationDetail/conversationClose
// commands and their replies, against the protocol both sides are built to
// (see the scratchpad's convo-protocol.md). It resolves a pane's transcript
// strictly through ws.ConversationOf and ws.PaneAgentSpec -- never a path or
// session id the client sends -- the same way history.go already does for
// resuming a stored conversation, so a client cannot ask for another pane's
// or another project's conversation by guessing an id.

// conversationPageSize bounds how many entries one page carries, per the
// protocol contract.
const conversationPageSize = 50

// conversationPollInterval is how often a pane whose status is Working is
// re-tailed even without a hook event, in case one was suppressed or slow.
// An idle pane is never polled: its hook events are the only signal, and
// there is nothing to catch up on between them.
var conversationPollInterval = 2 * time.Second

// conversationHub is every pane's live chat-view stream that at least one
// client has opened, and which clients are watching each. It outlives any
// one client's connection -- a cursor from an earlier page is honoured for
// as long as this process runs, which is what lets a phone reconnect after a
// gap and be sent only what it missed -- and is only ever added to: a pane
// closed and reopened keeps its entries rather than re-reading the file from
// the top.
type conversationHub struct {
	mu    sync.Mutex
	panes map[string]*paneConvo
}

func newConversationHub() conversationHub {
	return conversationHub{panes: map[string]*paneConvo{}}
}

// paneConvo is one pane's live stream and the clients watching it.
type paneConvo struct {
	mu        sync.Mutex
	spec      agent.Spec
	sessionID string
	supported bool
	stream    transcript.Stream
	watchers  map[*controlClient]bool
}

// get returns the pane's entry, creating it if this is the first client ever
// to open that pane's conversation.
func (h *conversationHub) get(paneID string) *paneConvo {
	h.mu.Lock()
	defer h.mu.Unlock()
	pc := h.panes[paneID]
	if pc == nil {
		pc = &paneConvo{watchers: map[*controlClient]bool{}}
		h.panes[paneID] = pc
	}
	return pc
}

// lookup returns the pane's entry without creating one, for a request that
// must not fabricate a conversation nobody has opened yet.
func (h *conversationHub) lookup(paneID string) (*paneConvo, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	pc, ok := h.panes[paneID]
	return pc, ok
}

// watched lists the panes at least one client is currently watching, for the
// fallback poll to check the status of.
func (h *conversationHub) watched() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var ids []string
	for id, pc := range h.panes {
		pc.mu.Lock()
		n := len(pc.watchers)
		pc.mu.Unlock()
		if n > 0 {
			ids = append(ids, id)
		}
	}
	return ids
}

// dropClient forgets a client that has gone, everywhere it was watching.
func (h *conversationHub) dropClient(c *controlClient) {
	h.mu.Lock()
	panes := make([]*paneConvo, 0, len(h.panes))
	for _, pc := range h.panes {
		panes = append(panes, pc)
	}
	h.mu.Unlock()
	for _, pc := range panes {
		pc.mu.Lock()
		delete(pc.watchers, c)
		pc.mu.Unlock()
	}
}

// ---------------------------------------------------------------------------
// Wire messages
// ---------------------------------------------------------------------------

type conversationPageMsg struct {
	Type      string             `json:"type"`
	ID        string             `json:"id"`
	Supported bool               `json:"supported"`
	Agent     string             `json:"agent,omitempty"`
	Entries   []transcript.Entry `json:"entries"`
	Cursor    string             `json:"cursor,omitempty"`
	AtStart   bool               `json:"atStart,omitempty"`
	Reset     bool               `json:"reset,omitempty"`
}

type conversationAppendMsg struct {
	Type    string             `json:"type"`
	ID      string             `json:"id"`
	Entries []transcript.Entry `json:"entries"`
	Cursor  string             `json:"cursor,omitempty"`
}

type conversationDetailMsg struct {
	Type   string            `json:"type"`
	ID     string            `json:"id"`
	Entry  string            `json:"entry"`
	Detail transcript.Detail `json:"detail"`
}

// ---------------------------------------------------------------------------
// Resolving a pane
// ---------------------------------------------------------------------------

// resolvedPane is what a pane's conversation is asked from: its agent and its
// current conversation id. found is false for a pane the workspace does not
// have -- closed since, or never open to this client at all -- which is
// refused exactly like an unknown pane anywhere else in the control
// protocol: silently, since a client that could not see the pane is not told
// anything about why.
type resolvedPane struct {
	spec      agent.Spec
	sessionID string
	found     bool
}

// resolvePane reads a pane's agent and conversation id on the workspace
// goroutine, which PaneAgentSpec must run on.
func (s *Server) resolvePane(paneID string) resolvedPane {
	r, ok := ask(s, func() resolvedPane {
		spec, ok := s.ws.PaneAgentSpec(paneID)
		if !ok {
			return resolvedPane{}
		}
		return resolvedPane{spec: spec, sessionID: s.ws.ConversationOf(paneID), found: true}
	})
	if !ok {
		return resolvedPane{}
	}
	return r
}

// ---------------------------------------------------------------------------
// conversationOpen
// ---------------------------------------------------------------------------

func (s *Server) conversationOpen(c *controlClient, paneID, after string) {
	if paneID == "" {
		return
	}
	go func() {
		defer s.surviveFor(c, "opening a conversation")
		r := s.resolvePane(paneID)
		if !r.found {
			// A client asking for a pane it cannot see -- closed since it
			// asked, or never open to it at all -- hears nothing back, the
			// same way an unknown pane id is refused everywhere else here.
			return
		}
		pc := s.convos.get(paneID)
		pc.mu.Lock()
		defer pc.mu.Unlock()
		s.syncStreamLocked(pc, r)
		if !pc.supported {
			c.sendJSON(conversationPageMsg{Type: "conversationPage", ID: paneID, Supported: false})
			return
		}
		pc.watchers[c] = true
		pc.stream.Refresh()
		// Drained, not read: a stream built just now from the whole file may
		// have passed over a past /clear (a chat pane's own, which keeps the
		// same session id -- see Stream.Reset) on its way to the end, and
		// that is not news to a client that is only now opening the pane for
		// the first time. pageFor's own cursor-mismatch fallback already
		// answers reset:true correctly when it matters (a cursor from before
		// a /clear it does not recognise); left undrained here, the flag
		// would instead surface later as a spurious reset on the first live
		// append.
		pc.stream.Reset()
		snapshot := pc.stream.Snapshot()

		entries, atStart, cursor, reset := pageFor(snapshot, after)
		c.sendJSON(conversationPageMsg{
			Type: "conversationPage", ID: paneID, Supported: true, Agent: r.spec.ID,
			Entries: entries, Cursor: cursor, AtStart: atStart, Reset: reset,
		})
	}()
}

// syncStreamLocked makes pc match what the pane is running now, building a
// fresh stream the first time a pane is opened, and again whenever the
// conversation it is in has changed under it -- a /clear or a resume, which
// the SessionStart hook already tells Workspace about. pc.mu must be held.
func (s *Server) syncStreamLocked(pc *paneConvo, r resolvedPane) {
	if pc.stream != nil && pc.sessionID == r.sessionID {
		return
	}
	pc.spec, pc.sessionID = r.spec, r.sessionID
	stream, ok := transcript.StreamFor(r.spec, r.sessionID)
	pc.stream, pc.supported = stream, ok
}

// pageFor answers an open request's page: the newest conversationPageSize
// entries when after is empty or unknown, or everything since after when it
// names an entry the snapshot still has.
func pageFor(snapshot []transcript.Entry, after string) (entries []transcript.Entry, atStart bool, cursor string, reset bool) {
	if after != "" {
		if i := indexOfEntry(snapshot, after); i >= 0 {
			entries = snapshot[i+1:]
			// There is always at least the cursor's own entry older than
			// this page, so this is never the start of the conversation.
			return entries, false, lastCursor(snapshot, entries, after), false
		}
		// The cursor is not one the stream still has: the desktop restarted,
		// or (rarely) a conversation was trimmed by something else. Answered
		// exactly as a first open would be, with reset said so the phone
		// knows to drop what it has rather than append onto it.
	}
	start := max(0, len(snapshot)-conversationPageSize)
	entries = snapshot[start:]
	return entries, start == 0, lastCursor(snapshot, entries, after), after != ""
}

// lastCursor is the cursor a page leaves the client at: its last entry's id,
// or, when it is empty, whatever the client already had -- an empty page is
// "nothing new since last time", not "forget where you were".
func lastCursor(snapshot, page []transcript.Entry, was string) string {
	if len(page) > 0 {
		return page[len(page)-1].ID
	}
	if was != "" {
		return was
	}
	if len(snapshot) > 0 {
		return snapshot[len(snapshot)-1].ID
	}
	return ""
}

func indexOfEntry(entries []transcript.Entry, id string) int {
	for i, e := range entries {
		if e.ID == id {
			return i
		}
	}
	return -1
}

// ---------------------------------------------------------------------------
// conversationOlder
// ---------------------------------------------------------------------------

func (s *Server) conversationOlder(c *controlClient, paneID, before string) {
	if paneID == "" || before == "" {
		return
	}
	pc, ok := s.convos.lookup(paneID)
	if !ok {
		return
	}
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if !pc.supported || pc.stream == nil {
		return
	}
	snapshot := pc.stream.Snapshot()
	idx := indexOfEntry(snapshot, before)
	if idx < 0 {
		return
	}
	start := max(0, idx-conversationPageSize)
	page := snapshot[start:idx]
	cursor := before
	if len(page) > 0 {
		cursor = page[len(page)-1].ID
	}
	c.sendJSON(conversationPageMsg{
		Type: "conversationPage", ID: paneID, Supported: true, Agent: pc.spec.ID,
		Entries: page, Cursor: cursor, AtStart: start == 0,
	})
}

// ---------------------------------------------------------------------------
// conversationDetail
// ---------------------------------------------------------------------------

func (s *Server) conversationDetailReq(c *controlClient, paneID, entryID string) {
	if paneID == "" || entryID == "" {
		return
	}
	pc, ok := s.convos.lookup(paneID)
	if !ok {
		return
	}
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if !pc.supported || pc.stream == nil {
		return
	}
	detail, ok := pc.stream.Detail(entryID)
	if !ok {
		return
	}
	c.sendJSON(conversationDetailMsg{Type: "conversationDetail", ID: paneID, Entry: entryID, Detail: detail})
}

// ---------------------------------------------------------------------------
// conversationClose
// ---------------------------------------------------------------------------

func (s *Server) conversationClose(c *controlClient, paneID string) {
	pc, ok := s.convos.lookup(paneID)
	if !ok {
		return
	}
	pc.mu.Lock()
	delete(pc.watchers, c)
	pc.mu.Unlock()
}

// ---------------------------------------------------------------------------
// Streaming appends
// ---------------------------------------------------------------------------

// ConversationHookEvent is told a pane's id whenever a hook event about it
// arrives (see Workspace.SetConversationHook), which is the sign that its
// transcript may have grown a line worth tailing. A pane already held by the
// hub -- a client has it open, or has had it open before -- is simply
// re-tailed. One never opened by any client is still worth a stream of its
// own now, so the phone's inbox row can show a preview of an agent pane
// nobody has looked at yet: resolvePane answers found=false for a shell or a
// pane that has gone, which is the only filter needed to keep this to agent
// panes.
func (s *Server) ConversationHookEvent(paneID string) {
	pc, existed := s.convos.lookup(paneID)
	if !existed {
		r := s.resolvePane(paneID)
		if !r.found {
			return
		}
		pc = s.convos.get(paneID)
		pc.mu.Lock()
		s.syncStreamLocked(pc, r)
		pc.mu.Unlock()
	}
	s.refreshConversation(paneID, pc)
}

// refreshConversation re-tails a pane's stream and pushes whatever changed to
// every client watching it, rebuilding the stream first if the pane has
// moved to a different conversation (/clear, resume) since it was last read.
func (s *Server) refreshConversation(paneID string, pc *paneConvo) {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	sessionID := s.ws.ConversationOf(paneID)
	if sessionID != "" && sessionID != pc.sessionID {
		pc.sessionID = sessionID
		stream, ok := transcript.StreamFor(pc.spec, sessionID)
		pc.stream, pc.supported = stream, ok
		if !pc.supported {
			s.preview.drop(paneID)
			for c := range pc.watchers {
				c.sendJSON(conversationPageMsg{Type: "conversationPage", ID: paneID, Supported: false})
			}
			return
		}
		s.notePreview(paneID, pc.stream.Refresh())
		// Already answered as a reset below, whatever this says -- a new
		// session id is reset enough on its own -- so this only drains it for
		// the next call, the same reason conversationOpen does.
		pc.stream.Reset()
		snapshot := pc.stream.Snapshot()
		start := max(0, len(snapshot)-conversationPageSize)
		page := snapshot[start:]
		cursor := ""
		if len(page) > 0 {
			cursor = page[len(page)-1].ID
		}
		for c := range pc.watchers {
			c.sendJSON(conversationPageMsg{
				Type: "conversationPage", ID: paneID, Supported: true, Agent: pc.spec.ID,
				Entries: page, Cursor: cursor, AtStart: start == 0, Reset: true,
			})
		}
		return
	}

	if !pc.supported || pc.stream == nil {
		return
	}
	// Refreshed for the inbox preview whether or not anyone is watching --
	// this is the one place a pane's transcript is tailed for a client that
	// has never opened it -- but only pushed on to clients that are.
	changed := pc.stream.Refresh()
	s.notePreview(paneID, changed)
	// A conversation can be replaced in place under an unchanged session id
	// -- a chat pane's own /clear, which (unlike Claude Code's) never gets a
	// new one; see Stream.Reset. A client already watching must be told to
	// drop what it has, the same as the session-id-changed branch above,
	// rather than have this arrive as an append underneath stale entries
	// nothing will ever remove.
	if pc.stream.Reset() {
		snapshot := pc.stream.Snapshot()
		start := max(0, len(snapshot)-conversationPageSize)
		page := snapshot[start:]
		cursor := ""
		if len(page) > 0 {
			cursor = page[len(page)-1].ID
		}
		for c := range pc.watchers {
			c.sendJSON(conversationPageMsg{
				Type: "conversationPage", ID: paneID, Supported: true, Agent: pc.spec.ID,
				Entries: page, Cursor: cursor, AtStart: start == 0, Reset: true,
			})
		}
		return
	}
	if len(changed) == 0 || len(pc.watchers) == 0 {
		return
	}
	cursor := changed[len(changed)-1].ID
	for c := range pc.watchers {
		c.sendJSON(conversationAppendMsg{Type: "conversationAppend", ID: paneID, Entries: changed, Cursor: cursor})
	}
}

// conversationPollLoop is the slow fallback for a pane whose hook events were
// suppressed or slow: while a pane being watched is Working, it is re-tailed
// every conversationPollInterval regardless. An idle pane costs nothing here
// -- its hook events are the only signal there ever is for it -- and neither
// does one with no client watching.
func (s *Server) conversationPollLoop() {
	tick := time.NewTicker(conversationPollInterval)
	defer tick.Stop()
	for {
		select {
		case <-s.closed:
			return
		case <-tick.C:
			ids := s.convos.watched()
			if len(ids) == 0 {
				continue
			}
			working, ok := ask(s, func() map[string]bool {
				out := make(map[string]bool, len(ids))
				for _, id := range ids {
					if p := s.ws.Pane(id); p != nil {
						if st, _ := p.Status(); st == session.StatusWorking {
							out[id] = true
						}
					}
				}
				return out
			})
			if !ok {
				continue
			}
			for id := range working {
				if pc, found := s.convos.lookup(id); found {
					s.refreshConversation(id, pc)
				}
			}
		}
	}
}
