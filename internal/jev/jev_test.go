package jev

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The two documents below are TypeSafe's own quickstart example
// (docs.typesafe.ai/introduction/quickstart), copied field for field: a
// support-ticket triage with a Choice, a Score and a Noul. The tests are held
// to them, so that what the package sends and reads is the real API's shape and
// not what this package assumes it to be.
const quickstartRequest = `{
  "state": "Hi, I've been trying to connect my Stripe account for 3 days and the integration keeps failing. I'm losing sales. Please help ASAP.",
  "model": "jev-latest",
  "questions": {
    "department": {
      "type": "choice",
      "instructions": "Which team should handle this",
      "criteria": {
        "billing": "Payment or subscription issues",
        "technical": "Bugs or integration problems",
        "sales": "Pricing or account questions"
      }
    },
    "frustration": {
      "type": "score",
      "instructions": "How frustrated the customer appears",
      "criteria": [
        "Calm, just stating facts",
        "Frustrated but civil",
        "Very angry, strong language"
      ]
    },
    "is_urgent": {
      "type": "noul",
      "instructions": "The message conveys urgency or time-sensitivity"
    }
  }
}`

const quickstartResponse = `{
  "model": "jev-1.13.0",
  "answers": {
    "department": {
      "type": "choice",
      "choice": "technical",
      "confidence": 0.78,
      "probabilities": {"technical": 0.85, "sales": 0.0, "billing": 0.15}
    },
    "frustration": {
      "type": "score",
      "score": 1.0,
      "confidence": 1.0,
      "legend": {"0": "Calm, just stating facts", "1": "Frustrated but civil", "2": "Very angry, strong language"},
      "probabilities": {"0": 0.0, "1": 1.0, "2": 0.0}
    },
    "is_urgent": {"type": "noul", "noul": 1.0}
  },
  "usage": {"input_tokens": 392, "output_tokens": 65}
}`

const testKey = "test-key-not-real"

const quickstartState = "Hi, I've been trying to connect my Stripe account for 3 days and the integration keeps failing. I'm losing sales. Please help ASAP."

func quickstartQuestions() map[string]Question {
	return map[string]Question{
		"department": Choice{
			Instructions: "Which team should handle this",
			Options: map[string]any{
				"billing":   "Payment or subscription issues",
				"technical": "Bugs or integration problems",
				"sales":     "Pricing or account questions",
			},
		},
		"frustration": Score{
			Instructions: "How frustrated the customer appears",
			Levels:       []any{"Calm, just stating facts", "Frustrated but civil", "Very angry, strong language"},
		},
		"is_urgent": Noul{Instructions: "The message conveys urgency or time-sensitivity"},
	}
}

// fake is TypeSafe's API, answering what each test says. No test here reaches
// the real thing, and no real key is ever used.
type fake struct {
	calls   atomic.Int32
	lastReq []byte
	handle  func(w http.ResponseWriter, r *http.Request, call int)
}

func newFake(t *testing.T, handle func(w http.ResponseWriter, r *http.Request, call int)) (*Client, *fake) {
	t.Helper()
	f := &fake{handle: handle}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(f.calls.Add(1))
		if r.Method != http.MethodPost || r.URL.Path != "/v1/systemone" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+testKey {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		f.lastReq, _ = io.ReadAll(r.Body)
		f.handle(w, r, n)
	}))
	t.Cleanup(ts.Close)
	origB, origM, origR := retryBackoff, maxBackoff, maxRetryAfter
	retryBackoff, maxBackoff, maxRetryAfter = time.Millisecond, 4*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { retryBackoff, maxBackoff, maxRetryAfter = origB, origM, origR })
	return &Client{APIBase: ts.URL, Key: testKey, HTTP: ts.Client()}, f
}

func reply(status int, body string) func(http.ResponseWriter, *http.Request, int) {
	return func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

func sameJSON(t *testing.T, got []byte, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("sent body is not JSON: %v\n%s", err, got)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("sent:\n%s\nwant:\n%s", got, want)
	}
}

