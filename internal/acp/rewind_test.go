package acp

import (
	"errors"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp/acptest"
)

const (
	tokA = "11111111-1111-4111-8111-111111111111"
	tokB = "22222222-2222-4222-8222-222222222222"
)

func lastChunk(r *recorder) string {
	us := r.all()
	for i := len(us) - 1; i >= 0; i-- {
		if us[i].Kind == UpdateAgentMessageChunk && us[i].Content != nil {
			return us[i].Content.Text
		}
	}
	return ""
}

func TestRewindIsOnlyForAgentsThatSayTheyHaveIt(t *testing.T) {
	c := startInit(t, newRecorder(), acptest.WithSessionResume())
	if c.Capabilities().Rewind {
		t.Fatal("an agent that did not advertise the extension has it")
	}
	if err := c.Rewind(ctxT(t), "s", tokA); err != ErrNoRewind {
		t.Errorf("rewind = %v, want ErrNoRewind", err)
	}
	if _, err := c.ForkPoint(ctxT(t), "s", tokA); err != ErrNoRewind {
		t.Errorf("fork point = %v, want ErrNoRewind", err)
	}
	// The token is not sent to such an agent: it keeps none.
	sid, err := c.NewSession(ctxT(t), acptest.SessionDir(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.PromptTurn(ctxT(t), sid, tokA, "plain"); err != nil {
		t.Fatal(err)
	}
}

func TestPromptTurnRewindAndForkPoint(t *testing.T) {
	r := newRecorder()
	c, sid := start(t, r, acptest.WithRewind())
	if !c.Capabilities().Rewind || !c.Capabilities().Fork {
		t.Fatalf("capabilities = %+v", c.Capabilities())
	}
	for _, tok := range []string{tokA, tokB} {
		if _, err := c.PromptTurn(ctxT(t), sid, tok, "plain"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.PromptTurn(ctxT(t), sid, "", "turns"); err != nil {
		t.Fatal(err)
	}
	r.waitFor(t, "the turn list", func([]Update) bool { return strings.HasPrefix(lastChunk(r), "turns=") })
	if got := lastChunk(r); got != "turns="+tokA+","+tokB {
		t.Errorf("the agent saw %q", got)
	}
	at, err := c.ForkPoint(ctxT(t), sid, tokB)
	if err != nil || at != "at-1" {
		t.Fatalf("fork point = %q, %v", at, err)
	}
	if whole, err := c.ForkPoint(ctxT(t), sid, ""); err != nil || whole != "" {
		t.Errorf("a fork point of the whole conversation = %q, %v", whole, err)
	}
	fk, err := c.ForkSession(ctxT(t), sid, acptest.SessionDir(t), nil, at)
	if err != nil || fk.SessionID != "fork-of-"+sid+"-at-1" {
		t.Fatalf("fork = %+v, %v", fk, err)
	}
	if err := c.Rewind(ctxT(t), sid, tokB); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PromptTurn(ctxT(t), sid, "", "turns"); err != nil {
		t.Fatal(err)
	}
	r.waitFor(t, "the turn list after the rewind", func([]Update) bool { return lastChunk(r) == "turns="+tokA })
	// A turn the agent does not know is refused with the agent's own error, which
	// the caller must not show.
	var rpc *RPCError
	if err := c.Rewind(ctxT(t), sid, tokB); !errors.As(err, &rpc) {
		t.Errorf("rewind of a gone turn = %v", err)
	}
}

func TestRewindRefusesBadArguments(t *testing.T) {
	c, sid := start(t, newRecorder(), acptest.WithRewind())
	for _, bad := range []string{"", "not-a-uuid", strings.ToUpper(tokA), tokA + "x"} {
		if err := c.Rewind(ctxT(t), sid, bad); err == nil {
			t.Errorf("rewind accepted turn %q", bad)
		}
		if _, err := c.ForkPoint(ctxT(t), sid, bad); bad != "" && err == nil {
			t.Errorf("fork point accepted turn %q", bad)
		}
	}
	for _, at := range []string{"a b", "a\nb", strings.Repeat("a", MaxAt+1), "../x"} {
		if _, err := c.ForkSession(ctxT(t), sid, acptest.SessionDir(t), nil, at); err == nil {
			t.Errorf("fork accepted at %q", at)
		}
	}
	if _, err := c.ForkSession(ctxT(t), sid, "relative", nil, ""); err == nil {
		t.Error("fork accepted a relative cwd")
	}
}

// An agent that says it has the extension is believed only when the caller has
// said it is one of Flockdeck's own: otherwise it gets no tokens and no rewind
// calls.
func TestAForeignAgentSayingItRewindsGetsNoTokensAndNoRewind(t *testing.T) {
	r := newRecorder()
	c, sid := startWith(t, r, nil, acptest.WithRewind())
	if c.Capabilities().Rewind {
		t.Fatal("an agent the caller did not trust has the extension")
	}
	if _, err := c.PromptTurn(ctxT(t), sid, tokA, "plain"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PromptTurn(ctxT(t), sid, "", "turns"); err != nil {
		t.Fatal(err)
	}
	r.waitFor(t, "the turn list", func([]Update) bool { return strings.HasPrefix(lastChunk(r), "turns=") })
	if got := lastChunk(r); got != "turns=" {
		t.Errorf("the agent was sent a token: %q", got)
	}
	if err := c.Rewind(ctxT(t), sid, tokA); err != ErrNoRewind {
		t.Errorf("rewind = %v", err)
	}
	if _, err := c.ForkPoint(ctxT(t), sid, tokA); err != ErrNoRewind {
		t.Errorf("fork point = %v", err)
	}
	if _, err := c.ForkSession(ctxT(t), sid, acptest.SessionDir(t), nil, "at-1"); err != ErrNoRewind {
		t.Errorf("fork at a point = %v", err)
	}
}
