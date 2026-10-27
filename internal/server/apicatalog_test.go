package server

import (
	"encoding/json"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"
)

// api/catalog.json lists every command a client may send and every message the
// server writes, with the decisions the server makes about each: who may send a
// command, and what a relay window is sent of a message. It is generated from
// the tables the server decides by (commandAccess, remoteTextCommands,
// relayScrubs, relayPass, relayDeskOnly, viewerMessageTypes and
// watcherMessageTypes), so it cannot say something the code does not. A client
// in another module reads this file, not Go.
//
// TestAPICatalogIsCurrent fails when the file differs from the tables;
// API_CATALOG_UPDATE=1 go test -run TestAPICatalogIsCurrent ./internal/server
// writes it again.

const apiCatalogPath = "../../api/catalog.json"

// apiVersion is the contract's version. A change that removes or alters a
// command or message raises the major; one that adds raises the minor.
const (
	apiMajor = 1
	apiMinor = 16
)

// addedIn names the minor version a command arrived in; any other is 1.0.
var addedIn = map[string]string{"uiPrefs": "1.1", "paneName": "1.4"}

// plannedMessages are the messages of the changes contract (docs/changes-api.md):
// the ones the server does not send yet, and changesResult, which it sends today only
// as a refusal. plannedCommands are its commands. Both are in the catalog with the
// status planned, since 1.3, until they are built.
var plannedMessages = map[string]bool{"changesResult": true}
var plannedCommands = map[string]bool{}

func init() {
	for _, n := range changesMessages {
		plannedMessages[n] = true
	}
	for _, n := range changesCommands {
		plannedCommands[n] = !slices.Contains(changesBuilt, n)
	}
	// What is built arrived with the changesTeam cap, in 1.5.
	for _, n := range slices.Concat(changesBuilt, []string{"changesTeam", "changesFiles", "diffHunks", "diffContext"}) {
		addedIn[n] = "1.5"
	}
	// The check file's confirmation came later, with the pin (1.8).
	addedIn["confirmChecks"], addedIn["changesChecks"] = "1.8", "1.8"
	// Comments came with the changesComments cap (1.9).
	addedIn["commentAdd"], addedIn["commentResolve"], addedIn["commentReopen"] = "1.9", "1.9", "1.9"
	// The gate came with the changesGate cap (1.10).
	addedIn["approveMerge"], addedIn["unapproveMerge"], addedIn["mergeBranch"] = "1.10", "1.10", "1.10"
	// Changing a structured pane's mode, model or settings came with structuredSettings (1.16).
	addedIn["structuredSet"] = "1.16"
}

func sinceOf(name string) string {
	if v, ok := addedIn[name]; ok {
		return v
	}
	if plannedCommands[name] || plannedMessages[name] {
		return "1.3"
	}
	return "1.0"
}

func statusOf(name string) string {
	if plannedCommands[name] || plannedMessages[name] {
		return "planned"
	}
	return "stable"
}

type catalogCommand struct {
	Name string `json:"name"`
	// Access is who may send it: "full" (a window on this machine or a full
	// device), "watcher" (also a viewer the desk let watch panes) or "viewer"
	// (any viewer).
	Access string `json:"access"`
	// RelayRefused is set when a relay window is refused the command whatever its
	// role, because the answer would carry conversation text.
	RelayRefused bool   `json:"relayRefused,omitempty"`
	Since        string `json:"since"`
	Status       string `json:"status"`
}

type catalogMessage struct {
	Name string `json:"name"`
	// Relay is what a relay window is sent: "scrub" (the message rewritten, or
	// dropped, by a scrub), "pass" (as it is) or "deskOnly" (never).
	Relay string `json:"relay"`
	// Why says, for a message sent as it is or never, why that is safe.
	Why string `json:"why,omitempty"`
	// Viewers is who of the viewing devices is sent it: "" (nobody), "viewer"
	// or "watcher".
	Viewers string `json:"viewers,omitempty"`
	Since   string `json:"since"`
	Status  string `json:"status"`
}

// catalogFrame is a frame a window sends that is not a command (clientFrames).
type catalogFrame struct {
	Name string `json:"name"`
	// Direction is who sends it: always "client" today.
	Direction string `json:"direction"`
	// Does says what the frame is for.
	Does   string `json:"does"`
	Since  string `json:"since"`
	Status string `json:"status"`
}

type catalogFile struct {
	API struct {
		Major int `json:"major"`
		Minor int `json:"minor"`
	} `json:"api"`
	Frames     []catalogFrame    `json:"frames"`
	Deprecated []deprecatedField `json:"deprecated"`
	Commands   []catalogCommand  `json:"commands"`
	Messages   []catalogMessage  `json:"messages"`
}

