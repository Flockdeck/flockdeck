package server

import (
	"strings"
	"testing"
)

// TestClearingAKeyThatIsNotStoredSaysSo covers Clear on a key the store does
// not hold -- cleared from another window a moment ago, or never saved here
// because it comes from flockdeck's environment. "cleared the key" was said
// either way, which for a key from the environment reads as though the agent
// had lost the key it goes on using.
func TestClearingAKeyThatIsNotStoredSaysSo(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)

	sendCmd(t, conn, command{Cmd: "keyClear", ID: "stand-in"})
	var note noticeMsg
	readUntil(t, conn, "notice", &note)
	if note.Error || strings.HasPrefix(note.Text, "Cleared") || !strings.Contains(note.Text, "no stored key") {
		t.Errorf("clearing a key that was never stored was answered %+v; want it to say there was none", note)
	}

}
