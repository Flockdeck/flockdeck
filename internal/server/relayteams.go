package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/team"
)

// The relay form of the team graph (R1).
//
// A window reached through the relay is never sent the graph message: its
// nodes carry the live line of each pane, its edges the text of each message
// between agents, and its teams the goal. The graph commands are refused to such
// a window with deskOnly, and the graph message is dropped for it (relayscrub.go).
// Neither changes here.
//
// What a phone is sent instead is relayTeams, a message of its own that the
// server pushes to a window reached through the relay without being asked: the
// shape of each project's teams and what each pane is doing, in words that carry
// no text anyone typed and no text an agent wrote. Every field is an enumerated
// word, an id Flockdeck made, a count, or an opaque label made here. The one free
// text is the project's name, which is redacted and cut. The pane names, the peer
// names, the goals, the live lines, the branches, the task titles, the message
// texts, the permission and question texts and the paths of the graph are not
// in it, and neither is any detail of a structured pane beyond its kind and
// status.
//
// Whether the names of teams and panes cross is the user's to decide, and is
// open: until it is, a pane is "Worker 2" and not what it was called. Team
// structure crosses the relay in the clear until the sealed team socket exists
// (decision 7 of the rebuild plan).
//
// Two locks hold the form. The builder (relayTeamsData) fills the message from the
// graph, and the scrub registered for the type (scrubRelayTeams) checks it again
// against the allow lists below before it is written to a window, so a mistake in
// the builder cannot put text on the wire. The form is versioned
// (relayTeamsVersion), and it is a type an older phone does not know, which it
// ignores; remote.Capability went from 1 to 2 with it.

// relayTeamsVersion is the version of the relayTeams form. A change that adds a
// field a client could misread raises it.
const relayTeamsVersion = 1

// Bounds on a relayTeams message, so a runaway project cannot make a large one.
const (
	maxRelayProjects = 50
	maxRelayPanes    = maxGraphPanes
	maxRelayNeeds    = team.MaxNeeds
)

// relayTeamsMsg is the relay form of the graphs of every project that has panes.
type relayTeamsMsg struct {
	Type     string             `json:"type"`
	V        int                `json:"v"`
	Projects []relayTeamProject `json:"projects"`
}

// relayTeamProject is one project. Project is the only text in it.
type relayTeamProject struct {
	ID        string      `json:"id"`
	Project   string      `json:"project"`
	Teams     []relayTeam `json:"teams"`
	Panes     []relayPane `json:"panes"`
	Solo      []string    `json:"solo"`
	SoloNeeds []relayNeed `json:"soloNeeds"`
}

// relayTeam is a manager and the panes below it.
type relayTeam struct {
	ID      string      `json:"id"`
	Label   string      `json:"label"`
	Manager string      `json:"manager"`
	Members []string    `json:"members"`
	Needs   []relayNeed `json:"needs"`
}

// relayNeed is one thing that wants the person: which pane, and what kind of
// thing. There is no text and no task in it.
type relayNeed struct {
	Pane string `json:"pane"`
	Kind string `json:"kind"`
}

// relayPane is one pane. A structured pane has only the fields up to Status.
type relayPane struct {
	ID     string `json:"id"`
	Parent string `json:"parent,omitempty"`
	Depth  int    `json:"depth"`
	Team   string `json:"team,omitempty"`
	Kind   string `json:"kind"`
	Label  string `json:"label"`
	Status string `json:"status"`
	// The rest is left out for a structured pane.
	Shell    bool       `json:"shell,omitempty"`
	Role     string     `json:"role,omitempty"`
	Activity string     `json:"activity,omitempty"`
	Wait     *relayWait `json:"wait,omitempty"`
	Unread   int        `json:"unread,omitempty"`
}