// One call carries all three question types, sends exactly what TypeSafe's
// quickstart sends, and reads back exactly what it documents, each answer
// under the key its question had.
func TestAskMixedBatchRoundTrip(t *testing.T) {
	c, f := newFake(t, reply(200, quickstartResponse))
	res, err := c.Ask(context.Background(), quickstartState, quickstartQuestions())
	if err != nil {
		t.Fatal(err)
	}
	if f.calls.Load() != 1 {
		t.Errorf("calls = %d, want 1: three questions are one request", f.calls.Load())
	}
	sameJSON(t, f.lastReq, quickstartRequest)

	if res.Model != "jev-1.13.0" || res.Usage != (Usage{InputTokens: 392, OutputTokens: 65}) {
		t.Errorf("model %q, usage %+v", res.Model, res.Usage)
	}
	dept, err := res.Choice("department")
	if err != nil {
		t.Fatal(err)
	}
	if dept.Choice != "technical" || dept.Confidence != 0.78 || dept.Probabilities["billing"] != 0.15 || dept.Probabilities["technical"] != 0.85 {
		t.Errorf("department = %+v", dept)
	}
	fr, err := res.Score("frustration")
	if err != nil {
		t.Fatal(err)
	}
	if fr.Score != 1.0 || fr.Confidence != 1.0 || fr.Legend["1"] != "Frustrated but civil" || fr.Probabilities["1"] != 1.0 {
		t.Errorf("frustration = %+v", fr)
	}
	urgent, err := res.Noul("is_urgent")
	if err != nil {
		t.Fatal(err)
	}
	if urgent.Probability != 1.0 {
		t.Errorf("is_urgent = %+v", urgent)
	}
	for k, want := range map[string]string{"department": "choice", "frustration": "score", "is_urgent": "noul"} {
		if res.Answers[k].Kind() != want {
			t.Errorf("%s answered as %s", k, res.Answers[k].Kind())
		}
	}
}

// Asking a Result for an answer as the wrong type, or for one that is not
// there, is an error saying so and not a zero value that passes for a real
// answer.
func TestResultAccessorsRefuseTheWrongKind(t *testing.T) {
	c, _ := newFake(t, reply(200, quickstartResponse))
	res, err := c.Ask(context.Background(), quickstartState, quickstartQuestions())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := res.Noul("department"); err == nil || !strings.Contains(err.Error(), "choice") {
		t.Errorf("Noul on a choice: %v", err)
	}
	if _, err := res.Score("nope"); err == nil || !strings.Contains(err.Error(), "no answer") {
		t.Errorf("Score on a missing key: %v", err)
	}
}

// A model that is set is the one asked for; a Noul's optional true/false
// descriptions go as criteria, and only then; a Choice option with no
// description goes as null; state may be structure.
func TestAskOptionalFields(t *testing.T) {
	c, f := newFake(t, reply(200, `{"model":"jev-1.13.0","answers":{"q":{"type":"noul","noul":0.25}},"usage":{"input_tokens":1,"output_tokens":1}}`))
	c.Model = "jev-1.13.0"
	state := map[string]any{"screen": []string{"a", "b"}}
	qs := map[string]Question{"q": Noul{Instructions: "Has the customer contacted support before?", True: "Mentions a prior ticket", False: "No previous contact"}}
	if _, err := c.Ask(context.Background(), state, qs); err != nil {
		t.Fatal(err)
	}
	sameJSON(t, f.lastReq, `{"state":{"screen":["a","b"]},"model":"jev-1.13.0","questions":{"q":{"type":"noul","instructions":"Has the customer contacted support before?","criteria":{"true":"Mentions a prior ticket","false":"No previous contact"}}}}`)

	c.Model = ""
	qs = map[string]Question{"q": Noul{Instructions: struct {
		Ask string `json:"ask"`
	}{"structured"}}}
	if _, err := c.Ask(context.Background(), "s", qs); err != nil {
		t.Fatal(err)
	}
	sameJSON(t, f.lastReq, `{"state":"s","model":"jev-latest","questions":{"q":{"type":"noul","instructions":{"ask":"structured"}}}}`)

	f.handle = reply(200, `{"model":"m","answers":{"c":{"type":"choice","choice":"a","confidence":1,"probabilities":{"a":1}}},"usage":{}}`)
	if _, err := c.Ask(context.Background(), "s", map[string]Question{"c": Choice{Instructions: "x", Options: map[string]any{"a": nil, "b": "the b one"}}}); err != nil {
		t.Fatal(err)
	}
	sameJSON(t, f.lastReq, `{"state":"s","model":"jev-latest","questions":{"c":{"type":"choice","instructions":"x","criteria":{"a":null,"b":"the b one"}}}}`)
}

