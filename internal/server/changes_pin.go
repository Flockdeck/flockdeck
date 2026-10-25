package server

import (
	"errors"
	"os"

	"github.com/jmwri/flockdeck/internal/changes"
)

// Pinning the check file (docs/changes-api.md, section 6). confirmChecks shows
// the window the commands the check file holds and a hash of them; confirmChecks
// with confirm and that hash records it as the one the person looked at. runChecks
// runs only commands whose hash is the confirmed one.
//
// The pin is a tripwire for a file that changed after the person read it. It is
// kept in the config directory beside the file, so it does not stop an agent that
// can write there as well, or that sends confirmChecks itself with a pane token
// and an Origin. That needs a credential of the window's own.

// changesChecksMsg answers confirmChecks without confirm: the commands as they
// would run, their hash, and whether that hash is the confirmed one.
type changesChecksMsg struct {
	Type     string             `json:"type"`
	Req      string             `json:"req,omitempty"`
	Commands []changes.CheckDef `json:"commands"`
	Hash     string             `json:"hash"`
	Pinned   bool               `json:"pinned"`
}

const notConfirmedText = "The commands in the check file have not been confirmed in the Flockdeck window. Open the checks, read the commands and confirm them."

// changesConfirmChecks answers confirmChecks.
func (s *Server) changesConfirmChecks(c *controlClient, cmd command) {
	if cmd.ID == "" {
		changesRefuse(c, cmd, "badRequest", "id is required")
		return
	}
	root, project, ok := s.projectOf(cmd.ID)
	if !ok {
		changesRefuse(c, cmd, "noBranch", "no such pane")
		return
	}
	checksPath, _, err := changesFiles(root, project)
	if err != nil {
		changesRefuse(c, cmd, "notFound", "the config directory cannot be found")
		return
	}
	confirmChecksAt(c, cmd, checksPath)
}

// checksConfirmed says whether the commands just loaded are the ones the person
// confirmed. defs is the list that will run, so a file changed after this look is
// not what runs.
func checksConfirmed(checksPath string, defs []changes.CheckDef) bool {
	pin := changes.LoadPin(changes.PinPath(checksPath))
	return pin != "" && pin == changes.HashChecks(defs)
}

// confirmChecksAt shows or confirms the check file at checksPath.
func confirmChecksAt(c *controlClient, cmd command, checksPath string) {
	defs, err := changes.LoadChecks(checksPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		changesRefuse(c, cmd, "notFound", "there is no check file for this project; write one at "+checksPath)
		return
	case err != nil:
		changesRefuse(c, cmd, "notFound", err.Error())
		return
	}
	hash := changes.HashChecks(defs)
	pinPath := changes.PinPath(checksPath)
	if !cmd.Confirm {
		if defs == nil {
			defs = []changes.CheckDef{}
		}
		c.sendJSON(changesChecksMsg{Type: "changesChecks", Req: cmd.Req, Commands: defs, Hash: hash, Pinned: checksConfirmed(checksPath, defs)})
		return
	}
	// The window confirms the hash it was shown. A file that changed since is not
	// confirmed: the person has to read the new commands.
	if cmd.Hash == "" {
		changesRefuse(c, cmd, "badRequest", "hash is required")
		return
	}
	if cmd.Hash != hash {
		changesRefuse(c, cmd, "changed", "the check file changed after it was shown; read the commands again")
		return
	}
	if len(defs) == 0 {
		changesRefuse(c, cmd, "notFound", "the check file has no checks")
		return
	}
	if err := changes.SavePin(pinPath, hash); err != nil {
		changesRefuse(c, cmd, "failed", "the confirmation could not be saved: "+err.Error())
		return
	}
	changesOK(c, cmd)
}
