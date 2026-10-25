package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/session"
)

// The graph fixtures of api/fixtures, recorded from a real server: panes and a
// board are put in the states of graphFixtureSpecs and the push is read with the
// same code a window's graphSnapshot runs, then made repeatable (see
// normalizeRecording) and compared with the file. testdata/graph-fixtures holds
// the same scenarios built without a server; these are what the server wrote.
//
// What a test cannot make a real server do is left out and said in
// api/fixtures/README.md: a helper that ends in error (a real exit status), a
// pair of helpers the conflict radar lists (a git checkout), per-helper spend and
// ages that differ from helper to helper. Fixture 06 needs all of these and is
// built, not recorded.
//
// API_FIXTURES_UPDATE=1 go test -run TestAPIFixturesAreCurrent ./internal/server
// writes the files again.

// recordedFixtures are the specs a real server can record.
var recordedFixtures = []string{
	"01-six-helpers-one-permission",
	"02-eight-helpers-permission-and-question",
	"03-nested-helper-question-and-returned-task",
	"04-eight-helpers-three-waiting",
	"05-six-helpers-undelivered-message",
}

const recordingAge = 5 * time.Minute

// recordingRound is what ages are rounded to, so a machine that stalls for a
// minute or two between setting a status and reading the push (ages of five
// minutes and a little) records the same file.
const recordingRound = 5 * time.Minute

