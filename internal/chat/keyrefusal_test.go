package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A refused key is recognised the way each vendor says it -- Gemini says it with
// a 400 -- and a 403, which is a key accepted but not allowed to do this, is
// not called a refused key.
func TestAKeyRefusalIsToldFromAForbiddenRequest(t *testing.T) {
	for _, c := range []struct {
		code int
		msg  string
		key  bool
	}{
		{http.StatusUnauthorized, "invalid x-api-key", true},
		{http.StatusBadRequest, "API key not valid. Please pass a valid API key.", true},
		{http.StatusBadRequest, "prompt is too long", false},
		{http.StatusForbidden, "your account does not have access to claude-opus-5", false},
	} {
		err := &apiError{Code: c.code, Status: http.StatusText(c.code), Msg: c.msg}
		if got := refusedKey(err); got != c.key {
			t.Errorf("refusedKey(%d %q) = %v, want %v", c.code, c.msg, got, c.key)
		}
	}
}

func TestAForbiddenRequestSaysSoAndDoesNotAskForANewKey(t *testing.T) {
	t.Setenv("MY_TEST_KEY", "sk-test")
	asked := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked++
		http.Error(w, `{"error":{"type":"permission_error","message":"your account does not have access to this model"}}`, http.StatusForbidden)
	}))
	defer srv.Close()

	var out strings.Builder
	Run(context.Background(), Options{
		Agent: "anthropic", Wire: "anthropic", BaseURL: srv.URL, KeyEnv: []string{"MY_TEST_KEY"},
		Model: "claude-opus-5", Session: "s", Dir: t.TempDir(), Task: "hello",
		In: strings.NewReader(""), Out: &out, Width: 200,
	})
	if !strings.Contains(out.String(), "/model switches to another") || strings.Contains(out.String(), "keys set") {
		t.Errorf("a forbidden request was not explained as one:\n%s", out.String())
	}
	if asked != 1 {
		t.Errorf("asked %d times", asked)
	}
}
