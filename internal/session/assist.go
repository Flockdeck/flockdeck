package session

import (
	"context"
	"crypto/sha256"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/jev"
)

// StatusAssist lets Jev (see internal/jev) settle the one call the output-
// reading fallback cannot make: a pane that has gone quiet, whose agent
// reports no lifecycle, is either finished (idle) or stopped on a question
// (waiting), and the bell and the quiet timer look the same for both.
//
// PRIVACY. Asking sends the tail of the pane's terminal output to a third
// party, TypeSafe, which nothing else in Flockdeck does with terminal content.
// So it is off unless BOTH the user has turned the setting on (Enabled) and
// TYPESAFE_API_KEY is set, and what is sent is only the last assistTailLines
// lines, at most assistTailBytes, with escape sequences taken out: never the
// scrollback, the pane's name, or its working directory. No redaction of
// secrets that may be on screen is done -- there is no general helper for it
// in the code base, and a weak one would promise more than it kept.
//
// SAFETY. It only ever supplements the fallback and only ever moves a pane
// from the quiet-timer's idle to waiting or blocked, which is the direction
// the fallback is blind in. Every failure -- off, no key, an error, a timeout,
// rate limiting, a low-confidence or agreeing answer, a pane that has moved on
// while the answer was on its way -- leaves the status exactly as the
// heuristic had it.
//
// The zero value is not usable; a nil *StatusAssist is, and asks nothing.
type StatusAssist struct {
	// Enabled reports whether the user has turned the setting on. It is asked
	// on every decision rather than once, so turning it off stops the next
	// ask and not the next launch.
	Enabled func() bool
	// NewClient is where a client comes from; nil is jev.NewClientFromEnv. It is
	// asked each time so a key exported after Flockdeck started is found, and a
	// test points it at a fake server.
	NewClient func() (*jev.Client, error)
	// Now is the clock; nil is time.Now.
	Now func() time.Time
	// Logger receives, at debug, every answer that disagrees with the
	// heuristic, acted on or not: it is the record real-world thresholds are
	// tuned from. Nil is slog.Default.
	Logger *slog.Logger

	mu sync.Mutex
	// calls are the start times of the calls made in the last minute, across
	// every pane; pausedUntil holds all calls back after a refusal.
	calls       []time.Time
	pausedUntil time.Time
	panes       map[string]*assistPane
}

// assistPane is what is remembered per pane: the last tail asked about and what
// came of it, when it was asked, and the calls made in the last hour.
type assistPane struct {
	hash    [sha256.Size]byte
	hasHash bool
	verdict assistVerdict
	lastAt  time.Time
	calls   []time.Time
}

// assistVerdict is Jev's reading of a tail, as far as it is acted on.
type assistVerdict struct {
	status Status
	// ok is false when Jev had nothing to add the heuristic did not know.
	ok bool
}

// What is sent, and how often.
const (
	// assistTailLines and assistTailBytes bound what leaves the machine. What a
	// pane is stopped on is at the bottom of its screen, so a screenful
	// suffices; more would only send more of the user's terminal to a third
	// party for no better answer.
	assistTailLines = 30
	assistTailBytes = 2000
	// assistRawBytes is how much raw output is read to find those lines, which
	// is more than they are worth once the escape sequences are out.
	assistRawBytes = 16 << 10

	// assistPaneInterval is the least time between two calls for one pane, and
	// assistPaneHourly the most in an hour: a pane that keeps going quiet on
	// changing output is a busy one, and the quiet timer has already waited out
	// each gap. assistGlobalMinute is the most across all panes in a minute, so
	// fifteen agents finishing together cost a bounded, small number of calls.
	assistPaneInterval = 15 * time.Second
	assistPaneHourly   = 12
	assistGlobalMinute = 10
	// assistTimeout bounds one ask, retries included (jev retries a 429 itself).
	assistTimeoutDefault = 12 * time.Second
	// A refusal holds every pane back: a rate limit or an overload for a
	// minute, a key or a request TypeSafe will not accept for a quarter of an
	// hour, since asking again gets the same answer.
	assistPauseTransient = time.Minute
	assistPauseRefused   = 15 * time.Minute
)

// assistTimeout is a var so a test does not have to wait it out.
var assistTimeout = assistTimeoutDefault

