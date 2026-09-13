package chat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// NewWire returns the wire named by an agent's APISpec.
//
// The three names are the ones the catalog uses, and "openai" covers every
// OpenAI-compatible endpoint -- Ollama, LM Studio, vLLM, a gateway -- which is
// why a local model needs no code of its own, only a base URL.
func NewWire(name, baseURL, key string) (Wire, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "anthropic", "":
		return &anthropicWire{base: baseURL, key: key}, nil
	case "openai", "openai-compatible":
		return &openaiWire{base: baseURL, key: key}, nil
	case "gemini", "google":
		return &geminiWire{base: baseURL, key: key}, nil
	}
	return nil, fmt.Errorf("unknown wire %q: it must be anthropic, openai or gemini", name)
}

// httpClient is what every wire posts with.
//
// It has no timeout of its own on purpose: a streamed turn can take minutes,
// and what ends one early is the context -- which is what Ctrl+C cancels.
var httpClient = &http.Client{CheckRedirect: keepToTheAddress}

// keepToTheAddress follows a redirect only on the host that was asked, and
// never from https down to plain http.
//
// Go carries a request's own headers across a redirect, and the key travels in
// one of them -- x-api-key, x-goog-api-key -- which Go does not know to drop
// the way it drops Authorization. A redirect to anywhere else handed the key
// to that address; one to plain http sent it in the clear.
func keepToTheAddress(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	asked := via[0].URL
	switch {
	case !strings.EqualFold(req.URL.Host, asked.Host):
		return fmt.Errorf("the endpoint redirected to %s, which is not followed: the key would be sent there", req.URL.Host)
	case asked.Scheme == "https" && req.URL.Scheme != "https":
		return errors.New("the endpoint redirected from https to plain http, which is not followed: the key would be sent unencrypted")
	}
	return nil
}

// endpoint builds a request URL for the Anthropic and Gemini wires from a base
// that may or may not already name the API version.
//
// Both spellings are in the wild and both are what somebody would paste into
// agents.json: a gateway is given as `https://gateway.example/anthropic/v1` as
// often as without the version, while the vendors' own roots carry none at
// all. Guessing wrong is a 404 the user cannot do anything about, so the
// version is added only when the base does not already end in one. Any
// version counts, not only the default: Gemini's stable API is /v1 and its
// preview /v1alpha, and a base naming either had /v1beta put after it.
func endpoint(base, fallback, version, path string) string {
	return joinEndpoint(base, fallback, version, path, func(p string) bool {
		return apiVersionRE.MatchString(p[strings.LastIndex(p, "/")+1:])
	})
}

// apiVersionRE is a path segment that names an API version: v1, v1beta,
// v1alpha, v1beta1.
var apiVersionRE = regexp.MustCompile(`^v\d+((alpha|beta)\d*)?$`)

// openaiEndpoint builds a request URL for the OpenAI wire.
//
// The version is added only to a bare root -- `http://127.0.0.1:11434` -- and
// a base with a path of its own is taken to be the whole of the API's root
// already. The servers that speak this wire put it wherever they like:
// Cloudflare's gateway under .../openai, Gemini's under /v1beta/openai,
// Zhipu's under /api/paas/v4. Given /v1 on the end of any of those, every
// request was a 404.
func openaiEndpoint(base, path string) string {
	return joinEndpoint(base, "https://api.openai.com", "v1", path, func(p string) bool { return p != "" })
}

