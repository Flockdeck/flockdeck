package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTheMissingKeyErrorNamesEachPlaceOnce(t *testing.T) {
	for _, name := range []string{"ANTHROPIC_API_KEY", "FLOCKDECK_API_KEY"} {
		t.Setenv(name, "")
	}
	_, err := resolveKey(Options{Agent: "anthropic", Wire: "anthropic", KeyEnv: []string{"ANTHROPIC_API_KEY"}})
	if err == nil {
		t.Fatal("no error without a key")
	}
	if n := strings.Count(err.Error(), "ANTHROPIC_API_KEY"); n != 1 {
		t.Errorf("the error names ANTHROPIC_API_KEY %d times: %v", n, err)
	}
}

func TestAnthropicWithNoModelSaysHowToNameOne(t *testing.T) {
	asked := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = true
		http.Error(w, `{"error":{"message":"model: Field required"}}`, http.StatusBadRequest)
	}))
	defer srv.Close()

	err := (&anthropicWire{base: srv.URL}).Stream(context.Background(), Request{}, func(Event) {})
	if err == nil || !strings.Contains(err.Error(), "/model") {
		t.Errorf("error = %v, want one saying how to name a model", err)
	}
	if asked {
		t.Error("a request the API can only refuse was sent anyway")
	}
}
