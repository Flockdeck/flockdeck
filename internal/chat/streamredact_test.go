package chat

import (
	"strings"
	"testing"
)

// An error sent in the middle of a stream is shown without any part of a key
// the vendor quoted in it, as a failed request's is.
func TestAStreamErrorShowsNoPartOfAKey(t *testing.T) {
	for _, c := range []struct{ wire, body, fragment string }{
		{"openai", `data: {"error":{"message":"invalid key sk-proj-abcd1234efgh"}}` + "\n\n", "abcd1234"},
		{"gemini", `data: {"error":{"message":"bad key AIzaSyA1b2C3d4E5f6G7h8"}}` + "\n\n", "SyA1b2"},
		{"anthropic", `data: {"type":"error","error":{"type":"invalid_request_error","message":"key sk-ant-api03-zzzz9999"}}` + "\n\n", "zzzz9999"},
	} {
		_, err := streamFrom(t, c.wire, c.body)
		if err == nil {
			t.Errorf("%s: the stream's error was not reported", c.wire)
			continue
		}
		if strings.Contains(err.Error(), c.fragment) || !strings.Contains(err.Error(), "[a key]") {
			t.Errorf("%s: error = %q", c.wire, err)
		}
	}
}