// joinEndpoint puts the API's path after a base's own, with the version
// between them where versioned says the base's path lacks it.
//
// The base is parsed rather than pasted onto, because a base can carry a query
// -- Azure's ?api-version= -- and the path pasted after it went into the
// query, not the path. The query of the base and the path's own are both kept.
func joinEndpoint(base, fallback, version, path string, versioned func(basePath string) bool) string {
	b := strings.TrimSpace(base)
	if b == "" {
		b = fallback
	}
	u, err := url.Parse(b)
	if err != nil || u.Host == "" {
		// Not an address that can be parsed: it is pasted onto as it stands,
		// and the request that fails with it says what is wrong with it.
		return strings.TrimRight(b, "/") + "/" + version + path
	}
	p := strings.TrimRight(u.EscapedPath(), "/")
	// The address a server's documentation gives is as often the whole
	// request URL as its root -- http://127.0.0.1:1234/v1/chat/completions --
	// and pasted as the base it had the path added a second time, which is a
	// 404 on every request. The API's own paths are taken back off first.
	for _, tail := range []string{"/chat/completions", "/messages", "/models"} {
		p = strings.TrimRight(strings.TrimSuffix(p, tail), "/")
	}
	if !versioned(p) {
		p += "/" + version
	}
	rest, query, _ := strings.Cut(path, "?")
	p += rest
	// The path arrives escaped already -- a Gemini model's name is -- so it is
	// set as the escaped form, with the plain one beside it as url.URL wants.
	if plain, err := url.PathUnescape(p); err == nil {
		u.Path, u.RawPath = plain, p
	}
	switch {
	case u.RawQuery == "":
		u.RawQuery = query
	case query != "":
		u.RawQuery += "&" + query
	}
	u.Fragment, u.RawFragment = "", ""
	return u.String()
}

// post sends one JSON body and returns the response body for streaming.
//
// A failure is turned into an error carrying whatever the vendor said in the
// body, because "400 Bad Request" alone tells the user nothing about which
// field of a request they cannot see was wrong.
func post(ctx context.Context, url string, header http.Header, body any) (io.ReadCloser, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header = header.Clone()
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		resp.Body.Close()
		e := &apiError{Code: resp.StatusCode, Status: resp.Status, Msg: apiMessage(msg),
			RetryAfter: retryAfter(resp.Header.Get("Retry-After"), time.Now())}
		return nil, e
	}
	return resp.Body, nil
}

// retryAfter is how long a Retry-After header asks to be left, or 0 where it
// says nothing that can be read. The header is a number of seconds or a date,
// and a proxy in front of an API sends the date as often as not: read only as
// seconds, a date was no request at all, and the API was asked again two
// seconds later.
func retryAfter(h string, now time.Time) time.Duration {
	h = strings.TrimSpace(h)
	if secs, err := strconv.Atoi(h); err == nil {
		if secs > 0 {
			return time.Duration(secs) * time.Second
		}
		return 0
	}
	if at, err := http.ParseTime(h); err == nil && at.After(now) {
		return at.Sub(now)
	}
	return 0
}

// apiError is a request the API answered with a failure, kept whole rather
// than flattened into a string so that the loop can tell a refused key --
// which the user can fix without leaving the pane -- or a busy API, which
// is worth asking again, from everything else.
type apiError struct {
	Code   int
	Status string
	Msg    string
	// RetryAfter is how long the API asked to be left, where it said.
	RetryAfter time.Duration
}

// Error is the failure as it is shown, with any part of a key the vendor quoted
// back taken out: every line that says why an answer or a listing failed goes
// through here.
func (e *apiError) Error() string { return e.Status + ": " + redactKeys(e.Msg) }

// outOfCredit reports whether err is the account behind the key having run out
// of what it pays with, rather than the API being busy for a moment. OpenAI
// says so with a 429, the status of a rate limit, and asking again gets the
// same answer; Anthropic says so with a 400 about the credit balance.
func outOfCredit(err error) bool {
	var e *apiError
	if !errors.As(err, &e) {
		return false
	}
	msg := strings.ToLower(e.Msg)
	return strings.Contains(msg, "exceeded your current quota") || strings.Contains(msg, "insufficient_quota") ||
		strings.Contains(msg, "credit balance is too low")
}

// busyWords is a busy API said in words, for the line that says it is being
// asked again. The status is what the server sent, and in the middle of a
// stream it is the error's own type -- "overloaded_error" -- which is a name
// for a program, not a reason for a person.
func busyWords(e *apiError) string {
	switch e.Code {
	case http.StatusTooManyRequests:
		return "the API is limiting how often this key asks"
	case 529:
		return "the API is overloaded"
	}
	return "the API is failing on its side (" + strings.TrimSpace(e.Status) + ")"
}

