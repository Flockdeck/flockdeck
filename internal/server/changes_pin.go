package server

import (
	"errors"
	"os"
	"sync"

	"github.com/jmwri/flockdeck/internal/changes"
)

// Confirming the check file (docs/changes-api.md, section 6). confirmChecks shows
// the window the commands the check file holds and a hash of them; confirmChecks
// with confirm and that hash records it as the one the person looked at. runChecks
// runs only commands whose hash is the confirmed one.
//
// The confirmed hash is kept in memory, per check file, and is forgotten when
// Flockdeck restarts. A file beside the check file would let an agent that can
// plant the check file compute the hash and plant the pin too. What is left open
// is an agent that sends confirmChecks itself with the instance token and an
// Origin; the gate is a process control (docs/changes-api.md, section 6).

// checkPins is the hash of the commands the person confirmed, by check file.
type checkPins struct {
	mu     sync.Mutex
	hashes map[string]string
}

func (p *checkPins) get(path string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.hashes[path]
}

func (p *checkPins) set(path, hash string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.hashes == nil {
		p.hashes = map[string]string{}
	}
	p.hashes[path] = hash
}

// changesChecksMsg answers confirmChecks without confirm: the commands as they
// would run, their hash, and whether that hash is the confirmed one.
type changesChecksMsg struct {
	Type     string             `json:"type"`
	Req      string             `json:"req,omitempty"`
	Commands []changes.CheckDef `json:"commands"`
	Hash     string             `json:"hash"`
	Pinned   bool               `json:"pinned"`
}

const notConfirmedText = "The commands in the check file have not been confirmed in the Flockdeck window since it started. Open the checks, read the commands and confirm them. A command runs whatever the checkout holds, such as its scripts and tests, with your rights."

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
	confirmChecksAt(c, cmd, checksPath, &s.pins)
}

// checksConfirmed says whether the commands just loaded are the ones the person
// confirmed. defs is the list that will run, so a file changed after this look is
// not what runs.
func checksConfirmed(pins *checkPins, checksPath string, defs []changes.CheckDef) bool {
	pin := pins.get(checksPath)
	return pin != "" && pin == changes.HashChecks(defs)
}

// refuseCheckFile answers a check file that cannot be used.
func refuseCheckFile(c *controlClient, cmd command, checksPath string, err error) {
	if errors.Is(err, os.ErrNotExist) {
		changesRefuse(c, cmd, "notFound", "there is no check file for this project; write one at "+checksPath)
		return
	}
	changesRefuse(c, cmd, "badRequest", err.Error())
}

// confirmChecksAt shows or confirms the check file at checksPath.
func confirmChecksAt(c *controlClient, cmd command, checksPath string, pins *checkPins) {
	defs, err := changes.LoadChecks(checksPath)
	if err != nil {
		refuseCheckFile(c, cmd, checksPath, err)
		return
	}
	hash := changes.HashChecks(defs)
	if !cmd.Confirm {
		if defs == nil {
			defs = []changes.CheckDef{}
		}
		c.sendJSON(changesChecksMsg{Type: "changesChecks", Req: cmd.Req, Commands: defs, Hash: hash, Pinned: checksConfirmed(pins, checksPath, defs)})
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
	pins.set(checksPath, hash)
	changesOK(c, cmd)
}