var rfc3339 = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:.]+(Z|[+-][0-9:]+)$`)

// recordingStall is a hook for proving the recording does not depend on how long
// the machine takes: a test of it sleeps here, after the panes are set up and
// before the startup shell's status is set and the push is read.
var recordingStall = func() {}

// recordGraphFixture runs a spec against a real server and returns the push.
func recordGraphFixture(t *testing.T, spec fxSpec) []byte {
	t.Helper()
	srv, ws := newTestServer(t)
	hookSrv := ws.HookServer()
	api, token := hookSrv.BaseURL(), hookSrv.Token()
	root := ws.ActiveRoot()

	// The shell the server starts with settles from starting to idle by itself
	// after three quiet seconds (session.quietBeforeIdle), which a loaded machine
	// can pass before the push is read. Setting its status is what stops that
	// timer, so it is set again just before the read.
	var startup []string
	if tab := ws.CurrentTab(); tab != nil {
		startup = tab.Tree.Panes()
	}

	ids := map[string]string{fxManagerID: agentPane(t, srv, ws, "manager", "manager")}
	for _, h := range spec.helpers {
		ids[h.name] = agentPane(t, srv, ws, h.name, "worker")
	}
	ask(srv, func() bool {
		for _, h := range spec.helpers {
			parent := fxManagerID
			if h.parent != "" {
				parent = h.parent
			}
			ws.Pane(ids[h.name]).Parent = ids[parent]
			ws.Pane(ids[h.name]).Name = h.name
		}
		ws.Pane(ids[fxManagerID]).Name = "manager"
		return true
	})

	task := func(pane string, req hooks.TaskRequest) {
		t.Helper()
		if _, err := hooks.Task(api, token, pane, req); err != nil {
			t.Fatalf("%s %+v: %v", spec.name, req, err)
		}
	}
	status := func(name string, s session.Status, tool, input string) {
		ask(srv, func() bool {
			p := ws.Pane(ids[name])
			if input == "" {
				p.Sess.SetStatus(s, tool)
			} else {
				p.Sess.SetStatusFull(s, tool, input)
			}
			return true
		})
	}
	// onTask gives a helper a task and takes it as far as the state needs.
	onTask := func(h fxHelper, id string, to ...string) {
		mgr, me := ids[fxManagerID], ids[h.name]
		task(mgr, hooks.TaskRequest{Op: hooks.TaskAdd, Title: h.task})
		task(mgr, hooks.TaskRequest{Op: hooks.TaskAssign, ID: id, Target: me})
		for _, s := range to {
			req := hooks.TaskRequest{Op: hooks.TaskStatus, ID: id, State: s}
			actor := me
			if s == "in-progress" && h.state == fxReturned && len(to) > 2 {
				actor = mgr
			}
			if s == "blocked" || (s == "in-progress" && h.state == fxReturned && actor == mgr) {
				req.Note = h.text
			}
			task(actor, req)
		}
	}

	n := 0
	for _, h := range spec.helpers {
		switch h.state {
		case fxWorking:
			status(h.name, session.StatusWorking, h.text, "")
		case fxExited, fxCrashed:
			status(h.name, session.StatusExited, "", "")
		case fxPerm:
			in, _ := json.Marshal(map[string]string{"command": h.text})
			status(h.name, session.StatusWaiting, "Bash", string(in))
		case fxAsk:
			in, _ := json.Marshal(map[string]any{"questions": []map[string]string{{"question": h.text}}})
			status(h.name, session.StatusWaiting, "AskUserQuestion", string(in))
		case fxInReview:
			n++
			onTask(h, fmt.Sprintf("T%d", n), "in-progress", "in-review")
		case fxBlocked:
			n++
			onTask(h, fmt.Sprintf("T%d", n), "in-progress", "blocked")
		case fxReturned:
			n++
			onTask(h, fmt.Sprintf("T%d", n), "in-progress", "in-review", "in-progress")
			status(h.name, session.StatusWorking, "Reading the review note", "")
		}
	}
	if spec.managerIdle {
		status(fxManagerID, session.StatusIdle, "", "")
	}
	if m := spec.message; m != nil {
		to := fxManagerID
		if m.to != "" {
			to = m.to
		}
		if _, err := hooks.Msg(api, token, ids[m.from], ids[to], m.text); err != nil {
			t.Fatal(err)
		}
	}

	recordingStall()
	ask(srv, func() bool {
		for _, id := range startup {
			if p := ws.Pane(id); p != nil {
				p.Sess.SetStatus(session.StatusStarting, "")
			}
		}
		return true
	})
	at := time.Now().Add(recordingAge)
	ask(srv, func() bool { srv.graphNow = func() time.Time { return at }; return true })
	c := &controlClient{out: make(chan []byte, 8)}
	srv.graphCommand(c, command{Cmd: "graphSnapshot", Root: root})
	var raw []byte
	select {
	case raw = <-c.out:
	case <-time.After(10 * time.Second):
		t.Fatal("the server did not answer graphSnapshot")
	}
	return normalizeRecording(t, spec, raw, ids, root, at)
}

// normalizeRecording makes a recording the same on every run: pane ids become
// the fixture's ids, times become their age before fxNow, and the project
// becomes a fixed path. Ages are rounded to five minutes, and a real server reads
// them from the clock the panes were set up on, so every recording's statuses are
// recordingAge old.
func normalizeRecording(t *testing.T, spec fxSpec, raw []byte, ids map[string]string, root string, at time.Time) []byte {
	t.Helper()
	byID := map[string]string{}
	for name, id := range ids {
		byID[id] = fxID(spec, name)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	// The panes the test did not make, the shell the server started with, are
	// named in the order the push lists them.
	if m, ok := v.(map[string]any); ok {
		extra := 0
		nodes, _ := m["nodes"].([]any)
		for _, n := range nodes {
			if pane, _ := n.(map[string]any)["pane"].(string); pane != "" && byID[pane] == "" {
				extra++
				byID[pane] = fxID(spec, fmt.Sprintf("shell-%d", extra))
			}
		}
	}
	var walk func(v any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case map[string]any:
			for k, e := range x {
				x[k] = walk(e)
			}
			return x
		case []any:
			for i, e := range x {
				x[i] = walk(e)
			}
			return x
		case string:
			if id, ok := byID[x]; ok {
				return id
			}
			if head, tail, ok := strings.Cut(x, ":"); ok {
				if id, ok := byID[tail]; ok {
					return head + ":" + id
				}
			}
			if x == root {
				return "/work/project"
			}
			if x == filepath.Base(root) {
				return "project"
			}
			if rfc3339.MatchString(x) {
				if when, err := time.Parse(time.RFC3339Nano, x); err == nil {
					return fxNow.Add(-at.Sub(when).Round(recordingRound)).Format(time.RFC3339)
				}
			}
		}
		return v
	}
	v = walk(v)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// firstDifference names the first line two recordings differ at, so a failure
// that comes from the machine's timing says what moved.
func firstDifference(want, got []byte) string {
	w, g := strings.Split(string(want), "\n"), strings.Split(string(got), "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		var a, b string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			b = g[i]
		}
		if a != b {
			return fmt.Sprintf("line %d: file has %q, server wrote %q", i+1, strings.TrimSpace(a), strings.TrimSpace(b))
		}
	}
	return "no line differs"
}

func specNamed(t *testing.T, name string) fxSpec {
	t.Helper()
	for _, s := range graphFixtureSpecs {
		if s.name == name {
			return s
		}
	}
	t.Fatalf("no fixture spec %s", name)
	return fxSpec{}
}

func TestAPIFixturesAreCurrent(t *testing.T) {
	dir := filepath.Join("..", "..", "api", "fixtures")
	for _, name := range recordedFixtures {
		spec := specNamed(t, name)
		t.Run(name, func(t *testing.T) {
			got := recordGraphFixture(t, spec)
			if errs := loadGraphSchema(t).Validate(got); len(errs) != 0 {
				t.Fatalf("the recording does not match the schema: %v", errs)
			}
			path := filepath.Join(dir, name+".json")
			if os.Getenv("API_FIXTURES_UPDATE") != "" {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v; API_FIXTURES_UPDATE=1 writes it", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("%s differs from what the server writes; API_FIXTURES_UPDATE=1 go test -run TestAPIFixturesAreCurrent ./internal/server writes it again\n%s", path, firstDifference(want, got))
			}
		})
	}
}

// A machine that stalls between setting the panes up and reading the push, for
// longer than a pane takes to settle on its own, records the same file for every
// fixture.
func TestAPIFixtureSurvivesAStall(t *testing.T) {
	old := recordingStall
	t.Cleanup(func() { recordingStall = old })
	recordingStall = func() { time.Sleep(4 * time.Second) }
	for _, name := range recordedFixtures {
		spec := specNamed(t, name)
		t.Run(name, func(t *testing.T) {
			got := recordGraphFixture(t, spec)
			want, err := os.ReadFile(filepath.Join("..", "..", "api", "fixtures", spec.name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("a recording made after a 4 s stall differs from %s", spec.name)
				_ = os.WriteFile(filepath.Join(os.TempDir(), "stall-"+spec.name+".json"), got, 0o644)
			}
		})
	}
}
