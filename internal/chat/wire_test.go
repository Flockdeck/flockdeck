package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// echoTool is a tool that exists only to be described in a request.
type echoTool struct{ name string }

func (e echoTool) Name() string { return e.name }
func (e echoTool) Describe() Schema {
	return Schema{
		Description: "say something back",
		Params:      json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`),
	}
}
func (e echoTool) Approval(json.RawMessage) string { return "" }
func (e echoTool) Run(context.Context, json.RawMessage) (string, error) {
	return "", nil
}

// conversation is the exchange every wire is asked to carry, so that the three
// translations are compared on the same material: a prompt, an answer that
// called a tool, the tool's output, and a second prompt.
var conversation = []Message{
	{Role: RoleUser, Text: "what is in the file?"},
	{Role: RoleAssistant, Text: "let me look", Calls: []ToolCall{
		{ID: "call-1", Name: "read_file", Args: json.RawMessage(`{"path":"a.txt"}`)},
	}},
	{Role: RoleTool, Call: ToolCall{ID: "call-1", Name: "read_file"}, Text: "hello"},
	{Role: RoleUser, Text: "and the other one?"},
}

func TestWiresStream(t *testing.T) {
	tests := []struct {
		name   string
		wire   string
		body   string
		verify func(t *testing.T, sent map[string]any, path string)
	}{
		{
			name: "anthropic",
			wire: "anthropic",
			body: strings.Join([]string{
				`event: message_start`,
				`data: {"type":"message_start","message":{"usage":{"input_tokens":11}}}`,
				``,
				`event: content_block_delta`,
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"one "}}`,
				``,
				`event: content_block_delta`,
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"two"}}`,
				``,
				`event: content_block_start`,
				`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"c9","name":"echo"}}`,
				``,
				`event: content_block_delta`,
				`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"text\":"}}`,
				``,
				`event: content_block_delta`,
				`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"hi\"}"}}`,
				``,
				`event: message_delta`,
				`data: {"type":"message_delta","usage":{"output_tokens":7}}`,
				``,
				`event: message_stop`,
				`data: {"type":"message_stop"}`,
				``,
			}, "\n"),
			verify: func(t *testing.T, sent map[string]any, path string) {
				if path != "/v1/messages" {
					t.Errorf("posted to %q, want /v1/messages", path)
				}
				if sent["model"] != "m1" {
					t.Errorf("model = %v", sent["model"])
				}
				system, _ := sent["system"].([]any)
				if len(system) != 1 || system[0].(map[string]any)["text"] != "be brief" {
					t.Errorf("system = %v", sent["system"])
				}
				// Three, not four: the tool's answer and the prompt that follows
				// it are both the user's side, and this wire is the strictest
				// about the two sides alternating.
				msgs, _ := sent["messages"].([]any)
				if len(msgs) != 3 {
					t.Fatalf("sent %d messages, want 3", len(msgs))
				}
				third, _ := msgs[2].(map[string]any)
				if third["role"] != "user" {
					t.Errorf("a tool answer went as %v, want user", third["role"])
				}
				blocks, _ := third["content"].([]any)
				if len(blocks) != 2 {
					t.Fatalf("the merged entry has %d blocks, want 2", len(blocks))
				}
				first, _ := blocks[0].(map[string]any)
				if first["type"] != "tool_result" || first["tool_use_id"] != "call-1" {
					t.Errorf("tool answer block = %v", first)
				}
				second, _ := blocks[1].(map[string]any)
				if second["type"] != "text" || second["text"] != "and the other one?" {
					t.Errorf("the prompt after it = %v", second)
				}
			},
		},
		{
			name: "openai",
			wire: "openai",
			body: strings.Join([]string{
				`data: {"choices":[{"delta":{"content":"one "}}]}`,
				``,
				`data: {"choices":[{"delta":{"content":"two"}}]}`,
				``,
				`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c9","function":{"name":"echo","arguments":"{\"text\":"}}]}}]}`,
				``,
				`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"hi\"}"}}]}}]}`,
				``,
				`data: {"usage":{"prompt_tokens":11,"completion_tokens":7}}`,
				``,
				`data: [DONE]`,
				``,
			}, "\n"),
			verify: func(t *testing.T, sent map[string]any, path string) {
				if path != "/v1/chat/completions" {
					t.Errorf("posted to %q, want /v1/chat/completions", path)
				}
				if sent["model"] != "m1" {
					t.Errorf("model = %v", sent["model"])
				}
				msgs, _ := sent["messages"].([]any)
				if len(msgs) != 5 {
					t.Fatalf("sent %d messages, want 5 including the system prompt", len(msgs))
				}
				first, _ := msgs[0].(map[string]any)
				if first["role"] != "system" {
					t.Errorf("the system prompt went as %v", first["role"])
				}
				fourth, _ := msgs[3].(map[string]any)
				if fourth["role"] != "tool" || fourth["tool_call_id"] != "call-1" {
					t.Errorf("tool answer = %v", fourth)
				}
			},
		},
		{
			name: "gemini",
			wire: "gemini",
			body: strings.Join([]string{
				`data: {"candidates":[{"content":{"parts":[{"text":"one "}]}}]}`,
				``,
				`data: {"candidates":[{"content":{"parts":[{"text":"two"}]}}]}`,
				``,
				`data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"echo","args":{"text":"hi"}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":7}}`,
				``,
			}, "\n"),
			verify: func(t *testing.T, sent map[string]any, path string) {
				// The model is named in the path here rather than in the body,
				// which is the whole reason this wire needed its own file.
				if want := "/v1beta/models/m1:streamGenerateContent"; path != want {
					t.Errorf("posted to %q, want %q", path, want)
				}
				sys, _ := sent["systemInstruction"].(map[string]any)
				if sys == nil {
					t.Fatalf("no system instruction was sent")
				}
				contents, _ := sent["contents"].([]any)
				if len(contents) != 3 {
					t.Fatalf("sent %d contents, want 3 after merging the user's side", len(contents))
				}
				second, _ := contents[1].(map[string]any)
				if second["role"] != "model" {
					t.Errorf("the assistant went as %v, want model", second["role"])
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var sent map[string]any
			var path string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path = r.URL.Path
				json.NewDecoder(r.Body).Decode(&sent)
				w.Header().Set("Content-Type", "text/event-stream")
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			wire, err := NewWire(tc.wire, srv.URL, "k")
			if err != nil {
				t.Fatal(err)
			}
			var text strings.Builder
			var calls []ToolCall
			var usage Usage
			err = wire.Stream(context.Background(), Request{
				Model:     "m1",
				System:    "be brief",
				Messages:  conversation,
				Tools:     []Tool{echoTool{name: "echo"}},
				MaxTokens: 99,
			}, func(ev Event) {
				switch ev.Kind {
				case EventText:
					text.WriteString(ev.Text)
				case EventCall:
					calls = append(calls, ev.Call)
				case EventUsage:
					usage = ev.Usage
				}
			})
			if err != nil {
				t.Fatalf("stream: %v", err)
			}
			if text.String() != "one two" {
				t.Errorf("text = %q, want %q", text.String(), "one two")
			}
			if len(calls) != 1 || calls[0].Name != "echo" {
				t.Fatalf("calls = %+v", calls)
			}
			var args struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(calls[0].Args, &args); err != nil || args.Text != "hi" {
				t.Errorf("call arguments = %s (%v)", calls[0].Args, err)
			}
			if usage != (Usage{In: 11, Out: 7}) {
				t.Errorf("usage = %+v, want 11 in and 7 out", usage)
			}
			tc.verify(t, sent, path)
		})
	}
}

