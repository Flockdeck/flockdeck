package server

import "encoding/json"

// The team-wide changes view (docs/changes-api.md, api/schema/changes.schema.json)
// is a contract written before the server serves it. Until each part is built, its
// commands are answered here, so a window that sends one is told so and not left
// waiting, and api/catalog.json can list them with the status planned.

// changesCommands are the commands of the contract, in the order the document
// lists them. Each is also a case of handleCommand, which the catalog is read from.
var changesCommands = []string{
	"changesWatch", "changesUnwatch", "changesSnapshot", "filesPage", "diffHunks", "diffContext",
	"commentAdd", "commentResolve", "commentReopen", "runChecks", "requestReview", "submitReview",
	"approveMerge", "unapproveMerge", "mergeBranch",
}

// changesCaps are the names the hello's api.caps will list as the parts are built.
// None is listed yet: a cap is added in the change that builds what it names.
var changesCaps = []string{"changesTeam", "changesDiff", "changesComments", "changesChecks", "changesGate"}

// changesMessages are the messages of the contract the server does not send yet.
var changesMessages = []string{"changesTeam", "changesFiles", "diffHunks", "diffContext"}

const (
	changesUnsupported = "Team-wide changes are not built into this version of Flockdeck yet"
	changesDeskOnly    = "Team-wide changes are used on the machine Flockdeck runs on, not from a window reached through the relay"
)

// changesResultMsg answers a command of the contract that changes something, and
// refuses any of them.
type changesResultMsg struct {
	Type  string `json:"type"`
	Req   string `json:"req,omitempty"`
	Op    string `json:"op"`
	OK    bool   `json:"ok"`
	Code  string `json:"code,omitempty"`
	Error string `json:"error,omitempty"`
}

// changesPlanned refuses a command of the contract: to a window through the relay
// whatever the build does, and to any other because this build does not do it.
func (s *Server) changesPlanned(c *controlClient, cmd command) {
	if c.remote {
		c.sendJSON(changesResultMsg{Type: "changesResult", Req: cmd.Req, Op: cmd.Cmd, Code: "deskOnly", Error: changesDeskOnly})
		c.notify(changesDeskOnly, true)
		return
	}
	c.sendJSON(changesResultMsg{Type: "changesResult", Req: cmd.Req, Op: cmd.Cmd, Code: "unsupported", Error: changesUnsupported})
}

// scrubChangesResult keeps the refusal a relay window is given for a command of the
// contract and drops any answer that holds data.
func scrubChangesResult(data []byte) []byte {
	var m changesResultMsg
	if json.Unmarshal(data, &m) != nil || m.OK {
		return nil
	}
	// Only the two refusals a relay window can be owed, in fixed words: what the
	// desktop wrote in error is not trusted to hold nothing.
	var text string
	switch m.Code {
	case "deskOnly":
		text = changesDeskOnly
	case "unsupported":
		text = changesUnsupported
	default:
		return nil
	}
	out, err := json.Marshal(changesResultMsg{Type: "changesResult", Req: m.Req, Op: m.Op, Code: m.Code, Error: text})
	if err != nil {
		return nil
	}
	return out
}
