package acp

import (
	"bytes"
	"encoding/json"
)

// ProtocolVersion is the ACP version the client speaks. All of the agents
// tried so far accepted it.
const ProtocolVersion = 1

// Update kinds the client knows by name. A kind not listed here is still
// delivered, with Kind and Raw set.
const (
	UpdateUserMessageChunk  = "user_message_chunk"
	UpdateAgentMessageChunk = "agent_message_chunk"
	UpdateAgentThoughtChunk = "agent_thought_chunk"
	UpdateToolCall          = "tool_call"
	UpdateToolCallUpdate    = "tool_call_update"
	UpdatePlan              = "plan"
	UpdateUsage             = "usage_update"
)

// Stop reasons a prompt can end with. The agent may send others.
const (
	StopEndTurn   = "end_turn"
	StopCancelled = "cancelled"
)

// ContentBlock is one piece of content in a message chunk or a tool result.
// Type is the block's discriminator; Raw is the whole block as the agent sent
// it, so a type this client does not know is kept rather than dropped.
type ContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	URI      string `json:"uri,omitempty"`

	Raw json.RawMessage `json:"-"`
}

func (b *ContentBlock) UnmarshalJSON(data []byte) error {
	type plain ContentBlock
	var p plain
	// A block that is not an object, or has fields of the wrong type, still
	// keeps its bytes: Type stays empty and Raw is set.
	_ = json.Unmarshal(data, &p)
	*b = ContentBlock(p)
	b.Raw = append(json.RawMessage(nil), data...)
	return nil
}

// Location is a file a tool call touches.
type Location struct {
	Path string `json:"path"`
	Line *int   `json:"line,omitempty"`
}

// ToolContent is one item of a tool_call or tool_call_update's content
// array. Type is "content", "diff" or "terminal" in the current schema; Raw
// holds the item whole.
type ToolContent struct {
	Type       string        `json:"type"`
	Content    *ContentBlock `json:"content,omitempty"`
	Path       string        `json:"path,omitempty"`
	OldText    *string       `json:"oldText,omitempty"`
	NewText    string        `json:"newText,omitempty"`
	TerminalID string        `json:"terminalId,omitempty"`

	Raw json.RawMessage `json:"-"`
}

func (c *ToolContent) UnmarshalJSON(data []byte) error {
	type plain ToolContent
	var p plain
	_ = json.Unmarshal(data, &p)
	*c = ToolContent(p)
	c.Raw = append(json.RawMessage(nil), data...)
	return nil
}

// PlanEntry is one line of an agent's plan.
type PlanEntry struct {
	Content  string `json:"content"`
	Priority string `json:"priority,omitempty"`
	Status   string `json:"status,omitempty"`
}

// Cost is the money an agent reports for a session.
type Cost struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency,omitempty"`
}

// Update is one session/update notification.
//
// Kind is the sessionUpdate discriminator and Raw is the update object as
// sent. Which of the other fields are set depends on Kind: message chunks set
// Content, tool_call and tool_call_update set the Tool fields, plan sets Plan,
// usage_update sets Used, Size and Cost. A kind this client does not know, or a
// known kind whose fields did not decode, is still delivered with Kind and Raw,
// and nothing else is set.
type Update struct {
	SessionID string
	Kind      string
	Raw       json.RawMessage

	// Content is set for the three message-chunk kinds.
	Content *ContentBlock

	// Tool fields, for tool_call and tool_call_update. In an update only the
	// fields that changed are set.
	ToolCallID  string
	Title       string
	ToolKind    string
	Status      string
	RawInput    json.RawMessage
	RawOutput   json.RawMessage
	Locations   []Location
	ToolContent []ToolContent

	// Plan is set for plan.
	Plan []PlanEntry

	// Used and Size are set for usage_update: tokens in the context and the
	// window's size. Cost is set when the agent reports one.
	Used *int64
	Size *int64
	Cost *Cost
}

