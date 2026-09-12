package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
)

// anthropicWire speaks the Messages API, streaming.
type anthropicWire struct {
	base string
	key  string
}

func (w *anthropicWire) Name() string { return "anthropic" }

// header is what every request to the API carries.
func (w *anthropicWire) header() http.Header {
	h := http.Header{}
	h.Set("anthropic-version", anthropicVersion)
	if w.key != "" {
		h.Set("x-api-key", w.key)
	}
	return h
}

// anthropicVersion is the API version header every request must carry. It is
// not the model's version and does not move when models do.
const anthropicVersion = "2023-06-01"

// streamErrorCodes are the statuses the API answers with for the errors it can
// also send part-way through a stream.
var streamErrorCodes = map[string]int{
	"overloaded_error": 529,
	"api_error":        http.StatusInternalServerError,
	"rate_limit_error": http.StatusTooManyRequests,
}

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

	CacheControl *anthropicCache `json:"cache_control,omitempty"`
}

// anthropicCache marks the end of a stretch of the request to be cached.
type anthropicCache struct {
	Type string `json:"type"`
}

// cacheHere is the mark itself: the default five-minute cache, which a
// conversation going on at a person's pace refreshes with every request.
var cacheHere = &anthropicCache{Type: "ephemeral"}

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
	System    []anthropicBlock   `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
	Stream    bool               `json:"stream"`
}

func (w *anthropicWire) Stream(ctx context.Context, req Request, emit func(Event)) error {
	if req.Model == "" {
		// This API has no default model, and says so as "model: Field
		// required", which reads as something wrong with the request rather
		// than something the user can set.
		return errors.New("the Anthropic API needs a model named: choose one with /model, for example /model claude-sonnet-5")
	}
	// This API refuses a request without a ceiling.
	ceilingAsked := req.MaxTokens > 0
	if !ceilingAsked {
		req.MaxTokens = defaultMaxTokens
	}
	body := anthropicRequest{
		Model:     req.Model,
		MaxTokens: req.MaxTokens,
		Messages:  anthropicMessages(req.Messages),
		Stream:    true,
	}
	// Every request of a conversation sends the whole of it again, and a turn
	// that uses tools sends it again for every call: without a cache, each of
	// thirty steps pays full price to be told what it was told a moment ago.
	// Two marks make it cheap -- one after the system prompt, which outlasts a
	// /clear, and one at the end of the conversation, so that the next request
	// reads everything up to here from the cache. A prefix too short to cache
	// is simply not cached; nothing is refused for it.
	if req.System != "" {
		body.System = []anthropicBlock{{Type: "text", Text: req.System, CacheControl: cacheHere}}
	}
	if n := len(body.Messages); n > 0 {
		if b := len(body.Messages[n-1].Content); b > 0 {
			body.Messages[n-1].Content[b-1].CacheControl = cacheHere
		}
	}
	for _, t := range req.Tools {
		d := t.Describe()
		body.Tools = append(body.Tools, anthropicTool{
			Name:        t.Name(),
			Description: d.Description,
			InputSchema: schemaParams(d),
		})
	}

	url := endpoint(w.base, "https://api.anthropic.com", "v1", "/messages")
	rc, err := post(ctx, url, w.header(), body)
	if n, ok := outputCeiling(err); ok && !ceilingAsked {
		// The ceiling nobody asked for is higher than an older model takes,
		// and the refusal says what it will take: asked again with that, the
		// model answers, where every /retry would be refused the same way.
		body.MaxTokens, req.MaxTokens = n, n
		rc, err = post(ctx, url, w.header(), body)
	}
	if err != nil {
		return err
	}
	defer rc.Close()

	var counts anthropicUsage
	// Blocks are identified by an index rather than arriving one after another,
	// so a call or a piece of reasoning being accumulated is kept per index.
	calls := map[int]*callBuffer{}
	thoughts := map[int]*Thinking{}
	stopReason, finished := "", false
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
				StopReason  string `json:"stop_reason"`
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
			msg := firstNonEmpty(ev.Error.Message, ev.Error.Type, "the stream reported an error")
			// An error sent in the stream is the same failure the API sends
			// as a status before one starts -- overloaded, rate-limited,
			// failing on its own side -- and is kept in the same shape, so
			// that the loop can ask again when nothing of the answer came.
			if code := streamErrorCodes[ev.Error.Type]; code != 0 {
				return &apiError{Code: code, Status: ev.Error.Type, Msg: msg}
			}
			return fmt.Errorf("%s", redactKeys(msg))
		case "message_start":
			counts = ev.Message.Usage
		case "content_block_start":
			switch ev.ContentBlock.Type {
			case "tool_use":
				calls[ev.Index] = &callBuffer{id: ev.ContentBlock.ID, name: ev.ContentBlock.Name}
			case "thinking":
				thoughts[ev.Index] = &Thinking{Text: ev.ContentBlock.Thinking, Signature: ev.ContentBlock.Signature}
				// Said as soon as it starts, with nothing in it yet: the
				// models that think by default send none of the reasoning
				// itself, and the pane would otherwise sit silent through it
				// as though nothing were happening.
				emit(Event{Kind: EventThinking})
			case "redacted_thinking":
				thoughts[ev.Index] = &Thinking{Redacted: ev.ContentBlock.Data}
				emit(Event{Kind: EventThinking})
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
		case "message_stop":
			finished = true
		case "message_delta":
			if ev.Delta.StopReason != "" {
				stopReason = ev.Delta.StopReason
			}
			// These are running totals for the whole answer rather than what
			// was added since message_start, and the input counts are repeated
			// here as well: adding them to the opening counts read every
			// prompt twice.
			counts.update(ev.Usage)
		}
		return nil
	})
	if err != nil {
		return err
	}
	usage := counts.usage()
	var stopped error
	switch {
	case !finished:
		// The body ended without the event that says the answer has: the
		// connection dropped, or something between here and the API cut it.
		// A call whose arguments were still arriving is not one to run.
		stopped = errors.New("the connection closed before the answer was finished")
	case stopReason == "max_tokens":
		stopped = &cutOffError{fmt.Sprintf("the answer reached its limit of %d tokens and was cut off there", req.MaxTokens)}
	case stopReason == "refusal":
		stopped = errors.New("the model declined to go on with this")
	case stopReason == "model_context_window_exceeded":
		stopped = errors.New("the conversation has filled the model's context window")
	}
	if stopped != nil {
		// Reasoning the API finished -- signed, or redacted whole -- goes back
		// with what was written of the answer, unchanged, as the vendor's
		// guidance for its thinking models asks: "go on" after an answer cut
		// off at its limit then carries on from the model's reasoning rather
		// than without it. Half a block, unsigned, is not sent.
		for _, i := range sortedKeys(thoughts) {
			if t := thoughts[i]; t.Signature != "" || t.Redacted != "" {
				emit(Event{Kind: EventReasoning, Thinking: *t})
			}
		}
		// What was read is spent whether or not the answer is whole.
		emit(Event{Kind: EventUsage, Usage: usage})
		return stopped
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

// outputCeiling is the most output a model takes, where err is the API
// refusing a request for asking for more: "max_tokens: 32000 > 8192, which is
// the maximum allowed number of output tokens for claude-3-5-haiku-...".
func outputCeiling(err error) (int, bool) {
	var e *apiError
	if !errors.As(err, &e) || e.Code != http.StatusBadRequest {
		return 0, false
	}
	m := outputCeilingRE.FindStringSubmatch(e.Msg)
	if m == nil {
		return 0, false
	}
	n, convErr := strconv.Atoi(m[1])
	return n, convErr == nil && n > 0
}

var outputCeilingRE = regexp.MustCompile(`max_tokens: \d+ > (\d+)`)

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	// The input read from the cache and written to it are counted apart from
	// input_tokens, which is only what was neither.
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

// update takes the running totals a later event repeats.
func (u *anthropicUsage) update(v anthropicUsage) {
	for _, f := range []struct {
		to   *int
		from int
	}{
		{&u.InputTokens, v.InputTokens},
		{&u.OutputTokens, v.OutputTokens},
		{&u.CacheReadInputTokens, v.CacheReadInputTokens},
		{&u.CacheCreationInputTokens, v.CacheCreationInputTokens},
	} {
		if f.from > 0 {
			*f.to = f.from
		}
	}
}

func (u anthropicUsage) usage() Usage {
	return Usage{
		In:         u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens,
		Out:        u.OutputTokens,
		CacheRead:  u.CacheReadInputTokens,
		CacheWrite: u.CacheCreationInputTokens,
	}
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
