package route

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
)

// fake is a Classifier that answers what it was given and counts being asked.
type fake struct {
	mu    sync.Mutex
	d     Difficulty
	err   error
	tasks []string
}

func (f *fake) Classify(task string) (Difficulty, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tasks = append(f.tasks, task)
	return f.d, f.err
}

func (f *fake) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.tasks)
}

func confident(score float64) *fake {
	return &fake{d: Difficulty{Score: score, Confidence: 0.9, Model: "jev-test"}}
}

// costPolicy is a policy with no rules at all, so that nothing but the
// fallback ever decides, on the cost strategy.
func costPolicy(floor string, jev bool) agent.RoutingPolicy {
	return agent.RoutingPolicy{Mode: agent.RoutingSuggest, Strategy: agent.StrategyCost, Floor: floor, Jev: jev, Rules: []agent.RoutingRule{}}
}

func fanoutTask(task, current string) Input {
	return Input{Kind: KindFanout, Task: task, Agent: claude(), Current: current}
}

// Jev off, or Jev on with nothing to ask, decides exactly as a router that has
// never heard of Jev does, and never calls anything.
func TestJevOffOrUnavailableChangesNothing(t *testing.T) {
	for _, current := range []string{"haiku", "sonnet", "opus", "", "opusplan"} {
		for _, floor := range []string{"", agent.TierMid, agent.TierTop} {
			for _, nd := range []bool{false, true} {
				in := fanoutTask("add a health endpoint", current)
				in.NoDowngrade = nd
				plain := New(costPolicy(floor, false)).Decide(in)

				f := confident(4)
				if got := New(costPolicy(floor, false)).WithClassifier(f).Decide(in); !reflect.DeepEqual(got, plain) || f.calls() != 0 {
					t.Errorf("Jev off (%s, floor %q, noDowngrade %v): %+v after %d calls, want %+v after none", current, floor, nd, got, f.calls(), plain)
				}
				// On in the policy, but no classifier: no key.
				if got := New(costPolicy(floor, true)).Decide(in); !reflect.DeepEqual(got, plain) {
					t.Errorf("Jev on with no classifier (%s, floor %q): %+v, want %+v", current, floor, got, plain)
				}
				if got := New(costPolicy(floor, true)).WithClassifier(nil).Decide(in); !reflect.DeepEqual(got, plain) {
					t.Errorf("Jev on with a nil classifier (%s, floor %q): %+v, want %+v", current, floor, got, plain)
				}
			}
		}
	}
}

func TestARuleThatMatchedNeverAsksJev(t *testing.T) {
	f := confident(4)
	p := costPolicy("", true)
	p.Rules = nil // the built-in rules
	r := New(p).WithClassifier(f)
	for _, task := range []string{"run the tests", "refactor the store", "fix a typo in the readme", "rename Foo to Bar"} {
		if d := r.Decide(fanoutTask(task, "sonnet")); d.Source != SourceRule || d.Jev != nil {
			t.Errorf("%q: %+v, want a rule's decision", task, d)
		}
	}
	if f.calls() != 0 {
		t.Errorf("Jev was asked %d times about work a rule decided", f.calls())
	}
	r.Prewarm([]Input{fanoutTask("run the tests", "sonnet"), fanoutTask("refactor the store", "sonnet")})
	if f.calls() != 0 {
		t.Errorf("Prewarm asked Jev %d times about work a rule decided", f.calls())
	}
}

// What a confident answer asks for, on a run on Opus with the floor at small,
// which leaves all three tiers open.
func TestAConfidentAnswerChoosesTheTier(t *testing.T) {
	tests := []struct {
		score float64
		tier  string
		model string
	}{
		{0, agent.TierSmall, "haiku"},
		{1.4, agent.TierSmall, "haiku"},
		{1.5, agent.TierMid, "sonnet"},
		{2.9, agent.TierMid, "sonnet"},
		{3, agent.TierTop, "opus"}, // on Opus already: left alone, see below
		{4, agent.TierTop, "opus"},
	}
	for _, tc := range tests {
		f := confident(tc.score)
		d := New(costPolicy("", true)).WithClassifier(f).Decide(fanoutTask("add a health endpoint", "opus"))
		if tc.tier == agent.TierTop {
			// Already on the top tier: nothing to route, and Jev asked
			// for nothing beyond that.
			if d.Routed || d.Jev != nil && d.Jev.Tier != agent.TierTop {
				t.Errorf("score %v: %+v", tc.score, d)
			}
			continue
		}
		if !d.Routed || d.Model != tc.model || d.Tier != tc.tier || d.Source != SourceFallback || d.Up {
			t.Errorf("score %v: %+v, want %s (%s)", tc.score, d, tc.model, tc.tier)
		}
		if d.Jev == nil || d.Jev.Score != tc.score || d.Jev.Model != "jev-test" || d.Jev.Tier != tc.tier {
			t.Errorf("score %v: the decision carries %+v", tc.score, d.Jev)
		}
		if f.calls() != 1 || f.tasks[0] != "add a health endpoint" {
			t.Errorf("score %v: asked %v", tc.score, f.tasks)
		}
	}
}

