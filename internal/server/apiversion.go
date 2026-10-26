package server

import (
	"encoding/json"
	"slices"
)

// This file is the version of the control socket's API, as a window is told of
// it and tells the server its own. The socket's messages are a contract between
// the server and the interface it serves (the desktop window, the same page
// through the relay, the phone app). The version says which contract is spoken,
// so an interface and a server that were built apart can tell they do not match
// instead of failing in ways that look like bugs.
//
// The rules:
//   - Inside one major version a change only adds: a new message, a new field, a
//     new command. Nothing is removed or changes its meaning. The minor version
//     goes up with each addition that a window may want to look for.
//   - caps names the features a window can ask for by name, so it need not
//     compare minor versions to find out whether one is there. Names are only
//     ever added.
//   - A window that sends a command this server does not know is told so
//     ("unsupported"), where before it was dropped without a word. A window that
//     speaks another major version is told ("apiMismatch") and is not cut off:
//     what it can still do is for it to decide.

// APIMajor and APIMinor are the version of the control socket's API.
const (
	APIMajor = 1
	APIMinor = 11
)

// apiCaps are the features of this version, in the order they were added.
var apiCaps = []string{"clientHello", "unsupported", "apiMismatch", "uiPrefs", "keyOverrides", "paneName", "changesTeam", "changesDiff", "changesChecks", "changesComments", "changesGate"}

// A deprecatedField is something the graph push still carries that a client
// should stop reading. Nothing here is removed: the old Graph view reads most of
// it for one more release, and a field goes only in a change that raises the
// major version. Scope is "field" (the field is deprecated), "use" (the field
// stays, one way of using it is deprecated) or "order" (the items stay, the order
// the server puts them in is deprecated). api/catalog.json lists them, the schema
// marks the fields, and docs/team-graph.md has the table.
type deprecatedField struct {
	Message string `json:"message"`
	// Path is the field as a client reads it: [] is each element of an array.
	Path  string `json:"path"`
	Scope string `json:"scope"`
	Since string `json:"since"`
	// Use is what a client does instead.
	Use string `json:"use"`
}

var deprecatedFields = []deprecatedField{
	{"graph", "nodes[].stuck", "field", "1.1", "Work it out from status and since (working with nothing written for ten minutes). The `stuck` needs item stays."},
	{"graph", "nodes[].lastOutput", "use", "1.1", "Do not use it to decide a pane is stuck; the field stays."},
	{"graph", "nodes[].below", "field", "1.1", "Count the panes under a node from parent and children."},
	{"graph", "nodes[].needYou", "field", "1.1", "Count the needs of the panes under a node."},
	{"graph", "nodes[].depth", "field", "1.1", "Work it out from parent."},
	{"graph", "nodes[].spendPartial", "field", "1.1", "Not shown by the new interface."},
	{"graph", "nodes[].spendIncludesPartial", "field", "1.1", "Not shown by the new interface."},
	{"graph", "teams[].spendUSD", "field", "1.1", "Sum the nodes' spendUSD."},
	{"graph", "teams[].spendPartial", "field", "1.1", "Sum the nodes' spendUSD."},
	{"graph", "teams[].spendIncludesPartial", "field", "1.1", "Sum the nodes' spendUSD."},
	{"graph", "teams[].caps[].used", "field", "1.1", "Count the open panes under the pane; caps[].max stays."},
	{"graph", "teams[].needs", "order", "1.1", "Order the items yourself; the items and their kind stay."},
	{"graph", "soloNeeds", "order", "1.1", "Order the items yourself; the items and their kind stay."},
}

// clientFrames are the frames a window sends over /ws/control that are not
// commands: a frame with a type and no cmd. Each is read in the socket's read
// loop (see clientFrame) and has no handler in handleCommand. The value says what
// the frame is for, and api/catalog.json lists them.
var clientFrames = map[string]string{
	"clientHello": "A window says once, after the hello, which API version it was built for and what it calls itself. A different major version is answered with apiMismatch; the same one is only noted.",
}

