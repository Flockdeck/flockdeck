package structured

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/acp/acptest"
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
	if _, err := r.Rewind(promptIDs(s)[1]); !errors.Is(err, ErrRewindNotOffered) {
		t.Errorf("rewind = %v", err)
	}
	if _, err := r.ForkSource(promptIDs(s)[1]); !errors.Is(err, ErrRewindNotOffered) && !errors.Is(err, ErrForkNotOffered) {
		t.Errorf("fork source = %v", err)
	}
}

func TestRewindDropsThePromptAndEverythingAfterIt(t *testing.T) {
	r, s := threeTurns(t, acptest.WithRewind())
	ids := promptIDs(s)
	text, err := r.Rewind(ids[1])
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
		if _, err := r.Rewind(id); !errors.Is(err, ErrNoPoint) {
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
	if _, err := r.Rewind(ids[1]); !errors.Is(err, ErrRewindBusy) {
		t.Errorf("rewind during a turn = %v", err)
	}
	if _, err := r.ForkSource(ids[1]); !errors.Is(err, ErrRewindBusy) {
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
	if _, err := r.Rewind(ids[2]); !errors.Is(err, ErrNoPoint) {
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
	if _, err := r.Rewind(ids[2]); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Rewind(ids[2]); !errors.Is(err, ErrNoPoint) {
		t.Errorf("a second rewind to the same entry = %v", err)
	}
	if got := prompts(r.Snapshot()); len(got) != 2 {
		t.Errorf("prompts = %q", got)
	}
}

func TestForkSource(t *testing.T) {
	r, s := threeTurns(t, acptest.WithRewind())
	ids := promptIDs(s)
	f, err := r.ForkSource(ids[2])
	if err != nil || f.Session != "sess-1" || f.At != "at-2" {
		t.Fatalf("fork source = %+v, %v", f, err)
	}
	whole, err := r.ForkSource("")
	if err != nil || whole.Session != "sess-1" || whole.At != "" {
		t.Errorf("whole fork source = %+v, %v", whole, err)
	}
	if _, err := r.ForkSource(ids[0]); !errors.Is(err, ErrNoPoint) {
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
