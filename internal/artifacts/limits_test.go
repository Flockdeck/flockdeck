package artifacts

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func removeFile(tr *tree, rel string) error { return os.Remove(filepath.Join(tr.root, rel)) }

func mustLimiter(t *testing.T, rate float64, burst int, now func() time.Time) *Limiter {
	t.Helper()
	l, err := NewLimiter(rate, burst, now)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// Threat: a limiter that does not limit. NaN compares false with everything, so
// a NaN rate would refill to nothing or to everything; zero, negative and
// infinite rates and a burst below one are the same kind of mistake. All are
// refused when the limiter is made, not found out when it is relied on.
func TestNewLimiterValidates(t *testing.T) {
	for _, c := range []struct {
		rate  float64
		burst int
	}{{math.NaN(), 5}, {math.Inf(1), 5}, {math.Inf(-1), 5}, {0, 5}, {-1, 5}, {1, 0}, {1, -3}} {
		if l, err := NewLimiter(c.rate, c.burst, nil); err == nil || l != nil {
			t.Errorf("NewLimiter(%v, %d) = %v, %v; want an error", c.rate, c.burst, l, err)
		}
	}
	if _, err := NewLimiter(0.5, 1, nil); err != nil {
		t.Errorf("a slow, valid limiter was refused: %v", err)
	}
}

// Threat: a device asking, or being sent, faster than allowed. A burst is let
// through, the next is refused, and the bucket refills with time.
func TestLimiter(t *testing.T) {
	c := newClock()
	l := mustLimiter(t, 3, 3, c.now) // three a second, three at once
	for i := range 3 {
		if !l.Allow(1) {
			t.Fatalf("burst request %d refused", i)
		}
	}
	if l.Allow(1) {
		t.Fatal("a fourth request at once was allowed")
	}
	c.advance(time.Second)
	for i := range 3 {
		if !l.Allow(1) {
			t.Fatalf("request %d after a refill refused", i)
		}
	}
	if l.Allow(1) {
		t.Fatal("more than a second's refill was allowed")
	}
}

// Threat: asking for more than the bucket could ever hold, or for a negative
// amount to push the balance up, or waiting a long time to build up a
// bigger burst than the cap.
func TestLimiterEdges(t *testing.T) {
	c := newClock()
	l := mustLimiter(t, 10, 5, c.now)
	if l.Allow(6) {
		t.Error("allowed more than the burst")
	}
	if l.Allow(-1) {
		t.Error("allowed a negative amount")
	}
	if !l.Allow(5) {
		t.Error("refused exactly the burst")
	}
	c.advance(24 * time.Hour)
	if !l.Allow(5) || l.Allow(1) {
		t.Error("a long wait must refill to the burst and no further")
	}
}

// Threat: bandwidth. The same bucket counts bytes: BytesPerMinute is the cap on
// what one device is sent, with the chunk size as the unit a request takes.
func TestLimiterAsByteBudget(t *testing.T) {
	c := newClock()
	l := mustLimiter(t, float64(BytesPerMinute)/60, BytesPerMinute, c.now)
	n := 0
	for l.Allow(ChunkBytes) {
		n++
	}
	if got := n * ChunkBytes; got > BytesPerMinute || got < BytesPerMinute-ChunkBytes {
		t.Errorf("a full budget let %d bytes through, want about %d", got, BytesPerMinute)
	}
}

// The chunk and request limits must leave room under the 1 MiB a control
// socket reads, with the encoding around a chunk.
func TestLimitsAreConsistent(t *testing.T) {
	if ChunkBytes*2 > 1<<20 {
		t.Error("a chunk, base64-encoded in a frame, could pass a 1 MiB message limit")
	}
	if MaxRequestBytes >= ChunkBytes {
		t.Error("a request may be larger than a reply chunk")
	}
}
