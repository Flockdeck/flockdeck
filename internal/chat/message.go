// Package chat is Flockdeck's own terminal chat client: the `flockdeck chat`
// subcommand, run inside a pane's pseudo-terminal, talking straight to a model
// API.
//
// It exists so that a model reached over HTTP is a first-class agent rather
// than something Flockdeck shells out to. There is no wrapper CLI, no node and no
// Python: the pane runs this binary, and this binary speaks the vendor's own
// protocol. Everything Flockdeck does around a pane -- status, resume, the history
// overlay, a fan-out reading a plan -- is fed from in here using the machinery
// the `claude` CLI already drives, so the workspace has nothing new to learn.
package chat

import (
	"context"
	"encoding/json"
)

// Role is who an entry in a conversation came from.
//
// There are three rather than the four or five a given vendor names, because
// these are the ones the transcript records and every wire can express: a
// system prompt is a field on the request, not an entry in the conversation.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ToolCall is one tool the model asked for.
type ToolCall struct {
	// ID is the vendor's identifier for the call, which the answer must quote.
	// Gemini has none, so it is filled in from the name there.
	ID   string
	Name string
	// Args are the arguments as the model wrote them. They stay raw JSON all
	// the way to the tool because a tool's own schema is the only thing that
	// knows their shape.
	Args json.RawMessage
	// Signature is an opaque token a vendor attached to the call and wants
	// back with it: Gemini's thought signature, without which a model that
	// thought before calling refuses the request carrying the answer.
	Signature string
}

// Message is one entry in a conversation, in the single shape all three wires
// are translated from. Keeping one shape is what lets the transcript, the
// renderer and the tool loop be written once.
type Message struct {
	Role Role
	Text string
	// Calls are the tools an assistant message asked for.
	Calls []ToolCall
	// Call is the call a tool message answers.
	Call ToolCall
	// Thinking is the reasoning an assistant message came with, kept only to
	// be handed back.
	Thinking []Thinking
}

// Thinking is one block of reasoning a model returned with its answer.
//
// It is never drawn or recorded -- EventThinking carries what is shown -- and
// is kept for one reason: Anthropic's models that think by default refuse the
// next request of a tool loop when the reasoning that led to a call has been
// left out of it, and the signature is how the server knows it was not edited.
type Thinking struct {
	Text      string
	Signature string
	// Redacted is the opaque data of a block the server would not show.
	Redacted string
}

// Usage is what a turn cost in tokens.
type Usage struct {
	// In is every token of input read, however it was priced; CacheRead and
	// CacheWrite are the parts of it read from and written to a prompt cache,
	// which cost a fraction and a premium of the rest.
	In         int
	Out        int
	CacheRead  int
	CacheWrite int
}

// Add accumulates one turn's usage into a running total.
func (u *Usage) Add(v Usage) {
	u.In += v.In
	u.Out += v.Out
	u.CacheRead += v.CacheRead
	u.CacheWrite += v.CacheWrite
}

// Schema is a tool's arguments described as JSON Schema. Each wire translates
// it into the shape that vendor asks for; the schema itself is the same.
type Schema struct {
	Description string
	// Params is the JSON Schema object for the arguments -- `{"type":"object",
	// "properties":{...}}`. A tool that takes none may leave it empty.
	Params json.RawMessage
}

// Tool is one thing the model can do beyond talking. The chat loop knows only
// this much about a tool, so the loop and the tools it runs can be built apart.
type Tool interface {
	Name() string
	Describe() Schema
	// Approval returns the question to put to the user before the call runs,
	// or "" when the call needs no asking.
	Approval(args json.RawMessage) string
	Run(ctx context.Context, args json.RawMessage) (string, error)
}

// AlwaysApprover is implemented by a tool that can be approved once for a
// whole family of calls rather than one at a time.
//
// It is an interface rather than a rule in the loop because what makes two
// calls the same family is the tool's own business: for `run_command` it is
// the command prefix, and nothing else in v1 offers it at all.
type AlwaysApprover interface {
	// AlwaysKey returns a key standing for every call the user would be
	// agreeing to, or "" when this call cannot be approved in bulk.
	AlwaysKey(args json.RawMessage) string
}

// Request is one turn asked of a model.
type Request struct {
	Model     string
	System    string
	Messages  []Message
	Tools     []Tool
	MaxTokens int
}

// EventKind is what a streamed event carries.
type EventKind int

const (
	// EventText is a piece of the answer, as it arrives.
	EventText EventKind = iota
	// EventThinking is a piece of the model's reasoning, where a model returns
	// it. It is drawn differently and never recorded: it is the model working
	// something out, not what it decided.
	EventThinking
	// EventCall is a completed request to run a tool.
	EventCall
	// EventUsage is the token count for the turn, sent once, at the end.
	EventUsage
	// EventReasoning is a completed block of reasoning, to go back with the
	// answer in the next request.
	EventReasoning
	// EventNotice is something the user should know about how the answer is
	// being got -- not part of it, and never recorded.
	EventNotice
)

// Event is one thing that happened while a turn streamed.
type Event struct {
	Kind     EventKind
	Text     string
	Call     ToolCall
	Usage    Usage
	Thinking Thinking
}

// Wire speaks one vendor's HTTP protocol.
//
// A wire's whole job is translation: it turns a Request into that vendor's JSON
// and its stream back into Events. It renders nothing, records nothing and
// runs no tools, which is why three of them fit in a few hundred lines each.
type Wire interface {
	// Name is the wire's name as an agent's APISpec spells it.
	Name() string
	// Stream sends one request and calls emit for each event until the answer
	// is complete. A cancelled context ends the turn; emit is only ever called
	// from the calling goroutine.
	Stream(ctx context.Context, req Request, emit func(Event)) error
}
