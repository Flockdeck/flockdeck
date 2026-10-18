// Package jevgate is the one path a question to Jev takes from Flockdeck to
// TypeSafe. Every feature that asks (routejev for the router's cost fallback,
// session.StatusAssist for a quiet pane) hands it text and a reader for the
// answer, and the decision flow is the same for all of them:
//
//  1. Scrub the text (jev.Scrub), then bound it with the caller's Bound. What
//     is hashed and what is sent are this text, so a secret that differs
//     between two asks is one ask to the cache.
//  2. Nothing left to send: ErrNoText, no call.
//  3. Get a client from NewClient. No key is an error, and no call. A client
//     whose key or address differs from the last one's lifts a pause, since
//     what the old key was refused for is nothing to hold against a corrected
//     one.
//  4. A cached answer for the same scope and text is returned, paused or not.
//  5. Paused after a failure: ErrPaused.
//  6. The scope's own limits (least gap between calls, most calls an hour),
//     then the global calls-per-minute cap: ErrRateCap. The place in the
//     window is taken before the call, so asks made at once cannot all slip
//     in under the cap.
//  7. Ask, bounded by Timeout (and by the caller's context, which ends the ask
//     when the thing asked about has gone).
//  8. A failure pauses every scope: for CooldownRefused when the error is one
//     of Refused, not at all when the caller's own context was cancelled, and
//     for Cooldown otherwise. An answer Read refuses counts as a failure.
//  9. A good answer is cached.
//
// Nothing outside this package and internal/jev calls jev.Client.Ask (a test
// here enforces it), so no feature can send text that skipped step 1.
package jevgate

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/jmwri/flockdeck/internal/jev"
)

var (
	// ErrNoText is the error when nothing is left to send once the text is
	// scrubbed and bounded.
	ErrNoText = errors.New("jev was not asked: no text to send")
	// ErrPaused is the error while a failure keeps every ask back.
	ErrPaused = errors.New("jev was not asked: it failed a moment ago")
	// ErrRateCap is the error when a scope's or the global limit is spent.
	ErrRateCap = errors.New("jev was not asked: the calls-per-minute cap is spent")
)

// Config is what differs between the features that ask. The zero value of an
// optional field switches that limit off.
type Config[T any] struct {
	// NewClient is where a client comes from, asked on every Do so that a key
	// set or cleared while running is found. Required.
	NewClient func() (*jev.Client, error)
	// Questions are asked of every text. Required.
	Questions map[string]jev.Question
	// Read turns the answer for a scope into the T that is cached and
	// returned. Required.
	Read func(scope string, res *jev.Result) (T, error)
	// Bound cuts the scrubbed text to what may be sent; nil sends it as it is.
	Bound func(string) string
	// Now is the clock; nil is time.Now.
	Now func() time.Time

	// Timeout bounds one ask, retries included. Required.
	Timeout time.Duration
	// PerMinute caps asks that reach TypeSafe, across all scopes. Required.
	PerMinute int
	// ScopeInterval is the least time between two asks of one scope, and
	// ScopeHourly the most in an hour.
	ScopeInterval time.Duration
	ScopeHourly   int

	// Cooldown pauses every scope after a failure; CooldownRefused does so
	// after one of Refused, which asking again would only repeat.
	Cooldown        time.Duration
	CooldownRefused time.Duration
	Refused         []error

	// CacheTTL is how long an answer is kept (0: until forgotten) and
	// CacheCap how many are (0: no bound). LatestPerScope keeps only the most
	// recent text's answer for each scope.
	CacheTTL       time.Duration
	CacheCap       int
	LatestPerScope bool
}

// Gate is the flow above, with the state its limits need. It is safe for
// concurrent use. Config's fields may be set before the first Do (tests do).
type Gate[T any] struct {
	Config[T]

	mu         sync.Mutex
	cache      map[cacheKey]cached[T]
	latest     map[string][sha256.Size]byte
	calls      []time.Time
	scopes     map[string]*scopeState
	pausedTil  time.Time
	key, base  string
	haveClient bool
}

type cacheKey struct {
	scope string
	hash  [sha256.Size]byte
}

type cached[T any] struct {
	v  T
	at time.Time
}

type scopeState struct {
	lastAt time.Time
	calls  []time.Time
}

// New is a Gate configured by c.
func New[T any](c Config[T]) *Gate[T] {
	g := &Gate[T]{Config: c}
	g.Reset()
	return g
}

func (g *Gate[T]) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

