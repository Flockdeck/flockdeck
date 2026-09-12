package chat

import (
	"context"
	"strings"
	"testing"
)

// A stored key is said to be stored with the command that changes it, once.
func TestStatusNamesTheKeysCommandOnceForAStoredKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("FLOCKDECK_API_KEY", "")
	saved := KeyStore
	t.Cleanup(func() { KeyStore = saved })
	KeyStore = func(string) string { return "sk-stored-secret" }

	var out strings.Builder
	err := Run(context.Background(), Options{
		Agent: "anthropic", Wire: "anthropic", Model: "claude-sonnet-5", Session: "s", Dir: t.TempDir(),
		In: strings.NewReader("/status\n/exit\n"), Out: &out, Width: 120,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "key stored with `flockdeck keys set anthropic`; running it again changes it"
	if !strings.Contains(out.String(), want) {
		t.Errorf("/status lacks %q:\n%s", want, out.String())
	}
	if strings.Contains(out.String(), "sk-stored-secret") {
		t.Errorf("/status showed the key:\n%s", out.String())
	}
}