// relayWait is what a pane is held up by: the kind of wait, for a person the
// kind of thing asked (permission or question), and for helpers the pane it
// waits on first.
type relayWait struct {
	Kind   string `json:"kind"`
	Prompt string `json:"prompt,omitempty"`
	On     string `json:"on,omitempty"`
}

// The allow lists. A value outside one is dropped, not passed on.
var (
	relayStatuses = map[string]bool{"starting": true, "working": true, "waiting": true, "blocked": true, "idle": true, "exited": true, "unknown": true}
	// relayActivities are workspace.Activity's words.
	relayActivities  = map[string]bool{"waiting": true, "blocked": true, "working": true, "idle": true, "starting": true, "exited": true, "failed": true}
	relayKinds       = map[string]bool{graphKindTerminal: true, graphKindStructured: true}
	relayWaitKinds   = map[string]bool{team.WaitUser: true, team.WaitDown: true, team.WaitReviewer: true, team.WaitTask: true, team.WaitHelpers: true}
	relayTeamPrompts = map[string]bool{"permission": true, "question": true}
	// relayNeedKinds maps the kinds of team.NeedsItem to the words a phone is told.
	relayNeedKinds = map[string]string{
		team.NeedPermission: "permission", team.NeedAsk: "question", team.NeedReturned: "returned",
		team.NeedConflict: "conflict", team.NeedDown: "down", team.NeedSetup: "setup", team.NeedStaleUnread: "unread",
	}
	// relayIDRe is what an id Flockdeck made looks like. A pane id that is not
	// this, whatever made it, is not sent.
	relayIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	// relayRoleLabels are the built-in roles and their labels. A role added in a
	// user's agents.json is "other", and a pane with none is a plain terminal.
	relayRoleLabels = func() map[string]string {
		m := map[string]string{}
		for _, r := range agent.BuiltinRoles() {
			m[r.ID] = strings.ToUpper(r.ID[:1]) + r.ID[1:]
		}
		return m
	}()
)

// relayProjectsFromGraphs is the relay form of the graphs of the projects, in
// their order. A graph with no panes is left out.
func relayProjectsFromGraphs(graphs []graphMsg) []relayTeamProject {
	out := []relayTeamProject{}
	for _, g := range graphs {
		if len(g.Nodes) == 0 {
			continue
		}
		out = append(out, relayProjectFromGraph(g))
	}
	return out
}

// relayProjectFromGraph reads the allowed fields of one graph and no others.
func relayProjectFromGraph(g graphMsg) relayTeamProject {
	p := relayTeamProject{Project: g.Project, Teams: []relayTeam{}, Panes: []relayPane{}, Solo: []string{}, SoloNeeds: []relayNeed{}}
	// What each pane waits on a person for: a permission or a question.
	prompt := map[string]string{}
	note := func(items []team.NeedsItem) []relayNeed {
		needs := []relayNeed{}
		for _, it := range items {
			kind, ok := relayNeedKinds[it.Kind]
			if !ok {
				continue
			}
			if kind == "permission" || kind == "question" {
				prompt[it.Pane] = kind
			}
			needs = append(needs, relayNeed{Pane: it.Pane, Kind: kind})
		}
		return needs
	}
	for _, t := range g.Teams {
		p.Teams = append(p.Teams, relayTeam{ID: t.ID, Manager: t.Manager, Members: append([]string{}, t.Members...), Needs: note(t.Needs)})
	}
	p.SoloNeeds = note(g.SoloNeeds)
	p.Solo = append(p.Solo, g.Solo...)
	for _, n := range g.Nodes {
		rp := relayPane{ID: n.Pane, Parent: n.Parent, Depth: n.Depth, Team: n.Team, Kind: n.Kind, Status: n.Status}
		if n.Kind != graphKindStructured {
			rp.Shell, rp.Role, rp.Activity, rp.Unread = n.Shell, n.Role, n.Activity, n.Unread
			if k := n.WaitingOn.Kind; k != "" && k != team.WaitNone {
				w := &relayWait{Kind: k}
				switch k {
				case team.WaitUser:
					w.Prompt = prompt[n.Pane]
				case team.WaitHelpers:
					w.On = n.WaitingOn.Ref
				}
				rp.Wait = w
			}
		}
		p.Panes = append(p.Panes, rp)
	}
	return p
}

