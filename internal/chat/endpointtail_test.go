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
		// A version other than the default is still a version: Gemini's stable
		// and preview APIs are given as /v1 and /v1alpha, and had /v1beta put
		// after them.
		{"https://generativelanguage.googleapis.com/v1", "v1beta", "/models/m:streamGenerateContent?alt=sse", "https://generativelanguage.googleapis.com/v1/models/m:streamGenerateContent?alt=sse"},
		{"https://generativelanguage.googleapis.com/v1alpha/", "v1beta", "/models", "https://generativelanguage.googleapis.com/v1alpha/models"},
		{"https://gateway.example/google/v1beta1", "v1beta", "/models", "https://gateway.example/google/v1beta1/models"},
		{"https://generativelanguage.googleapis.com", "v1beta", "/models", "https://generativelanguage.googleapis.com/v1beta/models"},
		// A path that only starts like a version is not one.
		{"https://gateway.example/video", "v1", "/messages", "https://gateway.example/video/v1/messages"},
	} {
		if got := endpoint(c.base, "https://fallback.example", c.version, c.path); got != c.want {
			t.Errorf("endpoint(%q, %q) = %q, want %q", c.base, c.path, got, c.want)
		}
	}
}
