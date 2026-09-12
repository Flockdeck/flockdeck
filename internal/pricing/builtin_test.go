package pricing_test

import (
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/pricing"
)

// Every model a built-in API agent offers with a tier has a price. Routing
// weighs a switch between two of them by what each costs, and the picker shows
// the figure beside them; a tiered model with no price is either a typo in its
// id or a price nobody went to look up.
func TestEveryTieredAPIModelHasAPrice(t *testing.T) {
	for _, spec := range agent.Builtins() {
		if spec.Runner != agent.RunnerAPI {
			continue
		}
		for _, m := range spec.Models {
			if m.Tier == "" {
				continue
			}
			if _, ok := pricing.Lookup(m.ID, time.Now()); !ok {
				t.Errorf("%s offers %s as a %s model, and it has no price", spec.ID, m.ID, m.Tier)
			}
		}
	}
}
