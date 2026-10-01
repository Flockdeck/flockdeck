package server

import (
	"errors"

	"github.com/jmwri/flockdeck/internal/appwindow"
	"github.com/jmwri/flockdeck/internal/store"
)

// openFolder shows a folder in the desktop's file manager. A variable so a
// test does not open a window on the developer's desktop.
var openFolder = appwindow.OpenDefault

// recordingConfirmNotice answers a recordPane command that would be the first
// and was not confirmed: the window asks before sending one, so this is only
// a client that did not.
const recordingConfirmNotice = "Recording stores what the agent and you say, including anything secret in it. Turn it on from the window to read what it keeps first"

// recordSpawnRefusal is why `flockdeck spawn -record` is refused, or nil. An
// agent is not the one to switch recording on for the first time: the user
// does that in the window, where it says what is kept, and from then on the
// flag is theirs to have agents use.
func recordSpawnRefusal(shell, acknowledged bool) error {
	switch {
	case shell:
		return errors.New("-record needs an agent: a shell reports no events to record")
	case !acknowledged:
		return errors.New("recording has not been turned on in the window yet; turn it on for any pane from its header or the command palette, which says what is stored, and -record works from then on")
	}
	return nil
}

// recordPane starts or stops a pane's recording. The first start of all is put
// to the user in the window and arrives confirmed; it is checked here as well,
// so a client that skips the question cannot skip the answer.
func (s *Server) recordPane(c *controlClient, cmd command) {
	if cmd.Recording && !store.LoadPrefs().RecordingAcknowledged {
		if !cmd.Confirmed {
			c.notify(recordingConfirmNotice, true)
			return
		}
		s.updatePrefs(c, func(p *store.Prefs) bool { return setPref(&p.RecordingAcknowledged, true) })
	}
	type result struct {
		found bool
		err   error
	}
	r, ok := ask(s, func() result {
		found, err := s.ws.SetPaneRecording(paneIDFor(s.ws, cmd.ID), cmd.Recording)
		return result{found, err}
	})
	if !ok {
		return
	}
	switch {
	case r.err != nil:
		c.notify("Could not start recording: "+r.err.Error(), true)
	case !r.found:
		c.notify("Only an agent pane can be recorded, and that pane is not one or is no longer open", true)
	case cmd.Recording:
		c.notify("Recording this pane. Transcripts are saved on this machine", false)
	}
}

// openRecordings shows the recordings folder in the machine's file manager.
func (s *Server) openRecordings(c *controlClient) {
	if c.remote {
		c.notify("The recordings folder opens on the machine flockdeck runs on, not from a window reached through the relay", true)
		return
	}
	dir, err := s.ws.RecordingsDir()
	if err == nil {
		err = openFolder(dir)
	}
	if err != nil {
		c.notify("Could not open the recordings folder: "+err.Error(), true)
	}
}