// Jev is never the reason work costs more than it would have without routing:
// a demanding task on Sonnet is left on Sonnet, not lifted to Opus.
func TestJevNeverRaisesAboveTheWorksOwnModel(t *testing.T) {
	d := New(costPolicy("", true)).WithClassifier(confident(4)).Decide(fanoutTask("design the sync engine", "sonnet"))
	if d.Routed || d.Up {
		t.Errorf("a hard task on Sonnet was moved: %+v", d)
	}
	// And a middling one on Opus is brought down only as far as it asks.
	d = New(costPolicy("", true)).WithClassifier(confident(2)).Decide(fanoutTask("add a health endpoint", "opus"))
	if !d.Routed || d.Model != "sonnet" || d.Up {
		t.Errorf("a middling task on Opus: %+v", d)
	}
	// Where the floor is above the work's model, the floor is what a plain
	// fallback does too, and Jev's answer does not change it.
	plain := New(costPolicy("mid", false)).Decide(fanoutTask("rename x", "haiku"))
	got := New(costPolicy("mid", true)).WithClassifier(confident(4)).Decide(fanoutTask("rename x", "haiku"))
	if got.Model != plain.Model || got.Up != plain.Up {
		t.Errorf("with the floor above the work: %+v, plain %+v", got, plain)
	}
}

// Every way an answer can be no use leaves exactly the plain fallback, with
// nothing in the decision to say Jev was ever asked.
func TestAnAnswerThatIsNoUseIsThePlainFallback(t *testing.T) {
	plain := New(costPolicy("", false)).Decide(fanoutTask("add a health endpoint", "opus"))
	if plain.Model != "haiku" {
		t.Fatalf("the plain fallback is %+v", plain)
	}
	cases := map[string]*fake{
		"an error":               &fake{err: errors.New("typesafe answered 429")},
		"low confidence":         {d: Difficulty{Score: 4, Confidence: 0.49}},
		"a score off the rubric": {d: Difficulty{Score: 7, Confidence: 0.9}},
		"a hard task that is also a mechanical one": {d: Difficulty{Score: 4, Confidence: 0.9, Mechanical: 0.9}},
	}
	for name, f := range cases {
		got := New(costPolicy("", true)).WithClassifier(f).Decide(fanoutTask("add a health endpoint", "opus"))
		if !reflect.DeepEqual(got, plain) {
			t.Errorf("%s: %+v, want the plain fallback %+v", name, got, plain)
		}
		if f.calls() != 1 {
			t.Errorf("%s: asked %d times", name, f.calls())
		}
	}
}

func TestATrivialTaskThatReachesManyFilesIsMid(t *testing.T) {
	f := &fake{d: Difficulty{Score: 0.5, Confidence: 0.9, MultiFile: 0.8}}
	d := New(costPolicy("", true)).WithClassifier(f).Decide(fanoutTask("update the copyright header", "opus"))
	if d.Model != "sonnet" {
		t.Errorf("%+v, want Sonnet", d)
	}
}

func TestTierForThresholds(t *testing.T) {
	tests := []struct {
		d    Difficulty
		rank int
		ok   bool
	}{
		{Difficulty{Score: 0, Confidence: 0.5}, 1, true},
		{Difficulty{Score: 0, Confidence: 0.4999}, 0, false},
		{Difficulty{Score: 1.49, Confidence: 1}, 1, true},
		{Difficulty{Score: 1.5, Confidence: 1}, 2, true},
		{Difficulty{Score: 2.99, Confidence: 1}, 2, true},
		{Difficulty{Score: 3, Confidence: 1}, 3, true},
		{Difficulty{Score: 4, Confidence: 1}, 3, true},
		{Difficulty{Score: 4.1, Confidence: 1}, 0, false},
		{Difficulty{Score: -1, Confidence: 1}, 0, false},
		{Difficulty{Score: 3, Confidence: 1, Mechanical: 0.7}, 0, false},
		{Difficulty{Score: 3, Confidence: 1, Mechanical: 0.69}, 3, true},
		{Difficulty{Score: 0, Confidence: 1, Mechanical: 0.99}, 1, true},
		{Difficulty{Score: 0, Confidence: 1, MultiFile: 0.7}, 2, true},
	}
	for _, tc := range tests {
		if rank, ok := tierFor(tc.d); rank != tc.rank || ok != tc.ok {
			t.Errorf("%+v: tier %d, %v; want %d, %v", tc.d, rank, ok, tc.rank, tc.ok)
		}
	}
}

// Spawned helpers and chat turns are never asked about: they have no dialog to
// change the choice in, and the log has no override to score it by.
func TestOnlyFanoutRowsAskJev(t *testing.T) {
	for _, kind := range []string{KindSpawn, KindTurn} {
		f := confident(2)
		in := fanoutTask("add a health endpoint", "opus")
		in.Kind = kind
		plain := New(costPolicy("", false)).Decide(in)
		if got := New(costPolicy("", true)).WithClassifier(f).Decide(in); !reflect.DeepEqual(got, plain) || f.calls() != 0 {
			t.Errorf("%s: %+v after %d calls, want %+v after none", kind, got, f.calls(), plain)
		}
	}
}

