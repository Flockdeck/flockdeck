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
	srv, ws := newTestServer(t)
	spec, ok := ws.Catalog().Find("anthropic")
	if !ok || len(spec.API.KeyEnv) == 0 {
		t.Skip("no built-in anthropic agent with a key variable")
	}
	for _, v := range spec.API.KeyEnv {
		t.Setenv(v, "")
	}
	conn := dialControl(t, srv)

	sendCmd(t, conn, command{Cmd: "keyClear", ID: spec.ID})
	var note noticeMsg
	readUntil(t, conn, "notice", &note)
	if note.Error || strings.HasPrefix(note.Text, "cleared") || !strings.Contains(note.Text, "no stored key") {
		t.Errorf("clearing a key that was never stored was answered %+v; want it to say there was none", note)
	}

	exported := spec.API.KeyEnv[0]
	t.Setenv(exported, "sk-from-the-environment")
	sendCmd(t, conn, command{Cmd: "keyClear", ID: spec.ID})
	readUntil(t, conn, "notice", &note)
	if note.Error || strings.HasPrefix(note.Text, "cleared") || !strings.Contains(note.Text, exported) {
		t.Errorf("clearing a key that comes from the environment was answered %+v; want it to name %s, which is still used", note, exported)
	}
}
