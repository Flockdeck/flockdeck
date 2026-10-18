// Package routejev is the route.Classifier that asks Jev: it rates how
// demanding a task is, so that the router's cost-strategy fallback can choose
// a tier to suit it.
//
// It is the router's side of the one path to Jev (internal/jevgate, which
// does the scrubbing, caching, capping, pausing and asking); this package
// supplies the questions, the bound on the task text, and the reading of the
// answer. The promises about the call are kept there:
//
//   - What is sent is the task text, cut to MaxTask, as the request's state,
//     beside three fixed questions. No repository content, no file the task
//     does not itself name, no conversation history, no path of this machine.
//   - A call is bounded by Timeout, and any failure -- no key, a refusal, a
//     rate limit, a deadline, an answer that is not the one asked for -- is
//     one error, which the router reads as "no answer" and decides as it
//     would have without Jev.
//   - A task is asked about once an hour (a cache keyed by a hash of the
//     bounded text), at most MaxPerMinute times a minute; after a failure
//     nothing is asked for a while (cooldown), so a Jev that is down costs one
//     Timeout, not one per row.
//
// The task text is redacted first, with jev.Scrub (record.Redact plus the
// names of secret files), because a prompt can have a key or a .env line
// pasted into it. That is best effort and by pattern: a secret with no
// recognisable shape, in text that does not name it as one, is sent as
// written, and so is the rest of the task. The setting that turns this on says
// the task text is sent. The cache is keyed by a hash of the redacted, bounded
// text, which is what is sent.
package routejev

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/creds"
	"github.com/jmwri/flockdeck/internal/jev"
	"github.com/jmwri/flockdeck/internal/jevgate"
	"github.com/jmwri/flockdeck/internal/route"
)

const (
	// MaxTask is the most of a task's text that is sent, in runes. A fan-out
	// row is a line or a short paragraph; a chat prompt pasted whole can be a
	// page of logs. 2000 runes (about 500 tokens) is far more than is needed
	// to tell a typo from a redesign, and it bounds both what leaves the
	// machine and what a call costs.
	MaxTask = 2000
	// Marker ends a task that was cut.
	Marker = " [truncated]"
	// Timeout bounds one classification, retries included (jev.Ask's own
	// 429 retries run inside it). A route decision is in line with a dialog
	// opening, so it must not wait on a third party: 2s is several times what
	// a single small structured call should take and short enough that a slow
	// Jev is noticed as a beat, not a hang. Prewarm asks all a fan-out's rows
	// at once, so the wait is one Timeout however many rows there are.
	Timeout = 2 * time.Second
	// MaxPerMinute caps calls that reach TypeSafe, cache hits not counted. A
	// person edits a fan-out's rows at typing speed and each new line is one
	// call; 20 a minute is more than that, and a paste of a few dozen rows
	// spends the minute's allowance and finds the rest routed as they would be
	// without Jev.
	MaxPerMinute = 20
	// CacheTTL and cacheCap bound the cache: the dialog routes the rows again
	// at every edit, so the same text comes round often, and an hour is longer
	// than a dialog stays open.
	CacheTTL = time.Hour
	cacheCap = 512
	// cooldown is how long nothing is asked after a failure; a refused key is
	// left for longer, since asking again with it gets the same answer.
	cooldown       = 30 * time.Second
	cooldownBadKey = 5 * time.Minute
)

// Errors a Classify can end in besides Jev's own.
var (
	ErrCooling  = jevgate.ErrPaused
	ErrRateCap  = jevgate.ErrRateCap
	ErrNoAnswer = errors.New("jev's answer is not usable")
)

// The questions. They are fixed text and say nothing about the user; the task
// is the state.
const (
	qDifficulty = "difficulty"
	qMechanical = "mechanical"
	qMultiFile  = "multifile"
)

func questions() map[string]jev.Question {
	return map[string]jev.Question{
		qDifficulty: jev.Score{
			Instructions: "How demanding is the software engineering task given as the state, for the model that will carry it out?",
			Levels: []any{
				"a trivial mechanical edit: a rename, a typo, a one-line change, running a command",
				"a small, local change: one function or one file, with an obvious approach",
				"a moderate change: one feature or bug, in a few files, needing some understanding of the code",
				"a substantial change: several components, needing investigation and care about how they interact",
				"open-ended architectural or design work: the approach itself has to be worked out",
			},
		},
		qMechanical: jev.Noul{
			Instructions: "The task is a small mechanical change that needs no design decisions.",
		},
		qMultiFile: jev.Noul{
			Instructions: "Doing the task requires reasoning across many files or components at once.",
		},
	}
}

