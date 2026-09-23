// Package jev is a client for Jev, TypeSafe AI's structured-decision model
// (https://docs.typesafe.ai). Jev is not asked to write anything: it is handed
// some state and a set of typed questions about it -- a yes/no probability
// (Noul), a pick from named options (Choice), a rating on an ordered rubric
// (Score) -- and answers each with calibrated probabilities, which is what
// makes it usable as a branch in code rather than as text to be parsed.
//
// The package knows nothing about who asks or what is asked. It is the wire
// and nothing else, so the things that will lean on it (reading a pane's
// state off its screen, choosing a model for a task) each bring their own
// questions and their own reading of the answers.
//
// Ask takes every question at once, and that is the shape to use: TypeSafe
// measures a batch of 13 as 12.2x cheaper and 10.0x faster than 13 calls,
// since adding a question barely changes the time a call takes. A caller with
// three things to ask should send one request with three questions, not three.
//
// Modelled on flockdeck-relay's internal/postmark client (a typed Client, one
// private do that builds the request and decodes it, a bounded retry for
// what is transient, a sentinel error for what a caller must tell apart).
package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// API is TypeSafe's own address. A test points Client.APIBase at a fake
// instead.
const API = "https://api.typesafe.ai"

// path is the one endpoint every System One question goes to.
const path = "/v1/systemone"

// DefaultModel is what a Client with no Model asks. "jev-latest" is TypeSafe's
// own alias for its newest Jev, so an answer says which one it really was in
// Result.Model; a caller who wants answers that do not shift under it when
// TypeSafe ships a new one names a version instead.
const DefaultModel = "jev-latest"

// KeyEnv is the environment variable the key is read from. It is TypeSafe's
// own, the one its SDKs read, and not a FLOCKDECK_ one: this repo's precedent
// for a vendor's key is the vendor's usual variable (OPENAI_API_KEY,
// ANTHROPIC_API_KEY -- see agent.KeyNames), so a key already exported for
// TypeSafe's tooling is found without setting it twice.
//
// FLOCKDECK_API_KEY, the fallback the agents' keys have, is deliberately not
// consulted: it is a key for whichever vendor an agent talks to, and sending
// it to TypeSafe would hand another vendor's key to a third party.
const KeyEnv = "TYPESAFE_API_KEY"

// Limits TypeSafe documents and Ask enforces itself, so that a request it
// would refuse with a 422 is refused here without the round trip, and with a
// message that names the question at fault.
const (
	minScoreLevels = 2
	maxScoreLevels = 10
	maxChoices     = 255
)

// ErrNoKey is the error when there is no API key to send: it is what
// NewClientFromEnv and Ask say for an unset or blank one, before any HTTP call,
// so that a missing key is never seen as a 401 from somewhere in the middle of
// a request that could never have worked.
var ErrNoKey = errors.New("no TypeSafe API key: set " + KeyEnv)

// The sentinels below are what an APIError unwraps to, so a caller who cares
// which refusal it was asks errors.Is and one who does not treats the error
// as any other.
var (
	// ErrUnauthorized is a 401: the key is missing, wrong or revoked. It is
	// never retried -- the same key will be refused the same way -- and it is
	// a matter for the person who set it, not something to try again later.
	ErrUnauthorized = errors.New("TypeSafe refused the API key")
	// ErrInvalidRequest is a 422: the request was well-formed JSON that
	// TypeSafe would not accept. Never retried. Ask's own checks catch the
	// documented cases first, so seeing this means a rule they do not know.
	ErrInvalidRequest = errors.New("TypeSafe rejected the request as invalid")
	// ErrRateLimited is a 429 that was still there after every retry.
	ErrRateLimited = errors.New("TypeSafe is rate limiting this key")
	// ErrOverloaded is a 529 that was still there after every retry: it is
	// TypeSafe saying it is overloaded, which is theirs and not something the
	// key or the request did.
	ErrOverloaded = errors.New("TypeSafe is overloaded")
)

// ErrBadResponse is what an answer that reached us but is not the answer to
// what was asked -- not JSON, an answer missing for a question, an answer of
// another type than its question -- unwraps to. The request went through and
// was charged for, and asking again is a matter for the caller: nothing here
// says it would come out differently.
var ErrBadResponse = errors.New("TypeSafe's answer is not the one expected")

