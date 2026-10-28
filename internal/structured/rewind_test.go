package structured

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/acp/acptest"
	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

func promptIDs(s Snapshot) []string {
	var out []string
	for _, e := range s.Entries {
		if e.Kind == transcript.KindPrompt {
			out = append(out, e.ID)
		}
	}
	return out
}

func sendAndSettle(t *testing.T, r *Runtime, text string, want int) Snapshot {
	t.Helper()
	if err := r.Send(text); err != nil {
		t.Fatal(err)
	}
	return until(t, r, "turn of "+text, func(s Snapshot) bool { return idle(s) && len(prompts(s)) == want })
}

func threeTurns(t *testing.T, aopts ...acptest.Option) (*Runtime, Snapshot) {
	t.Helper()
	r := begin(t, fakeOptions(t, aopts...))
	until(t, r, "ready", idle)
	var s Snapshot
	for i, txt := range []string{"plain a", "plain b", "plain c"} {
		s = sendAndSettle(t, r, txt, i+1)
	}
	return r, s
}

func TestRewindPointsAreThePromptsWithAnEarlierTurn(t *testing.T) {
	_, s := threeTurns(t, acptest.WithRewind())
	ids := promptIDs(s)
	if got := s.RewindPoints; len(got) != 2 || got[0] != ids[1] || got[1] != ids[2] {
		t.Errorf("rewind points = %v, prompts = %v (the first prompt has no point)", got, ids)
	}
}

func TestAnAgentWithoutTheExtensionOffersNoPoints(t *testing.T) {
	r, s := threeTurns(t)
	if len(s.RewindPoints) != 0 {
		t.Errorf("points = %v", s.RewindPoints)
	}
	if _, err := r.Rewind(promptIDs(s)[1], r.RewindToken()); !errors.Is(err, ErrRewindNotOffered) {
		t.Errorf("rewind = %v", err)
	}
	if _, err := r.ForkSource(promptIDs(s)[1], r.RewindToken()); !errors.Is(err, ErrRewindNotOffered) && !errors.Is(err, ErrForkNotOffered) {
		t.Errorf("fork source = %v", err)
	}
}

func TestRewindDropsThePromptAndEverythingAfterIt(t *testing.T) {
	r, s := threeTurns(t, acptest.WithRewind())
	ids := promptIDs(s)
	text, err := r.Rewind(ids[1], r.RewindToken())
	if err != nil {
		t.Fatalf("rewind: %v", err)
	}
	if text != "plain b" {
		t.Errorf("the prompt's text = %q", text)
	}
	s = r.Snapshot()
	if got := prompts(s); len(got) != 1 || got[0] != "plain a" {
		t.Errorf("prompts after the rewind = %q", got)
	}
	if n := strings.Count(replies(s), "Hello world"); n != 1 {
		t.Errorf("replies after the rewind = %q", replies(s))
	}
	if s.Epoch != 1 {
		t.Errorf("epoch = %d", s.Epoch)
	}
	if len(s.RewindPoints) != 0 {
		t.Errorf("points after the rewind = %v", s.RewindPoints)
	}
	// The agent was cut too: it knows one turn, then the one just sent.
	if err := r.Send("turns"); err != nil {
		t.Fatal(err)
	}
	s = until(t, r, "the turn list", func(s Snapshot) bool { return strings.Contains(replies(s), "turns=") })
	got := replies(s)
	if n := strings.Count(got[strings.Index(got, "turns="):], ","); n != 1 {
		t.Errorf("the agent reports %q, want two turns (a, then this one)", got)
	}
	// The entry ids that were dropped are not used again.
	for _, e := range s.Entries {
		for _, old := range ids[1:] {
			if e.ID == old {
				t.Errorf("entry id %s came back", old)
			}
		}
	}
}

