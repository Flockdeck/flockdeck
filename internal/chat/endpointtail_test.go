package chat

import "testing"

// A base given as the whole request URL, as a server's documentation often
// gives it, is taken back to its root rather than having the path added again.
func TestEndpointTakesTheAPIPathOffABase(t *testing.T) {
	for _, c := range []struct{ base, version, path, want string }{
		{"http://127.0.0.1:1234/v1/chat/completions", "v1", "/chat/completions", "http://127.0.0.1:1234/v1/chat/completions"},
		{"http://127.0.0.1:1234/v1/chat/completions/", "v1", "/models", "http://127.0.0.1:1234/v1/models"},
		{"https://gateway.example/v1/messages", "v1", "/messages", "https://gateway.example/v1/messages"},
		{"https://gateway.example/anthropic/v1/messages", "v1", "/models?limit=100", "https://gateway.example/anthropic/v1/models?limit=100"},
		{"https://generativelanguage.googleapis.com/v1beta/models", "v1beta", "/models?pageSize=200", "https://generativelanguage.googleapis.com/v1beta/models?pageSize=200"},
		// A root is left as it was.
		{"http://127.0.0.1:11434/v1", "v1", "/chat/completions", "http://127.0.0.1:11434/v1/chat/completions"},
		{"http://127.0.0.1:11434", "v1", "/chat/completions", "http://127.0.0.1:11434/v1/chat/completions"},
	} {
		if got := endpoint(c.base, "https://fallback.example", c.version, c.path); got != c.want {
			t.Errorf("endpoint(%q, %q) = %q, want %q", c.base, c.path, got, c.want)
		}
	}
}