// decodeUpdate builds an Update from the update object. It never fails: the
// kind is read on its own first, and each further piece that does not decode
// is left out instead of dropping the update.
func decodeUpdate(sessionID string, raw json.RawMessage) Update {
	u := Update{SessionID: sessionID, Raw: raw}
	var k struct {
		Kind string `json:"sessionUpdate"`
	}
	_ = json.Unmarshal(raw, &k)
	u.Kind = k.Kind

	switch u.Kind {
	case UpdateUserMessageChunk, UpdateAgentMessageChunk, UpdateAgentThoughtChunk:
		var w struct {
			Content *ContentBlock `json:"content"`
		}
		if json.Unmarshal(raw, &w) == nil {
			u.Content = w.Content
		}
	case UpdateToolCall, UpdateToolCallUpdate:
		// encoding/json fills every field it can and reports the first one of the
		// wrong type, so the error is ignored: one odd field must not lose the rest.
		var w struct {
			ToolCallID string          `json:"toolCallId"`
			Title      string          `json:"title"`
			Kind       string          `json:"kind"`
			Status     string          `json:"status"`
			RawInput   json.RawMessage `json:"rawInput"`
			RawOutput  json.RawMessage `json:"rawOutput"`
			Locations  []Location      `json:"locations"`
			Content    json.RawMessage `json:"content"`
		}
		_ = json.Unmarshal(raw, &w)
		u.ToolCallID, u.Title, u.ToolKind, u.Status = w.ToolCallID, w.Title, w.Kind, w.Status
		u.RawInput, u.RawOutput, u.Locations = w.RawInput, w.RawOutput, w.Locations
		// content is an array here and one object in a message chunk.
		if c := bytes.TrimSpace(w.Content); len(c) > 0 && c[0] == '[' {
			_ = json.Unmarshal(c, &u.ToolContent)
		}
	case UpdatePlan:
		var w struct {
			Entries []PlanEntry `json:"entries"`
		}
		if json.Unmarshal(raw, &w) == nil {
			u.Plan = w.Entries
		}
	case UpdateUsage:
		var w struct {
			Used *int64 `json:"used"`
			Size *int64 `json:"size"`
			Cost *Cost  `json:"cost"`
		}
		if json.Unmarshal(raw, &w) == nil {
			u.Used, u.Size, u.Cost = w.Used, w.Size, w.Cost
		}
	}
	return u
}

// PermissionOption is one choice offered by session/request_permission. The
// ids are the agent's own; an answer must use one of them.
type PermissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	// Kind is allow_once, allow_always, reject_once or reject_always. An agent
	// may use others.
	Kind string `json:"kind"`
}

// ToolCall is the tool call a permission request is about.
type ToolCall struct {
	ToolCallID string          `json:"toolCallId"`
	Title      string          `json:"title,omitempty"`
	Kind       string          `json:"kind,omitempty"`
	Status     string          `json:"status,omitempty"`
	RawInput   json.RawMessage `json:"rawInput,omitempty"`
	Locations  []Location      `json:"locations,omitempty"`
}

// PermissionRequest is an agent's session/request_permission.
type PermissionRequest struct {
	// RequestID is the JSON-RPC id of the request as the agent sent it, raw
	// ("7" or "\"perm-1\""). It is a key for the caller, not something to
	// parse.
	RequestID string             `json:"-"`
	SessionID string             `json:"sessionId"`
	ToolCall  ToolCall           `json:"toolCall"`
	Options   []PermissionOption `json:"options"`

	Raw json.RawMessage `json:"-"`
}

// AuthMethod is one way an agent says it can be authenticated.
type AuthMethod struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`

	Raw json.RawMessage `json:"-"`
}

func (a *AuthMethod) UnmarshalJSON(data []byte) error {
	type plain AuthMethod
	var p plain
	_ = json.Unmarshal(data, &p)
	*a = AuthMethod(p)
	a.Raw = append(json.RawMessage(nil), data...)
	return nil
}

// AgentInfo is what the agent says it is.
type AgentInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version,omitempty"`
}

// InitResult is the agent's reply to initialize.
type InitResult struct {
	ProtocolVersion int `json:"protocolVersion"`
	// AgentCapabilities is kept raw: the client reads none of it yet.
	AgentCapabilities json.RawMessage `json:"agentCapabilities,omitempty"`
	AgentInfo         *AgentInfo      `json:"agentInfo,omitempty"`
	AuthMethods       []AuthMethod    `json:"authMethods,omitempty"`
}

// EnvVar is one environment variable for an MCP server.
type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// MCPServer is a stdio MCP server the agent is asked to start for a session.
type MCPServer struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Env     []EnvVar `json:"env"`
}

// MarshalJSON sends args and env as empty arrays, never null: the schema
// requires arrays and an agent may reject null.
func (m MCPServer) MarshalJSON() ([]byte, error) {
	type plain MCPServer
	p := plain(m)
	if p.Args == nil {
		p.Args = []string{}
	}
	if p.Env == nil {
		p.Env = []EnvVar{}
	}
	return json.Marshal(p)
}
