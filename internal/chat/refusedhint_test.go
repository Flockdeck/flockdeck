package chat

import (
	"net/http"
	"strings"
	"testing"
)

// A refused key is answered with the command that stores another, naming the
// id the chat looks a stored key up under. Started by hand with only a wire,
// that is the built-in that speaks it -- google, for Gemini -- and the line
// said `flockdeck keys set <agent>`, which stores nothing anybody reads.
func TestARefusedKeyNamesTheAgentItsKeyIsStoredFor(t *testing.T) {
	var out strings.Builder
	s := &session{opts: Options{Wire: "gemini", Width: 200}, out: newPrinter(&out, 200, false)}
	s.sayWhyItStopped(&apiError{Code: http.StatusBadRequest, Status: "400 Bad Request", Msg: "API key not valid. Please pass a valid API key."})
	if !strings.Contains(out.String(), "flockdeck keys set google`") {
		t.Errorf("a refused key said:\n%s\nwant it to name google, the agent whose stored key the chat reads", out.String())
	}
}
