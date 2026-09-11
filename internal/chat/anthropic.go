package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
)

// anthropicWire speaks the Messages API, streaming.
type anthropicWire struct {
	base string
	key  string
}

func (w *anthropicWire) Name() string { return "anthropic" }

// anthropicVersion is the API version header every request must carry. It is
// not the model's version and does not move when models do.
const anthropicVersion = "2023-06-01"

// anthropicBlock is one piece of content, in the request and in the reply. The
// three shapes -- text, a tool call, a tool's answer -- share a struct because
// the wire tells them apart by their type field.
type anthropicBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`

	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`

	// Thinking is a pointer because a block whose reasoning was not shown
	// still has to say so with an empty string, and handing a block back
	// changed in any way is refused.
	Thinking  *string `json:"thinking,omitempty"`
	Signature string  `json:"signature,omitempty"`
	Data      string  `json:"data,omitempty"`
}

type anthropicMessage struct {
	Role    string           `json:"role"`
	Content []anthropicBlock `json:"content"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
	Stream    bool               `json:"stream"`
}

func (w *anthropicWire) Stream(ctx context.Context, req Request, emit func(Event)) error {
	body := anthropicRequest{
		Model:     req.Model,
		MaxTokens: req.MaxTokens,
		System:    req.System,
		Messages:  anthropicMessages(req.Messages),
		Stream:    true,
	}
	for _, t := range req.Tools {
		d := t.Describe()
		body.Tools = append(body.Tools, anthropicTool{
			Name:        t.Name(),
			Description: d.Description,
			InputSchema: schemaParams(d),
		})
	}

	h := http.Header{}
	h.Set("anthropic-version", anthropicVersion)
	if w.key != "" {
		h.Set("x-api-key", w.key)
	}
	rc, err := post(ctx, endpoint(w.base, "https://api.anthropic.com", "v1", "/messages"), h, body)
	if err != nil {
		return err
	}
	defer rc.Close()

	var usage Usage
	// Blocks are identified by an index rather than arriving one after another,
	// so a call or a piece of reasoning being accumulated is kept per index.
	calls := map[int]*callBuffer{}
	thoughts := map[int]*Thinking{}
	err = readSSE(rc, func(event, data string) error {
		var ev struct {
			Type  string `json:"type"`
			Index int    `json:"index"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				Signature   string `json:"signature"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
			ContentBlock struct {
				Type      string `json:"type"`
				ID        string `json:"id"`
				Name      string `json:"name"`
				Thinking  string `json:"thinking"`
				Signature string `json:"signature"`
				Data      string `json:"data"`
			} `json:"content_block"`
			Message struct {
				Usage anthropicUsage `json:"usage"`
			} `json:"message"`
			Usage anthropicUsage `json:"usage"`
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(data), &ev) != nil {
			return nil
		}
		switch ev.Type {
		case "error":
			return fmt.Errorf("%s", firstNonEmpty(ev.Error.Message, ev.Error.Type, "the stream reported an error"))
		case "message_start":
			usage.In += ev.Message.Usage.InputTokens
			usage.Out += ev.Message.Usage.OutputTokens
		case "content_block_start":
			switch ev.ContentBlock.Type {
			case "tool_use":
				calls[ev.Index] = &callBuffer{id: ev.ContentBlock.ID, name: ev.ContentBlock.Name}
			case "thinking":
				thoughts[ev.Index] = &Thinking{Text: ev.ContentBlock.Thinking, Signature: ev.ContentBlock.Signature}
			case "redacted_thinking":
				thoughts[ev.Index] = &Thinking{Redacted: ev.ContentBlock.Data}
			}
		case "content_block_delta":
			switch ev.Delta.Type {
			case "text_delta":
				emit(Event{Kind: EventText, Text: ev.Delta.Text})
			case "thinking_delta":
				if t := thoughts[ev.Index]; t != nil {
					t.Text += ev.Delta.Thinking
				}
				emit(Event{Kind: EventThinking, Text: ev.Delta.Thinking})
			case "signature_delta":
				if t := thoughts[ev.Index]; t != nil {
					t.Signature += ev.Delta.Signature
				}
			case "input_json_delta":
				if c := calls[ev.Index]; c != nil {
					c.args.WriteString(ev.Delta.PartialJSON)
				}
			}
		case "message_delta":
			usage.Out += ev.Usage.OutputTokens
			usage.In += ev.Usage.InputTokens
		}
		return nil
	})
	if err != nil {
		return err
	}
	// The reasoning and the calls go out in index order: that is the order the
	// model wrote them in, and a tool loop that runs them in map order runs
	// them differently every time.
	for _, i := range sortedKeys(thoughts) {
		emit(Event{Kind: EventReasoning, Thinking: *thoughts[i]})
	}
	for _, i := range sortedKeys(calls) {
		emit(Event{Kind: EventCall, Call: calls[i].done()})
	}
	emit(Event{Kind: EventUsage, Usage: usage})
	return nil
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// anthropicMessages translates a conversation into Messages API entries.
//
// Two wrinkles. A tool's answer is a user entry here, and answers to calls made
// in the same assistant turn have to arrive together in one entry -- splitting
// them across two is how a model learns to stop asking for tools in parallel.
// And entries on the same side are merged rather than repeated, because a turn
// the user interrupted before the model said anything leaves two prompts in a
// row, and the wire that is strictest about the two sides alternating should
// never be the one that finds out.
func anthropicMessages(msgs []Message) []anthropicMessage {
	var out []anthropicMessage
	add := func(role string, blocks ...anthropicBlock) {
		if n := len(out); n > 0 && out[n-1].Role == role {
			out[n-1].Content = append(out[n-1].Content, blocks...)
			return
		}
		out = append(out, anthropicMessage{Role: role, Content: blocks})
	}
	for _, m := range msgs {
		switch m.Role {
		case RoleTool:
			add("user", anthropicBlock{Type: "tool_result", ToolUseID: m.Call.ID, Content: m.Text})
		case RoleAssistant:
			var blocks []anthropicBlock
			// Reasoning comes first because that is where the model put it,
			// ahead of what it said and the calls it made.
			for _, t := range m.Thinking {
				if t.Redacted != "" {
					blocks = append(blocks, anthropicBlock{Type: "redacted_thinking", Data: t.Redacted})
					continue
				}
				text := t.Text
				blocks = append(blocks, anthropicBlock{Type: "thinking", Thinking: &text, Signature: t.Signature})
			}
			if m.Text != "" {
				blocks = append(blocks, anthropicBlock{Type: "text", Text: m.Text})
			}
			for _, c := range m.Calls {
				blocks = append(blocks, anthropicBlock{
					Type: "tool_use", ID: c.ID, Name: c.Name, Input: argsOrEmpty(c.Args),
				})
			}
			if len(blocks) == 0 {
				continue
			}
			add("assistant", blocks...)
		default:
			add("user", anthropicBlock{Type: "text", Text: m.Text})
		}
	}
	return out
}

// schemaParams returns a tool's arguments schema, or the empty object.
//
// Every wire insists on an object here even for a tool that takes no
// arguments, and a missing schema is rejected rather than defaulted.
func schemaParams(d Schema) json.RawMessage {
	if len(d.Params) > 0 && json.Valid(d.Params) {
		return d.Params
	}
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

func argsOrEmpty(args json.RawMessage) json.RawMessage {
	if len(args) > 0 && json.Valid(args) {
		return args
	}
	return json.RawMessage(`{}`)
}

func sortedKeys[V any](m map[int]V) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
