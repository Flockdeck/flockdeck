package chat

import "testing"

// A pane is handed its stored key in its environment, which is looked at
// first. A key set with `flockdeck keys set` after that is in the store alone,
// and once the key in hand has been refused it is the one taken up -- as
// `keys set` promises a running pane.
func TestARefusedKeyGivesWayToOneStoredSince(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-handed-to-the-pane")
	saved := KeyStore
	t.Cleanup(func() { KeyStore = saved })
	KeyStore = func(agent string) string {
		if agent != "anthropic" {
			t.Errorf("looked in the store for %q", agent)
		}
		return "sk-set-since"
	}

	s, _ := newTestSession(t, "", nil)
	s.opts.Agent = "anthropic"
	s.key = "sk-handed-to-the-pane"
	if !s.rekey() {
		t.Fatal("the key set since was not taken up")
	}
	if s.key != "sk-set-since" {
		t.Errorf("rekeyed to the wrong key")
	}
	// /status says where the key in use came from, not the refused one.
	if s.keyFrom != "stored with `flockdeck keys set anthropic`" {
		t.Errorf("keyFrom = %q", s.keyFrom)
	}
	// And a key that has not changed is not tried again as though it had.
	if s.rekey() {
		t.Error("rekeyed to the same key")
	}
}
