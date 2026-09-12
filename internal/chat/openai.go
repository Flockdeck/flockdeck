package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// openaiWire speaks Chat Completions, streaming -- and with it every
// OpenAI-compatible endpoint, which is most of them: Ollama, LM Studio, vLLM
// and the gateways all answer this shape at a base URL of their own.
type openaiWire struct {
	base string
	key  string
	// noTools is set once the endpoint has refused tools, so that they are
	// not offered to it again.
	noTools bool
}

func (w *openaiWire) Name() string { return "openai" }

type openaiToolCall struct {
	Index    int    `json:"index,omitempty"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

// openaiCallOut is a call as a request carries it back. It is not
// openaiToolCall because a request's calls have no index -- that belongs to the
// stream, where it says which call a fragment continues -- and a server strict
// about its schema refuses a field it does not know.
type openaiCallOut struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type openaiMessage struct {
	Role      string          `json:"role"`
	Content   string          `json:"content,omitempty"`
	ToolCalls []openaiCallOut `json:"tool_calls,omitempty"`
	// ToolCallID is what a tool entry answers, and the only way the server can
	// pair an answer with the call that asked for it.
	ToolCallID string `json:"tool_call_id,omitempty"`
}

type openaiTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type openaiRequest struct {
	Model    string          `json:"model"`
	Messages []openaiMessage `json:"messages"`
	Tools    []openaiTool    `json:"tools,omitempty"`
	// MaxTokens is sent only when the user set a ceiling, and under the newer
	// name because the current models reject the older one outright, while a
	// server that has only heard of the older one ignores this and falls back
	// to a ceiling of its own.
	MaxTokens int  `json:"max_completion_tokens,omitempty"`
	Stream    bool `json:"stream"`
	// StreamOptions is how a streamed request gets a token count at all: without
	// it the usage block is simply left out of the stream. Servers that have
	// never heard of it ignore it, which costs a request that would otherwise
	// have had to choose between streaming and knowing what it spent.
	StreamOptions *openaiStreamOptions `json:"stream_options,omitempty"`
}

type openaiStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

func (w *openaiWire) Stream(ctx context.Context, req Request, emit func(Event)) error {
	body := openaiRequest{
		Model:         req.Model,
		Messages:      openaiMessages(req.System, req.Messages),
		MaxTokens:     req.MaxTokens,
		Stream:        true,
		StreamOptions: &openaiStreamOptions{IncludeUsage: true},
	}
	for _, t := range req.Tools {
		if w.noTools {
			break
		}
		d := t.Describe()
		var ot openaiTool
		ot.Type = "function"
		ot.Function.Name = t.Name()
		ot.Function.Description = d.Description
		ot.Function.Parameters = schemaParams(d)
		body.Tools = append(body.Tools, ot)
	}

	h := http.Header{}
	if w.key != "" {
		h.Set("Authorization", "Bearer "+w.key)
	}
	url := endpoint(w.base, "https://api.openai.com", "v1", "/chat/completions")
	rc, err := post(ctx, url, h, body)
	if refusedStreamOptions(err) {
		// An endpoint that refuses the request over stream_options -- older
		// Azure deployments do -- would refuse every request, and what it
		// costs to leave out is only the token count.
		body.StreamOptions = nil
		rc, err = post(ctx, url, h, body)
	}
	if refusedTools(err) && len(body.Tools) > 0 {
		// A local model served without tool support refuses every request
		// that offers tools, which would leave the pane unable to talk to it
		// at all. It is talked to without them, and told so once.
		w.noTools = true
		body.Tools = nil
		emit(Event{Kind: EventNotice, Text: "this model does not take tools, so it is answering without them"})
		rc, err = post(ctx, url, h, body)
	}
	if err != nil {
		return err
	}
	defer rc.Close()

	var usage Usage
	var finish string
	calls := map[int]*callBuffer{}
	err = readSSE(rc, func(event, data string) error {
		// The end of the stream is a sentinel rather than the end of the body,
		// and it is not JSON.
		if strings.TrimSpace(data) == "[DONE]" {
			return nil
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string           `json:"content"`
					Reasoning string           `json:"reasoning_content"`
					ToolCalls []openaiToolCall `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(data), &chunk) != nil {
			return nil
		}
		if chunk.Error.Message != "" {
			return fmt.Errorf("%s", chunk.Error.Message)
		}
		if chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
			usage = Usage{In: chunk.Usage.PromptTokens, Out: chunk.Usage.CompletionTokens}
		}
		for _, ch := range chunk.Choices {
			if ch.FinishReason != "" {
				finish = ch.FinishReason
			}
			if ch.Delta.Content != "" {
				emit(Event{Kind: EventText, Text: ch.Delta.Content})
			}
			if ch.Delta.Reasoning != "" {
				emit(Event{Kind: EventThinking, Text: ch.Delta.Reasoning})
			}
			for _, tc := range ch.Delta.ToolCalls {
				c := calls[tc.Index]
				if c == nil {
					c = &callBuffer{}
					calls[tc.Index] = c
				}
				// Only the first chunk of a call carries its id and name; the
				// rest are arguments arriving a few characters at a time.
				if tc.ID != "" {
					c.id = tc.ID
				}
				if tc.Function.Name != "" {
					c.name = tc.Function.Name
				}
				c.args.WriteString(tc.Function.Arguments)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	// An answer that stopped short says why in its last choice. Read as
	// finished, it would be half a reply shown as the whole of one, and a call
	// whose arguments were still arriving would be run with them missing.
	switch finish {
	case "length":
		emit(Event{Kind: EventUsage, Usage: usage})
		return errors.New("the answer reached the model's limit on its length and was cut off there")
	case "content_filter":
		emit(Event{Kind: EventUsage, Usage: usage})
		return errors.New("the endpoint's content filter stopped the answer")
	}
	for _, i := range sortedKeys(calls) {
		if calls[i].name == "" {
			continue
		}
		emit(Event{Kind: EventCall, Call: calls[i].done()})
	}
	emit(Event{Kind: EventUsage, Usage: usage})
	return nil
}

// refusedStreamOptions reports whether err is an endpoint refusing a request
// because of the stream_options field.
func refusedStreamOptions(err error) bool {
	var e *apiError
	return errors.As(err, &e) && e.Code == http.StatusBadRequest &&
		strings.Contains(strings.ToLower(e.Msg), "stream_options")
}

// refusedTools reports whether err is an endpoint refusing a request because the
// model behind it cannot use tools, as Ollama says of a model without them.
func refusedTools(err error) bool {
	var e *apiError
	if !errors.As(err, &e) || e.Code != http.StatusBadRequest {
		return false
	}
	msg := strings.ToLower(e.Msg)
	return strings.Contains(msg, "does not support tools") || strings.Contains(msg, "tools are not supported")
}

// openaiMessages translates a conversation into Chat Completions entries. The
// system prompt is one of them here rather than a field of its own.
func openaiMessages(system string, msgs []Message) []openaiMessage {
	var out []openaiMessage
	if system != "" {
		out = append(out, openaiMessage{Role: "system", Content: system})
	}
	for _, m := range msgs {
		switch m.Role {
		case RoleTool:
			out = append(out, openaiMessage{Role: "tool", ToolCallID: m.Call.ID, Content: m.Text})
		case RoleAssistant:
			e := openaiMessage{Role: "assistant", Content: m.Text}
			for _, c := range m.Calls {
				var tc openaiCallOut
				tc.ID = c.ID
				tc.Type = "function"
				tc.Function.Name = c.Name
				tc.Function.Arguments = string(argsOrEmpty(c.Args))
				e.ToolCalls = append(e.ToolCalls, tc)
			}
			out = append(out, e)
		default:
			out = append(out, openaiMessage{Role: "user", Content: m.Text})
		}
	}
	return out
}
