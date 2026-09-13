package server

import "testing"

// TestActingOnAGoneTabSaysSo covers a tab bar or a rename dialog left showing
// a tab that another window has since closed. Renaming it or switching to it
// did nothing and said nothing -- the typed name simply vanished -- where the
// same click on a pane that had gone is answered that it has.
func TestActingOnAGoneTabSaysSo(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	for _, cmd := range []command{
		{Cmd: "renameTab", ID: "a-tab-that-has-gone", Text: "a new name"},
		{Cmd: "selectTab", ID: "a-tab-that-has-gone"},
	} {
		sendCmd(t, conn, cmd)
		var note noticeMsg
		readUntil(t, conn, "notice", &note)
		if !note.Error || note.Text != tabGone {
			t.Errorf("%s on a tab that has gone was answered %+v, want %q", cmd.Cmd, note, tabGone)
		}
	}
}
