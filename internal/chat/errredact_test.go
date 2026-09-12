package chat

import (
	"context"
	"strings"
	"testing"
)

// failingLister is an endpoint whose model listing is refused.
type failingLister struct {
	scriptedWire
	err error
}

func (w *failingLister) ListModels(context.Context) ([]string, error) { return nil, w.err }

// Any part of a key a vendor quotes back is taken out wherever its failure is
// shown -- a refused listing and a failed answer, as well as a refused key.
func TestNoFailureShowsPartOfAKey(t *testing.T) {
	quoting := &apiError{Code: 401, Status: "401 Unauthorized", Msg: "Invalid API key: sk-or-v1-abcdef123456"}
	if got := quoting.Error(); strings.Contains(got, "abcdef") || !strings.Contains(got, "[a key]") {
		t.Errorf("Error() = %q", got)
	}

	s, out := newTestSession(t, "", &failingLister{err: quoting})
	s.listModels(context.Background())
	if strings.Contains(out.String(), "abcdef") {
		t.Errorf("a refused listing showed part of the key:\n%s", out.String())
	}

	failed := func(context.Context, Request, func(Event)) error {
		return &apiError{Code: 400, Status: "400 Bad Request", Msg: "bad request for key sk-ant-api03-xyz789secret"}
	}
	answer := run(t, Options{Agent: "anthropic", Model: "claude-opus-5"}, "hello\n/exit\n", &scriptedWire{turns: []turnFunc{failed}})
	if strings.Contains(answer, "xyz789") {
		t.Errorf("a failed answer showed part of the key:\n%s", answer)
	}
}
