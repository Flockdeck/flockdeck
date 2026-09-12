package pricing

import (
	"math"
	"net/url"
	"os"
	"testing"
	"time"
)

func day(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse(Day, s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestCostIsOnlyClaimedWhenItIsKnown(t *testing.T) {
	on := day(t, checked)
	tests := []struct {
		name  string
		model string
		usage Usage
		want  float64
		known bool
	}{
		{"a priced model", "claude-sonnet-5", Usage{In: 1_000_000, Out: 1_000_000}, 12, true},
		{"a dated snapshot is its model", "claude-haiku-4-5-20251001", Usage{In: 1_000_000}, 1, true},
		{"an OpenAI snapshot", "gpt-5-2025-08-07", Usage{In: 1_000_000}, 1.25, true},
		{"Google Cloud's spelling of a snapshot", "claude-haiku-4-5@20251001", Usage{Out: 1_000_000}, 5, true},
		{"the Gemini API's own prefix", "models/gemini-2.5-flash", Usage{In: 1_000_000}, 0.30, true},
		{"case and space are forgiven", "  Claude-Opus-5 ", Usage{In: 1_000_000}, 5, true},
		// Matched by prefix, each of these was priced as a model it is not:
		// a newer GPT as GPT-5, and a Pro model at a sixth of its price.
		{"a model that only begins like a priced one", "gpt-5.7-nova", Usage{In: 1_000_000}, 0, false},
		{"a Pro model is not its base model", "gpt-5.5-pro", Usage{In: 1_000_000}, 30, true},
		{"a model nobody priced here", "qwen3-coder", Usage{In: 1_000_000, Out: 500_000}, 0, false},
		{"no model named at all", "", Usage{In: 10}, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, known := CostOn(tc.model, tc.usage, on)
			if known != tc.known {
				t.Fatalf("known = %v, want %v", known, tc.known)
			}
			if known && !near(got, tc.want) {
				t.Errorf("cost = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCacheReadsCostWhatTheProviderSays(t *testing.T) {
	on := day(t, checked)
	u := Usage{In: 1_000_000, CacheRead: 1_000_000}
	if got, _ := CostOn("claude-opus-5", u, on); !near(got, 0.50) {
		t.Errorf("a million cached tokens on Opus 5 cost %v, want the tenth of $5", got)
	}
	if got, _ := CostOn("claude-fable-5-1", u, on); !near(got, 0.25) {
		t.Errorf("a million cached tokens on Fable 5.1 cost %v, want $0.25", got)
	}
	// A cache write is a quarter more than the input it replaces.
	if got, _ := CostOn("claude-haiku-4-5", Usage{In: 1_000_000, CacheWrite: 1_000_000}, on); !near(got, 1.25) {
		t.Errorf("a million tokens written to the cache on Haiku cost %v, want $1.25", got)
	}
}

func TestALongPromptIsChargedTheLongRate(t *testing.T) {
	on := day(t, checked)
	short, _ := CostOn("gemini-3.1-pro-preview", Usage{In: 200_000}, on)
	long, _ := CostOn("gemini-3.1-pro-preview", Usage{In: 200_001}, on)
	if !near(short, 0.4) {
		t.Errorf("200k tokens cost %v, want $0.40 at $2", short)
	}
	if !near(long, 200_001*4/1e6) {
		t.Errorf("a prompt over 200k cost %v, want it all at $4", long)
	}
}

func TestAnAnnouncedPriceChangeTakesEffectOnItsDay(t *testing.T) {
	u := Usage{In: 1_000_000}
	before, _ := CostOn("gemini-3.8-flash", u, day(t, "2026-12-31"))
	after, _ := CostOn("gemini-3.8-flash", u, day(t, "2027-01-01"))
	if !near(before, 0.75) || !near(after, 1.50) {
		t.Errorf("3.8 Flash costs %v on the last day of 2026 and %v on the first of 2027, want $0.75 and $1.50", before, after)
	}
}

// Every rate says when and where it was read, and describes a price that can
// be charged: an entry without them is a figure nobody can check.
func TestEveryPriceSaysWhenAndWhereItWasRead(t *testing.T) {
	hosts := map[string]bool{
		"platform.claude.com": true, "developers.openai.com": true, "ai.google.dev": true,
	}
	spans := map[string][][2]string{}
	for _, e := range Table() {
		if _, err := time.Parse(Day, e.Checked); err != nil {
			t.Errorf("%s: checked %q is not a date: %v", e.Model, e.Checked, err)
		}
		u, err := url.Parse(e.Source)
		if err != nil || u.Scheme != "https" || !hosts[u.Host] {
			t.Errorf("%s: source %q is not a provider's own page", e.Model, e.Source)
		}
		if e.In <= 0 || e.Out <= 0 {
			t.Errorf("%s: a price of %v in and %v out", e.Model, e.In, e.Out)
		}
		if e.Above > 0 && (e.LongIn <= 0 || e.LongOut <= 0) {
			t.Errorf("%s: dearer above %d tokens, but not by how much", e.Model, e.Above)
		}
		for _, d := range []string{e.From, e.Until} {
			if _, err := time.Parse(Day, d); d != "" && err != nil {
				t.Errorf("%s: %q is not a date", e.Model, d)
			}
		}
		from, until := e.From, e.Until
		if until == "" {
			until = "9999-12-31"
		}
		for _, s := range spans[e.Model] {
			if from <= s[1] && s[0] <= until {
				t.Errorf("%s has two prices for the same day", e.Model)
			}
		}
		spans[e.Model] = append(spans[e.Model], [2]string{from, until})
	}
}

// maxAge is how long a price is trusted after it was read.
const maxAge = 120 * 24 * time.Hour

// staleOn names the entries read more than maxAge before a day.
func staleOn(entries []Entry, on time.Time) []string {
	var old []string
	for _, e := range entries {
		read, err := time.Parse(Day, e.Checked)
		if err != nil || on.Sub(read) > maxAge {
			old = append(old, e.Model+" (checked "+e.Checked+")")
		}
	}
	return old
}

func TestTheAgeCheckNoticesAnOldPrice(t *testing.T) {
	read := day(t, checked)
	if old := staleOn(Table(), read); len(old) > 0 {
		t.Errorf("prices read today are called stale: %v", old)
	}
	if old := staleOn(Table(), read.Add(maxAge+24*time.Hour)); len(old) == 0 {
		t.Error("prices read 121 days ago are not called stale")
	}
}

// A release must not ship prices nobody has looked at for months. The date
// the table is held to is the build's: FLOCKDECK_BUILD_DATE where it is given,
// and today in the release workflow, which runs these tests with
// FLOCKDECK_RELEASE_BUILD set. Any other run skips it, so an old commit checked
// out later still passes; cutting a release means reading the pages in the
// table again and moving their dates on.
func TestPricesWereCheckedRecently(t *testing.T) {
	var on time.Time
	switch {
	case os.Getenv("FLOCKDECK_BUILD_DATE") != "":
		on = day(t, os.Getenv("FLOCKDECK_BUILD_DATE"))
	case os.Getenv("FLOCKDECK_RELEASE_BUILD") != "":
		on = time.Now()
	default:
		t.Skip("only a release build is held to the age of its prices")
	}
	if old := staleOn(Table(), on); len(old) > 0 {
		t.Fatalf("these prices were read more than 120 days before the build; read their pages again and bump the dates in internal/pricing: %v", old)
	}
}
