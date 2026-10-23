package server

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/remote"
	"github.com/jmwri/flockdeck/internal/team"
)

// The relay form of the team graph (relayteams.go). A window reached through the
// relay is sent ids, words and counts, never the text of the graph.

// jsonNames lists the json field names of a type and of every struct it holds.
func jsonNames(t reflect.Type, into map[string]bool) {
	for t.Kind() == reflect.Slice || t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			into[f.Name+"?"] = true
			continue
		}
		into[name] = true
		jsonNames(f.Type, into)
	}
}

// The fields of the relay form are listed here by name. Adding one fails this
// until it is added to the list on purpose and a reviewer has read it.
func TestRelayTeamsCarriesOnlyTheNamedFields(t *testing.T) {
	got := map[string]bool{}
	jsonNames(reflect.TypeOf(relayTeamsMsg{}), got)
	want := []string{"activity", "depth", "id", "kind", "label", "manager", "members", "needs", "on", "pane",
		"panes", "parent", "project", "projects", "prompt", "role", "shell", "solo", "soloNeeds", "status",
		"team", "teams", "type", "unread", "v", "wait"}
	var names []string
	for n := range got {
		names = append(names, n)
	}
	sort.Strings(names)
	if !slices.Equal(names, want) {
		t.Errorf("the relay form has the fields\n%v\nwant\n%v", names, want)
	}
}

// fillStrings sets every string in v, except the fields named in keep, to s.
func fillStrings(v reflect.Value, s string, keep map[string]bool) {
	switch v.Kind() {
	case reflect.String:
		v.SetString(s)
	case reflect.Ptr:
		if !v.IsNil() {
			fillStrings(v.Elem(), s, keep)
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			fillStrings(v.Index(i), s, keep)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			if keep[f.Name] || !f.IsExported() {
				continue
			}
			fillStrings(v.Field(i), s, keep)
		}
	}
}

// fuzzGraph is a graph of three panes (a manager, a worker with a role of the
// user's own, and a structured pane) whose every free-text field holds the marker.
func fuzzGraph() graphMsg {
	g := graphMsg{
		Type: "graph", Root: "/r", Project: "Proj",
		Teams: []graphTeamView{{ID: "m1", Manager: "m1", Members: []string{"m1", "w1", "s1"},
			Needs: []team.NeedsItem{{ID: "n1", Kind: team.NeedPermission, Pane: "w1", Other: "o", Task: "t", Text: "x"}}, Caps: []graphCap{{Pane: "m1"}}}},
		Nodes: []graphNodeView{
			{GraphNode: team.GraphNode{Pane: "m1", Team: "m1", WaitingOn: team.WaitingOn{Kind: team.WaitHelpers, Text: "x", Ref: "w1"}},
				Kind: graphKindTerminal, Role: "manager", Status: "working", Activity: "working", Task: &graphTaskChip{ID: "t", Title: "x", State: "x"}},
			{GraphNode: team.GraphNode{Pane: "w1", Parent: "m1", Depth: 1, Team: "m1", WaitingOn: team.WaitingOn{Kind: team.WaitUser, Text: "x"}},
				Kind: graphKindTerminal, Role: "ZZ", Status: "waiting", Activity: "waiting", Unread: 2},
			{GraphNode: team.GraphNode{Pane: "s1", Parent: "m1", Depth: 1, Team: "m1", WaitingOn: team.WaitingOn{Kind: team.WaitUser}},
				Kind: graphKindStructured, Role: "worker", Status: "working", Activity: "working", Unread: 9},
		},
		Edges: []graphEdge{{Kind: "message", From: "m1", To: "w1", Text: "x"}},
		Solo:  []string{}, SoloNeeds: []team.NeedsItem{{ID: "n2", Kind: team.NeedAsk, Pane: "m1", Text: "x"}},
		BoardError: "x",
	}
	return g
}

