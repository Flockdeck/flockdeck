package spend

import (
	"encoding/json"
	"math"
	"testing"
)

func TestWithContextShowsContextAndCost(t *testing.T) {
	v := WithContext(nil, Context{Used: 1234, Size: 200000, Cost: 0.0042, Currency: "USD"})
	if v == nil {
		t.Fatal("a report with figures gave no view")
	}
	if v.ContextUsed != 1234 || v.ContextSize != 200000 || v.USD != 0.0042 || v.Source != "agent" {
		t.Errorf("view = %+v", v)
	}
	if !v.Partial {
		t.Error("a pane that reports only context and cost is not marked partial")
	}
	if v.Tokens != 0 || v.In != 0 || v.Out != 0 || len(v.Windows) != 0 {
		t.Errorf("a token split or window was made up: %+v", v)
	}
}

// An empty currency is dollars; opencode's free tier reports a cost of 0, which
// is no cost figure and no estimate made in its place.
func TestWithContextWithoutACost(t *testing.T) {
	for name, c := range map[string]Context{
		"no cost":      {Used: 500, Size: 1000},
		"zero cost":    {Used: 500, Size: 1000, Cost: 0, Currency: "USD"},
		"no size yet":  {Used: 500},
		"size no used": {Size: 1000},
	} {
		v := WithContext(nil, c)
		if v == nil {
			t.Errorf("%s: no view", name)
			continue
		}
		if v.USD != 0 || v.Source != "" || v.Cost != 0 || !v.Partial {
			t.Errorf("%s: view = %+v, want no cost and partial", name, v)
		}
	}
	if v := WithContext(nil, Context{}); v != nil {
		t.Errorf("an empty report gave a view: %+v", v)
	}
	if v := WithContext(nil, Context{Currency: "USD"}); v != nil {
		t.Errorf("a currency alone gave a view: %+v", v)
	}
}

// A cost with no currency is not reported, not taken for dollars.
func TestWithContextEmptyCurrencyIsNotDollars(t *testing.T) {
	v := WithContext(nil, Context{Used: 1, Cost: 1.5})
	if v.USD != 0 || v.Cost != 0 || v.Source != "" {
		t.Errorf("view = %+v, want no cost", v)
	}
}

// A cost the agent could not have had is not reported and cannot overflow the
// rounding or the sums, whatever the currency.
func TestWithContextRefusesUnbelievableCosts(t *testing.T) {
	for name, cost := range map[string]float64{
		"inf":      math.Inf(1),
		"-inf":     math.Inf(-1),
		"nan":      math.NaN(),
		"1e305":    1e305,
		"1e308":    1e308,
		"negative": -2,
		"ceiling+": MaxSessionCost * 2,
	} {
		for _, cur := range []string{"USD", "EUR"} {
			v := WithContext(nil, Context{Used: 1, Cost: cost, Currency: cur})
			if v.USD != 0 || v.Cost != 0 || v.Currency != "" || v.Source != "" {
				t.Errorf("%s %s: view = %+v, want no cost", name, cur, v)
			}
			if _, err := json.Marshal(v); err != nil {
				t.Errorf("%s %s: view does not marshal: %v", name, cur, err)
			}
		}
	}
	if v := WithContext(nil, Context{Used: 1, Cost: MaxSessionCost, Currency: "USD"}); v.USD != MaxSessionCost {
		t.Errorf("the ceiling itself is believed: %+v", v)
	}
}

// A negative cost reports nothing (a mutation of c.Cost > 0 to != 0 fails here).
func TestWithContextNegativeCostReportsNothing(t *testing.T) {
	if v := WithContext(nil, Context{Cost: -0.5, Currency: "USD"}); v != nil {
		t.Errorf("view = %+v, want none", v)
	}
	if v := WithContext(nil, Context{Used: 5, Cost: -0.5, Currency: "USD"}); v.USD != 0 || v.Source != "" {
		t.Errorf("view = %+v", v)
	}
}

// A cost under a twentieth of a cent rounds to nothing and then reports no
// dollar figure, with no "agent" source for a zero.
func TestWithContextCostThatRoundsToNothing(t *testing.T) {
	v := WithContext(nil, Context{Used: 1, Cost: 1e-9, Currency: "USD"})
	if v.USD != 0 || v.Source != "" {
		t.Errorf("view = %+v, want no dollar figure", v)
	}
}

func TestWithContextClampsNegativeContext(t *testing.T) {
	v := WithContext(nil, Context{Used: -5, Size: -9, Cost: 1, Currency: "USD"})
	if v.ContextUsed != 0 || v.ContextSize != 0 {
		t.Errorf("view = %+v", v)
	}
}

// The agent's figure replaces what the book holds for the pane; it is not added
// to it (a mutation of = to += fails here).
func TestWithContextReplacesTheBooksDollars(t *testing.T) {
	base := &PaneView{USD: 2, Source: "hook", Tokens: 10}
	v := WithContext(base, Context{Used: 1, Cost: 0.5, Currency: "USD"})
	if v.USD != 0.5 || v.Source != "agent" {
		t.Errorf("view = %+v, want the agent's 0.5 and not 2.5", v)
	}
	if base.USD != 2 {
		t.Errorf("the book's view changed: %+v", base)
	}
}

// A cost in another currency is shown as it came and never added to dollars.
func TestWithContextKeepsAnotherCurrencyApart(t *testing.T) {
	v := WithContext(nil, Context{Used: 1, Size: 2, Cost: 3.25, Currency: " eur "})
	if v.USD != 0 || v.Cost != 3.25 || v.Currency != "EUR" {
		t.Errorf("view = %+v, want 3.25 EUR and no USD", v)
	}
}

// The view is made from the latest report alone, which is the session's own
// running figures: a window that grows, one that shrinks (a model switch), and
// a session that starts over with nothing used and no cost leave nothing of the
// report before.
func TestWithContextTakesTheLatestReport(t *testing.T) {
	v := WithContext(nil, Context{Used: 900, Size: 1000, Cost: 0.04, Currency: "USD"})
	if v.ContextUsed != 900 || v.USD != 0.04 {
		t.Errorf("after growth: %+v", v)
	}
	v = WithContext(nil, Context{Used: 900, Size: 400, Cost: 0.04, Currency: "USD"})
	if v.ContextSize != 400 {
		t.Errorf("after a size change: %+v", v)
	}
	v = WithContext(nil, Context{Used: 10, Size: 400})
	if v.ContextUsed != 10 || v.USD != 0 || v.Source != "" {
		t.Errorf("after a new session (a report with no cost): %+v, want the old cost gone", v)
	}
}

// The view of a pane the hook server also hears from is copied, not changed
// in place, and keeps its windows.
func TestWithContextDoesNotChangeTheBookView(t *testing.T) {
	base := &PaneView{Tokens: 5, Windows: []WindowView{{Name: "five_hour", Pct: 40}}}
	v := WithContext(base, Context{Used: 1, Size: 2})
	if base.Partial || base.ContextUsed != 0 {
		t.Errorf("the original changed: %+v", base)
	}
	if !v.Partial || len(v.Windows) != 1 || v.Tokens != 5 {
		t.Errorf("view = %+v", v)
	}
}
