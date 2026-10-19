package hooks

import (
	"encoding/json"
	"testing"
)

func TestIsSendMessage(t *testing.T) {
	for tool, want := range map[string]bool{
		"SendMessage": true, "sendmessage": true, " send_message ": true,
		"Bash": false, "": false, "SendMessageNow": false, "mcp__x__SendMessage": false,
	} {
		if got := IsSendMessage(tool); got != want {
			t.Errorf("IsSendMessage(%q) = %v, want %v", tool, got, want)
		}
	}
}

// The inputs here are written from the shape table in sendmessage.go, not
// recorded from a session: the real shape has not been captured.
func TestSendMessageRecipient(t *testing.T) {
	good := map[string]struct{ input, to string }{
		"to and message":      {`{"to":"researcher","message":"look at the logs"}`, "researcher"},
		"recipient and text":  {`{"recipient":"worker-2","text":"done"}`, "worker-2"},
		"upper case names":    {`{"To":"Ana","Message":"hi"}`, "Ana"},
		"spaces trimmed":      {`{"to":"  ana  ","content":"hi"}`, "ana"},
		"object recipient":    {`{"to":{"name":"ana"},"message":"hi"}`, "ana"},
		"structured message":  {`{"to":"ana","message":{"type":"ping"}}`, "ana"},
		"extra fields":        {`{"to":"ana","message":"hi","summary":"x","priority":3}`, "ana"},
		"first name in table": {`{"to":"ana","target":"bob","message":"hi"}`, "ana"},
	}
	for name, c := range good {
		to, ok := SendMessageRecipient(json.RawMessage(c.input))
		if !ok || to != c.to {
			t.Errorf("%s: got %q, %v, want %q", name, to, ok, c.to)
		}
	}
	bad := map[string]string{
		"empty":             ``,
		"null":              `null`,
		"array":             `[1,2]`,
		"string":            `"hi"`,
		"damaged":           `{"to":"ana","message":`,
		"no recipient":      `{"message":"hi"}`,
		"no message":        `{"to":"ana"}`,
		"empty message":     `{"to":"ana","message":"  "}`,
		"empty object":      `{"to":"ana","message":{}}`,
		"empty recipient":   `{"to":"","message":"hi"}`,
		"number recipient":  `{"to":7,"message":"hi"}`,
		"broadcast star":    `{"to":"*","message":"hi"}`,
		"broadcast all":     `{"to":"All","message":"hi"}`,
		"recipient too big": `{"to":"` + longName() + `","message":"hi"}`,
		"object no name":    `{"to":{"x":1},"message":"hi"}`,
	}
	for name, in := range bad {
		if to, ok := SendMessageRecipient(json.RawMessage(in)); ok || to != "" {
			t.Errorf("%s: got %q, %v, want not understood", name, to, ok)
		}
	}
}

func longName() string {
	b := make([]byte, maxRecipient+1)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}

// The message text is checked for and never returned.
func TestSendMessageRecipientReturnsNothingOfTheText(t *testing.T) {
	to, ok := SendMessageRecipient(json.RawMessage(`{"to":"ana","message":"the password is hunter2"}`))
	if !ok || to != "ana" {
		t.Fatalf("got %q, %v", to, ok)
	}
}

// buildDetail carries a PostToolUse input whole enough for the tap, which
// buildToolInput does not (it keeps only a command, a path and questions).
func TestPostToolUseDetailCarriesTheSendMessageInput(t *testing.T) {
	cp := claudePayload{
		ToolName:  "SendMessage",
		ToolInput: json.RawMessage(`{"to":"ana","message":"hi"}`),
	}
	if buildToolInput(cp.ToolInput) != "" {
		t.Fatal("buildToolInput now keeps a SendMessage input; the tap can read it there instead")
	}
	d := buildDetail("PostToolUse", cp)
	if d == nil {
		t.Fatal("no detail")
	}
	if to, ok := SendMessageRecipient(d.Input); !ok || to != "ana" {
		t.Fatalf("recipient from the detail: %q, %v", to, ok)
	}
}
