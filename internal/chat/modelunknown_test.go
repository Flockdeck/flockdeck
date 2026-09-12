package chat

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// A mistyped model is found out at the next request, in each vendor's own
// words; the failure points at /model, where the models to choose from are,
// rather than at /retry, which would be refused the same way.
func TestAnUnknownModelPointsAtModel(t *testing.T) {
	for _, c := range []struct {
		code int
		msg  string
	}{
		{http.StatusNotFound, "model: claude-opus-6"},
		{http.StatusNotFound, "The model `gpt-5o` does not exist or you do not have access to it."},
		{http.StatusNotFound, "models/gemini-9 is not found for API version v1beta"},
	} {
		if !modelUnknown(&apiError{Code: c.code, Msg: c.msg}) {
			t.Errorf("modelUnknown(%q) = false, want it recognised", c.msg)
		}
	}
	if modelUnknown(&apiError{Code: http.StatusBadRequest, Msg: "model: field required"}) {
		t.Error("a missing model field was taken for an unknown model")
	}

	wire := &scriptedWire{turns: []turnFunc{
		func(context.Context, Request, func(Event)) error {
			return &apiError{Code: http.StatusNotFound, Status: "404 Not Found", Msg: "The model `gpt-5o` does not exist or you do not have access to it."}
		},
	}}
	out := strings.Join(strings.Fields(run(t, Options{Agent: "openai", Model: "gpt-5o", Task: "hi"}, "", wire)), " ")
	if !strings.Contains(out, "no model called gpt-5o; /model shows") || strings.Contains(out, "/retry asks again") {
		t.Errorf("an unknown model did not point at /model:\n%s", out)
	}
}
