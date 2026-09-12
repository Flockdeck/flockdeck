package chat

import (
	"context"
	"strings"
	"testing"
)

// A vendor's refusal is shown for what it says, without the part of the key it
// quotes back.
func TestARefusedKeyIsNotQuotedBack(t *testing.T) {
	refused := func(context.Context, Request, func(Event)) error {
		return &apiError{Code: 401, Status: "401 Unauthorized",
			Msg: "Incorrect API key provided: sk-proj-abc1****wxyz. You can find your API key at https://platform.openai.com/account/api-keys."}
	}
	out := run(t, Options{Agent: "openai", Model: "gpt-5"}, "hello\n/exit\n", &scriptedWire{turns: []turnFunc{refused}})
	if strings.Contains(out, "abc1") || strings.Contains(out, "wxyz") {
		t.Errorf("part of the key was shown:\n%s", out)
	}
	said := strings.Join(strings.Fields(out), " ")
	if !strings.Contains(said, "the API refused the key: 401 Unauthorized: Incorrect API key provided: [a key]") ||
		!strings.Contains(said, "platform.openai.com") {
		t.Errorf("the refusal was not shown for what it says:\n%s", out)
	}

	for _, msg := range []string{"invalid x-api-key", "API key not valid. Please pass a valid API key."} {
		if got := redactKeys(msg); got != msg {
			t.Errorf("redactKeys changed a message with no key in it: %q", got)
		}
	}
	if got := redactKeys("key AIzaSyA1b2C3d4E5f6G7h8 refused"); strings.Contains(got, "AIza") {
		t.Errorf("a Gemini key was left in: %q", got)
	}
}
