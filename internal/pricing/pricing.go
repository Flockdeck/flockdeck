// Package pricing is what a model costs, as its provider publishes it.
//
// It is the one table the application states a price in. The chat client adds
// up what a conversation has spent from it, the picker shows it beside a model,
// and routing weighs a switch of model against it. Nothing else in the code
// states a price.
//
// It is short and it is meant to be: a made-up price is worse than no price at
// all, so a model that is not in here is shown with its token counts and
// nothing else. Every rate says the day it was read and the page it was read
// from, because a price is a fact about a day rather than one that lasts, and
// a test fails a release whose table has not been read again for too long.
// Only a provider's own first-party rates are here: a model reached through a
// partner platform or a gateway is priced by whoever serves it, and is not
// guessed at.
package pricing

import (
	"regexp"
	"strings"
	"time"
)

// Rate is what a model costs, in dollars per million tokens.
type Rate struct {
	In  float64
	Out float64
	// CacheRead is what input read from the prompt cache costs, where it is
	// not the usual tenth of In. Writing to the cache is charged a quarter more
	// than In, which is Anthropic's five-minute write; only Anthropic's answers
	// report a cache write at all.
	CacheRead float64
	// Above, when set, is a prompt length in tokens beyond which a request is
	// charged the Long rates instead. Google prices its Pro models higher for a
	// long prompt.
	Above                     int
	LongIn, LongOut, LongRead float64
	// From and Until are the first and last days this rate is published for,
	// as YYYY-MM-DD; empty is unbounded. A price already announced to change
	// is two entries, one either side of the day it changes.
	From, Until string
	// Checked is the day the rate was read off Source, as YYYY-MM-DD.
	Checked string
	Source  string
}

// Entry is one model's rate for one stretch of days.
type Entry struct {
	Model string
	Rate
}

// Usage is the tokens one request read and wrote. In is every token of input,
// however it was priced; CacheRead and CacheWrite are the parts of it read from
// and written to a prompt cache.
type Usage struct {
	In, Out, CacheRead, CacheWrite int
}

// Day is the layout of every date in the table.
const Day = "2006-01-02"

// Where each provider publishes its prices, and the day they were read there.
const (
	checked   = "2026-09-12"
	anthropic = "https://platform.claude.com/docs/en/about-claude/pricing"
	openai    = "https://developers.openai.com/api/docs/pricing"
	google    = "https://ai.google.dev/gemini-api/docs/pricing"
)

