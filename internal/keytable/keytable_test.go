package keytable_test

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/keytable"
	"github.com/jmwri/flockdeck/ui/help"
)

// The front end's Key and this package's Key must carry the same fields, or a
// field added on one side would vanish on the way to the server.
func TestFromTakesTheFrontEndTable(t *testing.T) {
	got := keytable.From(help.Keys)
	if len(got) != len(help.Keys) || len(got) == 0 {
		t.Fatalf("got %d keys, want %d", len(got), len(help.Keys))
	}
	for i, k := range help.Keys {
		g := got[i]
		if g.ID != k.ID || g.Keys != k.Keys || g.Label != k.Label || g.Short != k.Short ||
			g.Section != k.Section || g.Page != k.Page || g.NoPalette != k.NoPalette || g.Confirm != k.Confirm {
			t.Fatalf("key %q changed on the way: %+v -> %+v", k.ID, k, g)
		}
	}
}

func TestFromPanicsOnAFieldItDoesNotKnow(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("From accepted a field the Key type lacks")
		}
	}()
	type wider struct {
		ID    string `json:"id"`
		Extra string `json:"extra"`
	}
	keytable.From([]wider{{ID: "a", Extra: "b"}})
}
