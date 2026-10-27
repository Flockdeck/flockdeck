package workspace

import (
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/creds"
)

// Every key in keys.json is scrubbed from a baton, whether the catalog has an
// agent for it or not, and the built-in structured agents' keys count as the
// catalog's own.
func TestTheBatonScrubberKnowsEveryStoredKey(t *testing.T) {
	isolateConfig(t)
	const gem, cod, orphan = "sk-gemini-0123456789", "sk-codex-0123456789", "sk-orphan-0123456789"
	for id, k := range map[string]string{"gemini-acp": gem, "codex-acp": cod, "left-the-catalog": orphan} {
		if err := creds.Set(id, k); err != nil {
			t.Fatal(err)
		}
	}
	specs := agent.Merge(nil).Specs
	var keyed []agent.Spec
	for _, s := range specs {
		if s.TakesKey() {
			keyed = append(keyed, s)
		}
	}
	sc := BatonSource{APISpecs: keyed}.Scrubber()
	out, _ := sc.Scrub("a " + gem + " b " + cod + " c " + orphan)
	for _, k := range []string{gem, cod, orphan} {
		if strings.Contains(out, k) {
			t.Errorf("%s survived the scrubber: %s", k, out)
		}
	}
}
