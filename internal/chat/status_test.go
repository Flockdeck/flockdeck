package chat

import (
	"context"
	"strings"
	"testing"
)

// When a pane is failing, the two settings behind it are the endpoint it talks
// to and the key it uses. /status names both, and where each is changed --
// without ever showing the key.
func TestStatusSaysWhereTheKeyAndTheEndpointComeFrom(t *testing.T) {
	t.Setenv("MY_TEST_KEY", "sk-secret-value")
	var out strings.Builder
	err := Run(context.Background(), Options{
		Agent: "anthropic", Wire: "anthropic", KeyEnv: []string{"MY_TEST_KEY"}, Model: "claude-sonnet-5",
		Session: "s", Dir: t.TempDir(), In: strings.NewReader("/status\n/exit\n"), Out: &out, Width: 90,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"endpoint https://api.anthropic.com (the vendor's own)", "key from MY_TEST_KEY", "flockdeck keys set anthropic"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("/status lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "sk-secret-value") {
		t.Errorf("/status showed the key:\n%s", out.String())
	}
}
