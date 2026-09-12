package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// Every tier a built-in gives a model is one of the three; a fourth would be
// read as unknown, and routing would never move work to or from that model.
func TestBuiltinTiersAreTiers(t *testing.T) {
	for _, s := range Builtins() {
		for _, m := range s.Models {
			if m.Tier != "" && TierRank(m.Tier) == 0 {
				t.Errorf("%s's %q has tier %q", s.ID, m.ID, m.Tier)
			}
		}
	}
}

// A model that stands for whatever the tool is set to, or that routes between
// models by design, has no tier, so Flockdeck's routing leaves it alone.
func TestMixedModelsHaveNoTier(t *testing.T) {
	c := Merge(nil)
	for _, want := range []struct{ agent, model string }{
		{"claude", ""}, {"claude", "opusplan"}, {"gemini", "auto"},
	} {
		spec, _ := c.Find(want.agent)
		found := false
		for _, m := range spec.Models {
			if m.ID != want.model {
				continue
			}
			found = true
			if m.Tier != "" {
				t.Errorf("%s's %q is routed as %q", want.agent, want.model, m.Tier)
			}
		}
		if !found {
			t.Errorf("%s does not offer %q", want.agent, want.model)
		}
	}
}

func TestTiersAreOrdered(t *testing.T) {
	if !(TierRank(TierSmall) < TierRank(TierMid) && TierRank(TierMid) < TierRank(TierTop)) {
		t.Error("small < mid < top does not hold")
	}
	if TierRank("") != 0 || TierRank("large") != 0 {
		t.Error("an unknown tier is ranked")
	}
}

// A tier is set or corrected in agents.json like any other field of a model,
// with its case forgiven; one that is not a tier is named and left unknown.
func TestATierInAgentsJSONIsReadAndAMistakeIsNamed(t *testing.T) {
	c := Merge(&File{Agents: []json.RawMessage{
		json.RawMessage(`{"id": "claude", "models": [{"id": "opus", "tier": "Mid"}, {"id": "haiku", "tier": "large"}]}`),
	}})
	spec, _ := c.Find("claude")
	tiers := map[string]string{}
	for _, m := range spec.Models {
		tiers[m.ID] = m.Tier
	}
	if tiers["opus"] != TierMid {
		t.Errorf("opus's tier = %q, want the mid it was given", tiers["opus"])
	}
	if tiers["haiku"] != "" {
		t.Errorf("haiku's tier = %q, want it left unknown", tiers["haiku"])
	}
	if !strings.Contains(c.Notice, `"large"`) || !strings.Contains(c.Notice, "small, mid and top") {
		t.Errorf("notice = %q, want it to name the tier and the three there are", c.Notice)
	}
}

// Typing a command into a running agent is safe only where its documentation
// says the command changes that session alone. None of the command-line
// agents' does -- Claude Code's /model saves the model as the default for
// every later session -- so each is switched by restarting it. The chat
// client's /model touches the session and nothing else.
func TestSwitchIsSetOnlyWhereTheCommandIsSessionScoped(t *testing.T) {
	for _, s := range Merge(nil).Specs {
		switch {
		case s.Runner == RunnerCLI && s.Switch != "":
			t.Errorf("%s is switched by typing %q into it", s.ID, s.Switch)
		case s.Runner == RunnerAPI && s.Switch != "/model {{model}}":
			t.Errorf("%s's switch = %q, want the chat client's /model", s.ID, s.Switch)
		}
	}
}

func TestAMisspeltTokenInASwitchIsNamed(t *testing.T) {
	c := Merge(&File{Agents: []json.RawMessage{json.RawMessage(`{"id": "aider", "switch": "/model {{modle}}"}`)}})
	if !strings.Contains(c.Notice, "modle") {
		t.Errorf("notice = %q, want it to name the token", c.Notice)
	}
}