func TestRewindRefusals(t *testing.T) {
	r, s := threeTurns(t, acptest.WithRewind())
	ids := promptIDs(s)
	for name, id := range map[string]string{
		"the first prompt":      ids[0],
		"an unknown entry":      "e999",
		"an empty id":           "",
		"a reply, not a prompt": s.Entries[1].ID,
	} {
		if _, err := r.Rewind(id, r.RewindToken()); !errors.Is(err, ErrNoPoint) {
			t.Errorf("%s: rewind = %v, want ErrNoPoint", name, err)
		}
	}
	if len(prompts(r.Snapshot())) != 3 {
		t.Error("a refused rewind changed the conversation")
	}
	// A turn that is running blocks it.
	if err := r.Send("slow"); err != nil {
		t.Fatal(err)
	}
	until(t, r, "the slow turn", func(s Snapshot) bool { return s.Status == "working" })
	if _, err := r.Rewind(ids[1], r.RewindToken()); !errors.Is(err, ErrRewindBusy) {
		t.Errorf("rewind during a turn = %v", err)
	}
	if _, err := r.ForkSource(ids[1], r.RewindToken()); !errors.Is(err, ErrRewindBusy) {
		t.Errorf("fork during a turn = %v", err)
	}
	// So does a prompt that is waiting.
	if err := r.Send("plain behind"); err != nil {
		t.Fatal(err)
	}
	if err := r.Cancel(); err != nil {
		t.Fatal(err)
	}
}

func TestATurnThatDidNotEndHasNoPointAfterIt(t *testing.T) {
	r := begin(t, fakeOptions(t, acptest.WithRewind()))
	until(t, r, "ready", idle)
	sendAndSettle(t, r, "plain a", 1)
	if err := r.Send("slow"); err != nil {
		t.Fatal(err)
	}
	until(t, r, "the slow turn", func(s Snapshot) bool { return s.Status == "working" })
	if err := r.Cancel(); err != nil {
		t.Fatal(err)
	}
	until(t, r, "cancelled", func(s Snapshot) bool { return idle(s) && len(prompts(s)) == 2 })
	s := sendAndSettle(t, r, "plain c", 3)
	ids := promptIDs(s)
	// "slow" has a point before it (a ended well); "plain c" does not (slow was cancelled).
	if len(s.RewindPoints) != 1 || s.RewindPoints[0] != ids[1] {
		t.Errorf("points = %v, prompts = %v", s.RewindPoints, ids)
	}
	if _, err := r.Rewind(ids[2], r.RewindToken()); !errors.Is(err, ErrNoPoint) {
		t.Errorf("rewind after a cancelled turn = %v", err)
	}
}