// scrubRelayTeams is the scrub registered for relayTeams. It rebuilds the message
// from the allow lists, field by field, and makes the labels itself, so nothing
// the sender put in a field that should hold a word can reach the window. A
// message it cannot read is dropped.
func scrubRelayTeams(data []byte) []byte {
	var m relayTeamsMsg
	if json.Unmarshal(data, &m) != nil || m.V != relayTeamsVersion {
		return nil
	}
	out := relayTeamsMsg{Type: "relayTeams", V: relayTeamsVersion, Projects: []relayTeamProject{}}
	for i, p := range m.Projects {
		if i >= maxRelayProjects {
			break
		}
		out.Projects = append(out.Projects, scrubRelayProject(i+1, p))
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil
	}
	return b
}

func scrubRelayProject(n int, p relayTeamProject) relayTeamProject {
	out := relayTeamProject{ID: fmt.Sprintf("p%d", n), Project: capName(p.Project),
		Teams: []relayTeam{}, Panes: []relayPane{}, Solo: []string{}, SoloNeeds: []relayNeed{}}
	ids := map[string]bool{}
	for i, rp := range p.Panes {
		if i >= maxRelayPanes {
			break
		}
		if !relayIDRe.MatchString(rp.ID) || ids[rp.ID] {
			continue
		}
		ids[rp.ID] = true
	}
	// id keeps an id only if it names a pane that is sent.
	id := func(s string) string {
		if ids[s] {
			return s
		}
		return ""
	}
	count := map[string]int{}
	for i, rp := range p.Panes {
		if i >= maxRelayPanes {
			break
		}
		if !ids[rp.ID] {
			continue
		}
		sp := relayPane{ID: rp.ID, Parent: id(rp.Parent), Depth: min(max(rp.Depth, 0), 64), Team: id(rp.Team), Kind: "terminal", Status: "unknown"}
		if relayKinds[rp.Kind] {
			sp.Kind = rp.Kind
		}
		if relayStatuses[rp.Status] {
			sp.Status = rp.Status
		}
		base := "Agent"
		if sp.Kind != graphKindStructured {
			sp.Shell = rp.Shell
			if relayActivities[rp.Activity] {
				sp.Activity = rp.Activity
			}
			sp.Unread = min(max(rp.Unread, 0), 99999)
			switch label, ok := relayRoleLabels[rp.Role]; {
			case ok:
				sp.Role, base = rp.Role, label
			case rp.Role != "":
				sp.Role = "other"
			case sp.Shell:
				base = "Terminal"
			}
			if w := rp.Wait; w != nil && relayWaitKinds[w.Kind] {
				sw := &relayWait{Kind: w.Kind}
				if w.Kind == team.WaitUser && relayTeamPrompts[w.Prompt] {
					sw.Prompt = w.Prompt
				}
				if w.Kind == team.WaitHelpers {
					sw.On = id(w.On)
				}
				sp.Wait = sw
			}
		}
		count[base]++
		sp.Label = fmt.Sprintf("%s %d", base, count[base])
		out.Panes = append(out.Panes, sp)
	}
	needs := func(in []relayNeed) []relayNeed {
		o := []relayNeed{}
		for _, n := range in {
			if len(o) >= maxRelayNeeds {
				break
			}
			if ids[n.Pane] && isRelayNeedKind(n.Kind) {
				o = append(o, relayNeed{Pane: n.Pane, Kind: n.Kind})
			}
		}
		return o
	}
	for i, t := range p.Teams {
		if !ids[t.ID] || !ids[t.Manager] {
			continue
		}
		st := relayTeam{ID: t.ID, Label: fmt.Sprintf("Team %d", i+1), Manager: t.Manager, Members: []string{}, Needs: needs(t.Needs)}
		for _, mem := range t.Members {
			if ids[mem] {
				st.Members = append(st.Members, mem)
			}
		}
		out.Teams = append(out.Teams, st)
	}
	for _, s := range p.Solo {
		if ids[s] {
			out.Solo = append(out.Solo, s)
		}
	}
	out.SoloNeeds = needs(p.SoloNeeds)
	return out
}

