package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// A refused key is fixed with `flockdeck keys set` in another terminal. The
// pane that was refused should pick the new key up and carry on, rather than
// needing a restart and the prompt typed again.
func TestARefusedKeyIsLookedForAgainAndTheTurnCarriesOn(t *testing.T) {
	t.Setenv("MY_TEST_KEY", "sk-old")
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("x-api-key")
		seen = append(seen, key)
		if key == "sk-old" {
			// The user sets a new key while this one is being refused.
			os.Setenv("MY_TEST_KEY", "sk-new")
			http.Error(w, `{"error":{"message":"invalid x-api-key"}}`, http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"carried on"}}` +
			"\n\n" + `data: {"type":"message_stop"}` + "\n\n"))
	}))
	defer srv.Close()

	var out strings.Builder
	err := Run(context.Background(), Options{
		Agent: "anthropic", Wire: "anthropic", BaseURL: srv.URL, KeyEnv: []string{"MY_TEST_KEY"},
		Model: "claude-sonnet-5", Session: "s", Dir: t.TempDir(), Task: "hello",
		In: strings.NewReader(""), Out: &out, Width: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(seen, ",") != "sk-old,sk-new" {
		t.Errorf("keys sent = %q, want the old one and then the new one", seen)
	}
	if !strings.Contains(out.String(), "carried on") {
		t.Errorf("the turn did not carry on with the new key:\n%s", out.String())
	}
	if strings.Contains(out.String(), "sk-") {
		t.Errorf("a key was drawn on the screen:\n%s", out.String())
	}
}

func TestARefusedKeyWithNoOtherSaysHowToSetOne(t *testing.T) {
	t.Setenv("MY_TEST_KEY", "sk-bad")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"invalid x-api-key"}}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	var out strings.Builder
	Run(context.Background(), Options{
		Agent: "anthropic", Wire: "anthropic", BaseURL: srv.URL, KeyEnv: []string{"MY_TEST_KEY"},
		Model: "claude-sonnet-5", Session: "s", Dir: t.TempDir(), Task: "hello",
		In: strings.NewReader(""), Out: &out, Width: 60,
	})
	if !strings.Contains(out.String(), "flockdeck keys set anthropic") {
		t.Errorf("a refused key did not say how to set another:\n%s", out.String())
	}
}
