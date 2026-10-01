package artifacts

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

// IDLife and MaxIDs bound what a Registry holds: an id is good for a quarter
// of an hour and a registry holds at most this many at once.
const (
	IDLife = 15 * time.Minute
	MaxIDs = 500
	idLen  = 22 // 16 random bytes, unpadded base64url
)

// Entry is what an id stands for. Nothing in it is ever sent to a client but
// the id and what the host chooses to say about it (a name, a size).
type Entry struct {
	// Kind and Pane say what and whose it is; the registry does not look at
	// them, the caller does.
	Kind string
	Pane string
	// Root and Path are what to open: Root.Open(Path) is made again on every
	// request, never cached as a handle.
	Root *Root
	Path string
}

// Registry hands out ids and takes them back. It is made for one remote device
// session -- one socket -- and its ids mean nothing to any other registry, so
// an id from one device is not an id on another by construction. Safe for
// concurrent use.
type Registry struct {
	mu      sync.Mutex
	now     func() time.Time
	life    time.Duration
	max     int
	entries map[string]regEntry
}

type regEntry struct {
	Entry
	expires time.Time
}

// NewRegistry makes an empty registry. Zero life and max, or a nil now, take
// the package defaults.
func NewRegistry(now func() time.Time, life time.Duration, max int) *Registry {
	if now == nil {
		now = time.Now
	}
	if life <= 0 {
		life = IDLife
	}
	if max <= 0 {
		max = MaxIDs
	}
	return &Registry{now: now, life: life, max: max, entries: map[string]regEntry{}}
}

// Issue gives e a new id: 128 random bits, so it cannot be guessed or counted
// up from another. When the registry is full the id closest to expiring is
// dropped to make room, so listing again always works.
func (r *Registry) Issue(e Entry) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	id := base64.RawURLEncoding.EncodeToString(b[:])
	r.mu.Lock()
	defer r.mu.Unlock()
	t := r.now()
	r.sweepLocked(t)
	for len(r.entries) >= r.max {
		var oldest string
		var at time.Time
		for k, v := range r.entries {
			if oldest == "" || v.expires.Before(at) {
				oldest, at = k, v.expires
			}
		}
		delete(r.entries, oldest)
	}
	r.entries[id] = regEntry{Entry: e, expires: t.Add(r.life)}
	return id, nil
}

// ErrUnknownID is for an id that was never issued here, has run out or has
// been revoked. It is deliberately all one thing.
var ErrUnknownID = errors.New("unknown id")

// Lookup is what id stands for. It does not extend the id's life.
func (r *Registry) Lookup(id string) (Entry, error) {
	if len(id) != idLen {
		return Entry{}, ErrUnknownID
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	if !ok {
		return Entry{}, ErrUnknownID
	}
	if !r.now().Before(e.expires) {
		delete(r.entries, id)
		return Entry{}, ErrUnknownID
	}
	return e.Entry, nil
}

// Clear forgets every id: a device was turned off, or the user said to stop.
func (r *Registry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	clear(r.entries)
}

// Len is how many unexpired ids are held.
func (r *Registry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sweepLocked(r.now())
	return len(r.entries)
}

func (r *Registry) sweepLocked(t time.Time) {
	for k, v := range r.entries {
		if !t.Before(v.expires) {
			delete(r.entries, k)
		}
	}
}
