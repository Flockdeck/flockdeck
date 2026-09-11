package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// geminiWire speaks streamGenerateContent.
//
// It is the odd one of the three: the model is named in the path rather than
// the body, the system prompt has a field of its own, an assistant is called a
// model, and a tool call has no id -- so the answer is paired with the call by
// the tool's name.
type geminiWire struct {
	base string
	key  string
}

func (w *geminiWire) Name() string { return "gemini" }

type geminiPart struct {
	Text         string            `json:"text,omitempty"`
	FunctionCall *geminiCall       `json:"functionCall,omitempty"`
	Response     *geminiCallAnswer `json:"functionResponse,omitempty"`
	// Thought marks a part that is the model's reasoning rather than its
	// answer, and ThoughtSignature is what has to come back with a call the
	// model made after thinking.
	Thought          bool   `json:"thought,omitempty"`
	ThoughtSignature string `json:"thoughtSignature,omitempty"`
}

type geminiCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type geminiCallAnswer struct {
	Name     string            `json:"name"`
	Response map[string]string `json:"response"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiRequest struct {
	Contents          []geminiContent `json:"contents"`
	SystemInstruction *geminiContent  `json:"systemInstruction,omitempty"`
	Tools             []geminiToolset `json:"tools,omitempty"`
	GenerationConfig  *geminiConfig   `json:"generationConfig,omitempty"`
}

type geminiToolset struct {
	FunctionDeclarations []geminiFunction `json:"functionDeclarations"`
}

type geminiFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type geminiConfig struct {
	MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
}

func (w *geminiWire) Stream(ctx context.Context, req Request, emit func(Event)) error {
	if req.Model == "" {
		// The model is part of the address here, so there is no endpoint
		// default to fall back on, and asking anyway is a 404 naming a path.
		return errors.New("the Gemini API needs a model named: choose one with /model, for example /model gemini-2.5-pro")
	}
	body := geminiRequest{Contents: geminiContents(req.Messages)}
	if req.System != "" {
		body.SystemInstruction = &geminiContent{Parts: []geminiPart{{Text: req.System}}}
	}
	if req.MaxTokens > 0 {
		body.GenerationConfig = &geminiConfig{MaxOutputTokens: req.MaxTokens}
	}
	var fns []geminiFunction
	for _, t := range req.Tools {
		d := t.Describe()
		fns = append(fns, geminiFunction{
			Name: t.Name(), Description: d.Description, Parameters: schemaParams(d),
		})
	}
	if len(fns) > 0 {
		body.Tools = []geminiToolset{{FunctionDeclarations: fns}}
	}

	h := http.Header{}
	if w.key != "" {
		h.Set("x-goog-api-key", w.key)
	}
	// alt=sse asks for the answer as an event stream; without it the same
	// endpoint streams a JSON array, which cannot be read a piece at a time.
	path := "/models/" + url.PathEscape(req.Model) + ":streamGenerateContent?alt=sse"
	rc, err := post(ctx, endpoint(w.base, "https://generativelanguage.googleapis.com", "v1beta", path), h, body)
	if err != nil {
		return err
	}
	defer rc.Close()

	var usage Usage
	var calls []ToolCall
	err = readSSE(rc, func(event, data string) error {
		var chunk struct {
			Candidates []struct {
				Content geminiContent `json:"content"`
			} `json:"candidates"`
			UsageMetadata struct {
				PromptTokenCount     int `json:"promptTokenCount"`
				CandidatesTokenCount int `json:"candidatesTokenCount"`
				// The reasoning a thinking model did is billed as output
				// but counted apart from the answer.
				ThoughtsTokenCount int `json:"thoughtsTokenCount"`
			} `json:"usageMetadata"`
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
		// Every chunk repeats the counts for the whole answer so far, so they
		// are taken rather than added up.
		if chunk.UsageMetadata.PromptTokenCount > 0 || chunk.UsageMetadata.CandidatesTokenCount > 0 {
			usage = Usage{
				In:  chunk.UsageMetadata.PromptTokenCount,
				Out: chunk.UsageMetadata.CandidatesTokenCount + chunk.UsageMetadata.ThoughtsTokenCount,
			}
		}
		for _, c := range chunk.Candidates {
			for _, p := range c.Content.Parts {
				switch {
				case p.Text != "" && p.Thought:
					emit(Event{Kind: EventThinking, Text: p.Text})
				case p.Text != "":
					emit(Event{Kind: EventText, Text: p.Text})
				}
				if p.FunctionCall != nil {
					calls = append(calls, ToolCall{
						// The name doubles as the id: there is nothing else to
						// quote in the answer, and the answer names the tool.
						ID:        p.FunctionCall.Name,
						Name:      p.FunctionCall.Name,
						Args:      argsOrEmpty(p.FunctionCall.Args),
						Signature: p.ThoughtSignature,
					})
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, c := range calls {
		emit(Event{Kind: EventCall, Call: c})
	}
	emit(Event{Kind: EventUsage, Usage: usage})
	return nil
}

// geminiContents translates a conversation into contents entries. Entries on the
// same side are merged, for the reason given in anthropicMessages.
func geminiContents(msgs []Message) []geminiContent {
	var out []geminiContent
	add := func(role string, parts ...geminiPart) {
		if n := len(out); n > 0 && out[n-1].Role == role {
			out[n-1].Parts = append(out[n-1].Parts, parts...)
			return
		}
		out = append(out, geminiContent{Role: role, Parts: parts})
	}
	for _, m := range msgs {
		switch m.Role {
		case RoleTool:
			// An answer belongs to the user's side of the exchange here, and
			// carries the tool's output under a key of our choosing.
			add("user", geminiPart{Response: &geminiCallAnswer{
				Name:     firstNonEmpty(m.Call.Name, m.Call.ID),
				Response: map[string]string{"output": m.Text},
			}})
		case RoleAssistant:
			var parts []geminiPart
			if m.Text != "" {
				parts = append(parts, geminiPart{Text: m.Text})
			}
			for _, c := range m.Calls {
				parts = append(parts, geminiPart{
					FunctionCall:     &geminiCall{Name: c.Name, Args: argsOrEmpty(c.Args)},
					ThoughtSignature: c.Signature,
				})
			}
			if len(parts) == 0 {
				continue
			}
			add("model", parts...)
		default:
			add("user", geminiPart{Text: m.Text})
		}
	}
	return out
}
