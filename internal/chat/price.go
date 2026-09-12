package chat

import (
	"fmt"
	"strings"

	"github.com/jmwri/flockdeck/internal/pricing"
)

// cost returns what a turn's tokens cost, and whether that is known at all.
//
// The prices are internal/pricing's, the one table the application states a
// price in, each with the day it was read and the page it was read from. A
// model that is not in it is shown with its token counts and nothing else,
// because a made-up price is worse than no price at all.
func cost(model string, u Usage) (float64, bool) {
	return pricing.Cost(model, pricing.Usage{In: u.In, Out: u.Out, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite})
}

// spend is what a conversation has cost so far.
//
// It is added up a turn at a time, at the rate of the model that answered that
// turn, because /model changes the model in the middle of a conversation and
// pricing every token so far at the new model's rate says something that never
// happened.
type spend struct {
	dollars float64
	// unpriced is set once any turn was answered by a model with no price here.
	unpriced bool
}

func (s *spend) add(model string, u Usage) {
	if u == (Usage{}) {
		return
	}
	if c, ok := cost(model, u); ok {
		s.dollars += c
		return
	}
	s.unpriced = true
}

// statusLine is the line under the conversation: which model is answering, what
// it has read and written, and what that has cost where the cost is known.
func statusLine(model string, u Usage, spent spend) string {
	parts := []string{model}
	if model == "" {
		// Nothing was asked for, so whatever the endpoint is configured with is
		// answering. Saying that is honest; naming a model would not be.
		parts = []string{"default model"}
	}
	if u == (Usage{}) && spent == (spend{}) {
		// Nothing has been asked yet, and "0 in · 0 out" in front of the
		// first prompt is a reading of nothing.
		return parts[0]
	}
	in := tokens(u.In) + " in"
	if u.CacheRead > 0 {
		in += " (" + tokens(u.CacheRead) + " cached)"
	}
	parts = append(parts, in, tokens(u.Out)+" out")
	// The cost is an estimate from the price table, never the bill, and is
	// marked "~" as the pane header marks the same figure; the tokens are
	// counted exactly and carry no mark.
	switch {
	case spent.dollars > 0 && spent.unpriced:
		// Part of it is known, and the rest was spent at a price nobody here
		// can say: what is known is a floor, and is shown as one.
		parts = append(parts, "~"+money(spent.dollars)+"+")
	case spent.dollars > 0:
		parts = append(parts, "~"+money(spent.dollars))
	}
	return strings.Join(parts, " · ")
}

// tokens writes a count the way somebody glancing at it reads it.
func tokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 10_000:
		return fmt.Sprintf("%dk", n/1000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprintf("%d", n)
}

// money writes a running cost with enough places to be worth showing. A turn of
// a few hundred tokens costs a fraction of a cent, and rounding that to two
// places shows every conversation costing nothing at all.
func money(v float64) string {
	switch {
	case v >= 1:
		return fmt.Sprintf("$%.2f", v)
	case v >= 0.01:
		return fmt.Sprintf("$%.3f", v)
	}
	return fmt.Sprintf("$%.4f", v)
}
