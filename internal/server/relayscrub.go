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
// A command, a diff, a file path or a question can hold a secret, and the relay
// carries them to a phone that is not the desk. So every message a control
// connection writes passes through controlClient.write, and when that
// connection is a relay one (controlClient.remote) the message is rewritten by
// the scrub registered for its type. The key is remote and not the device's
// role: a device with the full role gets the same scrub as a viewer. A window on
// this machine is never touched and is written the same bytes as before.
//
// A type with no scrub registered is sent as it is, which is right for a
// message that carries none of this text. A type that does carry it must be
// registered, and TestRelayScrubCoversEveryCarrier fails when a new message
// holds a permission or a question and is not.
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
	"state":              scrubStateMsg,
	"agents":             scrubAgentsMsg,
	"graph":              scrubGraphMsg,
	"conversationPage":   scrubConversationPage,
	"conversationAppend": scrubConversationAppend,
	"conversationDetail": scrubConversationDetail,
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
// as a relay window is told it: what kind of thing, not the command or the
// text. "Run: rm -rf build" is "Run a command, details on the desktop".
func scrubSummary(s string) string {
	switch {
	case s == "", s == "asking a question":
		return s
	case strings.HasPrefix(s, "blocked: "):
		return record.Redact(s)
	case strings.HasPrefix(s, "Run: "):
		return "Run a command, " + detailsOnDesktop
	case strings.HasPrefix(s, "Edit "), strings.HasPrefix(s, "Write "), strings.HasPrefix(s, "Use "):
		return team.CleanLine(record.Redact(s), 120) + ", " + detailsOnDesktop
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

// scrubPane makes one pane's view fit for the relay.
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

	if pv.Structured {
		// A structured pane has no sealed terminal connection yet, so nothing
		// it said leaves the desk: only the roster fields stay.
		pv.DetailsOnDesktop = true
		scrubPaneNames(pv)
		pv.Permission = nil
		pv.Detail, pv.Task, pv.Err = "", "", ""
		pv.Last = nil
		return
	}
	scrubPaneNames(pv)
	pv.Detail = record.Redact(pv.Detail)
	pv.Task = record.Redact(pv.Task)
	pv.Err = record.Redact(pv.Err)
	if pv.Last != nil {
		last := *pv.Last
		last.Text = record.Redact(last.Text)
		pv.Last = &last
	}
}

// scrubPaneNames redacts the text of a pane view that a person or an agent chose:
// its name, its peer name, its project, its branch, its directory and what the
// conflict radar names. A secret in a directory name or a branch is as much a
// secret on the phone. The graph does the same for its nodes.
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
		m.Items[i].Waiting = scrubSummary(m.Items[i].Waiting)
		it := &m.Items[i]
		it.Name, it.Branch = record.Redact(it.Name), record.Redact(it.Branch)
		it.Project, it.Tab, it.Root = record.Redact(it.Project), record.Redact(it.Tab), record.Redact(it.Root)
		if it.Structured {
			// The same as the state push: a structured pane says nothing but its roster fields.
			it.Detail = ""
		} else {
			it.Detail = record.Redact(it.Detail)
		}
	}
	out, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return out
}

// scrubNeeds makes a needs-you list fit for the relay. The text of a permission
// or a question is replaced; the text of the others is redacted.
func scrubNeeds(items []team.NeedsItem) {
	for i := range items {
		switch items[i].Kind {
		case team.NeedPermission, team.NeedAsk:
			items[i].Text = detailsOnDesktop
		default:
			items[i].Text = record.Redact(items[i].Text)
		}
	}
}

func scrubGraphMsg(data []byte) []byte {
	var m graphMsg
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	for i := range m.Teams {
		t := &m.Teams[i]
		t.Goal = record.Redact(t.Goal)
		scrubNeeds(t.Needs)
	}
	scrubNeeds(m.SoloNeeds)
	for i := range m.Nodes {
		n := &m.Nodes[i]
		if n.WaitingOn.Kind == team.WaitUser {
			n.WaitingOn.Text = detailsOnDesktop
		} else {
			n.WaitingOn.Text = record.Redact(n.WaitingOn.Text)
		}
		n.Name, n.Peer = record.Redact(n.Name), record.Redact(n.Peer)
		n.Branch, n.Agent = record.Redact(n.Branch), record.Redact(n.Agent)
		n.Live = record.Redact(n.Live)
		if n.Task != nil {
			t := *n.Task
			t.Title = record.Redact(t.Title)
			n.Task = &t
		}
	}
	for i := range m.Edges {
		m.Edges[i].Text = record.Redact(m.Edges[i].Text)
	}
	m.BoardError = record.Redact(m.BoardError)
	out, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return out
}

// scrubEntries redacts the secrets in what a tool row shows: its label, its
// summary and its diff. The conversation's own words are left as they are.
func scrubEntries(entries []transcript.Entry) {
	for i := range entries {
		e := &entries[i]
		e.Label = record.Redact(e.Label)
		e.Summary = record.Redact(e.Summary)
		e.Description = record.Redact(e.Description)
		e.Report = record.Redact(e.Report)
		e.Diff = record.Redact(e.Diff)
	}
}

func scrubConversationPage(data []byte) []byte {
	var m conversationPageMsg
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	scrubEntries(m.Entries)
	out, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return out
}

func scrubConversationAppend(data []byte) []byte {
	var m conversationAppendMsg
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	scrubEntries(m.Entries)
	out, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return out
}

func scrubConversationDetail(data []byte) []byte {
	var m conversationDetailMsg
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	m.Detail.Text = record.Redact(m.Detail.Text)
	m.Detail.Diff = record.Redact(m.Detail.Diff)
	scrubEntries(m.Detail.Entries)
	out, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return out
}
