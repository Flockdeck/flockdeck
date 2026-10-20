// Package structuredtest is a conformance test for the panes permissionAnswer
// talks to (server.StructuredPane), and a reference implementation that passes
// it. The adapter the workspace writes for a structured pane runs the test
// against itself:
//
//	structuredtest.Conformance(t, server.ErrPermissionStale, func(t *testing.T) structuredtest.Harness { ... })
//
// It exists because the server asks a pane for its card and then answers it in
// two calls, so that exactly one answer wins, and that no answer is taken for a
// later card with the same request id, is the pane's to guarantee.
//
// The package does not import internal/server, so that the server's own tests
// can use the reference implementation.
package structuredtest

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/acp/bridge"
)

// Pane is what the server needs of a structured pane: the same methods as
// server.StructuredPane.
//
// AnswerPermission must be atomic, because two windows can answer at once and
// exactly one answer is taken, and it must not block: the server calls it on its
// own goroutine, which every window waits on. It returns the stale error when
// no card with this Seq and RequestID is waiting, wrapped or not, and another
// error for an option that is not on that card.
type Pane interface {
	PendingPermission() *bridge.Card
	AnswerPermission(seq uint64, requestID, optionID string) error
}

// Answer is one answer a pane took.
type Answer struct {
	Seq       uint64
	RequestID string
	OptionID  string
}

// Harness is a pane under test and the means to drive it.
type Harness struct {
	Pane Pane
	// Open makes a card pending, as the agent asking would, and returns it with
	// the Seq the pane gave it. A card already pending is replaced or queued
	// behind, whichever the pane does; the test only opens a second card after
	// the first is closed, by Close or by an answer.
	Open func(requestID string, options []acp.PermissionOption) bridge.Card
	// Answered is every answer the pane took, in order.
	Answered func() []Answer
	// End has the agent end a request without an answer, as it does when it is
	// cancelled or the agent gives up. The card is no longer pending.
	End func(card bridge.Card)
	// Close ends the pane. After it nothing is pending and every answer is
	// stale.
	Close func()
}

// Conformance runs the checks as subtests. stale is the error a pane returns
// for a request that is no longer waiting (server.ErrPermissionStale). newPane
// is called for each check and returns a new pane.
func Conformance(t *testing.T, stale error, newPane func(t *testing.T) Harness) {
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			for _, f := range c.run(stale, newPane(t), func() Harness { return newPane(t) }) {
				t.Error(f)
			}
		})
	}
}

var opts = []acp.PermissionOption{
	{OptionID: "opt-a", Name: "Allow", Kind: "allow_once"},
	{OptionID: "opt-b", Name: "Reject", Kind: "reject_once"},
}

// within runs f and reports a failure if it has not returned in five seconds.
func within(what string, f func()) (failure string) {
	done := make(chan struct{})
	go func() { defer close(done); f() }()
	select {
	case <-done:
		return ""
	case <-time.After(5 * time.Second):
		return what + " did not return: AnswerPermission must not block"
	}
}

type check struct {
	name string
	// run gets one pane, and a function for more, and returns what failed.
	run func(stale error, h Harness, more func() Harness) []string
}

