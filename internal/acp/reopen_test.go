package acp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp/acptest"
)

func startInit(t *testing.T, r *recorder, opts ...acptest.Option) *Client {
	t.Helper()
	c := startRaw(t, r, nil, opts...)
	if _, err := c.Initialize(ctxT(t)); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	return c
}

func TestCapabilitiesSaySessionMethods(t *testing.T) {
	c := startInit(t, newRecorder(), acptest.WithSessionResume())
	if caps := c.Capabilities(); caps.LoadSession || !caps.Resume {
		t.Errorf("capabilities = %+v", caps)
	}
	c = startInit(t, newRecorder(), acptest.WithSessionLoad())
	if caps := c.Capabilities(); !caps.LoadSession || caps.Resume {
		t.Errorf("capabilities = %+v", caps)
	}
}

func TestLoadSessionMarksTheReplay(t *testing.T) {
	r := newRecorder()
	c := startInit(t, r, acptest.WithSessionLoad(), acptest.WithKnownSessions("old-1"))
	got, err := c.LoadSession(ctxT(t), "old-1", acptest.SessionDir(t), nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.SessionID != "old-1" {
		t.Errorf("session id = %q", got.SessionID)
	}
	// The update after the answer arrives later and is not replay.
	r.waitFor(t, "the update after the load", func(us []Update) bool {
		for _, u := range us {
			if u.Kind == UpdateAvailableCommands {
				return true
			}
		}
		return false
	})
	var replay, after int
	for _, u := range r.all() {
		if u.Kind == UpdateAvailableCommands {
			if u.Replay {
				t.Errorf("an update after the answer was marked as replay")
			}
			after++
			continue
		}
		if !u.Replay {
			t.Errorf("an update before the answer was not marked as replay: %s", u.Kind)
		}
		replay++
	}
	if replay != 7 || after != 1 {
		t.Errorf("replay = %d, after = %d", replay, after)
	}

	// A prompt on the reopened session is a turn, and its updates are not replay.
	r.reset()
	prompt(t, c, "old-1", "whoami")
	if r.text() != "session=old-1" {
		t.Errorf("the prompt went to session %q", r.text())
	}
	for _, u := range r.all() {
		if u.Replay {
			t.Errorf("an update of a prompt was marked as replay: %s", u.Kind)
		}
	}
}

func TestReplayIsOnlyForTheSessionBeingLoaded(t *testing.T) {
	c := &Client{loads: map[int64]string{5: "a"}}
	var got []Update
	c.cfg.Handler = handlerFunc(func(u Update) { got = append(got, u) })
	for _, sid := range []string{"a", "b"} {
		raw := `{"sessionId":"` + sid + `","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"x"}}}`
		c.dispatch(rpcMsg{Method: "session/update", Params: []byte(raw)})
	}
	if len(got) != 2 || !got[0].Replay || got[1].Replay {
		t.Errorf("updates = %+v", got)
	}
}

type handlerFunc func(Update)

func (f handlerFunc) Update(u Update) { f(u) }
func (handlerFunc) Permission(context.Context, PermissionRequest) string { return "" }

func TestResumeSessionHasNoReplay(t *testing.T) {
	r := newRecorder()
	c := startInit(t, r, acptest.WithSessionResume(), acptest.WithKnownSessions("old-1"))
	if _, err := c.ResumeSession(ctxT(t), "old-1", acptest.SessionDir(t), nil); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if n := len(r.all()); n != 0 {
		t.Errorf("a resume sent %d updates", n)
	}
	prompt(t, c, "old-1", "whoami")
	if r.text() != "session=old-1" {
		t.Errorf("text = %q", r.text())
	}
}

func TestReopenWithoutTheCapabilityIsRefusedBeforeAnythingIsSent(t *testing.T) {
	c := startInit(t, newRecorder(), acptest.WithKnownSessions("old-1")) // neither capability
	dir := acptest.SessionDir(t)
	if _, err := c.LoadSession(ctxT(t), "old-1", dir, nil); !errors.Is(err, ErrUnsupported) {
		t.Errorf("load = %v", err)
	}
	if _, err := c.ResumeSession(ctxT(t), "old-1", dir, nil); !errors.Is(err, ErrUnsupported) {
		t.Errorf("resume = %v", err)
	}
	// Only load: resume is refused though the agent has load.
	c = startInit(t, newRecorder(), acptest.WithSessionLoad(), acptest.WithKnownSessions("old-1"))
	if _, err := c.ResumeSession(ctxT(t), "old-1", dir, nil); !errors.Is(err, ErrUnsupported) {
		t.Errorf("resume on a load-only agent = %v", err)
	}
}

func TestReopenOfAnUnknownSessionIsAnRPCError(t *testing.T) {
	c := startInit(t, newRecorder(), acptest.WithSessionLoad(), acptest.WithSessionResume(), acptest.WithKnownSessions("old-1"))
	dir := acptest.SessionDir(t)
	for name, f := range map[string]func() error{
		"load":   func() error { _, err := c.LoadSession(ctxT(t), "gone", dir, nil); return err },
		"resume": func() error { _, err := c.ResumeSession(ctxT(t), "gone", dir, nil); return err },
	} {
		var rpc *RPCError
		if err := f(); !errors.As(err, &rpc) || rpc.Code != -32002 {
			t.Errorf("%s of an unknown id = %v", name, err)
		}
	}
	// The client is still usable.
	if _, err := c.NewSession(ctxT(t), dir, nil); err != nil {
		t.Errorf("session/new after the refusals: %v", err)
	}
}

func TestReopenChecksItsArguments(t *testing.T) {
	c := startInit(t, newRecorder(), acptest.WithSessionLoad(), acptest.WithKnownSessions("old-1"))
	if _, err := c.LoadSession(ctxT(t), "", acptest.SessionDir(t), nil); err == nil {
		t.Error("an empty id was accepted")
	}
	if _, err := c.LoadSession(ctxT(t), "old-1", "relative/dir", nil); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Errorf("a relative cwd: %v", err)
	}
}

func TestAgentThatExitsDuringReplayEndsTheLoad(t *testing.T) {
	c := startInit(t, newRecorder(), acptest.WithSessionLoad(), acptest.WithKnownSessions("old-1"), acptest.WithReplayThenExit())
	var exit *ExitError
	if _, err := c.LoadSession(ctxT(t), "old-1", acptest.SessionDir(t), nil); !errors.As(err, &exit) {
		t.Errorf("load = %v", err)
	}
	// Nothing is left marked as loading.
	c.mu.Lock()
	n := len(c.loads)
	c.mu.Unlock()
	if n != 0 {
		t.Errorf("%d loads still marked", n)
	}
}
