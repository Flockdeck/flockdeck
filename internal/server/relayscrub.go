package server

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"

	"github.com/jmwri/flockdeck/internal/record"
	"github.com/jmwri/flockdeck/internal/session/transcript"
	"github.com/jmwri/flockdeck/internal/team"
)

// What goes to a window reached through the relay is scrubbed on its way out.
//
// Until the scrub task (X1) is done and reviewed, a window reached through the
// relay is sent no conversation text: not the words of the conversation, not a
// tool row, not the command or the question of a permission prompt, not the
// opening prompt, the live line or the last reply of a pane. It is sent the
// roster (names, status, activity, branch, counts) and the reason a pane needs
// the person, in words that carry none of that text. The terminal is a separate,
// sealed connection and is not covered here.
//
// So every message a control connection writes passes through
// controlClient.write, and when that connection is a relay one
// (controlClient.remote) the message is rewritten by the scrub registered for its
// type. The key is remote and not the device's role: a device with the full role
// gets the same scrub as a viewer. A window on this machine is never touched and
// is written the same bytes as before. The conversation commands are refused to
// a relay window as well (see refuseRemoteText), so these scrubs are the second
// lock and not the only one.
//
// A type with no scrub registered is sent as it is, which is right for a
// message that carries none of this text. A type that does carry it must be
// registered, and TestRelayScrubCoversEveryCarrier fails when a new message
// holds a permission, a question or conversation text and is not.
//
// A scrub that cannot read its message drops it: the connection is never sent
// what could not be checked.

// detailsOnDesktop is the end of what a window reached through the relay is told
// of a permission prompt: what kind of thing it is, and that the rest is on the
// desktop. The same words are in the permission card's summary and in the
// agents list's waiting line, so a test can hold them together.
const detailsOnDesktop = "details on the desktop"

// relayScrubs is the registry: a message type to the function that makes it fit
// for the relay. A nil result drops the message.
var relayScrubs = map[string]func([]byte) []byte{
	"state":  scrubStateMsg,
	"agents": scrubAgentsMsg,
	// The conversation is for the desk until the scrub task is done. The page
	// is not dropped: it is the answer to conversationOpen, and a page that says
	// it is unsupported and holds nothing is how a phone is told to use the
	// terminal. Everything else about a conversation is dropped.
	"conversationPage":   scrubConversationPage,
	"conversationAppend": dropForRelay,
	"conversationDetail": dropForRelay,
	"conversationSearch": dropForRelay,
	"conversations":      dropForRelay,
	// The graph holds the live line of each pane, the text of each message
	// between agents and the text of each permission and question. Its commands
	// are refused to a relay window already; this is the second lock.
	"graph": dropForRelay,
	// A structured pane is for the desk until there is a sealed team socket.
	// The commands are refused to a relay window already; this is the second
	// lock if one is ever let through.
	"structured":       dropForRelay,
	"structuredDetail": dropForRelay,
}

func dropForRelay([]byte) []byte { return nil }

// scrubForRemote returns the message as a relay window may be sent it, or nil
// to send nothing.
func scrubForRemote(data []byte) []byte {
	fn, ok := relayScrubs[messageType(data)]
	if !ok {
		return data
	}
	return fn(data)
}

// messageType reads the type of a marshalled message. Every message here is a
// struct whose first field is the type, so it is read from the front; one that
// is not is parsed whole.
func messageType(data []byte) string {
	const head = `{"type":"`
	if rest, ok := bytes.CutPrefix(data, []byte(head)); ok {
		if i := bytes.IndexByte(rest, '"'); i >= 0 {
			return string(rest[:i])
		}
	}
	var m struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(data, &m) != nil {
		return ""
	}
	return m.Type
}

// scrubSummary is the one-line summary of a permission prompt or a waiting pane
// as a relay window is told it: what kind of thing, not the command, the file or
// the text. "Run: rm -rf build" is "Run a command, details on the desktop", and
// "Edit config.go" is "Edit a file, details on the desktop".
func scrubSummary(s string) string {
	switch {
	case s == "", s == "asking a question":
		return s
	case strings.HasPrefix(s, "blocked: "):
		return "Blocked, " + detailsOnDesktop
	case strings.HasPrefix(s, "Run: "):
		return "Run a command, " + detailsOnDesktop
	case strings.HasPrefix(s, "Edit "):
		return "Edit a file, " + detailsOnDesktop
	case strings.HasPrefix(s, "Write "):
		return "Write a file, " + detailsOnDesktop
	case strings.HasPrefix(s, "Use "):
		return "Use a tool, " + detailsOnDesktop
	}
	return "Waiting on a permission, " + detailsOnDesktop
}

// scrubPermission is a permission card as a relay window is told it: the tool
// and the summary, no command, description, file or diff, and no buttons. The
// answer is given at the desk, where the whole of it can be read.
func scrubPermission(p *permissionView) *permissionView {
	return &permissionView{
		Tool:     team.CleanLine(record.Redact(p.Tool), 40),
		Summary:  scrubSummary(p.Summary),
		Options:  []permissionOption{},
		Redacted: true,
	}
}

// scrubBackground keeps what the roster says of background work (its kind, its
// id, when it started and whether it is confirmed) and drops what the agent
// wrote for it: the command it runs, its description and what showed it ended.
func scrubBackground(work []backgroundView) []backgroundView {
	if len(work) == 0 {
		return work
	}
	out := slices.Clone(work)
	for i := range out {
		out[i].Description, out[i].Command, out[i].Evidence = "", "", ""
	}
	return out
}