func isRelayNeedKind(k string) bool {
	for _, v := range relayNeedKinds {
		if v == k {
			return true
		}
	}
	return false
}

// relayTeamsData is the relayTeams message for the windows through the relay:
// the graph of every project that has panes, in its relay form. It must run on
// the workspace goroutine. A project that cannot be read is left out.
func (s *Server) relayTeamsData() []byte {
	var graphs []graphMsg
	for _, p := range s.ws.Projects() {
		g, err := s.graphPayload(p.Root)
		if err != nil {
			continue
		}
		graphs = append(graphs, g)
	}
	data, err := json.Marshal(relayTeamsMsg{Type: "relayTeams", V: relayTeamsVersion, Projects: relayProjectsFromGraphs(graphs)})
	if err != nil {
		return nil
	}
	return data
}

// relayClients are the windows reached through the relay that have had their
// hello and may be sent the form: a viewer that may not watch panes is sent no
// roster, and so none of this.
func (s *Server) relayClients() []*controlClient {
	var out []*controlClient
	for _, c := range s.greetedClients() {
		if c.remote {
			out = append(out, c)
		}
	}
	return out
}

// sendRelayTeamsTo sends one window through the relay the form now, for a window
// that has just come in. It must run on the workspace goroutine.
func (s *Server) sendRelayTeamsTo(c *controlClient) {
	if !c.remote {
		return
	}
	data := s.relayTeamsData()
	if data == nil {
		return
	}
	if c.trySend(data) {
		c.relayTeamsSent = data
	}
}

// pushRelayTeams sends every window through the relay the form when it has
// changed, at most every teamInterval, and again when the clock alone may have
// changed it (graphTickEvery). It does nothing, and builds nothing, when no
// such window is open. It must run on the workspace goroutine, and is called
// with each state push.
func (s *Server) pushRelayTeams() {
	clients := s.relayClients()
	s.relayWatching = len(clients) > 0
	if !s.relayWatching {
		return
	}
	s.armRelayTeamsTick()
	if time.Since(s.relayTeamsAt) < teamInterval {
		s.armRelayTeams()
		return
	}
	s.relayTeamsAt = time.Now()
	data := s.relayTeamsData()
	if data == nil {
		return
	}
	retry := false
	for _, c := range clients {
		if bytes.Equal(data, c.relayTeamsSent) {
			continue
		}
		if c.trySend(data) {
			c.relayTeamsSent = data
		} else {
			retry = true
		}
	}
	if retry {
		s.armRelayTeams()
	}
}

// armRelayTeams arranges for a push in a moment, once.
func (s *Server) armRelayTeams() {
	if s.relayTeamsArmed {
		return
	}
	s.relayTeamsArmed = true
	wait := max(teamInterval-time.Since(s.relayTeamsAt), 50*time.Millisecond)
	time.AfterFunc(wait, func() {
		s.do(func() {
			s.relayTeamsArmed = false
			s.pushRelayTeams()
		})
	})
}

// armRelayTeamsTick arranges for a look at the form in graphTickEvery, once.
func (s *Server) armRelayTeamsTick() {
	if s.relayTickArmed {
		return
	}
	s.relayTickArmed = true
	time.AfterFunc(s.graphTickEvery, func() {
		s.do(func() {
			s.relayTickArmed = false
			s.pushRelayTeams()
		})
	})
}