// streamErrorCodes are the statuses the APIs answer with, before a stream
// starts, for the errors they can also send part-way through one -- by the
// name each gives the error in the stream.
var streamErrorCodes = map[string]int{
	// Anthropic's error types.
	"overloaded_error": 529,
	"api_error":        http.StatusInternalServerError,
	"rate_limit_error": http.StatusTooManyRequests,
	// OpenAI's, which the servers that copy its wire copy too.
	"server_error":        http.StatusInternalServerError,
	"rate_limit_exceeded": http.StatusTooManyRequests,
	// Gemini's statuses, which are Google's RPC codes.
	"RESOURCE_EXHAUSTED": http.StatusTooManyRequests,
	"INTERNAL":           http.StatusInternalServerError,
	"UNAVAILABLE":        http.StatusServiceUnavailable,
	"DEADLINE_EXCEEDED":  http.StatusGatewayTimeout,
}

// streamFailure is an error the OpenAI and Gemini wires send part-way through
// a stream, as the whole of a chunk. OpenAI's has a type and a code that is a
// name, a number or null; Gemini's a numeric code and a status.
type streamFailure struct {
	Message string          `json:"message"`
	Type    string          `json:"type"`
	Status  string          `json:"status"`
	Code    json.RawMessage `json:"code"`
}

// err is the failure as an error, or nil for a chunk that carried none.
//
// An error sent in the stream is the same failure the API sends as a status
// before one starts -- overloaded, rate-limited, failing on its own side --
// and is kept in the same shape where it can be told apart, so that the loop
// asks again when nothing of the answer came. Read as words alone, a 503 in
// the stream was never asked again, where the same 503 a moment earlier was.
func (f streamFailure) err() error {
	code := strings.Trim(strings.TrimSpace(string(f.Code)), `"`)
	if code == "null" {
		code = ""
	}
	if f.Message == "" && f.Type == "" && f.Status == "" && code == "" {
		return nil
	}
	msg := firstNonEmpty(f.Message, f.Status, f.Type, code, "the stream reported an error")
	for _, name := range []string{f.Status, f.Type, code} {
		if n := streamErrorCodes[name]; n != 0 {
			return &apiError{Code: n, Status: name, Msg: msg}
		}
	}
	if n, err := strconv.Atoi(code); err == nil && n >= 400 && n <= 599 {
		return &apiError{Code: n, Status: firstNonEmpty(f.Status, strconv.Itoa(n)+" "+http.StatusText(n)), Msg: msg}
	}
	return fmt.Errorf("%s", redactKeys(msg))
}

// unreadChunk is what a chunk of a stream that is not in the wire's shape
// amounts to: an error where it is words a server wrote in place of a chunk,
// or an error in a shape of its own, and nothing where it is JSON this wire
// does not read.
//
// Passed over, the first two were lost: a proxy's "upstream request timeout"
// in the stream ended the answer as a dropped connection ends one, and an
// error with a field of an unexpected type said nothing at all. JSON that is
// not an error -- content sent as a list of parts, by a server that does --
// is no reason to end an answer that is otherwise arriving.
func unreadChunk(data string) error {
	b := []byte(data)
	if !json.Valid(b) {
		return fmt.Errorf("the endpoint sent this in place of an answer: %s", redactKeys(apiMessage(b)))
	}
	var loose struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(b, &loose) == nil && len(loose.Error) > 0 && string(loose.Error) != "null" {
		return errors.New(redactKeys(apiMessage(b)))
	}
	return nil
}

// cutOffError is an answer that stopped because it reached a limit on its
// length. What was written of it stands, and is the start of the answer rather
// than a failed one: the way on is to ask for the rest, not to ask again.
type cutOffError struct{ msg string }

func (e *cutOffError) Error() string { return e.msg }

// cutOff reports whether err is an answer cut off at its length limit.
func cutOff(err error) bool {
	var e *cutOffError
	return errors.As(err, &e)
}

// refusalError is an answer the model or a filter in front of it would not
// give. It is said the same way whichever wire it came from, because what the
// user can do about it is the same on all of them: asking the same again is
// refused the same way.
type refusalError struct{ why string }

func (e *refusalError) Error() string { return "the request was refused: " + e.why }

// refused reports whether err is a refusal to answer.
func refused(err error) bool {
	var e *refusalError
	return errors.As(err, &e)
}

// busy reports whether err is the API being too busy to answer just now --
// rate-limited, overloaded, or failing on its own side -- rather than
// refusing the request.
func busy(err error) (*apiError, bool) {
	var e *apiError
	if !errors.As(err, &e) || outOfCredit(err) {
		return nil, false
	}
	switch e.Code {
	case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout, 529:
		return e, true
	}
	return nil, false
}