// clientFrame handles a frame of clientFrames.
func (s *Server) clientFrame(c *controlClient, typ string, data []byte) {
	switch typ {
	case "clientHello":
		s.clientHello(c, data)
	}
}

// apiVersion is a version as it travels: in the hello for the server's own and
// in clientHello for the window's.
type apiVersion struct {
	Major int `json:"major"`
	Minor int `json:"minor"`
}

// apiInfo is the server's version and features, the "api" block of the hello.
type apiInfo struct {
	Major int      `json:"major"`
	Minor int      `json:"minor"`
	Caps  []string `json:"caps"`
}

// currentAPI is the block the hello carries. The caps are a copy, so a message
// being marshalled cannot be changed under it.
func currentAPI() *apiInfo {
	return &apiInfo{Major: APIMajor, Minor: APIMinor, Caps: slices.Clone(apiCaps)}
}

// apiFor is the block a window is sent: the changes caps are left out for one
// through the relay, which is refused the whole of that contract.
func apiFor(remote bool) *apiInfo {
	a := currentAPI()
	a.Caps = changesCapsFor(remote, a.Caps)
	return a
}

// clientHelloMsg is what a window sends once, after the hello: which version of
// the API it was built for, and what it calls itself. Client is a label for the
// logs and the mismatch notice, not an identity; it is clamped where it is used.
type clientHelloMsg struct {
	Type   string     `json:"type"`
	API    apiVersion `json:"api"`
	Client string     `json:"client"`
}

// apiMismatchMsg is the answer to a clientHello of another major version. It
// carries both versions and nothing else the desk knows, so a relay window is
// sent it as it is.
type apiMismatchMsg struct {
	Type   string     `json:"type"`
	Server apiVersion `json:"server"`
	Client apiVersion `json:"client"`
	// Text is a sentence the window may show as it is.
	Text string `json:"text"`
}

// unsupportedMsg is the answer to a command this server has no handler for. Cmd
// is the name the window sent, clamped (cmdName's limit), so it is the window's
// own text coming back to it.
type unsupportedMsg struct {
	Type string `json:"type"`
	Cmd  string `json:"cmd"`
	Text string `json:"text"`
}

// maxClientLabel is how many characters of a window's own name are kept.
const maxClientLabel = 40

// clientHello handles the frame a window sends to say which API it speaks. A
// window of the same major version is simply noted. One of another major is told
// so; the socket stays open and the window decides what to do.
func (s *Server) clientHello(c *controlClient, data []byte) {
	var m clientHelloMsg
	if json.Unmarshal(data, &m) != nil {
		return
	}
	v := m.API
	c.api.Store(&v)
	if v.Major == APIMajor {
		return
	}
	c.sendJSON(apiMismatchMsg{
		Type:   "apiMismatch",
		Server: apiVersion{Major: APIMajor, Minor: APIMinor},
		Client: v,
		Text:   "This window was built for another version of Flockdeck's interface than the one it is connected to. Reload it, or update whichever is older.",
	})
}

// isKnownCommand says whether the server has a handler for a command: the
// access table lists every one (TestEveryCommandHasAnAccessDecision).
func isKnownCommand(name string) bool {
	_, ok := commandAccess[name]
	return ok
}

// refuseUnknown answers a command the server does not know with "unsupported"
// and reports whether it did. An empty name is a frame that is not a command at
// all and is left alone, as it always was.
func (c *controlClient) refuseUnknown(name string) bool {
	if name == "" || isKnownCommand(name) {
		return false
	}
	c.sendJSON(unsupportedMsg{
		Type: "unsupported",
		Cmd:  clampCmd(name),
		Text: "This version of Flockdeck does not know " + cmdName(name) + ".",
	})
	return true
}

// clampCmd is cmdName's clamping without its words.
func clampCmd(name string) string {
	const limit = 24
	if r := []rune(name); len(r) > limit {
		return string(r[:limit]) + "…"
	}
	return name
}