// Each documented status is its own error, unwrapping to its sentinel, and a
// 401 or a 422 costs exactly one request: they are permanent, and asking again
// would only look like hammering.
func TestPermanentErrorsAreNotRetried(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   error
	}{
		{401, ErrUnauthorized},
		{422, ErrInvalidRequest},
	} {
		c, f := newFake(t, reply(tc.status, `{"detail":"nope"}`))
		_, err := c.Ask(context.Background(), "s", quickstartQuestions())
		if !errors.Is(err, tc.want) {
			t.Errorf("%d: err = %v, want %v", tc.status, err, tc.want)
		}
		var ae *APIError
		if !errors.As(err, &ae) || ae.Status != tc.status || !strings.Contains(ae.Body, "nope") {
			t.Errorf("%d: APIError = %+v", tc.status, ae)
		}
		if n := f.calls.Load(); n != 1 {
			t.Errorf("%d: %d requests, want 1", tc.status, n)
		}
	}
}

// A 429 or a 529 that clears is retried and then a success: the caller never
// sees it.
func TestTransientErrorIsRetriedThenSucceeds(t *testing.T) {
	for _, status := range []int{429, 529} {
		c, f := newFake(t, func(w http.ResponseWriter, r *http.Request, call int) {
			if call < 3 {
				w.WriteHeader(status)
				return
			}
			_, _ = io.WriteString(w, quickstartResponse)
		})
		res, err := c.Ask(context.Background(), quickstartState, quickstartQuestions())
		if err != nil {
			t.Fatalf("%d: %v", status, err)
		}
		if res.Model != "jev-1.13.0" || f.calls.Load() != 3 {
			t.Errorf("%d: model %q after %d calls", status, res.Model, f.calls.Load())
		}
		sameJSON(t, f.lastReq, quickstartRequest) // a retry sends the same request
	}
}

// A 429 or a 529 that never clears stops after maxAttempts and says which it
// was, and how many times it tried.
func TestRetryExhaustion(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   error
	}{
		{429, ErrRateLimited},
		{529, ErrOverloaded},
	} {
		c, f := newFake(t, reply(tc.status, `{"detail":"busy"}`))
		_, err := c.Ask(context.Background(), "s", quickstartQuestions())
		if !errors.Is(err, tc.want) {
			t.Errorf("%d: err = %v, want %v", tc.status, err, tc.want)
		}
		var ae *APIError
		if !errors.As(err, &ae) || ae.Status != tc.status {
			t.Errorf("%d: no APIError in %v", tc.status, err)
		}
		if n := f.calls.Load(); int(n) != maxAttempts {
			t.Errorf("%d: %d requests, want %d", tc.status, n, maxAttempts)
		}
		if !strings.Contains(err.Error(), "3 attempts") {
			t.Errorf("%d: %v does not say how many attempts", tc.status, err)
		}
	}
}

// A Retry-After is waited out, up to the cap, in place of the backoff.
func TestRetryAfterIsHonouredAndCapped(t *testing.T) {
	c, _ := newFake(t, func(w http.ResponseWriter, r *http.Request, call int) {
		if call == 1 {
			w.Header().Set("Retry-After", "3600") // an hour: capped, or this test never ends
			w.WriteHeader(429)
			return
		}
		_, _ = io.WriteString(w, quickstartResponse)
	})
	maxBackoff = time.Hour // so that a capped Retry-After, not the backoff, is what is short
	start := time.Now()
	if _, err := c.Ask(context.Background(), "s", quickstartQuestions()); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < maxRetryAfter || d > 5*time.Second {
		t.Errorf("waited %v, want about %v", d, maxRetryAfter)
	}
}