// The bar an answer must clear to override the heuristic. A Choice's
// Confidence is how concentrated its probabilities are, not how likely the
// pick is to be right (see jev.ChoiceAnswer), so it is not trusted alone: the
// probability of the option itself must also be high, and for a question being
// asked the separately-posed yes/no must agree. Being wrong is asymmetric --
// a false waiting is an alarm nobody needed, a missed one is today's behaviour
// -- so the numbers err towards missing. They are a first guess, not tuned
// against real answers, which needs a live key: the debug log of
// disagreements is what to tune them from.
const (
	assistMinConfidence  = 0.75
	assistMinProbability = 0.80
	assistMinAsking      = 0.80
)

// The keys of the batch, and the options of the Choice. The option names are
// what comes back, so they are the same words as Status.String.
const (
	keyState  = "state"
	keyAsking = "asking"
)

var assistQuestions = map[string]jev.Question{
	keyState: jev.Choice{
		Instructions: "The state is the last lines of a terminal in which an AI coding agent is running. " +
			"Which best describes what the agent is doing right now?",
		Options: map[string]any{
			"working": "It is still busy: output is scrolling, a tool or command is running, or it is thinking.",
			"waiting": "It has stopped and cannot go on until the user answers: a permission prompt, a yes/no question, a menu of options, or a request for input.",
			"idle":    "It has finished what it was doing and is sitting at its ordinary prompt, waiting for the user's next instruction.",
			"blocked": "A tool call was refused outright and it gave up, so it has stopped without anything to answer.",
		},
	},
	keyAsking: jev.Noul{
		Instructions: "The agent is asking the user a question or for permission, and cannot continue until the user answers.",
	},
}

func (a *StatusAssist) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *StatusAssist) logger() *slog.Logger {
	if a.Logger != nil {
		return a.Logger
	}
	return slog.Default()
}

// Forget drops what is remembered about a pane that has gone.
func (a *StatusAssist) Forget(paneID string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	delete(a.panes, paneID)
	a.mu.Unlock()
}

// Assess asks Jev what a pane whose heuristic status is idle is really doing,
// given the tail of its output as sendable text (see assistTail). It returns
// the status to move the pane to and true only when Jev is confident the pane
// is waiting or blocked; otherwise false, and the heuristic stands. It blocks
// on the network, so it must not be called from the reader or under a lock.
func (a *StatusAssist) Assess(ctx context.Context, paneID, tail string) (Status, bool) {
	if a == nil || a.Enabled == nil || !a.Enabled() || strings.TrimSpace(tail) == "" {
		return StatusIdle, false
	}
	newClient := a.NewClient
	if newClient == nil {
		newClient = jev.NewClientFromEnv
	}
	// Without a key there is nothing to do and nothing to say: it is the
	// ordinary state of a machine that has not opted into TypeSafe.
	client, err := newClient()
	if err != nil {
		return StatusIdle, false
	}

	hash := sha256.Sum256([]byte(tail))
	now := a.now()
	a.mu.Lock()
	p := a.panes[paneID]
	if p == nil {
		if a.panes == nil {
			a.panes = map[string]*assistPane{}
		}
		p = &assistPane{}
		a.panes[paneID] = p
	}
	// The same tail as last time has the same answer: nothing is asked again.
	if p.hasHash && p.hash == hash {
		v := p.verdict
		a.mu.Unlock()
		return v.status, v.ok
	}
	if !a.reserve(p, now) {
		a.mu.Unlock()
		return StatusIdle, false
	}
	a.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, assistTimeout)
	defer cancel()
	res, err := client.Ask(ctx, tail, assistQuestions)
	if err != nil {
		a.mu.Lock()
		switch {
		case errors.Is(err, jev.ErrUnauthorized), errors.Is(err, jev.ErrInvalidRequest):
			a.pausedUntil = a.now().Add(assistPauseRefused)
		case errors.Is(ctx.Err(), context.Canceled):
			// The pane went away: nothing is wrong with TypeSafe.
		default:
			a.pausedUntil = a.now().Add(assistPauseTransient)
		}
		a.mu.Unlock()
		a.logger().Debug("jev status: no answer, keeping the heuristic", "pane", paneID, "err", err)
		return StatusIdle, false
	}

	v := a.read(paneID, res)
	a.mu.Lock()
	p.hash, p.hasHash, p.verdict = hash, true, v
	a.mu.Unlock()
	return v.status, v.ok
}

