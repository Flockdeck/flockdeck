package chat

import (
	"fmt"
	"strings"
)

// rate is what a model costs per million tokens.
type rate struct {
	in  float64
	out float64
	// read is what input read from the prompt cache costs, where it is not the
	// usual tenth of in. Writing to the cache costs a quarter more than in.
	read float64
}

// rates are published prices, in dollars per million tokens, matched against
// the start of a model id.
//
// It is short and it is meant to be: a made-up price is worse than no price at
// all, so a model that is not in here is shown with its token counts and
// nothing else. These are Anthropic's own first-party rates as published on
// 2026-06-24; a model reached through a partner platform, and every other
// vendor, is priced by whoever serves it and is not guessed at here. Extending
// the table is a line each.
var rates = map[string]rate{
	"claude-fable-5-1":  {in: 10, out: 50, read: 0.25},
	"claude-fable-5":    {in: 10, out: 50},
	"claude-mythos-5":   {in: 10, out: 50},
	"claude-opus-5":     {in: 5, out: 25},
	"claude-opus-4-8":   {in: 5, out: 25},
	"claude-opus-4-7":   {in: 5, out: 25},
	"claude-opus-4-6":   {in: 5, out: 25},
	"claude-sonnet-5":   {in: 2, out: 10},
	"claude-sonnet-4-6": {in: 3, out: 15},
	"claude-haiku-4-5":  {in: 1, out: 5},
}

// cost returns what a turn's tokens cost, and whether that is known at all.
//
// The longest matching prefix wins, so a dated variant of a model is priced as
// the model it is a variant of, and "claude-opus-4-8" is not read as
// "claude-opus-4".
func cost(model string, u Usage) (float64, bool) {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return 0, false
	}
	best, found := rate{}, ""
	for prefix, r := range rates {
		if strings.HasPrefix(model, prefix) && len(prefix) > len(found) {
			best, found = r, prefix
		}
	}
	if found == "" {
		return 0, false
	}
	read := best.read
	if read == 0 {
		read = best.in / 10
	}
	fresh := max(u.In-u.CacheRead-u.CacheWrite, 0)
	return (float64(fresh)*best.in + float64(u.CacheRead)*read +
		float64(u.CacheWrite)*best.in*1.25 + float64(u.Out)*best.out) / 1e6, true
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
	switch {
	case spent.dollars > 0 && spent.unpriced:
		// Part of it is known, and the rest was spent at a price nobody here
		// can say: what is known is a floor, and is shown as one.
		parts = append(parts, money(spent.dollars)+"+")
	case spent.dollars > 0:
		parts = append(parts, money(spent.dollars))
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