var checks = []check{
	{"one of several concurrent answers is taken", func(stale error, h Harness, _ func() Harness) []string {
		var out []string
		card := h.Open("7", opts)
		const n = 8
		errs := make([]error, n)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				errs[i] = h.Pane.AnswerPermission(card.Seq, card.RequestID, opts[i%2].OptionID)
			}(i)
		}
		close(start)
		if f := within("the answers", wg.Wait); f != "" {
			return []string{f}
		}
		taken := 0
		for _, err := range errs {
			switch {
			case err == nil:
				taken++
			case !errors.Is(err, stale):
				out = append(out, fmt.Sprintf("a losing answer returned %v, want the stale error", err))
			}
		}
		if taken != 1 {
			out = append(out, fmt.Sprintf("%d answers were taken, want exactly 1", taken))
		}
		if got := h.Answered(); len(got) != 1 || got[0].Seq != card.Seq {
			out = append(out, fmt.Sprintf("answered = %+v", got))
		}
		if h.Pane.PendingPermission() != nil {
			out = append(out, "the card is still pending after it was answered")
		}
		return out
	}},

	{"an answer for an old card is not taken by a new card with the same request id", func(stale error, h Harness, _ func() Harness) []string {
		var out []string
		old := h.Open("7", opts)
		h.End(old)
		fresh := h.Open("7", opts)
		if fresh.Seq == old.Seq {
			return []string{fmt.Sprintf("two cards share the Seq %d", fresh.Seq)}
		}
		var err error
		if f := within("the answer", func() { err = h.Pane.AnswerPermission(old.Seq, old.RequestID, "opt-a") }); f != "" {
			return []string{f}
		}
		if !errors.Is(err, stale) {
			out = append(out, fmt.Sprintf("an answer for the old card returned %v, want the stale error", err))
		}
		if len(h.Answered()) != 0 {
			out = append(out, fmt.Sprintf("the new card was answered: %+v", h.Answered()))
		}
		if p := h.Pane.PendingPermission(); p == nil || p.Seq != fresh.Seq {
			out = append(out, fmt.Sprintf("pending = %+v, want the new card", p))
		}
		return out
	}},

	{"an answer racing a new card never lands on the new card", func(stale error, _ Harness, more func() Harness) []string {
		for i := 0; i < 100; i++ {
			h := more()
			old := h.Open("7", opts)
			var fresh bridge.Card
			var wg sync.WaitGroup
			wg.Add(2)
			go func() { defer wg.Done(); _ = h.Pane.AnswerPermission(old.Seq, old.RequestID, "opt-a") }()
			go func() {
				defer wg.Done()
				// The agent ends the request and asks again with the same id.
				h.End(old)
				fresh = h.Open("7", opts)
			}()
			if f := within("the race", wg.Wait); f != "" {
				return []string{f}
			}
			for _, a := range h.Answered() {
				if a.Seq == fresh.Seq && fresh.Seq != old.Seq {
					return []string{fmt.Sprintf("round %d: the answer for card %d was taken by card %d", i, old.Seq, fresh.Seq)}
				}
			}
			h.Close()
		}
		return nil
	}},

	{"the right card number with the wrong request id is stale", func(stale error, h Harness, _ func() Harness) []string {
		var out []string
		card := h.Open("7", opts)
		var err error
		if f := within("the answer", func() { err = h.Pane.AnswerPermission(card.Seq, "8", "opt-a") }); f != "" {
			return []string{f}
		}
		if !errors.Is(err, stale) {
			out = append(out, fmt.Sprintf("err = %v, want the stale error", err))
		}
		if len(h.Answered()) != 0 || h.Pane.PendingPermission() == nil {
			out = append(out, "an answer with the wrong request id changed the card")
		}
		return out
	}},

	{"an option that is not on the card is refused and leaves it pending", func(stale error, h Harness, _ func() Harness) []string {
		var out []string
		card := h.Open("7", opts)
		var err error
		if f := within("the answer", func() { err = h.Pane.AnswerPermission(card.Seq, card.RequestID, "opt-z") }); f != "" {
			return []string{f}
		}
		if err == nil || errors.Is(err, stale) {
			out = append(out, fmt.Sprintf("err = %v, want an error that is not the stale one", err))
		}
		if len(h.Answered()) != 0 || h.Pane.PendingPermission() == nil {
			out = append(out, "a refused answer changed the card")
		}
		return out
	}},

	{"an answer after close is stale", func(stale error, h Harness, _ func() Harness) []string {
		var out []string
		card := h.Open("7", opts)
		h.Close()
		var err error
		if f := within("the answer", func() { err = h.Pane.AnswerPermission(card.Seq, card.RequestID, "opt-a") }); f != "" {
			return []string{f}
		}
		if !errors.Is(err, stale) {
			out = append(out, fmt.Sprintf("err = %v, want the stale error", err))
		}
		if h.Pane.PendingPermission() != nil || len(h.Answered()) != 0 {
			out = append(out, "a closed pane has a card or took an answer")
		}
		return out
	}},
}

// Failures runs the same checks and returns what failed, for a test that expects
// a pane to fail.
func Failures(stale error, newPane func() Harness) []string {
	var out []string
	for _, c := range checks {
		for _, f := range c.run(stale, newPane(), newPane) {
			out = append(out, c.name+": "+f)
		}
	}
	return out
}