// unreachable reports whether err is the endpoint not being reached at all --
// nothing listening at the address, a host that does not exist -- rather than
// answering. Asking again changes nothing until the server is started or the
// address is put right.
func unreachable(err error) bool {
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		return true
	}
	var dns *net.DNSError
	return errors.As(err, &dns)
}

// dropped reports whether err is a connection that opened and then broke while
// the answer was arriving -- reset by the far end, a proxy giving up -- rather
// than one never made, or a refusal the API sent in words.
func dropped(err error) bool {
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "read" {
		return true
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	// Over HTTP/2 a stream reset part-way, or a server going away, is said in
	// frames of the protocol's own rather than by the socket breaking, and Go
	// reports them in types it does not export: they are known by their words.
	// A refusal the API sent is its own, whatever words are in it.
	var e *apiError
	if err == nil || errors.As(err, &e) {
		return false
	}
	msg := err.Error()
	for _, s := range []string{"stream error: stream ID", "GOAWAY", "http2: client connection lost"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// contextFull reports whether err says the conversation is longer than the
// model can read. Each vendor says it in words of its own, and none of them
// says what to do, which is to start the conversation over: asking again
// sends the same conversation and is refused the same way.
func contextFull(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, s := range []string{
		"context window",             // Anthropic's stop reason, and ours for it
		"prompt is too long",         // Anthropic
		"maximum context length",     // OpenAI and the servers that copy it
		"input token count",          // Gemini
		"exceeds the context window", // gateways
	} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// modelUnknown reports whether err is the API saying it has no model by the
// name it was asked for -- a mistyped /model, or a model since retired -- in
// the words each vendor uses for it.
func modelUnknown(err error) bool {
	var e *apiError
	if !errors.As(err, &e) || (e.Code != http.StatusNotFound && e.Code != http.StatusBadRequest) {
		return false
	}
	msg := strings.ToLower(e.Msg)
	// Anthropic's 404 is the field and the value alone: "model: claude-x".
	if e.Code == http.StatusNotFound && strings.HasPrefix(msg, "model:") {
		return true
	}
	return strings.Contains(msg, "model") &&
		(strings.Contains(msg, "not found") || strings.Contains(msg, "does not exist") || strings.Contains(msg, "invalid model"))
}

// refusedKey reports whether err is the API refusing the key it was given.
// Anthropic and OpenAI say so with a 401; Gemini with a 400 whose message says
// the key is not valid. A 403 is not one: it is a key that was accepted and is
// not allowed to do what was asked, and sending somebody to set a new key over
// it would be sending them the wrong way.
func refusedKey(err error) bool {
	var e *apiError
	if !errors.As(err, &e) {
		return false
	}
	return e.Code == http.StatusUnauthorized ||
		e.Code == http.StatusBadRequest && strings.Contains(strings.ToLower(e.Msg), "api key not valid")
}

// forbidden reports whether err is the API accepting the key and refusing what
// was asked of it -- most often a model the account behind the key has no
// access to.
func forbidden(err error) bool {
	var e *apiError
	return errors.As(err, &e) && e.Code == http.StatusForbidden
}

// apiMessage digs the human half out of an error body.
//
// All three vendors nest the sentence worth reading inside an object, and the
// rest of the JSON is noise in front of it. Where it cannot be found the raw
// body is better than nothing.
func apiMessage(body []byte) string {
	var shapes struct {
		Error struct {
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &shapes) == nil {
		for _, s := range []string{shapes.Error.Message, shapes.Message, shapes.Error.Status} {
			if s = strings.TrimSpace(s); s != "" {
				return s
			}
		}
	}
	// A body of Gemini errors arrives as a one-element array.
	var list []struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &list) == nil {
		for _, e := range list {
			if s := strings.TrimSpace(e.Error.Message); s != "" {
				return s
			}
		}
	}
	if s := strings.TrimSpace(string(body)); s != "" {
		return plainMessage(s)
	}
	return "no explanation given"
}

// errorBodyMax is as much of an error body that is not JSON as is shown.
const errorBodyMax = 300

// plainMessage is what an error body that is not JSON says, cut to a few
// lines' worth.
//
// What stands in front of an API is often a proxy, and a proxy answers a
// failure with a web page: Cloudflare's 502 is kilobytes of markup, which
// filled the pane and said less than its own title. A page is shown as its
// title, or as no more than the status beside it where it has none, and any
// other body is clipped.
func plainMessage(s string) string {
	if htmlPageRE.MatchString(s) {
		if m := htmlTitleRE.FindStringSubmatch(s); m != nil {
			if t := strings.Join(strings.Fields(html.UnescapeString(m[1])), " "); t != "" {
				return clipBody(t)
			}
		}
		return "the endpoint answered with a web page rather than a message"
	}
	return clipBody(s)
}

var (
	htmlPageRE  = regexp.MustCompile(`(?i)<(!doctype\s+html|html[\s>]|head[\s>]|body[\s>])`)
	htmlTitleRE = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title`)
)

// clipBody cuts s to errorBodyMax bytes, at the start of a character.
func clipBody(s string) string {
	if len(s) <= errorBodyMax {
		return s
	}
	cut := errorBodyMax
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}

// sseMaxLine bounds one event's data. A single delta is tiny, but an error
// arriving in the stream carries a whole message, and a line cut in half is
// not JSON any more.
const sseMaxLine = 8 << 20

// readSSE reads a Server-Sent Events stream, handing each event's name and data
// to on. An empty data line, and the `[DONE]` sentinel OpenAI ends with, are
// left for the caller to recognise or ignore.
//
// bufio.Reader rather than Scanner because Scanner gives up on a line longer
// than its buffer by returning an error with no way to raise the limit after
// the fact.
func readSSE(r io.Reader, on func(event, data string) error) error {
	br := bufio.NewReaderSize(r, 64<<10)
	var event string
	var data strings.Builder
	flush := func() error {
		if data.Len() == 0 {
			event = ""
			return nil
		}
		text := data.String()
		data.Reset()
		name := event
		event = ""
		return on(name, text)
	}
	for {
		line, err := readLine(br)
		if err != nil {
			if err == io.EOF {
				return flush()
			}
			return err
		}
		switch {
		case line == "":
			if err := flush(); err != nil {
				return err
			}
		case strings.HasPrefix(line, ":"):
			// A comment, which servers send to keep the connection warm.
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(line[len("event:"):])
		case strings.HasPrefix(line, "data:"):
			// Several data lines in one event are joined with newlines, which
			// is what the specification says and what makes a pretty-printed
			// JSON body parse.
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(line[len("data:"):], " "))
		}
	}
}

// readLine returns one line without its terminator, refusing one long enough to
// be a runaway rather than a message.
//
// It gathers the line itself rather than calling ReadString, which would grow a
// buffer for as long as a server kept sending: the ceiling is the point.
func readLine(br *bufio.Reader) (string, error) {
	var b strings.Builder
	for {
		chunk, err := br.ReadSlice('\n')
		b.Write(chunk)
		switch err {
		case nil:
			return strings.TrimRight(b.String(), "\r\n"), nil
		case bufio.ErrBufferFull:
			if b.Len() > sseMaxLine {
				return "", fmt.Errorf("the stream sent a line of more than %d bytes", sseMaxLine)
			}
		case io.EOF:
			if b.Len() == 0 {
				return "", io.EOF
			}
			return strings.TrimRight(b.String(), "\r\n"), nil
		default:
			return "", err
		}
	}
}

// callBuffer accumulates a tool call whose arguments arrive in pieces, as they
// do on every wire: the model streams the JSON of its arguments a few
// characters at a time.
type callBuffer struct {
	id   string
	name string
	args strings.Builder
}

// done returns the finished call, with arguments that at least parse.
//
// No arguments at all are an empty object, which is the honest reading of "the
// model named this tool and said nothing more". Arguments that are not JSON --
// which a local model writes more often than one would like -- are an empty
// object too, since the call goes back to the API in the next request and has
// to parse there, and what was written is kept in BadArgs for the model to be
// told about: handed the empty object, a tool says a required argument is
// missing, and the model sends the same broken JSON again.
func (c *callBuffer) done() ToolCall {
	args := strings.TrimSpace(c.args.String())
	call := ToolCall{ID: c.id, Name: c.name, Args: json.RawMessage(args)}
	if args == "" || !json.Valid([]byte(args)) {
		call.Args, call.BadArgs = json.RawMessage("{}"), args
	}
	return call
}
