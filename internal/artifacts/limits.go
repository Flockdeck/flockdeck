package artifacts

import (
	"sync"
	"time"
)

// The limits a later slice enforces on one remote device. They are named here,
// once, so that the transport, the viewer and the tests agree on them; this
// package enforces only the ones that are about a file (MaxViewBytes) or an id
// (the registry's own).
const (
	// ChunkBytes is the most of a file or recording sent in one frame, far
	// under the 1 MiB a control socket reads.
	ChunkBytes = 256 << 10
	// MaxRequestBytes is the most a request from a client may be: they are a
	// few short words, so anything bigger is not one.
	MaxRequestBytes = 64 << 10
	// MaxSocketsPerDevice and MaxOpensPerDevice bound what one device holds.
	MaxSocketsPerDevice = 3
	MaxOpensPerDevice   = 4
	// MaxListItems caps a list reply.
	MaxListItems = 500
	// RequestsPerWindow and RequestWindow are the rate a device may ask at;
	// BytesPerMinute is the most it may be sent.
	RequestsPerWindow = 30
	RequestWindow     = 10 * time.Second
	BytesPerMinute    = 20 << 20
)

// Limiter is a token bucket: it allows up to burst units at once and refills
// at rate units a second. It is for requests (one unit each) and for bytes
// (one unit a byte). Safe for concurrent use.
type Limiter struct {
	mu     sync.Mutex
	now    func() time.Time
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
}

// NewLimiter makes a full bucket. now may be nil for time.Now.
func NewLimiter(rate float64, burst int, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{now: now, rate: rate, burst: float64(burst), tokens: float64(burst), last: now()}
}

// Allow takes n units and reports true, or takes nothing and reports false. A
// request for more than the bucket can ever hold is always refused.
func (l *Limiter) Allow(n int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	t := l.now()
	if el := t.Sub(l.last).Seconds(); el > 0 {
		l.tokens = min(l.burst, l.tokens+el*l.rate)
		l.last = t
	}
	if n < 0 || float64(n) > l.tokens {
		return false
	}
	l.tokens -= float64(n)
	return true
}
