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

func TestStatusLineSaysTokensAndOnlyAPriceItKnows(t *testing.T) {
	priced := statusLine("claude-opus-5", Usage{In: 12_345, Out: 678})
	for _, want := range []string{"claude-opus-5", "12k in", "678 out", "$"} {
		if !strings.Contains(priced, want) {
			t.Errorf("status line %q does not mention %q", priced, want)
		}
	}
	unpriced := statusLine("qwen3-coder", Usage{In: 5, Out: 6})
	if strings.Contains(unpriced, "$") {
		t.Errorf("status line %q invents a price", unpriced)
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
