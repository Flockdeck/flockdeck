package artifacts

import (
	"errors"
	"strings"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newClock() *clock { return &clock{t: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)} }

// Threat: guessing or counting up to another id. Ids are 128 random bits,
// different every time, of one fixed shape.
func TestIDsAreRandomAndOfFixedShape(t *testing.T) {
	r := NewRegistry(nil, 0, 0)
	seen := map[string]bool{}
	for range 200 {
		id, err := r.Issue(Entry{Kind: "files"})
		if err != nil {
			t.Fatal(err)
		}
		if len(id) != idLen || strings.ContainsAny(id, "/\\.:= +") {
			t.Fatalf("id %q is not 22 base64url characters", id)
		}
		if seen[id] {
			t.Fatalf("id %q issued twice", id)
		}
		seen[id] = true
	}
}

// Threat: an id from one device's socket being good on another's. Each
// registry is its own world; an id means nothing to any other.
func TestIDsDoNotCrossRegistries(t *testing.T) {
	a, b := NewRegistry(nil, 0, 0), NewRegistry(nil, 0, 0)
	id, _ := a.Issue(Entry{Kind: "files", Path: "x"})
	if _, err := a.Lookup(id, "files", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Lookup(id, "files", ""); !errors.Is(err, ErrUnknownID) {
		t.Fatalf("another registry knew the id: %v", err)
	}
}

// Threat: a stolen or leaked id being good for ever. It runs out, is not
// extended by use, and is forgotten.
func TestIDsExpire(t *testing.T) {
	c := newClock()
	r := NewRegistry(c.now, time.Minute, 10)
	id, _ := r.Issue(Entry{Path: "x"})
	c.advance(59 * time.Second)
	if _, err := r.Lookup(id, "", ""); err != nil {
		t.Fatalf("expired early: %v", err)
	}
	c.advance(2 * time.Second) // using it did not extend it
	if _, err := r.Lookup(id, "", ""); !errors.Is(err, ErrUnknownID) {
		t.Fatalf("still good after its life: %v", err)
	}
	if r.Len() != 0 {
		t.Errorf("Len = %d after expiry", r.Len())
	}
}

// Threat: a client that lists over and over to fill the host's memory. The
// registry holds a fixed number and drops the soonest to expire to make room.
func TestRegistryIsBounded(t *testing.T) {
	c := newClock()
	r := NewRegistry(c.now, time.Hour, 3)
	var ids []string
	for range 5 {
		id, _ := r.Issue(Entry{})
		ids = append(ids, id)
		c.advance(time.Second)
	}
	if r.Len() != 3 {
		t.Fatalf("Len = %d, want 3", r.Len())
	}
	for i, id := range ids {
		_, err := r.Lookup(id, "", "")
		if (i < 2) != (err != nil) {
			t.Errorf("id %d: err = %v; the two oldest should be gone and the rest kept", i, err)
		}
	}
}

// Threat: the user turning viewing off must take effect on every id at once.
func TestClear(t *testing.T) {
	r := NewRegistry(nil, 0, 0)
	id, _ := r.Issue(Entry{})
	r.Clear()
	if _, err := r.Lookup(id, "", ""); !errors.Is(err, ErrUnknownID) {
		t.Fatalf("an id survived Clear: %v", err)
	}
}

// Threat: an id that is not one -- a path, a traversal, an oversized string,
// the empty string -- is simply unknown. No parsing of it, no error that says
// more than that.
func TestMalformedIDsAreUnknown(t *testing.T) {
	r := NewRegistry(nil, 0, 0)
	r.Issue(Entry{})
	for _, id := range []string{"", "../../etc/passwd", `C:\Windows\win.ini`, strings.Repeat("A", 22+1), strings.Repeat("A", 1<<20), "\x00", "AAAAAAAAAAAAAAAAAAAAAA"} {
		if _, err := r.Lookup(id, "", ""); !errors.Is(err, ErrUnknownID) {
			t.Errorf("Lookup(%.30q) = %v", id, err)
		}
	}
}

// Threat: an entry's file being reached some way other than Root.Open. The
// registry stores where to open and never an open handle, so every request is
// vetted afresh; a file swapped for a link after listing is refused when asked
// for.
func TestRegistryHoldsNoHandle(t *testing.T) {
	tr := newTree(t)
	r := NewRegistry(nil, 0, 0)
	id, _ := r.Issue(Entry{Kind: "files", Root: tr.r, Path: "ok.txt"})
	e, err := r.Lookup(id, "files", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := removeFile(tr, "ok.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Root.Open(e.Path); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("the file was removed but Open said %v", err)
	}
}

// Threat: an id of one kind or pane being used to ask for another's. A client
// that was shown recordings, or one pane's files, cannot present those ids for
// a pane that was never shared with it.
func TestLookupBindsKindAndPane(t *testing.T) {
	r := NewRegistry(nil, 0, 0)
	id, _ := r.Issue(Entry{Kind: "files", Pane: "pane-1", Path: "x"})
	if _, err := r.Lookup(id, "files", "pane-1"); err != nil {
		t.Fatal(err)
	}
	for _, c := range [][2]string{{"recordings", "pane-1"}, {"files", "pane-2"}, {"files", ""}, {"", "pane-1"}, {"", ""}} {
		if _, err := r.Lookup(id, c[0], c[1]); !errors.Is(err, ErrUnknownID) {
			t.Errorf("Lookup(kind=%q, pane=%q) = %v, want unknown", c[0], c[1], err)
		}
	}
}

// Threat: a second listing taking the first's ids away while somebody reads
// from the first. A registry holds several full lists before it drops any.
func TestSecondListDoesNotEvictTheFirst(t *testing.T) {
	c := newClock()
	r := NewRegistry(c.now, 0, 0)
	var first []string
	for range MaxListItems {
		id, _ := r.Issue(Entry{Kind: "files", Pane: "p"})
		first = append(first, id)
		c.advance(time.Millisecond)
	}
	for range MaxListItems {
		r.Issue(Entry{Kind: "files", Pane: "p"})
		c.advance(time.Millisecond)
	}
	for i, id := range first {
		if _, err := r.Lookup(id, "files", "p"); err != nil {
			t.Fatalf("id %d of the first list was evicted by a second list: %v", i, err)
		}
	}
}
