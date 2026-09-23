package route

import (
	"fmt"
	"strings"
	"sync"

	"github.com/jmwri/flockdeck/internal/agent"
)

// Difficulty is how demanding a Classifier judged a task to be. The rubric is
// five levels, 0 to 4, from a trivial mechanical edit to open-ended
// architectural work, and Score is where on it the task fell -- fractional
// where the classifier's probability was spread over more than one level.
type Difficulty struct {
	// Score is 0 to 4.
	Score float64
	// Confidence, 0 to 1, is how concentrated the classifier's answer was. It
	// is not the probability that Score is right: see jev.ChoiceAnswer.
	Confidence float64
	// Mechanical is the probability the task is a small mechanical change, and
	// MultiFile that doing it takes reasoning across several files.
	Mechanical, MultiFile float64
	// Model is which model answered, for the log.
	Model string
}

// Classifier rates a task's difficulty. The Router asks it only when a
// decision could change with the answer, never for work a rule decided, and
// treats every error as "no answer". The deadline, cache and rate limit are
// the implementation's to keep (see package routejev), so that Decide stays a
// function of its inputs and a fake stands in for it in a test.
type Classifier interface {
	Classify(task string) (Difficulty, error)
}

// JevNote is a classification a decision was made with, as the log records it.
type JevNote struct {
	Score      float64 `json:"score"`
	Confidence float64 `json:"confidence"`
	Mechanical float64 `json:"mechanical"`
	MultiFile  float64 `json:"multiFile"`
	// Tier is the tier the classification asked for, before the floor and the
	// work's own model bounded it.
	Tier  string `json:"tier"`
	Model string `json:"model,omitempty"`
}

// The numbers a classification is acted on by. None can be calibrated without
// real answers, so each sits on the cautious side of the question it decides;
// the log's override rate (FallbackStats) is how a person would later say
// whether they were right.
const (
	// MinConfidence is the concentration below which an answer is ignored.
	// Confidence is concentration, not correctness: TypeSafe's own example has
	// 0.85 on the choice and a confidence of 0.78. Below 0.5 the probability
	// is spread over several levels, so Score is a mean of levels that mean
	// different things. It gates only lifting the choice above the floor, and
	// that is capped by the work's own model, so a wrongly confident answer
	// costs at most what the work would have cost without routing.
	MinConfidence = 0.5
	// midAt and topAt are where on the 0-4 rubric a Score asks for the mid and
	// top tiers: 1.5, between level 1 (small, local) and 2 (moderate), and 3.0,
	// level 3 (substantial, several components) itself. Top is the dearest, so
	// it takes an answer centred on it, not one that only leans that way.
	midAt, topAt = 1.5, 3.0
	// contradicts is the probability at which a Noul is taken to disagree
	// with the Score.
	contradicts = 0.7
)

// tierFor is the tier rank a classification asks for, or ok false where it is
// not to be acted on: not confident enough, out of range, or contradicting
// itself.
func tierFor(d Difficulty) (rank int, ok bool) {
	if d.Confidence < MinConfidence || d.Score < 0 || d.Score > 4 {
		return 0, false
	}
	rank = 1
	switch {
	case d.Score >= topAt:
		rank = 3
	case d.Score >= midAt:
		rank = 2
	}
	// Rated demanding yet very likely a small mechanical change is the
	// classifier disagreeing with itself: not acted on, so the work gets what
	// it would have without Jev. Rated trivial yet very likely to reach across
	// many files is lifted to mid, the safer side of the doubt.
	if rank > 1 && d.Mechanical >= contradicts {
		return 0, false
	}
	if rank == 1 && d.MultiFile >= contradicts {
		rank = 2
	}
	return rank, true
}

// jevOn reports whether this router may consult a classifier at all: the
// policy turned it on, there is one, and the strategy is the only one that has
// a fallback for it to refine.
func (r *Router) jevOn() bool {
	return r.jev && r.classifier != nil && r.strategy == agent.StrategyCost
}

// WithClassifier lets the router's cost-strategy fallback ask c how demanding
// a fan-out row is. It does nothing unless the policy itself turned Jev on, so
// a caller may pass whatever classifier it has and the policy stays the only
// switch. A nil c leaves the router as it was.
func (r *Router) WithClassifier(c Classifier) *Router {
	if c != nil {
		r.classifier = c
	}
	return r
}

// wantsJev reports whether deciding in would ask the classifier: it is a
// fan-out row (the one kind the user sees, can override, and the log can
// score), no rule matched, and the answer could move the choice -- the work's
// model is above the floor, since the choice is never lifted past it.
func (r *Router) wantsJev(in Input) bool {
	if !r.On() || !r.jevOn() || in.Kind != KindFanout {
		return false
	}
	files := filesIn(in.Task)
	words := len(strings.Fields(in.Task))
	crossOK := r.crossAgent && in.OtherAgent != nil
	for _, ru := range r.rules {
		if ru.matches(in, words, files, crossOK) {
			return false
		}
	}
	return tierOf(in.Agent, in.Current) > r.floor
}

// Prewarm asks the classifier about every input Decide would ask it about, all
// at once, so that a fan-out of a dozen rows waits for one answer's time and
// not a dozen. The answers are the classifier's to keep; Decide, called
// after, finds them. Identical tasks are asked once.
func (r *Router) Prewarm(ins []Input) {
	seen := map[string]bool{}
	var wg sync.WaitGroup
	for _, in := range ins {
		if seen[in.Task] || !r.wantsJev(in) {
			continue
		}
		seen[in.Task] = true
		wg.Add(1)
		go func(task string) {
			defer wg.Done()
			_, _ = r.classifier.Classify(task)
		}(in.Task)
	}
	wg.Wait()
}

// jevReason is the sentence for a fallback a classification shaped.
func jevReason(d Difficulty, asked, got int) string {
	s := fmt.Sprintf("no rule matched; Jev rated the task %.1f of 4 (confidence %.2f), which asks for %s", d.Score, d.Confidence, tierName(asked))
	if got == asked {
		return s + " → " + tierName(got)
	}
	return s + ", which the floor and the work's own model bound to " + tierName(got) + " → " + tierName(got)
}