// reserve counts a call against the limits, or refuses it. It is called with
// a.mu held.
func (a *StatusAssist) reserve(p *assistPane, now time.Time) bool {
	if now.Before(a.pausedUntil) {
		return false
	}
	if !p.lastAt.IsZero() && now.Sub(p.lastAt) < assistPaneInterval {
		return false
	}
	p.calls = pruneBefore(p.calls, now.Add(-time.Hour))
	if len(p.calls) >= assistPaneHourly {
		return false
	}
	a.calls = pruneBefore(a.calls, now.Add(-time.Minute))
	if len(a.calls) >= assistGlobalMinute {
		return false
	}
	p.lastAt = now
	p.calls = append(p.calls, now)
	a.calls = append(a.calls, now)
	return true
}

func pruneBefore(ts []time.Time, cutoff time.Time) []time.Time {
	i := 0
	for i < len(ts) && ts[i].Before(cutoff) {
		i++
	}
	return ts[i:]
}

// read turns Jev's answers into a verdict, logging every disagreement with the
// idle the heuristic gave.
func (a *StatusAssist) read(paneID string, res *jev.Result) assistVerdict {
	choice, err := res.Choice(keyState)
	if err != nil {
		return assistVerdict{}
	}
	asking, err := res.Noul(keyAsking)
	if err != nil {
		return assistVerdict{}
	}
	if choice.Choice != "idle" {
		a.logger().Debug("jev status: disagrees with the heuristic (idle)", "pane", paneID,
			"jev", choice.Choice, "confidence", choice.Confidence,
			"probabilities", choice.Probabilities, "asking", asking.Probability, "model", res.Model)
	}
	sure := choice.Confidence >= assistMinConfidence && choice.Probabilities[choice.Choice] >= assistMinProbability
	switch {
	case sure && choice.Choice == "waiting" && asking.Probability >= assistMinAsking:
		return assistVerdict{status: StatusWaiting, ok: true}
	case sure && choice.Choice == "blocked":
		return assistVerdict{status: StatusBlocked, ok: true}
	}
	return assistVerdict{}
}

// assistTail is the text that may be sent: the end of the pane's output with
// escape sequences removed, cut to assistTailLines lines and assistTailBytes
// bytes. raw is the pane's own recent output, which the caller has cut to a
// generous size already.
func assistTail(raw []byte) string {
	text := strings.TrimRight(stripANSI(raw), " \t\r\n")
	lines := strings.Split(text, "\n")
	if len(lines) > assistTailLines {
		lines = lines[len(lines)-assistTailLines:]
	}
	text = strings.Join(lines, "\n")
	if len(text) > assistTailBytes {
		text = text[len(text)-assistTailBytes:]
		// Not in the middle of a character.
		for len(text) > 0 && !utf8.RuneStart(text[0]) {
			text = text[1:]
		}
	}
	return strings.TrimSpace(text)
}

// assistIdle is the goroutine that follows a pane's move to idle by the quiet
// timer: it asks Jev, off to the side of the reader, and applies a confident
// answer only if the pane is still exactly as it was asked about. written and
// since are how much the pane had printed, and when its idle began, at that
// moment.
func (s *Session) assistIdle(written int64, since time.Time) {
	if s.assistDone != nil {
		defer s.assistDone()
	}
	// The tail is read under the lock, and only if the pane is still the one
	// being asked about: nothing is sent for one that has already moved on.
	s.mu.RLock()
	stale := s.staleForAssist(written, since)
	var raw []byte
	if !stale {
		var truncated bool
		raw, truncated = s.history.tail(assistRawBytes)
		if truncated {
			raw = dropPartialLine(raw)
		}
	}
	s.mu.RUnlock()
	if stale {
		return
	}
	tail := assistTail(raw)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if s.reaped != nil {
		// A pane whose process is gone has nothing to be asked about.
		go func() {
			select {
			case <-s.reaped:
				cancel()
			case <-ctx.Done():
			}
		}()
	}
	st, ok := s.assist.Assess(ctx, s.ID, tail)
	if !ok {
		return
	}

	s.mu.Lock()
	// Checked again, now that the answer is in: the pane may have printed,
	// been answered, heard from a hook or exited while it was on its way, and
	// an answer about how it was is not one about how it is.
	if s.staleForAssist(written, since) {
		s.mu.Unlock()
		return
	}
	s.status = st
	s.statusSince = time.Now()
	s.mu.Unlock()
	s.changed()
}

// staleForAssist reports whether the pane is no longer as it was when it was
// asked about: not idle by the quiet timer any more, having printed since, or
// under a hook's say-so, or gone. It is called with s.mu held.
func (s *Session) staleForAssist(written int64, since time.Time) bool {
	return s.status != StatusIdle || s.closed || s.hooksSeen ||
		s.written != written || !s.statusSince.Equal(since)
}
