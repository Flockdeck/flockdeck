package server

import (
	"errors"
	"fmt"
	"sync"

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
	// The file is written on the machine Flockdeck runs on, and what is said of
	// it names a path there: neither is for a window reached through the relay.
	if c.remote {
		c.notify("A transcript is exported on the machine Flockdeck runs on, from its own window, not from one reached through the relay", true)
		return
	}
	if !cmd.Confirmed {
		c.notify(exportConfirmNotice, true)
		return
	}
	// Only the quick part runs on the workspace goroutine: finding the pane's
	// conversation. Reading it and writing the file, which takes about a second
	// for a large one and longer where another program holds the old export open,
	// is done on a goroutine of its own, so the window, the others and the
	// workspace carry on meanwhile.
	type prepared struct {
		job *workspace.ExportJob
		err error
	}
	p, ok := ask(s, func() prepared {
		job, err := exportPrepare(s.ws, paneIDFor(s.ws, cmd.ID))
		return prepared{job, err}
	})
	if !ok {
		return
	}
	if p.err != nil {
		c.notify("Could not export the transcript: "+p.err.Error(), true)
		return
	}
	// One export of a conversation at a time. A window that asks while another
	// export of it is running waits for that one and gets its answer.
	if first, ok := s.joinExport(p.job.Conversation, c); ok && first {
		go s.runExport(p.job, c)
	}
}

// exportPrepare and exportRun are what an export does on the workspace goroutine
// and off it. Variables so that a test can make the slow part wait.
var (
	exportPrepare = func(ws *workspace.Workspace, id string) (*workspace.ExportJob, error) { return ws.PrepareExport(id) }
	exportRun     = func(j *workspace.ExportJob, cancel <-chan struct{}) (record.ExportResult, error) {
		return j.Run("", cancel)
	}
)

// exports is the exports of conversations under way, and the windows waiting on
// each: the first to ask runs it.
type exports struct {
	mu      sync.Mutex
	waiting map[string][]*controlClient
}

// joinExport adds c to the windows waiting for the export of a conversation, and
// reports whether c is the first, which runs it. ok is false once the server is
// closing, when nothing is started: the goroutine is counted in connWG under the
// same lock Close takes, so Close never waits on one it did not see.
func (s *Server) joinExport(conv string, c *controlClient) (first, ok bool) {
	s.mu.Lock()
	select {
	case <-s.closed:
		s.mu.Unlock()
		return false, false
	default:
	}
	s.exports.mu.Lock()
	waiters, running := s.exports.waiting[conv]
	if s.exports.waiting == nil {
		s.exports.waiting = map[string][]*controlClient{}
	}
	s.exports.waiting[conv] = append(waiters, c)
	s.exports.mu.Unlock()
	if !running {
		s.connWG.Add(1)
	}
	s.mu.Unlock()
	return !running, true
}

// runExport runs an export on its own goroutine and tells every window that was
// waiting for it. A pane closed or changed meanwhile does not matter: the job
// holds the conversation's id, and the export is of that conversation. If the
// server closes first the export stops between events, removes what it made, and
// says nothing, since there is nobody to say it to.
func (s *Server) runExport(job *workspace.ExportJob, c *controlClient) {
	defer s.connWG.Done()
	var res record.ExportResult
	var err error
	done := false
	defer func() {
		s.exports.mu.Lock()
		waiters := s.exports.waiting[job.Conversation]
		delete(s.exports.waiting, job.Conversation)
		s.exports.mu.Unlock()
		if !done {
			err = errors.New("something went wrong, and nothing was exported")
		}
		select {
		case <-s.closed:
			return
		default:
		}
		for _, w := range waiters {
			exportNotice(w, res, err)
		}
	}()
	defer s.surviveFor(c, "exporting a transcript")
	res, err = exportRun(job, s.closed)
	done = true
}

// exportNotice tells a window how an export went.
func exportNotice(c *controlClient, res record.ExportResult, err error) {
	if err != nil {
		c.notify("Could not export the transcript: "+err.Error(), true)
		return
	}
	if res.Kept {
		c.notify(fmt.Sprintf("Not exported again: the earlier export at %s has events this one would lack (the stored conversation was cut or changed since), so it was kept as it was and is not up to date. Delete it to have a fresh one", res.Path), true)
		return
	}
	msg := fmt.Sprintf("Exported %d lines to %s", res.Lines, res.Path)
	if res.Replaced {
		msg = fmt.Sprintf("Exported %d lines to %s, replacing the earlier export of this conversation, which had nothing this one lacks", res.Lines, res.Path)
	}
	if res.Full {
		msg += fmt.Sprintf(", cut at the %d MiB size cap", record.MaxFileBytes>>20)
	}
	switch {
	case res.Skipped == 1:
		msg += "; 1 entry of the stored conversation could not be read and is left out"
	case res.Skipped > 1:
		msg += fmt.Sprintf("; %d entries of the stored conversation could not be read and are left out", res.Skipped)
	}
	msg += ". It may contain secrets. Reveal transcript shows it in your file manager"
	c.notify(msg, false)
}

// openRecordings shows the recordings folder in the machine's file manager.
func (s *Server) openRecordings(c *controlClient) {
	if c.remote {
		c.notify("The recordings folder opens on the machine Flockdeck runs on, not from a window reached through the relay", true)
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
		c.notify("A transcript is shown on the machine Flockdeck runs on, not from a window reached through the relay", true)
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