// Cancelling while a request is in flight returns at once with the context's
// error, and does not retry.
func TestContextCancelledMidRequest(t *testing.T) {
	started := make(chan struct{})
	c, f := newFake(t, func(w http.ResponseWriter, r *http.Request, call int) {
		close(started)
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-started; cancel() }()
	_, err := c.Ask(ctx, "s", quickstartQuestions())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if n := f.calls.Load(); n != 1 {
		t.Errorf("%d requests, want 1", n)
	}
}

// Cancelling during the wait between retries ends the wait.
func TestContextCancelledDuringBackoff(t *testing.T) {
	c, f := newFake(t, reply(429, ""))
	retryBackoff, maxBackoff = time.Hour, time.Hour
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.Ask(ctx, "s", quickstartQuestions())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	if time.Since(start) > 5*time.Second || f.calls.Load() != 1 {
		t.Errorf("took %v over %d calls", time.Since(start), f.calls.Load())
	}
}

// One attempt that never answers is bounded by attemptTimeout, not left to hang.
func TestAttemptTimeout(t *testing.T) {
	c, _ := newFake(t, func(w http.ResponseWriter, r *http.Request, call int) { <-r.Context().Done() })
	orig := attemptTimeout
	attemptTimeout = 30 * time.Millisecond
	t.Cleanup(func() { attemptTimeout = orig })
	if _, err := c.Ask(context.Background(), "s", quickstartQuestions()); err == nil {
		t.Fatal("no error from a server that never answers")
	}
}

// Whatever comes back that is not the answer to what was asked is
// ErrBadResponse, never a partial Result.
func TestMalformedResponses(t *testing.T) {
	for name, body := range map[string]string{
		"not json":            `<html>bad gateway</html>`,
		"empty":               ``,
		"answers is a list":   `{"model":"m","answers":[],"usage":{}}`,
		"missing an answer":   `{"model":"m","answers":{"department":{"type":"choice","choice":"sales","confidence":1,"probabilities":{"sales":1}}},"usage":{}}`,
		"wrong type":          strings.Replace(quickstartResponse, `"is_urgent": {"type": "noul", "noul": 1.0}`, `"is_urgent": {"type": "score", "score": 1.0, "confidence": 1.0}`, 1),
		"noul without value":  strings.Replace(quickstartResponse, `"noul": 1.0`, `"other": 1.0`, 1),
		"choice no choice":    strings.Replace(quickstartResponse, `"choice": "technical",`, ``, 1),
		"score no confidence": strings.Replace(quickstartResponse, `"confidence": 1.0,`, ``, 1),
		"unknown type":        strings.Replace(quickstartResponse, `"type": "noul"`, `"type": "mystery"`, 1),
		"answer not object":   strings.Replace(quickstartResponse, `{"type": "noul", "noul": 1.0}`, `7`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			c, f := newFake(t, reply(200, body))
			res, err := c.Ask(context.Background(), quickstartState, quickstartQuestions())
			if !errors.Is(err, ErrBadResponse) || res != nil {
				t.Errorf("res = %v, err = %v; want ErrBadResponse", res, err)
			}
			if f.calls.Load() != 1 {
				t.Errorf("%d requests: a bad answer is not retried", f.calls.Load())
			}
		})
	}
}

