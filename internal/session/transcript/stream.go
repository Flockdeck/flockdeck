package transcript

import "github.com/jmwri/flockdeck/internal/agent"

// Entry is one agent-agnostic event out of a conversation: a prompt, a reply,
// a tool call, and the rest of the kinds the phone's chat view draws. It is
// the shape every adapter translates its own format into, and the shape the
// desktop-to-phone protocol carries -- see docs/plans/phone-conversation-view.md.
//
// Fields are flat and named the same across kinds that share a name (both
// prompt and notice carry "text"), rather than nested under a kind-named
// object, matching the wire contract.
type Entry struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// TS is RFC 3339, already formatted: adapters read a timestamp once and
	// hand it over as the wire wants it, rather than every caller downstream
	// converting it again.
	TS string `json:"ts"`

	// prompt.text, notice.text
	Text string `json:"text,omitempty"`
	// reply.markdown
	Markdown string `json:"markdown,omitempty"`
	// thinking.chars, thinking.seconds
	Chars   int  `json:"chars,omitempty"`
	Seconds *int `json:"seconds,omitempty"`

	// tool.name, tool.label, tool.status, tool.summary, tool.diff, tool.hasDetail
	// subagent.status, subagent.hasDetail (subagent.description, subagent.report below)
	Name      string `json:"name,omitempty"`
	Label     string `json:"label,omitempty"`
	Status    string `json:"status,omitempty"`
	Summary   string `json:"summary,omitempty"`
	Diff      string `json:"diff,omitempty"`
	HasDetail bool   `json:"hasDetail,omitempty"`

	// subagent.description, subagent.report
	Description string `json:"description,omitempty"`
	Report      string `json:"report,omitempty"`

	// compaction.turns
	Turns *int `json:"turns,omitempty"`

	// image.mediaType, image.bytes, image.width, image.height (image.hasDetail
	// is the shared HasDetail field above, and is always true for this kind).
	MediaType string `json:"mediaType,omitempty"`
	Bytes     int    `json:"bytes,omitempty"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
}

// Kinds an Entry can be, per the protocol.
const (
	KindPrompt     = "prompt"
	KindReply      = "reply"
	KindThinking   = "thinking"
	KindTool       = "tool"
	KindSubagent   = "subagent"
	KindCompaction = "compaction"
	KindNotice     = "notice"
	KindImage      = "image"
)

// Tool and subagent statuses.
const (
	StatusRunning = "running"
	StatusOK      = "ok"
	StatusError   = "error"
)

// Detail is the full body of an entry that arrived trimmed: a tool's full
// output or diff, a thinking block's text, a subagent's own transcript, or an
// image's own bytes.
type Detail struct {
	Text    string  `json:"text,omitempty"`
	Diff    string  `json:"diff,omitempty"`
	Entries []Entry `json:"entries,omitempty"`
	// Data is an image entry's bytes, base64, in the entry's own mediaType --
	// never sent inline on the entry itself, so a page listing several
	// pictures stays small until one is actually drawn.
	Data string `json:"data,omitempty"`
}

// Size caps from the protocol contract: a tool result over ToolOutputCap
// never arrives inline, and a diff over DiffCap becomes a summary with detail
// fetched on demand.
const (
	ToolOutputCap = 4 << 10
	DiffCap       = 64 << 10
)

// Stream is a live, tailing view onto one agent's conversation, translating
// its own transcript format into Entry as new lines are written.
//
// A Stream holds everything it has read so far -- entries are small, trimmed
// events, not raw transcript lines, so keeping the whole conversation in
// memory for as long as a pane's chat view might be open costs little next to
// the file itself. Paging (newest page, older, since-a-cursor) is done by the
// caller over Snapshot's slice; the Stream's own job is only turning bytes
// into Entry values and answering for the ones that arrived trimmed.
type Stream interface {
	// Refresh reads whatever is new since the last call, and returns every
	// entry that is new or was updated in place (a tool call whose result
	// just arrived), oldest first.
	Refresh() []Entry
	// Snapshot returns every entry seen so far, oldest first. The slice is
	// the Stream's own and must not be modified.
	Snapshot() []Entry
	// Detail returns the full body of an entry that arrived trimmed: a tool's
	// full output, a thinking block's text, an oversized diff, or a
	// subagent's own transcript, addressed by the subagent tool's entry id.
	Detail(id string) (Detail, bool)
	// Reset reports whether the conversation was replaced in place since this
	// was last asked -- true once, the first time it is asked after the
	// replacement, then false again until another happens. This is for an
	// agent whose own /clear (or equivalent) keeps the same session id and
	// file rather than starting a new one, the way Flockdeck's own chat client's
	// does: Claude Code needs nothing here, because its /clear is already
	// caught by the session id changing under it (see
	// internal/server/conversation.go's refreshConversation), so its Stream
	// always answers false.
	Reset() bool
}

// Streamer is a Reader that can also tail its agent's conversation live. Only
// an agent verified against the real tool implements it; every other agent's
// Reader does not, and StreamFor answers false for it, which is what the
// phone shows as "no chat view for this pane, only the terminal."
type Streamer interface {
	Stream(spec agent.Spec, sessionID string) (Stream, bool)
}

// StreamFor returns a live Stream for a pane's conversation, or ok=false when
// the agent has no adapter for the phone's chat view.
func StreamFor(spec agent.Spec, sessionID string) (Stream, bool) {
	st, ok := For(spec).(Streamer)
	if !ok {
		return nil, false
	}
	return st.Stream(spec, sessionID)
}
