package server

import (
	"errors"
	"fmt"

	"github.com/jmwri/flockdeck/internal/appwindow"
	"github.com/jmwri/flockdeck/internal/record"
	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// openFolder shows a folder in the desktop's file manager. A variable so a
// test does not open a window on the developer's desktop.
var openFolder = appwindow.OpenDefault

// revealFile shows a file selected in the desktop's file manager. A variable
// for the same reason as openFolder.
var revealFile = appwindow.RevealFile

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
		c.notify("Recording this pane, from the start of its conversation. Transcripts are saved on this machine", false)
	}
}

// exportConfirmNotice answers an exportTranscript command the window did not
// confirm: the window asks before sending one, so this is only a client that
// did not.
const exportConfirmNotice = "Exporting writes the pane's whole stored conversation to a file, including anything secret in it. Export from the window to read what it keeps first"

// exportTranscript writes the conversation a pane's agent stored as a
// transcript file, whether or not the pane is being recorded. Unlike turning
// recording on it is put to the user every time, and arrives confirmed.
func (s *Server) exportTranscript(c *controlClient, cmd command) {
	// The file is written on the machine flockdeck runs on, and what is said of
	// it names a path there: neither is for a window reached through the relay.
	if c.remote {
		c.notify("A transcript is exported on the machine flockdeck runs on, from its own window, not from one reached through the relay", true)
		return
	}
	if !cmd.Confirmed {
		c.notify(exportConfirmNotice, true)
		return
	}
	type result struct {
		res record.ExportResult
		err error
	}
	r, ok := ask(s, func() result {
		res, err := s.ws.ExportTranscript(paneIDFor(s.ws, cmd.ID), "")
		return result{res, err}
	})
	if !ok {
		return
	}
	if r.err != nil {
		c.notify("Could not export the transcript: "+r.err.Error(), true)
		return
	}
	if r.res.Kept {
		c.notify(fmt.Sprintf("Nothing new was written: the earlier export at %s has events this one would lack (the stored conversation was cut or changed), so it was kept as it was. Delete it to have a fresh one", r.res.Path), true)
		return
	}
	msg := fmt.Sprintf("Exported %d lines to %s. It may contain secrets. Reveal transcript shows it in your file manager", r.res.Lines, r.res.Path)
	if r.res.Full {
		msg += ", and it was cut at its size cap"
	}
	if r.res.Skipped > 0 {
		msg += fmt.Sprintf("; %d entries could not be read and are left out", r.res.Skipped)
	}
	c.notify(msg, false)
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

// revealTranscript shows a pane's transcript file in the machine's file manager,
// with the file selected. The file is found from Flockdeck's own records for the
// pane -- the client says only which pane -- and has been checked to be a regular
// file inside the recordings folder, so nothing a client sends can name a path.
func (s *Server) revealTranscript(c *controlClient, cmd command) {
	if c.remote {
		c.notify("A transcript is shown on the machine flockdeck runs on, not from a window reached through the relay", true)
		return
	}
	type result struct {
		path string
		err  error
	}
	r, ok := ask(s, func() result {
		path, err := s.ws.TranscriptFile(paneIDFor(s.ws, cmd.ID))
		return result{path, err}
	})
	if !ok {
		return
	}
	switch {
	case errors.Is(r.err, workspace.ErrNoTranscriptFile):
		c.notify("This pane has no transcript file yet: it is not being recorded and has not been exported. Export transcript or Start recording makes one, and Open recordings folder shows where they go", true)
		return
	case r.err != nil:
		c.notify("Could not find the transcript: "+r.err.Error(), true)
		return
	}
	if err := revealFile(r.path); err != nil {
		c.notify("Could not show the transcript in the file manager: "+err.Error(), true)
	}
}