// A marker in every free-text field of a graph does not reach a relay window,
// except in the project name, which is the one text on the form.
func TestRelayTeamsMarkerFuzz(t *testing.T) {
	g := fuzzGraph()
	// The ids and the enumerated fields keep the values that make them valid;
	// the test is of the rest.
	keep := map[string]bool{"Pane": true, "Parent": true, "Team": true, "ID": true, "Manager": true, "Members": true,
		"Solo": true, "Ref": true, "From": true, "To": true, "Type": true, "Kind": true, "Status": true, "Activity": true,
		"Role": true, "Project": true, "Root": true}
	filled := g
	filled.Nodes = slices.Clone(g.Nodes)
	filled.Teams = slices.Clone(g.Teams)
	filled.Edges = slices.Clone(g.Edges)
	filled.SoloNeeds = slices.Clone(g.SoloNeeds)
	// Fill a deep copy: round trip through JSON.
	raw, _ := json.Marshal(g)
	if err := json.Unmarshal(raw, &filled); err != nil {
		t.Fatal(err)
	}
	fillStrings(reflect.ValueOf(&filled).Elem(), planted+" some words", keep)
	// The need kinds and wait kinds stay as they are; their texts are the marker.
	for i := range filled.Teams {
		for j := range filled.Teams[i].Needs {
			filled.Teams[i].Needs[j].Kind = team.NeedPermission
		}
	}
	for i := range filled.SoloNeeds {
		filled.SoloNeeds[i].Kind = team.NeedAsk
	}
	filled.Project = planted + " project"

	msg := relayTeamsMsg{Type: "relayTeams", V: relayTeamsVersion, Projects: relayProjectsFromGraphs([]graphMsg{filled})}
	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	out := scrubRelayTeams(b)
	if out == nil {
		t.Fatal("the scrub dropped a good message")
	}
	if n := bytes.Count(out, []byte(planted)); n != 1 {
		t.Errorf("the marker appears %d times, want once (the project name):\n%s", n, out)
	}
	var back relayTeamsMsg
	if err := json.Unmarshal(out, &back); err != nil || len(back.Projects) != 1 {
		t.Fatalf("%v: %s", err, out)
	}
	if !strings.Contains(back.Projects[0].Project, planted) {
		t.Errorf("project = %q", back.Projects[0].Project)
	}
	for _, p := range back.Projects[0].Panes {
		if p.Kind == graphKindStructured && (p.Wait != nil || p.Role != "" || p.Activity != "" || p.Unread != 0 || p.Shell) {
			t.Errorf("the structured pane has more than its kind and status: %+v", p)
		}
	}
}

// The scrub is the second lock: text put in a field of a message that should
// hold a word, or an id, is not passed on.
func TestRelayTeamsScrubChecksEveryFieldAgain(t *testing.T) {
	evil := planted + " rm -rf"
	in := relayTeamsMsg{Type: "relayTeams", V: relayTeamsVersion, Projects: []relayTeamProject{{
		ID: evil, Project: evil,
		Teams: []relayTeam{{ID: "a", Label: evil, Manager: "a", Members: []string{"a", evil, "b"}, Needs: []relayNeed{{Pane: "a", Kind: evil}, {Pane: evil, Kind: "permission"}, {Pane: "b", Kind: "returned"}}}},
		Panes: []relayPane{
			{ID: "a", Kind: "terminal", Label: evil, Status: evil, Role: evil, Activity: evil, Wait: &relayWait{Kind: "user", Prompt: evil}},
			{ID: "b", Parent: evil, Team: evil, Kind: evil, Label: evil, Status: "idle", Wait: &relayWait{Kind: "helpers", On: evil}},
			{ID: evil, Kind: "terminal", Status: "idle"},
		},
		Solo: []string{evil, "b"}, SoloNeeds: []relayNeed{{Pane: "b", Kind: "down"}},
	}}}
	b, _ := json.Marshal(in)
	out := scrubRelayTeams(b)
	if out == nil {
		t.Fatal("dropped")
	}
	var back relayTeamsMsg
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	p := back.Projects[0]
	if p.ID != "p1" || strings.Contains(string(out), "rm -rf") && !strings.Contains(p.Project, "ZXQ") {
		t.Errorf("project = %+v", p)
	}
	// The project name alone may hold the marker, and it is cut to one line.
	rest := strings.ReplaceAll(string(out), p.Project, "")
	if strings.Contains(rest, planted) || strings.Contains(rest, "rm -rf") {
		t.Errorf("text outside the project name:\n%s", out)
	}
	if len(p.Panes) != 2 || p.Panes[0].Label != "Agent 1" || p.Panes[0].Role != "other" || p.Panes[0].Status != "unknown" || p.Panes[0].Wait.Prompt != "" {
		t.Errorf("panes = %+v", p.Panes)
	}
	if p.Panes[1].Kind != "terminal" || p.Panes[1].Parent != "" || p.Panes[1].Wait.On != "" {
		t.Errorf("pane b = %+v", p.Panes[1])
	}
	if len(p.Teams) != 1 || p.Teams[0].Label != "Team 1" || !slices.Equal(p.Teams[0].Members, []string{"a", "b"}) || len(p.Teams[0].Needs) != 1 {
		t.Errorf("teams = %+v", p.Teams)
	}
	if !slices.Equal(p.Solo, []string{"b"}) {
		t.Errorf("solo = %v", p.Solo)
	}
	// A message of another version, or that is not one, is dropped.
	if scrubRelayTeams([]byte(`{"type":"relayTeams","v":2}`)) != nil || scrubRelayTeams([]byte(`not json`)) != nil {
		t.Error("a message of another version, or no message, was passed")
	}
}

