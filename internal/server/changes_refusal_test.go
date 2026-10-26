package server

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/changes"
)

func TestARefusedCheckoutIsRememberedOncePerChangeAndForgottenWithItsPane(t *testing.T) {
	srv, _ := newTestServer(t)
	count := func() int {
		refusedConfigsMu.Lock()
		defer refusedConfigsMu.Unlock()
		return len(refusedConfigs)
	}
	t.Cleanup(func() {
		refusedConfigsMu.Lock()
		clear(refusedConfigs)
		refusedConfigsMu.Unlock()
	})
	refused := &changes.Branch{Pane: "p1", Error: "its config runs programs: filter.evil.clean"}
	srv.noteConfigRefusal("r", "a", refused)
	srv.noteConfigRefusal("r", "a", refused) // the same refusal again is not a new record
	srv.noteConfigRefusal("r", "b", &changes.Branch{Pane: "p1", Error: refused.Error})
	if n := count(); n != 2 {
		t.Fatalf("%d records, want one per project", n)
	}
	// A read that succeeds clears the pane.
	srv.noteConfigRefusal("r", "b", &changes.Branch{Pane: "p1"})
	if n := count(); n != 1 {
		t.Fatalf("%d records after a good read, want 1", n)
	}
	// A pane that is gone from its project's push is dropped; another project's stays.
	srv.noteConfigRefusal("r", "b", &changes.Branch{Pane: "p2", Error: refused.Error})
	forgetConfigRefusals("r", "a", map[string]bool{})
	if n := count(); n != 1 {
		t.Fatalf("%d records after the pane left project a, want only project b's", n)
	}
}