// Classifier asks Jev through a jevgate.Gate with the cache, cap and
// cooldown described above.
type Classifier struct {
	gate *jevgate.Gate[route.Difficulty]
}

// New is a Classifier that asks with the client newClient returns.
func New(newClient func() (*jev.Client, error)) *Classifier {
	return &Classifier{gate: jevgate.New(jevgate.Config[route.Difficulty]{
		NewClient: newClient,
		Questions: questions(),
		Read:      func(_ string, res *jev.Result) (route.Difficulty, error) { return read(res) },
		Bound:     Bound,
		Timeout:   Timeout,
		PerMinute: MaxPerMinute,
		Cooldown:  cooldown, CooldownRefused: cooldownBadKey,
		Refused:  []error{jev.ErrUnauthorized, jev.ErrNoKey},
		CacheTTL: CacheTTL, CacheCap: cacheCap,
	})}
}

// Bound is the text that is sent for a task, after Scrub: trimmed, and cut to MaxTask runes
// with Marker after it when it was longer.
func Bound(task string) string {
	task = strings.TrimSpace(task)
	if utf8.RuneCountInString(task) <= MaxTask {
		return task
	}
	runes := []rune(task)
	return strings.TrimSpace(string(runes[:MaxTask])) + Marker
}

// Classify rates a task, from the cache if it was rated within CacheTTL, and
// otherwise by asking Jev once.
func (c *Classifier) Classify(task string) (route.Difficulty, error) {
	d, err := c.gate.Do(context.Background(), "", task)
	if errors.Is(err, jevgate.ErrNoText) {
		return route.Difficulty{}, fmt.Errorf("%w: no task", ErrNoAnswer)
	}
	return d, err
}

// read is the Difficulty in Jev's answer, or ErrNoAnswer where it is out of
// the ranges it should be in.
func read(res *jev.Result) (route.Difficulty, error) {
	s, err := res.Score(qDifficulty)
	if err != nil {
		return route.Difficulty{}, err
	}
	m, err := res.Noul(qMechanical)
	if err != nil {
		return route.Difficulty{}, err
	}
	f, err := res.Noul(qMultiFile)
	if err != nil {
		return route.Difficulty{}, err
	}
	for _, p := range []float64{s.Confidence, m.Probability, f.Probability} {
		if p < 0 || p > 1 {
			return route.Difficulty{}, fmt.Errorf("%w: a probability of %v", ErrNoAnswer, p)
		}
	}
	if s.Score < 0 || s.Score > 4 {
		return route.Difficulty{}, fmt.Errorf("%w: a score of %v on a rubric of 0 to 4", ErrNoAnswer, s.Score)
	}
	return route.Difficulty{Score: s.Score, Confidence: s.Confidence,
		Mechanical: m.Probability, MultiFile: f.Probability, Model: res.Model}, nil
}

// Enabled reports whether Jev could be asked at all: a key is set, in Settings or the environment. The
// policy's own switch is the other half, and For checks both.
func Enabled() bool { return jev.Key(StoredKey) != "" }

// StoredKey is where the key set in Settings comes from. It is a variable
// only so a test can put a fake there.
var StoredKey jev.KeyFunc = creds.JevKey

var (
	sharedMu sync.Mutex
	shared   *Classifier
	// APIBase is where the shared classifier asks, TypeSafe's own when empty.
	// It is set only by a test, to point the whole path at a fake server.
	APIBase string
)

// Reset forgets the shared classifier and with it its cache, cap and
// cooldown. Tests use it between cases; nothing else has a reason to.
func Reset() {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	shared = nil
}

// newClient is the client for the key as it is now, so a key changed under the
// running process is used by the next ask.
func newClient() (*jev.Client, error) {
	c, err := jev.NewClient(StoredKey)
	if err != nil {
		return nil, err
	}
	c.APIBase = APIBase
	return c, nil
}

// For is the classifier a policy may use: the process's one shared Classifier
// -- its cache, cap and cooldown are what make the calls bounded, so they must
// not be reset with every fan-out -- when the policy turned Jev on and the key
// is set, and nil otherwise, so that neither is asked about again downstream.
func For(p agent.RoutingPolicy) route.Classifier {
	if !p.Jev || p.Strategy != agent.StrategyCost || !Enabled() {
		return nil
	}
	sharedMu.Lock()
	defer sharedMu.Unlock()
	if shared == nil {
		shared = New(newClient)
	}
	return shared
}
