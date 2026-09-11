package chat

import (
	"strings"
	"testing"
)

func TestCostIsOnlyClaimedWhenItIsKnown(t *testing.T) {
	tests := []struct {
		name  string
		model string
		usage Usage
		want  float64
		known bool
	}{
		{
			name:  "a priced model",
			model: "claude-sonnet-5",
			usage: Usage{In: 1_000_000, Out: 1_000_000},
			want:  12,
			known: true,
		},
		{
			name:  "the longest matching prefix wins",
			model: "claude-opus-4-8",
			usage: Usage{In: 1_000_000},
			want:  5,
			known: true,
		},
		{
			name:  "a model nobody priced here",
			model: "qwen3-coder",
			usage: Usage{In: 1_000_000, Out: 500_000},
			known: false,
		},
		{
			name:  "no model named at all",
			usage: Usage{In: 10},
			known: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, known := cost(tc.model, tc.usage)
			if known != tc.known {
				t.Fatalf("known = %v, want %v", known, tc.known)
			}
			if known && got != tc.want {
				t.Errorf("cost = %v, want %v", got, tc.want)
			}
		})
	}
}

// spendOf is what one turn cost, as the loop adds it up.
func spendOf(model string, u Usage) spend {
	var s spend
	s.add(model, u)
	return s
}

func TestStatusLineSaysTokensAndOnlyAPriceItKnows(t *testing.T) {
	u := Usage{In: 12_345, Out: 678}
	priced := statusLine("claude-opus-5", u, spendOf("claude-opus-5", u))
	for _, want := range []string{"claude-opus-5", "12k in", "678 out", "$"} {
		if !strings.Contains(priced, want) {
			t.Errorf("status line %q does not mention %q", priced, want)
		}
	}
	u = Usage{In: 5, Out: 6}
	unpriced := statusLine("qwen3-coder", u, spendOf("qwen3-coder", u))
	if strings.Contains(unpriced, "$") {
		t.Errorf("status line %q invents a price", unpriced)
	}
}

// /model changes who answers from the next turn on, and what was already spent
// was spent at the old model's rate.
func TestSwitchingModelsDoesNotRepriceWhatWasSpent(t *testing.T) {
	var s spend
	s.add("claude-opus-5", Usage{In: 1_000_000})
	s.add("claude-haiku-4-5", Usage{In: 1_000_000})
	line := statusLine("claude-haiku-4-5", Usage{In: 2_000_000}, s)
	if !strings.Contains(line, "$6.00") {
		t.Errorf("status line %q, want the $5 and $1 the two turns cost", line)
	}

	s.add("qwen3-coder", Usage{In: 10})
	if line := statusLine("qwen3-coder", Usage{}, s); !strings.Contains(line, "$6.00+") {
		t.Errorf("status line %q, want what is known shown as a floor", line)
	}
}

func TestTokensReadTheWayAGlanceReadsThem(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1500, "1.5k"},
		{12_345, "12k"},
		{2_500_000, "2.5M"},
	}
	for _, tc := range tests {
		if got := tokens(tc.n); got != tc.want {
			t.Errorf("tokens(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}
