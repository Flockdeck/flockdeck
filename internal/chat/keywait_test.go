package chat

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A chat with a person at the terminal and no key says how to set one and
// waits for it, rather than ending and having to be restarted after it.
func TestAChatWithNoKeyWaitsForOneToBeStored(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("FLOCKDECK_API_KEY", "")
	savedStore, savedPoll := KeyStore, keyPoll
	t.Cleanup(func() { KeyStore, keyPoll = savedStore, savedPoll })
	keyPoll = 5 * time.Millisecond
	asked := 0
	KeyStore = func(string) string {
		if asked++; asked < 3 {
			return ""
		}
		return "sk-stored-meanwhile"
	}

	var out strings.Builder
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := Run(ctx, Options{
		Agent: "anthropic", Wire: "anthropic", Model: "claude-opus-5", Session: "s", Dir: t.TempDir(),
		In: strings.NewReader("/exit\n"), Out: &out, Width: 90, waitForKey: true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, want := range []string{"flockdeck keys set anthropic` in any terminal; this waits for it",
		"(found a key stored with `flockdeck keys set anthropic`; carrying on)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "sk-stored-meanwhile") {
		t.Errorf("the key was shown:\n%s", out.String())
	}

	// A script is told at once, as before.
	KeyStore = func(string) string { return "" }
	err = Run(ctx, Options{Agent: "anthropic", Wire: "anthropic", Session: "s2", Dir: t.TempDir(),
		In: strings.NewReader(""), Out: &out})
	if err == nil || !strings.Contains(err.Error(), "flockdeck keys set anthropic") {
		t.Errorf("a script without a key: %v", err)
	}
}
