package server

import (
	"strconv"
	"strings"

	"github.com/jmwri/flockdeck/internal/workspace"
)

// Pane info and Find pane answer one window on this machine, on request. The
// ids, process ids and file paths they carry are not part of the state every
// window is sent, so nothing here reaches the relay: a window reached through
// it is refused, as Reveal transcript is.

const paneInfoRemote = "Pane info and Find pane are shown on the machine Flockdeck runs on, not from a window reached through the relay"

// paneInfoField is one labelled value. An empty Value is a pane that does not
// have one yet, which the window says as "not set".
type paneInfoField struct {
	Label string `json:"label"`
	Value string `json:"value"`
	// Note says what the value is for, or what makes it change.
	Note string `json:"note,omitempty"`
}

type paneInfoMsg struct {
	Type   string          `json:"type"`
	ID     string          `json:"id"`
	Name   string          `json:"name"`
	Fields []paneInfoField `json:"fields"`
}

// paneInfoFields lays a pane's identifiers out in the order the window shows
// them. A shell has no agent, conversation or transcript, so those are left
// out for it rather than shown as unset.
func paneInfoFields(d workspace.PaneDetails, project string) []paneInfoField {
	f := []paneInfoField{
		{"Pane id", d.ID, "What flockdeck commands take, and the pane field in transcript files"},
		{"Pane name", d.Name, ""},
		{"Type", map[bool]string{true: "Agent", false: "Shell"}[d.Agent], ""},
		{"Project", project, ""},
		{"Project folder", d.Root, ""},
		{"Directory", d.Cwd, "Where the pane runs; a worktree has its own"},
		{"Branch", d.Branch, ""},
		{"Tab", d.Tab, ""},
	}
	if d.Agent {
		f = append(f,
			paneInfoField{"Agent", d.AgentID, ""},
			paneInfoField{"Model", d.Model, "Empty means the agent's own default"},
			paneInfoField{"Conversation id", d.Conversation, "The agent's own id for this conversation. It changes after /clear, and the first 8 characters are in the transcript file name"},
		)
	}
	f = append(f,
		paneInfoField{"Process id", pidText(d.Pid), "Of the pane's own process, empty once it has exited"},
		paneInfoField{"Peer name", d.PeerName, "Set by flockdeck peer-name, the name other sessions message this pane by"},
		paneInfoField{"Status", d.Status, ""},
	)
	if d.Agent {
		rec := "Off"
		if d.Recording {
			rec = "On"
		}
		f = append(f,
			paneInfoField{"Recording", rec, ""},
			paneInfoField{"Transcript file", d.TranscriptPath, "The file being recorded to, or the latest export"},
			paneInfoField{"Stored conversation file", d.StoredPath, "Where the agent keeps the conversation itself"},
		)
	}
	return f
}

func pidText(pid int) string {
	if pid <= 0 {
		return ""
	}
	return strconv.Itoa(pid)
}

// projectNames maps each open project's root to the name it is shown under.
func (s *Server) projectNames() map[string]string {
	names := map[string]string{}
	for _, pr := range s.ws.Projects() {
		names[pr.Root] = pr.Name
	}
	return names
}

// paneInfo answers one window with what identifies one pane.
func (s *Server) paneInfo(c *controlClient, cmd command) {
	if c.remote {
		c.notify(paneInfoRemote, true)
		return
	}
	s.do(func() {
		id := paneIDFor(s.ws, cmd.ID)
		d, ok := s.ws.PaneDetailsOf(id, true)
		if !ok {
			c.notify(paneGone, true)
			return
		}
		c.sendJSON(paneInfoMsg{Type: "paneInfo", ID: d.ID, Name: d.Name,
			Fields: paneInfoFields(d, projectLabel(s.projectNames(), d.Root))})
	})
}

// paneMatch is one pane a search found, and what of it matched.
type paneMatch struct {
	PaneID  string `json:"paneId"`
	TabID   string `json:"tabId"`
	Root    string `json:"root"`
	Name    string `json:"name"`
	Project string `json:"project"`
	Tab     string `json:"tab"`
	// Matched says which fields held the words typed, as label and value.
	Matched []paneInfoField `json:"matched,omitempty"`
}

type paneMatchesMsg struct {
	Type  string      `json:"type"`
	Query string      `json:"query"`
	Items []paneMatch `json:"items"`
}

// searchPanes returns the panes whose fields hold every word of the query,
// ignoring case, in tab order. An empty query returns every pane. A word
// matches inside any field, so the first characters of a conversation id, as
// a transcript file name carries them, find its pane.
func searchPanes(all []workspace.PaneDetails, names map[string]string, query string) []paneMatch {
	words := strings.Fields(strings.ToLower(query))
	out := []paneMatch{}
	for _, d := range all {
		project := projectLabel(names, d.Root)
		fields := []paneInfoField{
			{"Name", d.Name, ""}, {"Project", project, ""}, {"Tab", d.Tab, ""},
			{"Agent", d.AgentID, ""}, {"Model", d.Model, ""},
			{"Directory", d.Cwd, ""}, {"Branch", d.Branch, ""},
			{"Pane id", d.ID, ""}, {"Conversation id", d.Conversation, ""},
			{"Process id", pidText(d.Pid), ""}, {"Peer name", d.PeerName, ""},
		}
		m := paneMatch{PaneID: d.ID, TabID: d.TabID, Root: d.Root, Name: d.Name, Project: project, Tab: d.Tab}
		ok := true
		for _, w := range words {
			found := false
			for _, f := range fields {
				if f.Value != "" && strings.Contains(strings.ToLower(f.Value), w) {
					found = true
					if !hasLabel(m.Matched, f.Label) {
						m.Matched = append(m.Matched, f)
					}
				}
			}
			if !found {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, m)
		}
	}
	return out
}

func hasLabel(fs []paneInfoField, label string) bool {
	for _, f := range fs {
		if f.Label == label {
			return true
		}
	}
	return false
}

// findPane answers one window with the panes matching a query, in every open
// project. Choosing one goes through revealPane.
func (s *Server) findPane(c *controlClient, cmd command) {
	if c.remote {
		c.notify(paneInfoRemote, true)
		return
	}
	s.do(func() {
		c.sendJSON(paneMatchesMsg{Type: "paneMatches", Query: cmd.Text,
			Items: searchPanes(s.ws.AllPaneDetails(), s.projectNames(), cmd.Text)})
	})
}