// buildAPICatalog is the catalog as the tables say it.
func buildAPICatalog(t *testing.T) catalogFile {
	t.Helper()
	var c catalogFile
	c.API.Major, c.API.Minor = apiMajor, apiMinor
	c.Deprecated = deprecatedFields
	var frames []string
	for name := range clientFrames {
		frames = append(frames, name)
	}
	sort.Strings(frames)
	for _, name := range frames {
		c.Frames = append(c.Frames, catalogFrame{Name: name, Direction: "client", Does: clientFrames[name], Since: sinceOf(name), Status: "stable"})
	}
	access := map[cmdAccess]string{accessFull: "full", accessWatcher: "watcher", accessViewer: "viewer"}
	names := handleCommandCases(t)
	sort.Strings(names)
	for _, name := range names {
		c.Commands = append(c.Commands, catalogCommand{Name: name, Access: access[commandAccess[name]],
			RelayRefused: remoteTextCommands[name], Since: sinceOf(name), Status: statusOf(name)})
	}
	sent := sentMessageTypes(t)
	var types []string
	for typ := range sent {
		types = append(types, typ)
	}
	sort.Strings(types)
	for _, typ := range types {
		m := catalogMessage{Name: typ, Since: sinceOf(typ), Status: statusOf(typ)}
		switch _, scrubbed := relayScrubs[typ]; {
		case scrubbed:
			m.Relay = "scrub"
		case relayPass[typ] != "":
			m.Relay, m.Why = "pass", relayPass[typ]
		case relayDeskOnly[typ] != "":
			m.Relay, m.Why = "deskOnly", relayDeskOnly[typ]
		}
		switch {
		case viewerMessageTypes[typ]:
			m.Viewers = "viewer"
		case watcherMessageTypes[typ]:
			m.Viewers = "watcher"
		}
		c.Messages = append(c.Messages, m)
	}
	// The messages of the contract that are not sent yet are never sent to a relay
	// window: one the server does not know is dropped for it.
	for _, typ := range changesMessages {
		c.Messages = append(c.Messages, catalogMessage{Name: typ, Relay: "deskOnly", Why: "the changes view is refused to a relay window (docs/changes-api.md, section 9)",
			Since: sinceOf(typ), Status: statusOf(typ)})
	}
	sort.Slice(c.Messages, func(i, j int) bool { return c.Messages[i].Name < c.Messages[j].Name })
	return c
}

func TestAPICatalogIsCurrent(t *testing.T) {
	want, err := json.MarshalIndent(buildAPICatalog(t), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, '\n')
	if os.Getenv("API_CATALOG_UPDATE") != "" {
		if err := os.WriteFile(apiCatalogPath, want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(apiCatalogPath)
	if err != nil {
		t.Fatalf("%v (API_CATALOG_UPDATE=1 writes the file)", err)
	}
	if strings.ReplaceAll(string(got), "\r\n", "\n") != string(want) {
		t.Error("api/catalog.json is out of date (API_CATALOG_UPDATE=1 go test -run TestAPICatalogIsCurrent ./internal/server writes it)")
	}
}

// The file is read back on its own terms, not through the builder: it names
// every command handleCommand handles with the access commandAccess gives it,
// every message the server writes with the relay handling the registries give
// it, and nothing else.
func TestAPICatalogAgreesWithTheServer(t *testing.T) {
	raw, err := os.ReadFile(apiCatalogPath)
	if err != nil {
		t.Fatal(err)
	}
	var c catalogFile
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	if c.API.Major != apiMajor || c.API.Minor != apiMinor {
		t.Errorf("the catalog is version %d.%d, the server %d.%d", c.API.Major, c.API.Minor, apiMajor, apiMinor)
	}
	if len(c.Commands) < 150 {
		t.Fatalf("%d commands in the catalog; the file is probably wrong", len(c.Commands))
	}
	for _, f := range c.Frames {
		if clientFrames[f.Name] == "" {
			t.Errorf("the catalog has the frame %q, which the server does not read", f.Name)
		}
	}
	if len(c.Frames) != len(clientFrames) {
		t.Errorf("the catalog has %d frames, the server reads %d", len(c.Frames), len(clientFrames))
	}
	cases := handleCommandCases(t)
	var inFile []string
	for _, cmd := range c.Commands {
		inFile = append(inFile, cmd.Name)
		a, ok := commandAccess[cmd.Name]
		if !ok {
			t.Errorf("the catalog has %q, which commandAccess does not know", cmd.Name)
			continue
		}
		if want := map[cmdAccess]string{accessFull: "full", accessWatcher: "watcher", accessViewer: "viewer"}[a]; cmd.Access != want {
			t.Errorf("%s: the catalog says access %q, commandAccess says %q", cmd.Name, cmd.Access, want)
		}
		if cmd.RelayRefused != remoteTextCommands[cmd.Name] {
			t.Errorf("%s: relayRefused is %v in the catalog, %v in remoteTextCommands", cmd.Name, cmd.RelayRefused, remoteTextCommands[cmd.Name])
		}
	}
	sort.Strings(inFile)
	sort.Strings(cases)
	if !slices.Equal(inFile, cases) {
		t.Errorf("the catalog's commands are not the ones handleCommand handles")
	}
	sent := sentMessageTypes(t)
	seen := map[string]bool{}
	unsent := map[string]bool{}
	for _, n := range changesMessages {
		unsent[n] = true
	}
	for _, m := range c.Messages {
		seen[m.Name] = true
		if unsent[m.Name] {
			if m.Status != "planned" || m.Relay != "deskOnly" || sent[m.Name] {
				t.Errorf("%s: a planned message is status planned and desk only until it is sent; got %+v, sent %v", m.Name, m, sent[m.Name])
			}
			continue
		}
		if !sent[m.Name] {
			t.Errorf("the catalog has the message %q, which the server no longer sends", m.Name)
		}
		_, scrubbed := relayScrubs[m.Name]
		_, passed := relayPass[m.Name]
		_, desk := relayDeskOnly[m.Name]
		got := map[string]bool{"scrub": scrubbed, "pass": passed, "deskOnly": desk}
		if !got[m.Relay] {
			t.Errorf("%s: the catalog says relay %q, which the registries do not", m.Name, m.Relay)
		}
	}
	for typ := range sent {
		if !seen[typ] {
			t.Errorf("the server sends %q, which the catalog does not list", typ)
		}
	}
}
