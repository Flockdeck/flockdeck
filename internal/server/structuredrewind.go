package server

import (
	"errors"
	"strings"
	"sync"

	"github.com/jmwri/flockdeck/internal/filerewind"
	"github.com/jmwri/flockdeck/internal/structured"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// Going back to an earlier prompt of a structured pane, and forking the
// conversation at one (API 1.25). Both name the prompt by the id of its entry in
// the conversation, taken from the push's rewindPoints; a place in the list is
// never used, since the list changes as turns end. Like the other structured
// commands they are for a window on this machine and refused to a viewer.
//
// Both also carry rewindToken, the one the push they were chosen from had. It
// names the pane's runtime and how many times it has gone back, so a window that
// is out of date (the pane was restarted, or went back from another window) is
// refused instead of acting on entry ids that now mean something else.
//
// structuredRewind removes the prompt and everything after it from the
// conversation and from the agent's own history, so it asks for confirm: true
// and refuses without it. It answers with the prompt's text, for the window to
// put back in the prompt box; cut is true when the text was longer than is sent
// back. structuredFork starts a new pane beside the original with the
// conversation up to that point, or all of it when entry is empty, and answers
// with the new pane's id. The original is not changed. Neither passes on
// anything the agent said about a failure.
//
// Files (API 1.34, cap structuredRewindFiles). When the pane's folder is in a git
// repository, Flockdeck saves the files before each prompt the person types
// (internal/filerewind), and the push lists the prompts that have them in
// rewindFiles. structuredRewindFiles answers with what going back to before a
// prompt would change: each file and whether it is overwritten with its earlier
// content, written back (deleted since) or removed (created since). A
// structuredRewind that carries restoreFiles and filePaths, the list the person
// confirmed, puts exactly those files back after the conversation goes back. If
// the files differ from the list at that moment nothing at all happens and the
// answer is refused with the list as it is now. The files before the restore are
// saved under a git ref the answer names. Ignored files, submodules and the
// checkout's index are not touched.

const (
	structuredNeedConfirm   = "Confirm to go back: the prompt and everything after it, including messages other agents sent after it, are removed from the conversation and from the agent"
	structuredNoRewind      = "This agent cannot go back to an earlier point"
	structuredRewindBusy    = "Stop the turn and clear the waiting prompts first"
	structuredNoPoint       = "There is no earlier point before that prompt"
	structuredRewindFailed  = "The agent could not go back"
	structuredNoFork        = "This agent cannot fork a conversation"
	structuredForkFailed    = "The conversation could not be forked"
	structuredRewindEntry   = "Name the prompt to go back to"
	structuredRewindStale   = "The conversation changed since this window last showed it. Look at the list again and choose once more"
	structuredRewindUnknown = "The agent did not answer in time, so Flockdeck restarted the pane. The conversation so far is kept above, and the agent starts a new session"
	structuredTooManyForks  = "This pane already has 4 forks open. Close one first"
	structuredRewindPointsN = 200

	structuredNoFiles      = "No saved files exist for that prompt, so only the conversation can go back"
	structuredFilesChanged = "The files changed since the list was shown. Look at the list again and confirm it"
	structuredFilesTooMany = "More than 300 files differ, which is too many to list and confirm. Go back without the files, or put them back with git"
	structuredFilesFailed  = "Flockdeck could not compare the files with the saved ones"
)

// rewindFileView is one file of a restore. Path is relative to the repository
// root; Action is overwrite, recreate or remove.
type rewindFileView struct {
	Path   string `json:"path"`
	Action string `json:"action"`
}

// rewindRestoreView says what a restore of files did. Error is set when the
// conversation went back but the files were not all put back.
type rewindRestoreView struct {
	Written int      `json:"written"`
	Removed int      `json:"removed"`
	Undo    string   `json:"undo,omitempty"`
	Failed  []string `json:"failed,omitempty"`
	Error   string   `json:"error,omitempty"`
}

func fileViews(cs []filerewind.Change) []rewindFileView {
	out := make([]rewindFileView, 0, len(cs))
	for _, c := range cs {
		p, _ := clipStructured(c.Path, 400)
		out = append(out, rewindFileView{Path: p, Action: string(c.Action)})
	}
	return out
}

// filesRefusal is the fixed text for an error about the files.
func filesRefusal(err error) (string, bool) {
	switch {
	case errors.Is(err, structured.ErrNoFiles):
		return structuredNoFiles, true
	case errors.Is(err, structured.ErrFilesChanged):
		return structuredFilesChanged, true
	case errors.Is(err, filerewind.ErrTooMany):
		return structuredFilesTooMany, true
	case errors.Is(err, filerewind.ErrNotRepo):
		return structured.FilesWhyNotRepo, true
	case errors.Is(err, filerewind.ErrNoGit):
		return structured.FilesWhyNoGit, true
	case errors.Is(err, filerewind.ErrMidOperation):
		return structured.FilesWhyMid, true
	}
	return "", false
}

// buildStructuredRewind adds the rewind points and the token the push carries.
func buildStructuredRewind(m *structuredMsg, sn structured.Snapshot) {
	pts := sn.RewindPoints
	if len(pts) > structuredRewindPointsN {
		pts = pts[len(pts)-structuredRewindPointsN:]
	}
	m.RewindPoints = append([]string(nil), pts...)
	m.RewindToken = sn.RewindToken
	m.RewindFilesWhy = sn.FilesWhy
	if len(sn.FilePoints) > 0 {
		have := make(map[string]bool, len(m.RewindPoints))
		for _, id := range m.RewindPoints {
			have[id] = true
		}
		for _, id := range sn.FilePoints {
			if have[id] {
				m.RewindFiles = append(m.RewindFiles, id)
			}
		}
	}
}

// forking holds the panes with a fork or a rewind under way, so that one click
// sent twice does not make two panes.
var forking sync.Map

// structuredRewind runs structuredRewind and structuredFork. It must run on the
// workspace goroutine; the agent is asked on a goroutine of its own.
func (s *Server) structuredRewind(c *controlClient, cmd command) {
	rt := s.ws.StructuredRuntime(cmd.ID)
	if rt == nil {
		refuse(c, cmd, errStructured(structuredNotPane))
		return
	}
	fork := cmd.Cmd == "structuredFork"
	if cmd.Cmd == "structuredRewindFiles" {
		if cmd.Entry == "" {
			refuse(c, cmd, errStructured(structuredRewindEntry))
			return
		}
		go s.doRewindFiles(c, cmd, rt)
		return
	}
	if !fork {
		if cmd.Entry == "" {
			refuse(c, cmd, errStructured(structuredRewindEntry))
			return
		}
		if !cmd.Confirm {
			refuse(c, cmd, errStructured(structuredNeedConfirm))
			return
		}
	}
	if _, busy := forking.LoadOrStore(cmd.ID, struct{}{}); busy {
		refuse(c, cmd, errStructured(structuredRewindBusy))
		return
	}
	go func() {
		defer forking.Delete(cmd.ID)
		if fork {
			s.doFork(c, cmd, rt)
			return
		}
		var text string
		var out structured.FilesOutcome
		var err error
		if cmd.RestoreFiles {
			text, out, err = rt.RewindFiles(cmd.Entry, cmd.RewindToken, cmd.FilePaths)
		} else {
			text, err = rt.Rewind(cmd.Entry, cmd.RewindToken)
		}
		if errors.Is(err, structured.ErrRewindUnknown) {
			// Whether the agent went back is not known, so the window and the
			// agent could disagree about what the conversation is. The pane
			// starts over, reopening the conversation as it was.
			// The agent's conversation from before is still whole, so the pane
			// reopens it. The layout may already hold the new id the agent moved to;
			// the old one wins.
			from := rt.RewindFrom()
			ask(s, func() bool {
				if s.ws.RestartStructuredPaneResuming(cmd.ID, from) {
					return true
				}
				return s.ws.RestartPaneByID(cmd.ID)
			})
		}
		if err != nil {
			refuseWithFiles(c, cmd, err)
			return
		}
		cut, wasCut := clipStructured(text, structuredRewindTextCut)
		msg := teamResultMsg{Type: "teamResult", Req: cmd.Req, Op: cmd.Cmd, OK: true, Text: cut, Cut: wasCut}
		if cmd.RestoreFiles {
			v := &rewindRestoreView{Written: out.Result.Written, Removed: out.Result.Removed, Undo: out.Result.Undo}
			for _, f := range out.Result.Failed {
				f, _ = clipStructured(f, 400)
				v.Failed = append(v.Failed, f)
			}
			if out.Err != nil {
				v.Error = filesFailureText(out.Err)
			}
			msg.Restore = v
		}
		c.sendJSON(msg)
	}()
}

// doRewindFiles answers structuredRewindFiles: what going back would change.
func (s *Server) doRewindFiles(c *controlClient, cmd command, rt *structured.Runtime) {
	plan, err := rt.FilePlan(cmd.Entry, cmd.RewindToken)
	if err != nil {
		refuseWithFiles(c, cmd, err)
		return
	}
	c.sendJSON(teamResultMsg{Type: "teamResult", Req: cmd.Req, Op: cmd.Cmd, OK: true, Files: fileViews(plan)})
}

// refuseWithFiles refuses a command for err. A list that went out of date is
// refused with the list as it is now, so the window can show it again.
func refuseWithFiles(c *controlClient, cmd command, err error) {
	var fe *structured.FilesError
	if errors.As(err, &fe) {
		c.sendJSON(teamResultMsg{Type: "teamResult", Req: cmd.Req, Op: cmd.Cmd, Error: structuredFilesChanged, Files: fileViews(fe.Now)})
		return
	}
	if text, ok := filesRefusal(err); ok {
		refuse(c, cmd, errStructured(text))
		return
	}
	text := rewindRefusal(err, false)
	if text == structuredRewindFailed && !errors.Is(err, structured.ErrRewindFailed) {
		// Not an answer of the agent: the comparison of the files failed.
		text = structuredFilesFailed
	}
	refuse(c, cmd, errStructured(text))
}

// filesFailureText is why the files were not put back after the conversation
// went back.
func filesFailureText(err error) string {
	var fe *structured.FilesError
	if errors.As(err, &fe) {
		return "The files changed after you confirmed the list, so none were put back. The conversation did go back."
	}
	if text, ok := filesRefusal(err); ok {
		return strings.TrimRight(text, ".") + ". The conversation did go back."
	}
	return "The files could not be put back. The conversation did go back."
}

// structuredRewindTextCut is the most of a prompt's text that comes back. A
// longer prompt is cut; the person's own box then holds the start of it.
const structuredRewindTextCut = 16 << 10

func (s *Server) doFork(c *controlClient, cmd command, rt *structured.Runtime) {
	from, err := rt.ForkSource(cmd.Entry, cmd.RewindToken)
	if err != nil {
		refuse(c, cmd, errStructured(rewindRefusal(err, true)))
		return
	}
	type out struct {
		id  string
		err error
	}
	r, ok := ask(s, func() out {
		id, err := s.ws.SpawnFork(cmd.ID, from)
		return out{id, err}
	})
	if !ok {
		return
	}
	if errors.Is(r.err, workspace.ErrTooManyForks) {
		refuse(c, cmd, errStructured(structuredTooManyForks))
		return
	}
	if r.err != nil {
		refuse(c, cmd, errStructured(structuredForkFailed))
		return
	}
	c.sendJSON(teamResultMsg{Type: "teamResult", Req: cmd.Req, Op: cmd.Cmd, OK: true, Pane: r.id})
}

// rewindRefusal is the fixed text for an error of the runtime.
func rewindRefusal(err error, fork bool) string {
	switch {
	case errors.Is(err, structured.ErrNotRunning):
		return structuredSetNotReady
	case errors.Is(err, structured.ErrRewindNotOffered):
		return structuredNoRewind
	case errors.Is(err, structured.ErrForkNotOffered):
		return structuredNoFork
	case errors.Is(err, structured.ErrRewindStale):
		return structuredRewindStale
	case errors.Is(err, structured.ErrRewindUnknown):
		return structuredRewindUnknown
	case errors.Is(err, structured.ErrRewindBusy):
		return structuredRewindBusy
	case errors.Is(err, structured.ErrNoPoint):
		return structuredNoPoint
	}
	if fork {
		return structuredForkFailed
	}
	return structuredRewindFailed
}
