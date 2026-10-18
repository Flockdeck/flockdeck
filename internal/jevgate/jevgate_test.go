package jevgate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/jev"
)

// fake is a TypeSafe that records what it is sent and answers one noul.
type fake struct {
	srv    *httptest.Server
	hits   atomic.Int32
	mu     sync.Mutex
	bodies []string
	status int
	hang   bool
}

func newFake(t *testing.T) *fake {
	t.Helper()
	f := &fake{status: 200}
	release := make(chan struct{})
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.hits.Add(1)
		f.mu.Lock()
		f.bodies = append(f.bodies, string(b))
		status, hang := f.status, f.hang
		f.mu.Unlock()
		if hang {
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}
		if status != 200 {
			http.Error(w, "no", status)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   "jev-test",
			"answers": map[string]any{"q": map[string]any{"type": "noul", "noul": 0.25}},
		})
	}))
	t.Cleanup(func() { close(release); f.srv.Close() })
	return f
}

func (f *fake) set(fn func()) { f.mu.Lock(); fn(); f.mu.Unlock() }

func (f *fake) body(i int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[i]
}

// gate is a Gate asking f, with limits wide enough that a test narrows only
// the one it is about.
func gate(f *fake, mod func(*Config[float64])) *Gate[float64] {
	c := Config[float64]{
		NewClient: func() (*jev.Client, error) { return &jev.Client{Key: "k", APIBase: f.srv.URL}, nil },
		Questions: map[string]jev.Question{"q": jev.Noul{Instructions: "is it?"}},
		Read: func(_ string, res *jev.Result) (float64, error) {
			n, err := res.Noul("q")
			return n.Probability, err
		},
		Timeout:   2 * time.Second,
		PerMinute: 1000,
		Cooldown:  30 * time.Second, CooldownRefused: 5 * time.Minute,
		Refused: []error{jev.ErrUnauthorized},
	}
	if mod != nil {
		mod(&c)
	}
	return New(c)
}

var bg = context.Background()

func TestAnAnswerIsReadAndCached(t *testing.T) {
	f := newFake(t)
	g := gate(f, nil)
	for i := 0; i < 3; i++ {
		v, err := g.Do(bg, "", "same text")
		if err != nil || v != 0.25 {
			t.Fatalf("got %v, %v", v, err)
		}
	}
	if n := f.hits.Load(); n != 1 {
		t.Errorf("%d requests for one text", n)
	}
	// The cache is per scope.
	if _, err := g.Do(bg, "other", "same text"); err != nil || f.hits.Load() != 2 {
		t.Errorf("another scope shared the answer: %v, %d requests", err, f.hits.Load())
	}
}

func TestTextIsScrubbedBeforeItIsHashedAndSent(t *testing.T) {
	f := newFake(t)
	g := gate(f, nil)
	const key = "sk-ant-api03-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	if _, err := g.Do(bg, "", "fix it, it uses "+key+"\nsee /home/me/app/.env"); err != nil {
		t.Fatal(err)
	}
	body := f.body(0)
	for _, leak := range []string{key, ".env"} {
		if strings.Contains(body, leak) {
			t.Errorf("%q was sent: %s", leak, body)
		}
	}
	// A different secret in the same place is the same text to the cache.
	other := "sk-ant-api03-BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
	if _, err := g.Do(bg, "", "fix it, it uses "+other+"\nsee /home/me/app/.env"); err != nil {
		t.Fatal(err)
	}
	if n := f.hits.Load(); n != 1 {
		t.Errorf("%d requests: the cache was keyed by the secret", n)
	}
}

func TestBoundCutsTheScrubbedText(t *testing.T) {
	f := newFake(t)
	g := gate(f, func(c *Config[float64]) {
		c.Bound = func(s string) string { return s[:4] }
	})
	if _, err := g.Do(bg, "", "abcdefgh"); err != nil {
		t.Fatal(err)
	}
	if body := f.body(0); !strings.Contains(body, `"abcd"`) || strings.Contains(body, "efgh") {
		t.Errorf("sent %s", body)
	}
}

func TestNothingToSendMakesNoCall(t *testing.T) {
	f := newFake(t)
	g := gate(f, nil)
	for _, text := range []string{"", "  \n\t "} {
		if _, err := g.Do(bg, "", text); !errors.Is(err, ErrNoText) {
			t.Errorf("%q: %v", text, err)
		}
	}
	if f.hits.Load() != 0 {
		t.Error("an empty text was asked about")
	}
}

func TestNoKeyMakesNoCallAndIsReturned(t *testing.T) {
	f := newFake(t)
	g := gate(f, func(c *Config[float64]) {
		c.NewClient = func() (*jev.Client, error) { return nil, jev.ErrNoKey }
	})
	if _, err := g.Do(bg, "", "text"); !errors.Is(err, jev.ErrNoKey) {
		t.Errorf("got %v", err)
	}
	if f.hits.Load() != 0 {
		t.Error("a call was made without a key")
	}
	// And it is not a failure to pause for: the key may be set a moment later.
	g.NewClient = func() (*jev.Client, error) { return &jev.Client{Key: "k", APIBase: f.srv.URL}, nil }
	if _, err := g.Do(bg, "", "text"); err != nil {
		t.Errorf("after the key was set: %v", err)
	}
}