// An answer to something that was not asked is ignored, not an error.
func TestExtraAnswersAreIgnored(t *testing.T) {
	c, _ := newFake(t, reply(200, quickstartResponse))
	res, err := c.Ask(context.Background(), "s", map[string]Question{"is_urgent": Noul{Instructions: "urgent?"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Answers) != 1 {
		t.Errorf("answers = %v", res.Answers)
	}
}

// A request TypeSafe would refuse is refused here, before a request is made
// and with the question named.
func TestInvalidRequestsNeverReachTheWire(t *testing.T) {
	levels := func(n int) []any { return make([]any, n) }
	options := func(n int) map[string]any {
		m := map[string]any{}
		for i := 0; i < n; i++ {
			m[string(rune('a'+i%26))+strings.Repeat("x", i/26)] = nil
		}
		return m
	}
	for name, tc := range map[string]struct {
		state any
		qs    map[string]Question
		want  string
	}{
		"no questions":    {"s", nil, "no questions"},
		"nil state":       {nil, quickstartQuestions(), "no state"},
		"nil question":    {"s", map[string]Question{"q": nil}, `"q" is nil`},
		"empty key":       {"s", map[string]Question{"": Noul{Instructions: "x"}}, "no key"},
		"no instructions": {"s", map[string]Question{"q": Noul{}}, "no instructions"},
		"one score level": {"s", map[string]Question{"q": Score{Instructions: "x", Levels: levels(1)}}, "1 levels"},
		"11 score levels": {"s", map[string]Question{"q": Score{Instructions: "x", Levels: levels(11)}}, "11 levels"},
		"no options":      {"s", map[string]Question{"q": Choice{Instructions: "x"}}, "0 options"},
		"256 options":     {"s", map[string]Question{"q": Choice{Instructions: "x", Options: options(256)}}, "256 options"},
		"unnamed option":  {"s", map[string]Question{"q": Choice{Instructions: "x", Options: map[string]any{"": nil}}}, "no name"},
		"unmarshalable":   {make(chan int), quickstartQuestions(), "JSON"},
	} {
		t.Run(name, func(t *testing.T) {
			c, f := newFake(t, reply(200, quickstartResponse))
			_, err := c.Ask(context.Background(), tc.state, tc.qs)
			if !errors.Is(err, ErrInvalidRequest) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want ErrInvalidRequest mentioning %q", err, tc.want)
			}
			if f.calls.Load() != 0 {
				t.Error("a request was made")
			}
		})
	}
	// The limits themselves are allowed.
	c, _ := newFake(t, reply(200, `{"model":"m","answers":{"c":{"type":"choice","choice":"a","confidence":1,"probabilities":{}},"s":{"type":"score","score":0,"confidence":1}},"usage":{}}`))
	_, err := c.Ask(context.Background(), "s", map[string]Question{
		"c": Choice{Instructions: "x", Options: options(255)},
		"s": Score{Instructions: "x", Levels: levels(10)},
	})
	if err != nil {
		t.Errorf("255 options and 10 levels: %v", err)
	}
}

// A missing key is ErrNoKey from both ways in, with no request made, and a
// key that is set is read from TYPESAFE_API_KEY and from nowhere else.
func TestKeyFromEnvironment(t *testing.T) {
	t.Setenv(KeyEnv, "")
	t.Setenv("FLOCKDECK_API_KEY", "another-vendors-key")
	if _, err := NewClientFromEnv(); !errors.Is(err, ErrNoKey) {
		t.Errorf("unset: %v", err)
	}
	t.Setenv(KeyEnv, "  \t ")
	if _, err := NewClientFromEnv(); !errors.Is(err, ErrNoKey) {
		t.Errorf("blank: %v", err)
	}
	if !strings.Contains(ErrNoKey.Error(), "TYPESAFE_API_KEY") {
		t.Errorf("ErrNoKey does not say what to set: %v", ErrNoKey)
	}
	t.Setenv(KeyEnv, " "+testKey+"\n")
	c, err := NewClientFromEnv()
	if err != nil || c.Key != testKey {
		t.Errorf("set: %+v, %v", c, err)
	}

	c, f := newFake(t, reply(200, quickstartResponse))
	c.Key = ""
	if _, err := c.Ask(context.Background(), "s", quickstartQuestions()); !errors.Is(err, ErrNoKey) || f.calls.Load() != 0 {
		t.Errorf("Ask with no key: %v after %d calls", err, f.calls.Load())
	}
}

// The key goes in the Authorization header and nowhere else: not in an error.
func TestKeyIsNotInErrors(t *testing.T) {
	c, _ := newFake(t, reply(401, `{"detail":"invalid key"}`))
	_, err := c.Ask(context.Background(), "s", quickstartQuestions())
	if err == nil || strings.Contains(err.Error(), testKey) {
		t.Errorf("err = %v", err)
	}
}

// A redirect is not followed with the key: with no HTTP client of its own, the
// package's default refuses it, and the target is never reached.
func TestRedirectIsNotFollowed(t *testing.T) {
	var reached atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached.Store(true) }))
	defer target.Close()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer ts.Close()
	c := &Client{APIBase: ts.URL, Key: testKey}
	if _, err := c.Ask(context.Background(), "s", quickstartQuestions()); err == nil || reached.Load() {
		t.Errorf("err = %v, redirect target reached = %v", err, reached.Load())
	}
}

// A refusal's body in an error is cut, and a body that is not there leaves the
// error still reading well.
func TestAPIErrorText(t *testing.T) {
	c, _ := newFake(t, reply(422, strings.Repeat("x", 5000)))
	_, err := c.Ask(context.Background(), "s", quickstartQuestions())
	if err == nil || len(err.Error()) > 500 {
		t.Errorf("error is %d bytes", len(err.Error()))
	}
	// net/http has no text for 529, and the error must not end in a dangling space.
	if got := (&APIError{Status: 529}).Error(); got != "typesafe answered 529" {
		t.Errorf("529 reads %q", got)
	}
}
