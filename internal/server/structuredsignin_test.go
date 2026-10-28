package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/acp/acptest"
	"github.com/jmwri/flockdeck/internal/record/schemacheck"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/structured"
)

func TestSignInBlockCarriesOnlyACheckedAddressAndAState(t *testing.T) {
	cases := []struct {
		name string
		sn   structured.Snapshot
		want string
	}{
		{"none", structured.Snapshot{Phase: structured.PhaseReady}, ""},
		{"available", structured.Snapshot{Phase: structured.PhaseSetup, CanSignIn: true}, `"signIn":{"available":true}`},
		{"waiting", structured.Snapshot{Phase: structured.PhaseSetup, CanSignIn: true,
			SignIn: &structured.SignIn{State: structured.SignInWaiting, URL: "https://claude.ai/oauth/authorize?a=b"}},
			`"signIn":{"available":true,"state":"waiting","url":"https://claude.ai/oauth/authorize?a=b"}`},
		{"http address dropped", structured.Snapshot{Phase: structured.PhaseSetup, CanSignIn: true,
			SignIn: &structured.SignIn{State: structured.SignInWaiting, URL: "http://claude.ai/x"}},
			`"signIn":{"available":true,"state":"waiting"}`},
		{"credentials dropped", structured.Snapshot{Phase: structured.PhaseSetup, CanSignIn: true,
			SignIn: &structured.SignIn{State: structured.SignInWaiting, URL: "https://u:p@claude.ai/x"}},
			`"signIn":{"available":true,"state":"waiting"}`},
		{"failed", structured.Snapshot{Phase: structured.PhaseSetup, CanSignIn: true, SignIn: &structured.SignIn{State: structured.SignInFailed}},
			`"signIn":{"available":true,"state":"failed"}`},
	}
	schema, err := schemacheck.Load(structuredSchemaPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		raw, err := json.Marshal(buildStructured("p1", c.sn))
		if err != nil {
			t.Fatal(err)
		}
		got := string(raw)
		if c.want == "" {
			if strings.Contains(got, "signIn") {
				t.Errorf("%s: %s", c.name, got)
			}
		} else if !strings.Contains(got, c.want) {
			t.Errorf("%s: want %s in %s", c.name, c.want, got)
		}
		checkStructured(t, c.name, schema, raw)
	}
}

func TestStructuredSignInRefusesAPaneThatIsNotWaitingForALogin(t *testing.T) {
	e := newStructuredEnv(t, "plain")
	e.status(t, session.StatusIdle)
	for _, cmd := range []string{"structuredSignIn", "structuredSignInCancel"} {
		raw := structuredReply(t, e.srv, e.c, command{Cmd: cmd, Req: "s1", ID: e.child}, "teamResult")
		var res teamResultMsg
		if err := json.Unmarshal(raw, &res); err != nil {
			t.Fatal(err)
		}
		want := structuredSignInNone
		if cmd == "structuredSignInCancel" {
			want = structuredSignInEnd
		}
		if res.OK || res.Error != want {
			t.Errorf("%s: %+v", cmd, res)
		}
	}
	raw := structuredReply(t, e.srv, e.c, command{Cmd: "structuredSignIn", Req: "s2", ID: "nope"}, "teamResult")
	if !strings.Contains(string(raw), structuredNotPane) {
		t.Errorf("a pane that is not there: %s", raw)
	}
}

// A sign-in that ends well restarts the pane: the runtime that waited for the
// login is closed and another one holds the pane.
func TestAFinishedSignInRestartsThePane(t *testing.T) {
	e := newStructuredEnvArgs(t, "plain", acptest.ArgAuth+"prompt", acptest.ArgSignIn+"ok")
	old, _ := ask(e.srv, func() *structured.Runtime { return e.ws.StructuredRuntime(e.child) })
	if old == nil {
		t.Fatal("no runtime")
	}
	if err := old.Send("plain"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for old.Snapshot().Phase != structured.PhaseSetup {
		if time.Now().After(deadline) {
			t.Fatalf("phase %q, want the setup state", old.Snapshot().Phase)
		}
		time.Sleep(20 * time.Millisecond)
	}
	raw := structuredReply(t, e.srv, e.c, command{Cmd: "structuredSignIn", Req: "r1", ID: e.child}, "teamResult")
	var res teamResultMsg
	if err := json.Unmarshal(raw, &res); err != nil || !res.OK {
		t.Fatalf("structuredSignIn: %s", raw)
	}
	for {
		cur, _ := ask(e.srv, func() *structured.Runtime { return e.ws.StructuredRuntime(e.child) })
		if cur != nil && cur != old {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the pane was not restarted after the sign-in ended")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A sign-in that fails, or is cancelled, leaves the pane as it was.
func TestAFailedSignInLeavesThePane(t *testing.T) {
	e := newStructuredEnvArgs(t, "plain", acptest.ArgAuth+"prompt", acptest.ArgSignIn+"fail")
	old, _ := ask(e.srv, func() *structured.Runtime { return e.ws.StructuredRuntime(e.child) })
	_ = old.Send("plain")
	deadline := time.Now().Add(30 * time.Second)
	for old.Snapshot().Phase != structured.PhaseSetup {
		if time.Now().After(deadline) {
			t.Fatal("no setup state")
		}
		time.Sleep(20 * time.Millisecond)
	}
	structuredReply(t, e.srv, e.c, command{Cmd: "structuredSignIn", Req: "r1", ID: e.child}, "teamResult")
	for {
		if si := old.Snapshot().SignIn; si != nil && si.State == structured.SignInFailed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no failed state")
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)
	if cur, _ := ask(e.srv, func() *structured.Runtime { return e.ws.StructuredRuntime(e.child) }); cur != old {
		t.Error("the pane was restarted after a failed sign-in")
	}
}