func TestTheCacheExpiresAndIsBounded(t *testing.T) {
	f := newFake(t)
	now := time.Now()
	g := gate(f, func(c *Config[float64]) {
		c.CacheTTL, c.CacheCap = time.Hour, 8
		c.Now = func() time.Time { return now }
	})
	_, _ = g.Do(bg, "", "text")
	_, _ = g.Do(bg, "", "text")
	if f.hits.Load() != 1 {
		t.Fatalf("%d requests", f.hits.Load())
	}
	now = now.Add(time.Hour + time.Second)
	_, _ = g.Do(bg, "", "text")
	if f.hits.Load() != 2 {
		t.Errorf("%d requests after the entry expired", f.hits.Load())
	}
	for i := 0; i < 30; i++ {
		now = now.Add(time.Second)
		if _, err := g.Do(bg, "", fmt.Sprint("text ", i)); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(g.cache); n > 8 {
		t.Errorf("the cache holds %d entries, cap 8", n)
	}
}

func TestLatestPerScopeKeepsOneAnswerEach(t *testing.T) {
	f := newFake(t)
	g := gate(f, func(c *Config[float64]) { c.LatestPerScope = true })
	_, _ = g.Do(bg, "a", "one")
	_, _ = g.Do(bg, "a", "two")
	_, _ = g.Do(bg, "b", "one")
	if n := len(g.cache); n != 2 {
		t.Errorf("%d answers kept, want the latest of each of 2 scopes", n)
	}
	// "one" for a was replaced by "two", so it is asked again.
	before := f.hits.Load()
	_, _ = g.Do(bg, "a", "one")
	if f.hits.Load() != before+1 {
		t.Error("an answer that was replaced was still served")
	}
	g.Forget("a")
	g.Forget("a") // twice is harmless
	if n := len(g.cache); n != 1 {
		t.Errorf("%d answers after forgetting a scope, want 1", n)
	}
}

func TestTheGlobalCapHoldsAndLifts(t *testing.T) {
	f := newFake(t)
	now := time.Now()
	g := gate(f, func(c *Config[float64]) {
		c.PerMinute = 3
		c.Now = func() time.Time { return now }
	})
	for i := 0; i < 3; i++ {
		if _, err := g.Do(bg, "", fmt.Sprint("t", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := g.Do(bg, "", "one more"); !errors.Is(err, ErrRateCap) {
		t.Errorf("the fourth call in a minute: %v", err)
	}
	if _, err := g.Do(bg, "", "t0"); err != nil {
		t.Errorf("a cached text hit the cap: %v", err)
	}
	now = now.Add(61 * time.Second)
	if _, err := g.Do(bg, "", "one more"); err != nil {
		t.Errorf("after the minute: %v", err)
	}
	if f.hits.Load() != 4 {
		t.Errorf("%d requests, want 4", f.hits.Load())
	}
}

func TestTheGlobalCapHoldsUnderConcurrency(t *testing.T) {
	f := newFake(t)
	g := gate(f, func(c *Config[float64]) { c.PerMinute = 5 })
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = g.Do(bg, "", fmt.Sprint("text ", i))
		}(i)
	}
	wg.Wait()
	if n := f.hits.Load(); n > 5 {
		t.Errorf("%d requests through a cap of 5", n)
	}
}

func TestAScopeHasItsOwnGapAndHourlyLimit(t *testing.T) {
	f := newFake(t)
	now := time.Now()
	g := gate(f, func(c *Config[float64]) {
		c.ScopeInterval, c.ScopeHourly = 15*time.Second, 3
		c.Now = func() time.Time { return now }
	})
	_, _ = g.Do(bg, "p", "t0")
	now = now.Add(5 * time.Second)
	if _, err := g.Do(bg, "p", "t1"); !errors.Is(err, ErrRateCap) {
		t.Errorf("inside the gap: %v", err)
	}
	// Another scope is not held by it.
	if _, err := g.Do(bg, "q", "t1"); err != nil {
		t.Errorf("another scope inside the gap: %v", err)
	}
	for i := 2; i < 4; i++ {
		now = now.Add(16 * time.Second)
		if _, err := g.Do(bg, "p", fmt.Sprint("t", i)); err != nil {
			t.Fatal(err)
		}
	}
	now = now.Add(16 * time.Second)
	if _, err := g.Do(bg, "p", "t4"); !errors.Is(err, ErrRateCap) {
		t.Errorf("the fourth call in the hour: %v", err)
	}
	now = now.Add(time.Hour)
	if _, err := g.Do(bg, "p", "t5"); err != nil {
		t.Errorf("an hour later: %v", err)
	}
}

func TestAFailurePausesEveryScopeAndARefusalLonger(t *testing.T) {
	for _, tc := range []struct {
		status int
		pause  time.Duration
	}{{500, 30 * time.Second}, {401, 5 * time.Minute}} {
		f := newFake(t)
		f.set(func() { f.status = tc.status })
		now := time.Now()
		g := gate(f, func(c *Config[float64]) { c.Now = func() time.Time { return now } })
		if _, err := g.Do(bg, "a", "text"); err == nil {
			t.Fatalf("%d: no error", tc.status)
		}
		hits := f.hits.Load()
		if _, err := g.Do(bg, "b", "other"); !errors.Is(err, ErrPaused) {
			t.Errorf("%d: straight after: %v", tc.status, err)
		}
		now = now.Add(tc.pause - time.Second)
		if _, err := g.Do(bg, "b", "other"); !errors.Is(err, ErrPaused) {
			t.Errorf("%d: just before %v: %v", tc.status, tc.pause, err)
		}
		if f.hits.Load() != hits {
			t.Errorf("%d: a call was made while paused", tc.status)
		}
		now = now.Add(2 * time.Second)
		if _, err := g.Do(bg, "b", "other"); errors.Is(err, ErrPaused) {
			t.Errorf("%d: still paused after %v", tc.status, tc.pause)
		}
	}
}

func TestACachedAnswerIsServedWhilePaused(t *testing.T) {
	f := newFake(t)
	g := gate(f, nil)
	_, _ = g.Do(bg, "", "known")
	f.set(func() { f.status = 500 })
	_, _ = g.Do(bg, "", "unknown")
	if v, err := g.Do(bg, "", "known"); err != nil || v != 0.25 {
		t.Errorf("a cached answer while paused: %v, %v", v, err)
	}
}

func TestASlowJevIsGivenUpOn(t *testing.T) {
	f := newFake(t)
	f.set(func() { f.hang = true })
	g := gate(f, func(c *Config[float64]) { c.Timeout = 100 * time.Millisecond })
	start := time.Now()
	if _, err := g.Do(bg, "", "text"); err == nil {
		t.Fatal("a call that never answered succeeded")
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("waited %v on a 100ms deadline", took)
	}
	if _, err := g.Do(bg, "", "next"); !errors.Is(err, ErrPaused) {
		t.Errorf("straight after a timeout: %v", err)
	}
}

func TestACancelledCallerIsNotATypeSafeFailure(t *testing.T) {
	f := newFake(t)
	f.set(func() { f.hang = true })
	g := gate(f, nil)
	ctx, cancel := context.WithCancel(bg)
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	if _, err := g.Do(ctx, "p", "text"); err == nil {
		t.Fatal("a cancelled call succeeded")
	}
	f.set(func() { f.hang = false })
	if _, err := g.Do(bg, "q", "other"); err != nil {
		t.Errorf("a pane going away paused everyone: %v", err)
	}
}

func TestAnAnswerReadRefusesIsAFailure(t *testing.T) {
	f := newFake(t)
	bad := errors.New("not the answer asked for")
	g := gate(f, func(c *Config[float64]) {
		c.Read = func(string, *jev.Result) (float64, error) { return 0, bad }
	})
	if _, err := g.Do(bg, "", "text"); !errors.Is(err, bad) {
		t.Errorf("got %v", err)
	}
	if _, err := g.Do(bg, "", "next"); !errors.Is(err, ErrPaused) {
		t.Errorf("straight after: %v", err)
	}
	g.Reset()
	g.Read = func(string, *jev.Result) (float64, error) { return 1, nil }
	if _, err := g.Do(bg, "", "next"); err != nil {
		t.Errorf("after Reset: %v", err)
	}
}

func TestACorrectedKeyIsNotHeldBackByTheOldOnesPause(t *testing.T) {
	f := newFake(t)
	f.set(func() { f.status = 401 })
	key := "wrong"
	g := gate(f, func(c *Config[float64]) {
		c.NewClient = func() (*jev.Client, error) { return &jev.Client{Key: key, APIBase: f.srv.URL}, nil }
	})
	if _, err := g.Do(bg, "", "text"); !errors.Is(err, jev.ErrUnauthorized) {
		t.Fatalf("got %v", err)
	}
	if _, err := g.Do(bg, "", "text 2"); !errors.Is(err, ErrPaused) {
		t.Fatalf("the same key straight after: %v", err)
	}
	key = "right"
	f.set(func() { f.status = 200 })
	if _, err := g.Do(bg, "", "text 2"); err != nil {
		t.Errorf("the corrected key was not tried: %v", err)
	}
}

// Ask is the one call that sends text to TypeSafe. Calling it anywhere but the
// gate (and the jev package's own tests) is a path that skips Scrub.
func TestNoOneElseCallsAsk(t *testing.T) {
	ask := regexp.MustCompile(`\.Ask\(`)
	root := filepath.Join("..", "..")
	var found []string
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		dir := filepath.Base(filepath.Dir(path))
		if dir == "jev" || dir == "jevgate" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if ask.Match(b) && strings.Contains(string(b), "jev.") {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) > 0 {
		t.Errorf("these files call Ask on a Jev client without going through jevgate: %v", found)
	}
}