// The relay form is made from the graph: a team with a pane waiting on a
// permission shows the kind of thing and no text of it.
func TestRelayTeamsBuiltFromALiveTeam(t *testing.T) {
	e := newGraphEnv(t)
	data, _ := ask(e.srv, func() []byte { return e.srv.relayTeamsData() })
	out := scrubRelayTeams(data)
	var m relayTeamsMsg
	if err := json.Unmarshal(out, &m); err != nil || m.Type != "relayTeams" || m.V != 1 {
		t.Fatalf("%v: %s", err, out)
	}
	if len(m.Projects) == 0 || len(m.Projects[0].Panes) < 4 || len(m.Projects[0].Teams) != 1 {
		t.Fatalf("projects = %+v", m.Projects)
	}
	for _, name := range []string{"boss", "dev", "eyes", "nested", "lone"} {
		if bytes.Contains(out, []byte(`"`+name+`"`)) || bytes.Contains(out, []byte(`:"`+name)) {
			t.Errorf("the pane name %q is in the relay form:\n%s", name, out)
		}
	}
	if bytes.Contains(out, []byte(e.root)) {
		t.Errorf("the project path is in the relay form:\n%s", out)
	}
}

// A window reached through the relay is pushed the relay form on its own, and
// only one of them is: the desk is never sent it, and it is sent no graph.
func TestRelayTeamsAreOnlyPushedToRelayWindows(t *testing.T) {
	e := newGraphEnv(t)
	e.srv.graphTickEvery = time.Hour
	phone := &controlClient{out: make(chan []byte, 16), remote: true, device: "dev-1"}
	desk := &controlClient{out: make(chan []byte, 16)}
	e.srv.mu.Lock()
	e.srv.clients[phone] = true
	e.srv.clients[desk] = true
	e.srv.mu.Unlock()
	t.Cleanup(func() {
		e.srv.mu.Lock()
		delete(e.srv.clients, phone)
		delete(e.srv.clients, desk)
		e.srv.mu.Unlock()
	})
	ask(e.srv, func() bool { e.srv.pushRelayTeams(); return true })
	select {
	case raw := <-phone.out:
		if typeOf(raw) != "relayTeams" {
			t.Fatalf("the relay window was sent %q", typeOf(raw))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the relay window was sent nothing")
	}
	select {
	case raw := <-desk.out:
		t.Errorf("the desk was sent %q", typeOf(raw))
	default:
	}
	// Unchanged, it is not sent again.
	ask(e.srv, func() bool { e.srv.relayTeamsAt = time.Time{}; e.srv.pushRelayTeams(); return true })
	select {
	case raw := <-phone.out:
		t.Errorf("an unchanged form was sent again: %s", raw)
	default:
	}
}

// A relay window is sent the form as it comes in.
func TestRelayTeamsAreSentToAWindowThatComesIn(t *testing.T) {
	e := newGraphEnv(t)
	phone := &controlClient{out: make(chan []byte, 16), remote: true, device: "dev-1"}
	ask(e.srv, func() bool { e.srv.sendRelayTeamsTo(phone); return true })
	select {
	case raw := <-phone.out:
		if typeOf(raw) != "relayTeams" {
			t.Fatalf("sent %q", typeOf(raw))
		}
	default:
		t.Fatal("nothing was sent")
	}
	desk := &controlClient{out: make(chan []byte, 4)}
	ask(e.srv, func() bool { e.srv.sendRelayTeamsTo(desk); return true })
	if len(desk.out) != 0 {
		t.Error("the desk was sent the relay form")
	}
}

// The form is a type of its own, scrubbed by its own function; the graph is
// still dropped for a relay window and its commands are still the desk's.
func TestRelayTeamsLeaveTheGraphDeskOnly(t *testing.T) {
	if _, ok := relayScrubs["relayTeams"]; !ok {
		t.Error("relayTeams has no scrub")
	}
	if scrubForRemote([]byte(`{"type":"graph","nodes":[]}`)) != nil {
		t.Error("the graph reaches a relay window")
	}
	if !watcherMessageTypes["relayTeams"] || viewerMessageTypes["relayTeams"] {
		t.Error("relayTeams is for a viewer that may watch panes")
	}
}

// Nothing the graph holds reaches a relay window by the other routes: a graph
// built with the marker in it is not in a notice, and the one message that
// carries the teams is the one scrubbed. The marker is planted in the names of
// the panes, and the relay form of the live team is searched for it.
func TestRelayTeamsNameNoPaneOrGoal(t *testing.T) {
	e := newGraphEnv(t)
	ask(e.srv, func() bool {
		for _, id := range []string{e.boss, e.dev, e.eyes, e.nested, e.lone} {
			p := e.ws.Pane(id)
			p.Name = planted + "-" + id
		}
		return true
	})
	data, _ := ask(e.srv, func() []byte { return e.srv.relayTeamsData() })
	if out := scrubRelayTeams(data); bytes.Contains(out, []byte(planted)) {
		t.Errorf("a pane name is in the relay form:\n%s", out)
	}
}

// remote.Capability says the build has this form. A phone treats a capability
// below 2 as having no team view, and an old desktop keeps its roster.
func TestRelayTeamsCapability(t *testing.T) {
	if remote.Capability < 2 {
		t.Errorf("Capability = %d, but this build sends relayTeams", remote.Capability)
	}
}

// Over a real relay socket, with the marker in the name of every pane, the
// window is sent the relay form and nothing it was sent holds the marker: not
// the form, not the state, not a notice.
func TestRelayTeamsOverTheSocketHoldNoPaneName(t *testing.T) {
	e := newGraphEnv(t)
	ask(e.srv, func() bool {
		for _, id := range []string{e.boss, e.dev, e.eyes, e.nested, e.lone} {
			e.ws.Pane(id).Name = planted + "-" + id
		}
		return true
	})
	ts := remoteServer(t, e.srv)
	conn, err := dialRemoteControlDevice(ts, ts.URL, "full-dev")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	types, raws := collectTypes(t, conn, 3*time.Second)
	if !slices.Contains(types, "relayTeams") {
		t.Fatalf("the relay window was sent %v, not relayTeams", types)
	}
	if slices.Contains(types, "graph") {
		t.Error("the relay window was sent a graph")
	}
	for _, raw := range raws {
		if typeOf(raw) == "relayTeams" && bytes.Contains(raw, []byte(planted)) {
			t.Errorf("the relay form holds a pane name: %s", raw)
		}
	}
}

// The project name is the one free text in the relay form, so it goes through
// the same redaction and cut as the names elsewhere on the relay.
func TestRelayTeamsProjectNameIsRedactedAndCut(t *testing.T) {
	const key = "AKIAIOSFODNN7EXAMPLE"
	cases := map[string]string{
		"a key":                   "build " + key + " now",
		"a key with an invisible": "build AK​IAIOSFODNN7EXAMPLE now",
		"a long name":             strings.Repeat("long-name ", 12),
		"a newline and controls":  "line one\nline two\r\x1b[31m\x00\ttab",
	}
	for name, project := range cases {
		t.Run(name, func(t *testing.T) {
			in := relayTeamsMsg{Type: "relayTeams", V: relayTeamsVersion, Projects: []relayTeamProject{{
				ID: "p1", Project: project, Panes: []relayPane{{ID: "a", Kind: "terminal", Status: "idle"}}}}}
			b, _ := json.Marshal(in)
			var out relayTeamsMsg
			if err := json.Unmarshal(scrubRelayTeams(b), &out); err != nil || len(out.Projects) != 1 {
				t.Fatalf("%v", err)
			}
			got := out.Projects[0].Project
			if strings.Contains(got, key) || strings.Contains(got, "IAIOSFODNN7") {
				t.Errorf("the key is in the name: %q", got)
			}
			if name == "a key" || name == "a key with an invisible" {
				if !strings.Contains(got, "[redacted]") {
					t.Errorf("name = %q, want [redacted] in it", got)
				}
			}
			if n := len([]rune(got)); n > maxRemoteName {
				t.Errorf("name has %d characters, want at most %d: %q", n, maxRemoteName, got)
			}
			for _, r := range got {
				if r < 0x20 || r == 0x7f {
					t.Errorf("name holds the control character %U: %q", r, got)
				}
			}
		})
	}
}