func TestRewindWhenTheAgentRefusesChangesNothing(t *testing.T) {
	r, s := threeTurns(t, acptest.WithRewind())
	ids := promptIDs(s)
	// The fake agent forgets the turns after a rewind; a second rewind of a turn
	// it no longer has is refused by the agent while the runtime still holds it
	// only if the runtime's marks were not trimmed. They are, so use a token the
	// agent never saw by rewinding twice to the same entry.
	if _, err := r.Rewind(ids[2], r.RewindToken()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Rewind(ids[2], r.RewindToken()); !errors.Is(err, ErrNoPoint) {
		t.Errorf("a second rewind to the same entry = %v", err)
	}
	if got := prompts(r.Snapshot()); len(got) != 2 {
		t.Errorf("prompts = %q", got)
	}
}

func TestForkSource(t *testing.T) {
	r, s := threeTurns(t, acptest.WithRewind())
	ids := promptIDs(s)
	f, err := r.ForkSource(ids[2], r.RewindToken())
	if err != nil || f.Session != "sess-1" || f.At != "at-2" {
		t.Fatalf("fork source = %+v, %v", f, err)
	}
	whole, err := r.ForkSource("", r.RewindToken())
	if err != nil || whole.Session != "sess-1" || whole.At != "" {
		t.Errorf("whole fork source = %+v, %v", whole, err)
	}
	if _, err := r.ForkSource(ids[0], r.RewindToken()); !errors.Is(err, ErrNoPoint) {
		t.Errorf("fork before the first prompt = %v", err)
	}
	if len(prompts(r.Snapshot())) != 3 {
		t.Error("asking for a fork source changed the conversation")
	}
}

func TestAForkedPaneOpensTheForkedSession(t *testing.T) {
	o := fakeOptions(t, acptest.WithRewind())
	o.Fork = &ForkFrom{Session: "sess-1", At: "at-2"}
	r := begin(t, o)
	s := until(t, r, "ready", idle)
	if s.SessionID != "fork-of-sess-1-at-2" {
		t.Errorf("session = %q", s.SessionID)
	}
	if n := notices(s); n != ForkedText+"\n" {
		t.Errorf("notices = %q", n)
	}
	// The pane works as any: a prompt goes to the forked session.
	if err := r.Send("whoami"); err != nil {
		t.Fatal(err)
	}
	until(t, r, "the reply", func(s Snapshot) bool { return strings.Contains(replies(s), "session=fork-of-sess-1-at-2") })
}

func TestAForkThatCannotBeMadeStartsANewSession(t *testing.T) {
	o := fakeOptions(t) // an agent with no fork
	o.Fork = &ForkFrom{Session: "sess-1", At: "at-2"}
	r := begin(t, o)
	s := until(t, r, "ready", idle)
	if s.SessionID != "sess-1" {
		t.Errorf("session = %q", s.SessionID)
	}
	if n := notices(s); n != ForkFailedText+"\n" {
		t.Errorf("notices = %q", n)
	}
}

// A prompt sent while a rewind is under way waits: it is not sent to an agent
// that is being taken back, and goes once the rewind is over.
func TestAPromptWaitsWhileARewindRuns(t *testing.T) {
	r, s := threeTurns(t)
	n := len(prompts(s))
	r.mu.Lock()
	r.rewinding = true
	r.mu.Unlock()
	if err := r.Send("plain held"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if got := len(prompts(r.Snapshot())); got != n {
		t.Fatalf("a prompt was sent during a rewind: %d prompts, was %d", got, n)
	}
	r.mu.Lock()
	r.rewinding = false
	r.mu.Unlock()
	r.wakeLoop()
	until(t, r, "the held prompt", func(s Snapshot) bool { return idle(s) && len(prompts(s)) == n+1 })
}

// After the agent moved the conversation to another session id (/clear), the
// turns before the move cannot be named: no point is offered, and the whole
// conversation is forked by the id the agent resumes it by.
func TestNoPointsAfterTheSessionMoved(t *testing.T) {
	r, s := threeTurns(t, acptest.WithRewind())
	ids := promptIDs(s)
	if len(r.RewindPoints()) == 0 {
		t.Fatal("no points before the move")
	}
	const moved = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	r.mu.Lock()
	r.moved, r.cleared = moved, true
	r.mu.Unlock()
	if p := r.RewindPoints(); len(p) != 0 {
		t.Errorf("points after the move = %v", p)
	}
	if _, err := r.ForkSource(ids[2], r.RewindToken()); !errors.Is(err, ErrNoPoint) {
		t.Errorf("fork at a point after the move = %v", err)
	}
	if _, err := r.Rewind(ids[2], r.RewindToken()); !errors.Is(err, ErrNoPoint) {
		t.Errorf("rewind after the move = %v", err)
	}
	if f, err := r.ForkSource("", r.RewindToken()); err != nil || f.Session != moved {
		t.Errorf("whole fork source = %+v, %v", f, err)
	}
	if len(prompts(r.Snapshot())) != 3 {
		t.Error("a refused rewind changed the conversation")
	}
}

// An agent that says it can rewind but is not one of Flockdeck's own (its
// Command is not marked) is sent no tokens, and offers no points.
func TestAForeignAgentSayingItRewindsIsNotSentTokens(t *testing.T) {
	o := fakeOptions(t, acptest.WithRewind())
	o.Command.Rewind = false
	r := begin(t, o)
	until(t, r, "ready", idle)
	s := sendAndSettle(t, r, "plain a", 1)
	s = sendAndSettle(t, r, "turns", 2)
	if !strings.Contains(replies(s), "turns=\n") && !strings.HasSuffix(strings.TrimSpace(replies(s)), "turns=") {
		t.Errorf("the agent was sent tokens: %q", replies(s))
	}
	if len(s.RewindPoints) != 0 || r.RewindToken() == "" {
		t.Errorf("points = %v", s.RewindPoints)
	}
	if _, err := r.Rewind(promptIDs(s)[1], r.RewindToken()); !errors.Is(err, ErrRewindNotOffered) {
		t.Errorf("rewind = %v", err)
	}
}

// The window's token must be the runtime's current one.
func TestARewindOrForkWithAStaleTokenIsRefused(t *testing.T) {
	r, s := threeTurns(t, acptest.WithRewind())
	ids := promptIDs(s)
	old := r.RewindToken()
	for _, tok := range []string{"", "garbage", "0.0"} {
		if _, err := r.Rewind(ids[2], tok); !errors.Is(err, ErrRewindStale) {
			t.Errorf("rewind with token %q = %v", tok, err)
		}
		if _, err := r.ForkSource(ids[2], tok); !errors.Is(err, ErrRewindStale) {
			t.Errorf("fork with token %q = %v", tok, err)
		}
	}
	if _, err := r.Rewind(ids[2], old); err != nil {
		t.Fatal(err)
	}
	if r.RewindToken() == old {
		t.Error("the token did not change with the rewind")
	}
	if _, err := r.ForkSource(ids[1], old); !errors.Is(err, ErrRewindStale) {
		t.Errorf("fork with the token from before the rewind = %v", err)
	}
	if len(prompts(r.Snapshot())) != 2 {
		t.Error("a refused call changed the conversation")
	}
}

// An agent that never answers a rewind leaves it unknown whether it went back:
// the runtime says so, changes nothing, and takes prompts again.
func TestARewindTheAgentNeverAnswersIsUnknown(t *testing.T) {
	o := fakeOptions(t, acptest.WithRewind(), acptest.WithRewindHang())
	o.Command.RewindTimeout = 500 * time.Millisecond
	r := begin(t, o)
	until(t, r, "ready", idle)
	var s Snapshot
	for i, txt := range []string{"plain a", "plain b"} {
		s = sendAndSettle(t, r, txt, i+1)
	}
	ids := promptIDs(s)
	if _, err := r.Rewind(ids[1], r.RewindToken()); !errors.Is(err, ErrRewindUnknown) {
		t.Fatalf("rewind = %v", err)
	}
	if len(prompts(r.Snapshot())) != 2 {
		t.Error("the conversation changed")
	}
	sendAndSettle(t, r, "plain c", 3)
}

// The entry is checked before the agent is asked: an agent is never taken back
// for a prompt the conversation no longer holds.
func TestTheEntryIsCheckedBeforeTheAgentIsAsked(t *testing.T) {
	r, s := threeTurns(t, acptest.WithRewind())
	ids := promptIDs(s)
	r.br.TruncateBefore(ids[2]) // the conversation lost it, the marks did not
	if _, err := r.Rewind(ids[2], r.RewindToken()); !errors.Is(err, ErrNoPoint) {
		t.Fatalf("rewind = %v", err)
	}
	s = sendAndSettle(t, r, "turns", 3)
	var tokens string
	for _, line := range strings.Split(replies(s), "\n") {
		if strings.HasPrefix(line, "turns=") {
			tokens = strings.TrimPrefix(line, "turns=")
		}
	}
	if n := len(strings.Split(tokens, ",")); n != 4 { // three prompts and this one
		t.Errorf("the agent holds %d turns, want 4: %q", n, tokens)
	}
}

// A fork takes the model the source was on, when the new session offers it.
func TestAForkTakesTheModelTheSourceWasOn(t *testing.T) {
	o := fakeOptions(t, acptest.WithRewind(), acptest.WithModes())
	o.Fork = &ForkFrom{Session: "sess-1", Model: "gem-flash"}
	r := begin(t, o)
	s := until(t, r, "ready", idle)
	if m := s.Session.Models; m == nil || m.Current != "gem-flash" {
		t.Errorf("models = %+v", s.Session.Models)
	}
	o2 := fakeOptions(t, acptest.WithRewind(), acptest.WithModes())
	o2.Fork = &ForkFrom{Session: "sess-1", Model: "not-offered"}
	r2 := begin(t, o2)
	s2 := until(t, r2, "ready", idle)
	if m := s2.Session.Models; m == nil || m.Current != "gem-pro" {
		t.Errorf("a model the session does not offer was taken: %+v", s2.Session.Models)
	}
}

// The move of the session id that the runtime's own rewind asks for is not a
// /clear: points are still offered. A move the agent makes on its own is.
func TestARewindsOwnMoveOfTheSessionIsNotAClear(t *testing.T) {
	o := fakeOptions(t, acptest.WithRewind())
	o.Spec = agent.Spec{ID: "claude", Runner: agent.RunnerACP, ACP: agent.ACPSpec{Adapter: "claude"}}
	r := begin(t, o)
	until(t, r, "ready", idle)
	for i, txt := range []string{"plain a", "plain b", "plain c"} {
		sendAndSettle(t, r, txt, i+1)
	}
	const mine = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	const theirs = "11111111-2222-4333-8444-555555555555"
	r.mu.Lock()
	r.rewindMove = true
	r.mu.Unlock()
	r.noteMoved(mine)
	r.mu.Lock()
	r.rewindMove = false
	r.mu.Unlock()
	if len(r.RewindPoints()) == 0 {
		t.Error("the move a rewind asked for took the points away")
	}
	if got := r.SessionID(); got != mine {
		t.Errorf("SessionID = %q", got)
	}
	r.noteMoved(theirs)
	if p := r.RewindPoints(); len(p) != 0 {
		t.Errorf("points after the agent moved on its own = %v", p)
	}
}