// Do runs text through the flow. scope names what is asking (a pane's id; ""
// for a feature with no per-scope limits); ctx ends the ask early.
func (g *Gate[T]) Do(ctx context.Context, scope, text string) (T, error) {
	var zero T
	text = strings.TrimSpace(jev.Scrub(text))
	if g.Bound != nil {
		text = g.Bound(text)
	}
	if text == "" {
		return zero, ErrNoText
	}
	client, err := g.NewClient()
	if err != nil {
		return zero, err
	}
	ck := cacheKey{scope, sha256.Sum256([]byte(text))}

	g.mu.Lock()
	now := g.now()
	if g.haveClient && (client.Key != g.key || client.APIBase != g.base) {
		g.pausedTil = time.Time{}
	}
	g.key, g.base, g.haveClient = client.Key, client.APIBase, true
	if hit, ok := g.cache[ck]; ok && (g.CacheTTL == 0 || now.Sub(hit.at) < g.CacheTTL) {
		g.mu.Unlock()
		return hit.v, nil
	}
	if err := g.reserve(scope, now); err != nil {
		g.mu.Unlock()
		return zero, err
	}
	g.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, g.Timeout)
	defer cancel()
	res, err := client.Ask(ctx, text, g.Questions)
	var v T
	if err == nil {
		v, err = g.Read(scope, res)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err != nil {
		g.pause(ctx, err)
		return zero, err
	}
	g.store(ck, v)
	return v, nil
}

// reserve counts an ask against the pause and the limits, or says which
// refused it. It is called with g.mu held.
func (g *Gate[T]) reserve(scope string, now time.Time) error {
	if now.Before(g.pausedTil) {
		return ErrPaused
	}
	var s *scopeState
	if g.ScopeInterval > 0 || g.ScopeHourly > 0 {
		if s = g.scopes[scope]; s == nil {
			s = &scopeState{}
			g.scopes[scope] = s
		}
		if g.ScopeInterval > 0 && !s.lastAt.IsZero() && now.Sub(s.lastAt) < g.ScopeInterval {
			return ErrRateCap
		}
		s.calls = within(s.calls, now, time.Hour)
		if g.ScopeHourly > 0 && len(s.calls) >= g.ScopeHourly {
			return ErrRateCap
		}
	}
	g.calls = within(g.calls, now, time.Minute)
	if len(g.calls) >= g.PerMinute {
		return ErrRateCap
	}
	g.calls = append(g.calls, now)
	if s != nil {
		s.lastAt = now
		s.calls = append(s.calls, now)
	}
	return nil
}

// within keeps the times that are less than window before now.
func within(ts []time.Time, now time.Time, window time.Duration) []time.Time {
	kept := ts[:0]
	for _, t := range ts {
		if now.Sub(t) < window {
			kept = append(kept, t)
		}
	}
	return kept
}

// pause holds every scope back after err. It is called with g.mu held.
func (g *Gate[T]) pause(ctx context.Context, err error) {
	wait := g.Cooldown
	refused := false
	for _, r := range g.Refused {
		if errors.Is(err, r) {
			wait, refused = g.CooldownRefused, true
			break
		}
	}
	if !refused && errors.Is(ctx.Err(), context.Canceled) {
		// The thing asked about went away: nothing is wrong with TypeSafe.
		return
	}
	g.pausedTil = g.now().Add(wait)
}

// store keeps an answer. It is called with g.mu held.
func (g *Gate[T]) store(ck cacheKey, v T) {
	if g.LatestPerScope {
		if old, ok := g.latest[ck.scope]; ok {
			delete(g.cache, cacheKey{ck.scope, old})
		}
		g.latest[ck.scope] = ck.hash
	} else if g.CacheCap > 0 && len(g.cache) >= g.CacheCap {
		g.evict()
	}
	g.cache[ck] = cached[T]{v: v, at: g.now()}
}

// evict makes room: what has expired goes, and if that is nothing, the oldest.
func (g *Gate[T]) evict() {
	now := g.now()
	var oldest cacheKey
	var oldestAt time.Time
	for k, v := range g.cache {
		if g.CacheTTL > 0 && now.Sub(v.at) >= g.CacheTTL {
			delete(g.cache, k)
			continue
		}
		if oldestAt.IsZero() || v.at.Before(oldestAt) {
			oldest, oldestAt = k, v.at
		}
	}
	if len(g.cache) >= g.CacheCap {
		delete(g.cache, oldest)
	}
}

// Forget drops what is remembered about a scope that has gone.
func (g *Gate[T]) Forget(scope string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if h, ok := g.latest[scope]; ok {
		delete(g.cache, cacheKey{scope, h})
		delete(g.latest, scope)
	}
	delete(g.scopes, scope)
}

// Reset forgets everything: answers, limits and pause.
func (g *Gate[T]) Reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.cache = map[cacheKey]cached[T]{}
	g.latest = map[string][sha256.Size]byte{}
	g.scopes = map[string]*scopeState{}
	g.calls, g.pausedTil, g.haveClient = nil, time.Time{}, false
}