// table is every rate this build knows. Models are matched exactly, or as a
// dated snapshot of the model (see snapshot); nothing else, so an id that only
// begins like a priced one -- "gpt-5.7" after "gpt-5" -- has no price rather
// than someone else's.
var table = []Entry{
	// Anthropic. A cache read on Fable 5.1 and Mythos 5.1 costs 0.025 of the
	// input price rather than the usual tenth.
	{"claude-fable-5-1", Rate{In: 10, Out: 50, CacheRead: 0.25, Checked: checked, Source: anthropic}},
	{"claude-mythos-5-1", Rate{In: 10, Out: 50, CacheRead: 0.25, Checked: checked, Source: anthropic}},
	{"claude-fable-5", Rate{In: 10, Out: 50, Checked: checked, Source: anthropic}},
	{"claude-mythos-5", Rate{In: 10, Out: 50, Checked: checked, Source: anthropic}},
	{"claude-opus-5", Rate{In: 5, Out: 25, Checked: checked, Source: anthropic}},
	{"claude-opus-4-8", Rate{In: 5, Out: 25, Checked: checked, Source: anthropic}},
	{"claude-opus-4-7", Rate{In: 5, Out: 25, Checked: checked, Source: anthropic}},
	{"claude-opus-4-6", Rate{In: 5, Out: 25, Checked: checked, Source: anthropic}},
	{"claude-opus-4-5", Rate{In: 5, Out: 25, Checked: checked, Source: anthropic}},
	{"claude-sonnet-5", Rate{In: 2, Out: 10, Checked: checked, Source: anthropic}},
	{"claude-sonnet-4-6", Rate{In: 3, Out: 15, Checked: checked, Source: anthropic}},
	{"claude-sonnet-4-5", Rate{In: 3, Out: 15, Checked: checked, Source: anthropic}},
	{"claude-haiku-4-5", Rate{In: 1, Out: 5, Checked: checked, Source: anthropic}},

	// OpenAI, standard tier. A cached read is a tenth of the input price on
	// every model here but the two Pro models, which the page gives no cached
	// price for: a cache read there is charged as ordinary input, which can
	// only overstate what it cost.
	{"gpt-6-astra", Rate{In: 10, Out: 50, Checked: checked, Source: openai}},
	{"gpt-5.6-sol", Rate{In: 4, Out: 20, Checked: checked, Source: openai}},
	{"gpt-5.6-terra", Rate{In: 2, Out: 12, Checked: checked, Source: openai}},
	{"gpt-5.6-luna", Rate{In: 0.20, Out: 1.20, Checked: checked, Source: openai}},
	{"gpt-5.5", Rate{In: 5, Out: 30, Checked: checked, Source: openai}},
	{"gpt-5.5-pro", Rate{In: 30, Out: 180, CacheRead: 30, Checked: checked, Source: openai}},
	{"gpt-5.4", Rate{In: 2.50, Out: 15, Checked: checked, Source: openai}},
	{"gpt-5.4-pro", Rate{In: 30, Out: 180, CacheRead: 30, Checked: checked, Source: openai}},
	{"gpt-5.4-mini", Rate{In: 0.75, Out: 4.50, Checked: checked, Source: openai}},
	{"gpt-5.4-nano", Rate{In: 0.20, Out: 1.25, Checked: checked, Source: openai}},
	{"gpt-5.3-codex", Rate{In: 1.75, Out: 14, Checked: checked, Source: openai}},
	{"gpt-5.2", Rate{In: 1.75, Out: 14, Checked: checked, Source: openai}},
	{"gpt-5.1", Rate{In: 1.25, Out: 10, Checked: checked, Source: openai}},
	{"gpt-5", Rate{In: 1.25, Out: 10, Checked: checked, Source: openai}},
	{"gpt-5-mini", Rate{In: 0.25, Out: 2, Checked: checked, Source: openai}},
	{"gpt-5-nano", Rate{In: 0.05, Out: 0.40, Checked: checked, Source: openai}},

	// Google, paid tier, text input. The Flash models from 3.6 on double in
	// price on 1 January 2027, as the page already says; the Pro models cost
	// more for a prompt of more than 200,000 tokens.
	{"gemini-3.8-flash", Rate{In: 0.75, Out: 3.75, Until: "2026-12-31", Checked: checked, Source: google}},
	{"gemini-3.8-flash", Rate{In: 1.50, Out: 7.50, From: "2027-01-01", Checked: checked, Source: google}},
	{"gemini-3.7-flash", Rate{In: 0.75, Out: 3.75, Until: "2026-12-31", Checked: checked, Source: google}},
	{"gemini-3.7-flash", Rate{In: 1.50, Out: 7.50, From: "2027-01-01", Checked: checked, Source: google}},
	{"gemini-3.6-flash", Rate{In: 0.75, Out: 3.75, Until: "2026-12-31", Checked: checked, Source: google}},
	{"gemini-3.6-flash", Rate{In: 1.50, Out: 7.50, From: "2027-01-01", Checked: checked, Source: google}},
	{"gemini-3.5-flash", Rate{In: 1.50, Out: 9, Checked: checked, Source: google}},
	{"gemini-3.5-flash-lite", Rate{In: 0.30, Out: 2.50, Checked: checked, Source: google}},
	{"gemini-3.1-flash-lite", Rate{In: 0.25, Out: 1.50, Checked: checked, Source: google}},
	{"gemini-3.1-pro-preview", Rate{In: 2, Out: 12, Above: 200_000, LongIn: 4, LongOut: 18, LongRead: 0.40,
		Checked: checked, Source: google}},
	{"gemini-2.5-pro", Rate{In: 1.25, Out: 10, Above: 200_000, LongIn: 2.50, LongOut: 15, LongRead: 0.25,
		Checked: checked, Source: google}},
	{"gemini-2.5-flash", Rate{In: 0.30, Out: 2.50, Checked: checked, Source: google}},
}

// Table returns a copy of every rate this build knows, for whatever has to
// list or check them.
func Table() []Entry {
	return append([]Entry(nil), table...)
}

// snapshot matches what may follow a model's id and still name that model: a
// dated snapshot of it, as "claude-haiku-4-5-20251001", "gpt-5-2025-08-07" or
// Google Cloud's "claude-haiku-4-5@20251001".
var snapshot = regexp.MustCompile(`^(-\d{8}|-\d{4}-\d{2}-\d{2}|@\d{8})$`)

// Lookup returns the rate a model is charged at on a day, and whether this
// table knows one.
func Lookup(model string, day time.Time) (Rate, bool) {
	model = strings.ToLower(strings.TrimSpace(model))
	// The Gemini API names its models "models/gemini-2.5-pro", and an id
	// copied from its own list keeps the prefix.
	model = strings.TrimPrefix(model, "models/")
	if model == "" {
		return Rate{}, false
	}
	on := day.Format(Day)
	for _, e := range table {
		rest, ok := strings.CutPrefix(model, e.Model)
		if !ok || (rest != "" && !snapshot.MatchString(rest)) {
			continue
		}
		if (e.From == "" || on >= e.From) && (e.Until == "" || on <= e.Until) {
			return e.Rate, true
		}
	}
	return Rate{}, false
}

// Cost returns what one request's tokens cost today, and whether that is known
// at all.
func Cost(model string, u Usage) (float64, bool) {
	return CostOn(model, u, time.Now())
}

// CostOn is Cost on a given day.
func CostOn(model string, u Usage, day time.Time) (float64, bool) {
	r, ok := Lookup(model, day)
	if !ok {
		return 0, false
	}
	return r.Cost(u), true
}

// Cost is what one request's tokens cost at this rate.
func (r Rate) Cost(u Usage) float64 {
	in, out, read := r.In, r.Out, r.CacheRead
	if r.Above > 0 && u.In > r.Above {
		in, out, read = r.LongIn, r.LongOut, r.LongRead
	}
	if read == 0 {
		read = in / 10
	}
	fresh := max(u.In-u.CacheRead-u.CacheWrite, 0)
	return (float64(fresh)*in + float64(u.CacheRead)*read +
		float64(u.CacheWrite)*in*1.25 + float64(u.Out)*out) / 1e6
}