func TestWireReportsRefusalWithTheReasonGiven(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"credit balance is too low"}}`, http.StatusBadRequest)
	}))
	defer srv.Close()

	for _, name := range []string{"anthropic", "openai", "gemini"} {
		wire, err := NewWire(name, srv.URL, "k")
		if err != nil {
			t.Fatal(err)
		}
		err = wire.Stream(context.Background(), Request{Model: "m"}, func(Event) {})
		if err == nil || !strings.Contains(err.Error(), "credit balance is too low") {
			t.Errorf("%s: error = %v, want the vendor's own explanation", name, err)
		}
	}
}

func TestUnknownWireIsRefusedByName(t *testing.T) {
	if _, err := NewWire("cohere", "", ""); err == nil || !strings.Contains(err.Error(), "cohere") {
		t.Errorf("error = %v, want it to name the wire", err)
	}
}

func TestEndpointKeepsAVersionTheBaseAlreadyHas(t *testing.T) {
	tests := []struct {
		base string
		want string
	}{
		{"", "https://api.openai.com/v1/chat/completions"},
		{"http://127.0.0.1:11434/v1", "http://127.0.0.1:11434/v1/chat/completions"},
		{"http://127.0.0.1:11434/v1/", "http://127.0.0.1:11434/v1/chat/completions"},
		{"http://127.0.0.1:8000", "http://127.0.0.1:8000/v1/chat/completions"},
	}
	for _, tc := range tests {
		if got := endpoint(tc.base, "https://api.openai.com", "v1", "/chat/completions"); got != tc.want {
			t.Errorf("endpoint(%q) = %q, want %q", tc.base, got, tc.want)
		}
	}
}

func TestReadSSEJoinsDataLinesAndSkipsComments(t *testing.T) {
	stream := ": keeping the connection warm\n" +
		"event: one\ndata: {\"a\":\ndata: 1}\n\n" +
		"data: plain\n\n"
	type got struct{ event, data string }
	var seen []got
	err := readSSE(strings.NewReader(stream), func(event, data string) error {
		seen = append(seen, got{event, data})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 {
		t.Fatalf("read %d events, want 2: %+v", len(seen), seen)
	}
	if seen[0].event != "one" || seen[0].data != "{\"a\":\n1}" {
		t.Errorf("first event = %+v", seen[0])
	}
	if seen[1].data != "plain" {
		t.Errorf("second event = %+v", seen[1])
	}
}

// A whole file coming back from a tool arrives on one line, so the reader must
// not be limited to what fits in its buffer.
func TestReadSSEHandlesALineLongerThanTheBuffer(t *testing.T) {
	long := strings.Repeat("x", 300<<10)
	var got string
	err := readSSE(strings.NewReader("data: "+long+"\n\n"), func(_, data string) error {
		got = data
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != long {
		t.Errorf("read %d bytes, want %d", len(got), len(long))
	}
}