// scrubPane makes one pane's view fit for the relay. What the agent said or was
// asked is cleared whatever the kind of pane: the live line, the opening
// prompt, the last reply and the question. The error is the process's own (an
// exit code, a failed start), not the conversation, and is redacted.
func scrubPane(pv *paneView) {
	if pv.Permission != nil || pv.Ask != nil {
		pv.DetailsOnDesktop = true
	}
	if pv.Permission != nil {
		pv.Permission = scrubPermission(pv.Permission)
	}
	// A question is not sent. A window with no ask draws "waiting for you" and
	// says to open the terminal, as docs/remote.md describes.
	pv.Ask = nil

	scrubPaneNames(pv)
	pv.Detail, pv.Task = "", ""
	if pv.Last != nil {
		// The phone's unread dot is worked out from the time of the last reply,
		// so the time and kind stay and the words go.
		pv.Last = &lastReplyView{TS: pv.Last.TS, Kind: pv.Last.Kind}
	}
	pv.Err = record.Redact(pv.Err)
	pv.BackgroundWork = scrubBackground(pv.BackgroundWork)
	pv.BackgroundEnded = scrubBackground(pv.BackgroundEnded)
	if pv.Structured {
		// A structured pane has no sealed terminal connection yet, so nothing
		// it said leaves the desk: only the roster fields stay.
		pv.DetailsOnDesktop = true
		pv.Permission = nil
		pv.Last = nil
		pv.Err = ""
	}
}

// scrubPaneNames redacts the text of a pane view that a person or an agent chose:
// its name, its peer name, its project, its branch, its directory and what the
// conflict radar names. A secret in a directory name or a branch is as much a
// secret on the phone.
func scrubPaneNames(pv *paneView) {
	pv.Name, pv.PeerName = record.Redact(pv.Name), record.Redact(pv.PeerName)
	pv.Project, pv.Branch, pv.Cwd = record.Redact(pv.Project), record.Redact(pv.Branch), record.Redact(pv.Cwd)
	if len(pv.Conflicts) > 0 {
		cs := make([]conflictView, len(pv.Conflicts))
		for i, c := range pv.Conflicts {
			c.Name, c.Branch = record.Redact(c.Name), record.Redact(c.Branch)
			c.Paths = slices.Clone(c.Paths)
			for j := range c.Paths {
				c.Paths[j] = record.Redact(c.Paths[j])
			}
			cs[i] = c
		}
		pv.Conflicts = cs
	}
}

func scrubStateMsg(data []byte) []byte {
	var m stateMsg
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	for id, pv := range m.Panes {
		scrubPane(&pv)
		m.Panes[id] = pv
	}
	out, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return out
}

func scrubAgentsMsg(data []byte) []byte {
	var m agentsMsg
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	for i := range m.Items {
		it := &m.Items[i]
		it.Waiting = scrubSummary(it.Waiting)
		it.Name, it.Branch = record.Redact(it.Name), record.Redact(it.Branch)
		it.Project, it.Tab, it.Root = record.Redact(it.Project), record.Redact(it.Tab), record.Redact(it.Root)
		it.Detail = ""
		it.BackgroundWork = scrubBackground(it.BackgroundWork)
		it.BackgroundEnded = scrubBackground(it.BackgroundEnded)
	}
	out, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return out
}

// scrubConversationPage turns a page into the answer for a pane with no
// conversation to show: unsupported, no entries, no cursor. A phone that gets it
// falls back to the terminal.
func scrubConversationPage(data []byte) []byte {
	var m conversationPageMsg
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	out, err := json.Marshal(conversationPageMsg{Type: m.Type, ID: m.ID, Entries: []transcript.Entry{}})
	if err != nil {
		return nil
	}
	return out
}

// conversationsOnDesktop is what a relay window is told when it asks for a
// conversation. The new shell matches on its start to draw one plain line.
const conversationsOnDesktop = "Conversations are shown on the desktop only for now"

// remoteTextCommands are the commands a relay window is refused whatever its
// role, because each one would send conversation text back. conversationClose
// is not here: it sends nothing and only undoes an open. A relay window has no
// watcher, so it does nothing.
var remoteTextCommands = map[string]bool{
	"conversationOpen":   true,
	"conversationOlder":  true,
	"conversationDetail": true,
	"conversationSearch": true,
	"conversations":      true,
}

// refuseRemoteText answers a relay window's conversation command with a plain
// refusal and reports whether it did. A conversationOpen is answered with the
// page a pane with no chat view gets, which is what the phone already draws as
// its way back to the terminal; the others get the notice.
func (c *controlClient) refuseRemoteText(cmd command) bool {
	if !c.remote || !remoteTextCommands[cmd.Cmd] {
		return false
	}
	if cmd.Cmd == "conversationOpen" {
		c.sendJSON(conversationPageMsg{Type: "conversationPage", ID: cmd.ID, Entries: []transcript.Entry{}})
		return true
	}
	// Round the outgoing filter, which drops a notice for a viewer: this one is
	// the answer to the window's own request and says nothing about the desk.
	if data, err := json.Marshal(noticeMsg{Type: "notice", Text: conversationsOnDesktop, Error: true}); err == nil {
		c.putMessage(data)
	}
	return true
}