// APIError is TypeSafe answering with a refusal. It unwraps to the sentinel for
// its status, when there is one.
type APIError struct {
	Status int
	// Body is what TypeSafe said, cut to a length fit for an error message.
	// Its shape for an error is not documented, so it is passed on as it came
	// instead of picked apart for a field that may not be there.
	Body string

	// retryAfter is how long TypeSafe's Retry-After asked to be left alone,
	// when it sent one. Ask reads it; a caller has no use for it, since by the
	// time it sees the error the waiting is done.
	retryAfter time.Duration
}

func (e *APIError) Error() string {
	s := fmt.Sprintf("typesafe answered %d", e.Status)
	if text := http.StatusText(e.Status); text != "" {
		s += " " + text
	}
	if e.Body != "" {
		s += ": " + e.Body
	}
	return s
}

func (e *APIError) Unwrap() error {
	switch e.Status {
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusUnprocessableEntity:
		return ErrInvalidRequest
	case http.StatusTooManyRequests:
		return ErrRateLimited
	case statusOverloaded:
		return ErrOverloaded
	}
	return nil
}

// statusOverloaded is 529, which net/http has no name for. TypeSafe answers it
// when it is temporarily overloaded.
const statusOverloaded = 529

// Client asks Jev. The zero value is not usable without a Key; see
// NewClientFromEnv.
type Client struct {
	// APIBase is API in production; a test points it at an httptest server.
	APIBase string
	// Key is the TypeSafe API key. Never logged and never put in an error
	// message: it goes in the Authorization header and nowhere else. Read from
	// KeyEnv by NewClientFromEnv, and committed nowhere.
	Key string
	// Model is what to ask; DefaultModel when empty.
	Model string
	// HTTP is the client requests are sent with. Nil is one that follows no
	// redirect: Go carries the Authorization header across a redirect to the
	// same host whatever the scheme, so one to http:// would send the key in
	// the clear, and TypeSafe's API never redirects.
	HTTP *http.Client
}

// NewClientFromEnv is a client with the key in KeyEnv, or ErrNoKey when there
// is none. A caller that wants Jev only when it is set -- these are features
// that should quietly not happen without a key, not break -- checks for that
// error and carries on without.
func NewClientFromEnv() (*Client, error) {
	key := strings.TrimSpace(os.Getenv(KeyEnv))
	if key == "" {
		return nil, ErrNoKey
	}
	return &Client{Key: key}, nil
}

// Usage is what a call cost, in tokens, as TypeSafe counts them.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Result is what Ask returns: the answers, keyed as the questions were, and
// what asking cost.
type Result struct {
	// Model is the version that answered ("jev-1.13.0"), which is not what was
	// asked when that was "jev-latest".
	Model   string
	Answers map[string]Answer
	Usage   Usage
}

// Noul is the answer to the question at key, or an error when there is none
// or it is of another type. Ask has already checked that every question has
// an answer of its own type, so for a key that was asked as a Noul the error
// is a programming mistake in the caller and not a runtime condition.
func (r *Result) Noul(key string) (NoulAnswer, error) {
	a, ok := r.Answers[key].(NoulAnswer)
	if !ok {
		return NoulAnswer{}, r.wrongKind(key, "noul")
	}
	return a, nil
}

// Choice is the answer to the Choice at key. See Noul.
func (r *Result) Choice(key string) (ChoiceAnswer, error) {
	a, ok := r.Answers[key].(ChoiceAnswer)
	if !ok {
		return ChoiceAnswer{}, r.wrongKind(key, "choice")
	}
	return a, nil
}

// Score is the answer to the Score at key. See Noul.
func (r *Result) Score(key string) (ScoreAnswer, error) {
	a, ok := r.Answers[key].(ScoreAnswer)
	if !ok {
		return ScoreAnswer{}, r.wrongKind(key, "score")
	}
	return a, nil
}

func (r *Result) wrongKind(key, want string) error {
	if a, ok := r.Answers[key]; ok {
		return fmt.Errorf("jev: the answer to %q is a %s, not a %s", key, a.Kind(), want)
	}
	return fmt.Errorf("jev: there is no answer to %q", key)
}