// Work already on the floor tier cannot be moved by an answer, so nobody is
// asked.
func TestNothingIsAskedWhenTheAnswerCouldNotMatter(t *testing.T) {
	f := confident(4)
	New(costPolicy("", true)).WithClassifier(f).Decide(fanoutTask("add a health endpoint", "haiku"))
	New(costPolicy("mid", true)).WithClassifier(f).Decide(fanoutTask("add a health endpoint", "sonnet"))
	New(costPolicy("", true)).WithClassifier(f).Decide(fanoutTask("add a health endpoint", "")) // Default: no known size
	if f.calls() != 0 {
		t.Errorf("asked %d times", f.calls())
	}
}

func TestPrewarmAsksOncePerDistinctTaskThatWouldAsk(t *testing.T) {
	f := confident(2)
	r := New(costPolicy("", true)).WithClassifier(f)
	r.Prewarm([]Input{fanoutTask("a", "opus"), fanoutTask("b", "opus"), fanoutTask("a", "opus"), fanoutTask("c", "haiku")})
	if f.calls() != 2 {
		t.Errorf("asked %v, want a and b once each", f.tasks)
	}
}

// The table that matters: whatever Jev says, under any combination of the
// user's settings, no bound they set is broken -- and where it may not apply,
// it changes nothing at all.
func TestJevCanNeverBreakABoundTheUserSet(t *testing.T) {
	currents := []string{"haiku", "sonnet", "opus", "", "opusplan"}
	answers := []Difficulty{
		{Score: 0, Confidence: 0.9}, {Score: 1.5, Confidence: 0.9}, {Score: 2, Confidence: 0.9},
		{Score: 3, Confidence: 0.9}, {Score: 4, Confidence: 1}, {Score: 4, Confidence: 0.2},
		{Score: 0, Confidence: 0.9, MultiFile: 0.9}, {Score: 4, Confidence: 0.9, Mechanical: 0.9},
	}
	for _, mode := range []string{"", agent.RoutingOff, agent.RoutingSuggest, agent.RoutingAuto} {
		for _, strategy := range []string{"", agent.StrategyBalanced, agent.StrategyCost} {
			for _, floor := range []string{"", agent.TierSmall, agent.TierMid, agent.TierTop, "bogus"} {
				for _, kind := range []string{KindFanout, KindSpawn, KindTurn} {
					for _, nd := range []bool{false, true} {
						for _, cur := range currents {
							for _, ans := range answers {
								name := fmt.Sprintf("mode %q strategy %q floor %q %s noDowngrade %v on %q answer %+v", mode, strategy, floor, kind, nd, cur, ans)
								p := agent.RoutingPolicy{Mode: mode, Strategy: strategy, Floor: floor, Rules: []agent.RoutingRule{}}
								in := Input{Kind: kind, Task: "add a health endpoint", Agent: claude(), Current: cur, NoDowngrade: nd}
								plain := New(p).Decide(in)

								p.Jev = true
								f := &fake{d: ans}
								got := New(p).WithClassifier(f).Decide(in)

								mayAsk := (mode == agent.RoutingSuggest || mode == agent.RoutingAuto) &&
									strategy == agent.StrategyCost && kind == KindFanout
								if !mayAsk {
									if !reflect.DeepEqual(got, plain) || f.calls() != 0 {
										t.Fatalf("%s: %+v after %d calls; want the plain %+v after none", name, got, f.calls(), plain)
									}
									continue
								}
								if f.calls() > 1 {
									t.Fatalf("%s: asked %d times for one decision", name, f.calls())
								}
								if got.Agent != "" {
									t.Fatalf("%s: crossed to %q", name, got.Agent)
								}
								if !got.Routed {
									continue
								}
								rank, curRank := agent.TierRank(got.Tier), tierOf(claude(), cur)
								floorRank := max(agent.TierRank(floor), 1)
								if floor != "" && agent.TierRank(floor) == 0 {
									floorRank = 3
								}
								switch {
								case curRank == 0:
									t.Fatalf("%s: routed from a model of no known size: %+v", name, got)
								case rank < floorRank:
									t.Fatalf("%s: chose %s, below the floor: %+v", name, got.Tier, got)
								case nd && rank < curRank:
									t.Fatalf("%s: moved to a smaller model though it may not be: %+v", name, got)
								case rank > max(curRank, floorRank):
									t.Fatalf("%s: chose %s, dearer than the work's own model or the floor: %+v", name, got.Tier, got)
								case got.Source != SourceFallback || got.Rule != "":
									t.Fatalf("%s: not a fallback: %+v", name, got)
								case got.Up && rank <= curRank:
									t.Fatalf("%s: Up without a stronger tier: %+v", name, got)
								}
								// Jev never chooses a cheaper model than the plain
								// fallback would have: it only lifts.
								if plain.Routed && got.Routed && rank < agent.TierRank(plain.Tier) {
									t.Fatalf("%s: %s, cheaper than the plain fallback's %s", name, got.Tier, plain.Tier)
								}
							}
						}
					}
				}
			}
		}
	}
}
