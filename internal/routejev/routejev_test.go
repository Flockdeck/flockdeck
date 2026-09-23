package routejev

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/jev"
)

// server is a fake TypeSafe: it records what it was sent and answers as
// scripted.
type server struct {
	*httptest.Server
	mu      sync.Mutex
	bodies  [][]byte
	auths   []string
	status  int
	hang    chan struct{} // closed to release a handler that is holding
	holding bool
	score   float64
	conf    float64
}

func newServer(t *testing.T) *server {
	t.Helper()
	s := &server{status: 200, score: 2.2, conf: 0.8, hang: make(chan struct{})}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.bodies = append(s.bodies, b)
		s.auths = append(s.auths, r.Header.Get("Authorization"))
		status, holding, score, conf := s.status, s.holding, s.score, s.conf
		s.mu.Unlock()
		if holding {
			select {
			case <-s.hang:
			case <-r.Context().Done():
			}
		}
		if status != 200 {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"error":"no"}`)
			return
		}
		out, _ := json.Marshal(map[string]any{
			"model": "jev-1.13.0",
			"answers": map[string]any{
				"difficulty": map[string]any{"type": "score", "score": score, "confidence": conf,
					"legend": map[string]any{}, "probabilities": map[string]any{}},
				"mechanical": map[string]any{"type": "noul", "noul": 0.1},
				"multifile":  map[string]any{"type": "noul", "noul": 0.6},
			},
			"usage": map[string]any{"input_tokens": 10, "output_tokens": 5},
		})
		_, _ = w.Write(out)
	}))
	t.Cleanup(func() { close(s.hang); s.Close() })
	return s
}

func (s *server) requests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.bodies)
}

func (s *server) set(f func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f()
}

func classifier(s *server) *Classifier {
	return New(&jev.Client{APIBase: s.URL, Key: "k-test"})
}

func TestClassifyReadsTheAnswers(t *testing.T) {
	s := newServer(t)
	d, err := classifier(s).Classify("add a health endpoint")
	if err != nil {
		t.Fatal(err)
	}
	if d.Score != 2.2 || d.Confidence != 0.8 || d.Mechanical != 0.1 || d.MultiFile != 0.6 || d.Model != "jev-1.13.0" {
		t.Errorf("%+v", d)
	}
	if s.auths[0] != "Bearer k-test" {
		t.Errorf("the key went as %q", s.auths[0])
	}
}

// The privacy guarantee: what is sent is the bounded task text as the state,
// the model, and the three fixed questions -- nothing else, and nothing of
// the question set depends on the task.
func TestTheRequestCarriesOnlyTheBoundedTaskText(t *testing.T) {
	s := newServer(t)
	c := classifier(s)
	const task = "  add a health endpoint to the api  "
	if _, err := c.Classify(task); err != nil {
		t.Fatal(err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(s.bodies[0], &body); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for k := range body {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") != "model,questions,state" {
		t.Errorf("the request has the fields %v", keys)
	}
	var state string
	if err := json.Unmarshal(body["state"], &state); err != nil || state != "add a health endpoint to the api" {
		t.Errorf("state = %s (%v), want the trimmed task and nothing else", body["state"], err)
	}
	var qs map[string]map[string]any
	if err := json.Unmarshal(body["questions"], &qs); err != nil {
		t.Fatal(err)
	}
	var names []string
	for k := range qs {
		names = append(names, k)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "difficulty,mechanical,multifile" {
		t.Errorf("the questions are %v", names)
	}

	// The questions are the same for any task: the task text appears in the
	// body exactly once, as the state.
	if _, err := c.Classify("another task entirely"); err != nil {
		t.Fatal(err)
	}
	var b2 map[string]json.RawMessage
	_ = json.Unmarshal(s.bodies[1], &b2)
	if string(b2["questions"]) != string(body["questions"]) {
		t.Error("the questions changed with the task")
	}
	if n := strings.Count(string(s.bodies[1]), "another task entirely"); n != 1 {
		t.Errorf("the task text is in the request %d times", n)
	}
}

func TestALongTaskIsCutWithAMarker(t *testing.T) {
	s := newServer(t)
	long := strings.Repeat("é", MaxTask+500) // multibyte: cut on a rune, not a byte
	if _, err := classifier(s).Classify(long); err != nil {
		t.Fatal(err)
	}
	var body struct{ State string }
	if err := json.Unmarshal(s.bodies[0], &body); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(body.State, Marker) || !utf8.ValidString(body.State) {
		t.Errorf("state ends %q", body.State[max(0, len(body.State)-20):])
	}
	if n := utf8.RuneCountInString(body.State); n != MaxTask+utf8.RuneCountInString(Marker) {
		t.Errorf("state is %d runes", n)
	}
	if Bound("short") != "short" {
		t.Error("a short task was changed")
	}
}

func TestTheCacheAnswersARepeatWithoutACall(t *testing.T) {
	s := newServer(t)
	c := classifier(s)
	for i := 0; i < 3; i++ {
		if _, err := c.Classify(" same task "); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Classify("same task"); err != nil {
			t.Fatal(err)
		}
	}
	if n := s.requests(); n != 1 {
		t.Errorf("%d requests for one task", n)
	}
	// An hour later it is asked again.
	now := time.Now()
	c.now = func() time.Time { return now.Add(CacheTTL + time.Second) }
	if _, err := c.Classify("same task"); err != nil {
		t.Fatal(err)
	}
	if n := s.requests(); n != 2 {
		t.Errorf("%d requests after the entry expired", n)
	}
}

func TestTheCacheIsBounded(t *testing.T) {
	s := newServer(t)
	c := classifier(s)
	c.maxPerMinute = 1 << 20
	for i := 0; i < cacheCap+40; i++ {
		if _, err := c.Classify("task " + string(rune('a'+i%26)) + strings.Repeat("x", i)); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.cache) > cacheCap {
		t.Errorf("the cache holds %d entries", len(c.cache))
	}
}

func TestTheRateCapHolds(t *testing.T) {
	s := newServer(t)
	c := classifier(s)
	c.maxPerMinute = 3
	now := time.Now()
	c.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if _, err := c.Classify("task " + strings.Repeat("x", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.Classify("one more"); !errors.Is(err, ErrRateCap) {
		t.Errorf("the fourth call in a minute: %v", err)
	}
	// A cached answer is not a call.
	if _, err := c.Classify("task "); err != nil {
		t.Errorf("a cached task hit the cap: %v", err)
	}
	if n := s.requests(); n != 3 {
		t.Errorf("%d requests, want 3", n)
	}
	now = now.Add(61 * time.Second)
	if _, err := c.Classify("one more"); err != nil {
		t.Errorf("after the minute: %v", err)
	}
}

func TestTheRateCapHoldsUnderConcurrency(t *testing.T) {
	s := newServer(t)
	c := classifier(s)
	c.maxPerMinute = 5
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = c.Classify("task " + strings.Repeat("y", i))
		}(i)
	}
	wg.Wait()
	if n := s.requests(); n > 5 {
		t.Errorf("%d requests through a cap of 5", n)
	}
}

func TestASlowJevIsGivenUpOnAndNotAskedAgainForAWhile(t *testing.T) {
	s := newServer(t)
	s.set(func() { s.holding = true })
	c := classifier(s)
	c.timeout = 100 * time.Millisecond
	start := time.Now()
	if _, err := c.Classify("a task"); err == nil {
		t.Fatal("a call that never answered succeeded")
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("waited %v on a %v deadline", took, c.timeout)
	}
	before := s.requests()
	start = time.Now()
	if _, err := c.Classify("another task"); !errors.Is(err, ErrCooling) {
		t.Errorf("straight after a failure: %v", err)
	}
	if s.requests() != before || time.Since(start) > 50*time.Millisecond {
		t.Error("a task was asked about during the cooldown")
	}
}

func TestRefusalsAreErrorsAndCoolDown(t *testing.T) {
	for _, status := range []int{401, 422, 500, 429} {
		s := newServer(t)
		s.set(func() { s.status = status })
		c := classifier(s)
		c.timeout = 300 * time.Millisecond // a 429 is retried inside this
		if _, err := c.Classify("a task"); err == nil {
			t.Errorf("%d: no error", status)
		}
		if _, err := c.Classify("b task"); !errors.Is(err, ErrCooling) {
			t.Errorf("%d: %v straight after", status, err)
		}
	}
	// A key TypeSafe refused is not tried again for longer.
	s := newServer(t)
	s.set(func() { s.status = 401 })
	c := classifier(s)
	now := time.Now()
	c.now = func() time.Time { return now }
	_, _ = c.Classify("a task")
	c.now = func() time.Time { return now.Add(cooldown + time.Second) }
	if _, err := c.Classify("b task"); !errors.Is(err, ErrCooling) {
		t.Errorf("a refused key was asked again after %v: %v", cooldown, err)
	}
	c.now = func() time.Time { return now.Add(cooldownBadKey + time.Second) }
	if _, err := c.Classify("b task"); errors.Is(err, ErrCooling) {
		t.Errorf("a refused key is still cooling after %v", cooldownBadKey)
	}
}

func TestAnAnswerOffTheRubricIsNoAnswer(t *testing.T) {
	s := newServer(t)
	s.set(func() { s.score = 9 })
	if _, err := classifier(s).Classify("a task"); !errors.Is(err, ErrNoAnswer) {
		t.Errorf("a score of 9 on a rubric of 5 levels: %v", err)
	}
	s = newServer(t)
	s.set(func() { s.conf = 1.5 })
	if _, err := classifier(s).Classify("a task"); !errors.Is(err, ErrNoAnswer) {
		t.Errorf("a confidence of 1.5: %v", err)
	}
	if _, err := classifier(s).Classify("   "); !errors.Is(err, ErrNoAnswer) {
		t.Errorf("an empty task: %v", err)
	}
}

// For is nil, so the router never sees a classifier, unless the policy turned
// Jev on and the key is set.
func TestForNeedsBothTheSettingAndTheKey(t *testing.T) {
	on := agent.RoutingPolicy{Mode: agent.RoutingAuto, Strategy: agent.StrategyCost, Jev: true}

	t.Setenv(jev.KeyEnv, "")
	if For(on) != nil || Enabled() {
		t.Error("a classifier without a key")
	}
	t.Setenv(jev.KeyEnv, "k-test")
	if !Enabled() {
		t.Error("a key that is set is not seen")
	}
	if For(on) == nil {
		t.Error("no classifier with the setting and the key")
	}
	off := on
	off.Jev = false
	if For(off) != nil {
		t.Error("a classifier with the setting off")
	}
	balanced := on
	balanced.Strategy = agent.StrategyBalanced
	if For(balanced) != nil {
		t.Error("a classifier under the balanced strategy, which has no fallback to refine")
	}
	if a, b := For(on), For(on); a != b {
		t.Error("the shared classifier, and so its cache and cap, was replaced")
	}
}