// Ask puts the questions, keyed by names of the caller's choosing, about state
// to Jev in one call, and returns an answer for each under the same key.
//
// state is whatever is being judged: a string, or anything that marshals to
// JSON as an object or an array, which TypeSafe reads as structure and not as
// text. Questions and state are checked here first (see the limits above), and
// a missing key is ErrNoKey, before any request is made.
//
// A 429 or a 529 is retried, bounded and with backoff, since both are TypeSafe
// asking to be tried again shortly; a 401 or a 422 never is, since asking the
// same thing again gets the same answer and only looks like hammering. What
// remains after the retries is an *APIError wrapping ErrRateLimited or
// ErrOverloaded. A dropped connection is not retried either: it is not
// something TypeSafe said was transient, and a caller with a use for it
// has ctx to bound how long it is willing to wait on all of it.
func (c *Client) Ask(ctx context.Context, state any, questions map[string]Question) (*Result, error) {
	if c.Key == "" {
		return nil, ErrNoKey
	}
	if state == nil {
		return nil, fmt.Errorf("%w: no state to ask about", ErrInvalidRequest)
	}
	wq, err := encodeQuestions(questions)
	if err != nil {
		return nil, err
	}
	model := c.Model
	if model == "" {
		model = DefaultModel
	}
	body, err := json.Marshal(request{State: state, Model: model, Questions: wq})
	if err != nil {
		return nil, fmt.Errorf("%w: state cannot be sent as JSON: %v", ErrInvalidRequest, err)
	}

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(backoff(attempt, lastErr)):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		data, err := c.do(ctx, body)
		if err == nil {
			return decode(data, questions)
		}
		var ae *APIError
		if !errors.As(err, &ae) || !transient(ae.Status) {
			return nil, err
		}
		lastErr = err
	}
	return nil, fmt.Errorf("ask jev, after %d attempts: %w", maxAttempts, lastErr)
}

// maxAttempts is how many times Ask tries a call TypeSafe said to try again:
// the first, and two retries, which is what TypeSafe's own SDKs do.
const maxAttempts = 3

// Backoff between attempts doubles from retryBackoff up to maxBackoff, and
// TypeSafe's own Retry-After, when it sends one, is taken over it. They are
// vars so a test does not have to sit through them.
var (
	retryBackoff = 500 * time.Millisecond
	maxBackoff   = 5 * time.Second
	// maxRetryAfter caps what a Retry-After can make Ask wait: a server that
	// says to come back in an hour is not going to be waited for by a caller
	// with somebody at the other end of it, who would rather have the error.
	maxRetryAfter = 10 * time.Second
	// attemptTimeout bounds one request, which TypeSafe's SDKs do too (10s).
	// A call in a batch is not slower than a single one to any degree that
	// matters, so this is not a number to raise for a bigger batch.
	attemptTimeout = 10 * time.Second
)

// transient reports whether a status is TypeSafe asking to be tried again.
// TypeSafe's SDKs also retry a 408 and every other 5xx; the docs for the API
// itself list only these two, and a 500 is as likely to be something a
// retry repeats, so this does the documented pair and no more.
func transient(status int) bool {
	return status == http.StatusTooManyRequests || status == statusOverloaded
}

// backoff is the wait before the given attempt (1 is the first retry):
// retryBackoff doubled for each one before it, no more than maxBackoff, unless
// the last refusal carried a Retry-After, which is honoured up to
// maxRetryAfter.
func backoff(attempt int, last error) time.Duration {
	var ae *APIError
	if errors.As(last, &ae) && ae.retryAfter > 0 {
		return min(ae.retryAfter, maxRetryAfter)
	}
	d := retryBackoff << (attempt - 1)
	if d <= 0 || d > maxBackoff {
		d = maxBackoff
	}
	return d
}

// do makes one attempt: the request, and the body of a 2xx answer. A refusal
// is an *APIError.
func (c *Client) do(ctx context.Context, body []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, attemptTimeout)
	defer cancel()
	base := c.APIBase
	if base == "" {
		base = API
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		// The caller's own cancellation or deadline is theirs to see as such,
		// not buried in the transport's wrapping of it.
		if cerr := ctx.Err(); cerr != nil {
			return nil, fmt.Errorf("reach typesafe: %w", cerr)
		}
		return nil, fmt.Errorf("reach typesafe: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("read typesafe's answer: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		ae := &APIError{Status: resp.StatusCode, Body: clip(string(data))}
		if secs, err := strconv.ParseFloat(strings.TrimSpace(resp.Header.Get("Retry-After")), 64); err == nil && secs > 0 {
			ae.retryAfter = time.Duration(secs * float64(time.Second))
		}
		return nil, ae
	}
	return data, nil
}

// maxBody bounds what is read of an answer. A batch of 255-option choices is
// still well under it.
const maxBody = 4 << 20

// clip cuts a refusal's body to a length that belongs in an error message.
func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return defaultHTTP
}

var defaultHTTP = &http.Client{
	CheckRedirect: func(req *http.Request, _ []*http.Request) error {
		return fmt.Errorf("it answered with a redirect to %s://%s, which is not followed with this API key", req.URL.Scheme, req.URL.Host)
	},
}
