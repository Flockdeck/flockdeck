package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/acp/acptest"
	"github.com/jmwri/flockdeck/internal/structured"
)

// structuredSet changes the mode, model or a setting of one structured pane,
// and only to what the pane has pushed.

func (e *structuredEnv) set(t *testing.T, c *controlClient, id, kind, setting, value string) teamResultMsg {
	t.Helper()
	e.srv.handleCommand(c, command{Cmd: "structuredSet", ID: id, Kind: kind, Setting: setting, Value: value, Req: "s1"})
	deadline := time.After(10 * time.Second)
	for {
		select {
		case raw := <-c.out:
			if typeOf(raw) == "teamResult" {
				var res teamResultMsg
				if err := json.Unmarshal(raw, &res); err != nil {
					t.Fatal(err)
				}
				return res
			}
		case <-deadline:
			t.Fatal("structuredSet was not answered")
		}
	}
}

func (e *structuredEnv) session(t *testing.T) structured.Snapshot {
	t.Helper()
	sn, _ := ask(e.srv, func() structured.Snapshot { return e.ws.StructuredRuntime(e.child).Snapshot() })
	return sn
}

func (e *structuredEnv) waitSession(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for e.session(t).Session.Modes == nil {
		if time.Now().After(deadline) {
			t.Fatal("the agent never listed its modes")
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestStructuredSetChangesTheModeAndModel(t *testing.T) {
	log := filepath.Join(t.TempDir(), "set.log")
	e := newStructuredEnvArgs(t, "plain", acptest.ArgShape+"modes", acptest.ArgSetLog+log)
	e.waitSession(t)
	if res := e.set(t, e.c, e.child, "mode", "", "yolo"); !res.OK || res.Op != "structuredSet" || res.Req != "s1" {
		t.Fatalf("mode: %+v", res)
	}
	if res := e.set(t, e.c, e.child, "model", "", "gem-flash"); !res.OK {
		t.Fatalf("model: %+v", res)
	}
	s := e.session(t).Session
	if s.Modes.Current != "yolo" || s.Models.Current != "gem-flash" {
		t.Errorf("mode %q model %q", s.Modes.Current, s.Models.Current)
	}
	// The window is told by the next push too.
	raw, _ := json.Marshal(buildStructured(e.child, e.session(t)))
	if !strings.Contains(string(raw), `"current":"yolo"`) {
		t.Errorf("the push does not carry the new mode: %s", raw)
	}
}

func TestStructuredSetRefusesWhatIsNotOnOfferWithoutAskingTheAgent(t *testing.T) {
	log := filepath.Join(t.TempDir(), "set.log")
	e := newStructuredEnvArgs(t, "plain", acptest.ArgShape+"modes", acptest.ArgSetLog+log)
	e.waitSession(t)
	for _, c := range [][3]string{{"mode", "", "bypass"}, {"model", "", "x"}, {"config", "model", "big"}} {
		if res := e.set(t, e.c, e.child, c[0], c[1], c[2]); res.OK || res.Error != structuredNoSetting {
			t.Errorf("%v: %+v", c, res)
		}
	}
	if res := e.set(t, e.c, e.child, "effort", "", "high"); res.OK || res.Error != structuredSetKind {
		t.Errorf("unknown kind: %+v", res)
	}
	if res := e.set(t, e.c, e.dev, "mode", "", "yolo"); res.OK || res.Error != structuredNotPane {
		t.Errorf("a terminal pane: %+v", res)
	}
	if b, _ := os.ReadFile(log); len(b) != 0 {
		t.Errorf("the agent was sent %q", b)
	}
}

func TestStructuredSetTellsNothingTheAgentSaid(t *testing.T) {
	e := newStructuredEnvArgs(t, "plain", acptest.ArgShape+"modes", acptest.ArgSet+"refuse")
	e.waitSession(t)
	res := e.set(t, e.c, e.child, "mode", "", "yolo")
	if res.OK || res.Error != structuredSetFailed || strings.Contains(res.Error, "SECRET") {
		t.Errorf("answer = %+v, want the fixed refusal", res)
	}
}

func TestStructuredSetIsRefusedToTheRelayAndTheTableKeepsViewersOut(t *testing.T) {
	e := newStructuredEnvArgs(t, "plain", acptest.ArgShape+"modes")
	e.waitSession(t)
	remote := &controlClient{out: make(chan []byte, 8), remote: true, device: "full-dev"}
	if res := e.set(t, remote, e.child, "mode", "", "yolo"); res.OK || res.Error != structuredDeskOnly {
		t.Fatalf("answer = %+v", res)
	}
	if m := e.session(t).Session.Modes.Current; m != "default" {
		t.Errorf("the relay's refused change took: mode %q", m)
	}
	if commandAccess["structuredSet"] != accessFull {
		t.Error("structuredSet is not accessFull, so a viewer could send it")
	}
}
