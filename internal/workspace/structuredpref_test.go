package workspace

import (
	"slices"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/store"
)

func offered(c *agent.Catalog, id string) bool {
	for _, s := range c.Visible() {
		if s.ID == id {
			return true
		}
	}
	return false
}

// The beta for structured agents is the structuredPanes preference: the
// catalog the workspace builds lists them when it is on and not when it is off.
func TestTheCatalogListsStructuredAgentsOnlyWhenThePrefIsOn(t *testing.T) {
	isolateConfig(t)
	if offered(loadCatalog(), "opencode-acp") {
		t.Fatal("opencode-acp is offered with the preference unset")
	}
	if err := store.SavePrefs(store.Prefs{StructuredPanes: true}); err != nil {
		t.Fatalf("SavePrefs: %v", err)
	}
	// The key survives the round trip through the file.
	if !store.LoadPrefs().StructuredPanes {
		t.Fatal("structuredPanes did not survive the round trip")
	}
	c := loadCatalog()
	if !c.Structured || !offered(c, "opencode-acp") {
		t.Errorf("opencode-acp is not offered with the preference on (Structured %v)", c.Structured)
	}
	if !slices.Contains(ids(c.Visible()), "opencode") {
		t.Error("the terminal opencode entry should still be offered")
	}
	if err := store.SavePrefs(store.Prefs{}); err != nil {
		t.Fatal(err)
	}
	if offered(loadCatalog(), "opencode-acp") {
		t.Error("opencode-acp is still offered after the preference was turned off")
	}
}

func ids(specs []agent.Spec) []string {
	var out []string
	for _, s := range specs {
		out = append(out, s.ID)
	}
	return out
}
