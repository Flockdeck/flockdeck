package chat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
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
var httpClient = &http.Client{}

// endpoint builds a request URL from a base that may or may not already name
// the API version.
//
// Both spellings are in the wild and both are what somebody would paste into
// agents.json: OpenAI-compatible servers are usually given as
// `http://127.0.0.1:11434/v1`, while the vendors' own roots carry no version
// at all. Guessing wrong is a 404 the user cannot do anything about, so the
// version is added only when it is not already there.
func endpoint(base, fallback, version, path string) string {
	b := strings.TrimRight(strings.TrimSpace(base), "/")
	if b == "" {
		b = strings.TrimRight(fallback, "/")
	}
	if !strings.HasSuffix(b, "/"+version) {
		b += "/" + version
	}
	return b + path
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
		e := &apiError{Code: resp.StatusCode, Status: resp.Status, Msg: apiMessage(msg)}
		if secs, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && secs > 0 {
			e.RetryAfter = time.Duration(secs) * time.Second
		}
		return nil, e
	}
	return resp.Body, nil
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

func (e *apiError) Error() string { return e.Status + ": " + e.Msg }

// busy reports whether err is the API being too busy to answer just now --
// rate-limited, overloaded, or failing on its own side -- rather than
// refusing the request.
func busy(err error) (*apiError, bool) {
	var e *apiError
	if !errors.As(err, &e) {
		return nil, false
	}
	switch e.Code {
	case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout, 529:
		return e, true
	}
	return nil, false
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

// refusedKey reports whether err is the API refusing the key it was given.
func refusedKey(err error) bool {
	var e *apiError
	return errors.As(err, &e) && (e.Code == http.StatusUnauthorized || e.Code == http.StatusForbidden)
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
		return s
	}
	return "no explanation given"
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
// A stream cut off mid-argument leaves a fragment, and handing that to a tool
// would have it fail on JSON rather than on anything the user did; an empty
// object is the honest reading of "the model named this tool and said nothing
// more".
func (c *callBuffer) done() ToolCall {
	args := strings.TrimSpace(c.args.String())
	if args == "" || !json.Valid([]byte(args)) {
		args = "{}"
	}
	return ToolCall{ID: c.id, Name: c.name, Args: json.RawMessage(args)}
}
