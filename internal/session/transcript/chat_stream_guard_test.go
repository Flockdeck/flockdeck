package transcript

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
)

// A chat pane's session id names its transcript file inside the chats folder,
// and it comes from whatever the pane's own process last reported. One that
// is not a single plain name gets no stream at all, so a misbehaving process
// cannot point the phone's chat view at a file outside that folder.
func TestChatStreamRefusesASessionIDThatIsNotAPlainName(t *testing.T) {
	for _, id := range []string{"", ".", "..", "../x", "a/b", "../../chats/other"} {
		if _, ok := (Chat{}).Stream(agent.Spec{}, id); ok {
			t.Errorf("a stream was opened for session id %q", id)
		}
	}
	for _, id := range []string{"abc-123", "5f0c2d7e-8a1b-4c3d-9e2f-0a1b2c3d4e5f"} {
		if _, ok := (Chat{}).Stream(agent.Spec{}, id); !ok {
			t.Errorf("no stream was opened for the plain session id %q", id)
		}
	}
}
