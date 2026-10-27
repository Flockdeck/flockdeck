package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
)

// The rewind extension. ACP has no call to go back to an earlier turn, and its
// session/fork takes no turn. Flockdeck's own adapters (claudeacp, codexacp) add
// three things, all under _meta or a method name that starts with an underscore,
// which the protocol reserves for extensions:
//
//   - initialize: agentCapabilities._meta["flockdeck.dev"] = {"rewind": 1}.
//   - session/prompt: _meta["flockdeck.dev"] = {"turn": "<token>"}. The client
//     makes the token; the adapter remembers which turn of the conversation it
//     names.
//   - _flockdeck/rewind {sessionId, beforeTurn}: drop the turn named and every
//     turn after it from the session itself.
//   - _flockdeck/forkpoint {sessionId, beforeTurn}: answer {at}, an opaque string
//     that a later session/fork of the same conversation takes as
//     _meta["flockdeck.dev"] = {"at": at}. The new session has the history up to
//     the turn before beforeTurn. Without the key the fork has all of it.
//
// An agent that does not advertise the extension is never sent any of it.
const (
	ExtKey     = "flockdeck.dev"
	ExtVersion = 1
	// MaxAt bounds the opaque fork point; an adapter's own value is short.
	MaxAt = 256
)

var (
	turnToken = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	atToken   = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,256}$`)
)

// ErrNoRewind is returned when the agent does not have the extension.
var ErrNoRewind = errors.New("acp: the agent cannot go back to an earlier turn")

// ValidTurn says s is a turn token as the client makes them (a lower-case UUID).
func ValidTurn(s string) bool { return turnToken.MatchString(s) }

// PromptTurn is PromptBlocks that names the turn with token, when the agent has
// the extension. Without it, the token is not sent.
func (c *Client) PromptTurn(ctx context.Context, sessionID, token string, blocks ...string) (string, error) {
	if token == "" || !c.Capabilities().Rewind || !ValidTurn(token) {
		return c.PromptBlocks(ctx, sessionID, blocks...)
	}
	var r struct {
		StopReason string `json:"stopReason"`
	}
	content := make([]any, 0, len(blocks))
	for _, b := range blocks {
		content = append(content, map[string]any{"type": "text", "text": b})
	}
	err := c.call(ctx, "session/prompt", map[string]any{
		"sessionId": sessionID,
		"prompt":    content,
		"_meta":     map[string]any{ExtKey: map[string]any{"turn": token}},
	}, &r)
	return r.StopReason, err
}

// Rewind drops the turn named by beforeTurn, and every turn after it, from the
// session. The session keeps its id.
func (c *Client) Rewind(ctx context.Context, sessionID, beforeTurn string) error {
	if !c.Capabilities().Rewind {
		return ErrNoRewind
	}
	if sessionID == "" || !ValidTurn(beforeTurn) {
		return errors.New("acp: rewind needs a session and a turn")
	}
	ctx, cancel := c.setup(ctx)
	defer cancel()
	return c.call(ctx, "_flockdeck/rewind", map[string]any{"sessionId": sessionID, "beforeTurn": beforeTurn}, nil)
}

// ForkPoint asks for the opaque value that makes a fork of the session start
// before the turn beforeTurn names. An empty beforeTurn means the whole
// conversation, and the value is empty.
func (c *Client) ForkPoint(ctx context.Context, sessionID, beforeTurn string) (string, error) {
	if !c.Capabilities().Rewind {
		return "", ErrNoRewind
	}
	if beforeTurn == "" {
		return "", nil
	}
	if sessionID == "" || !ValidTurn(beforeTurn) {
		return "", errors.New("acp: a fork point needs a session and a turn")
	}
	ctx, cancel := c.setup(ctx)
	defer cancel()
	var r struct {
		At string `json:"at"`
	}
	if err := c.call(ctx, "_flockdeck/forkpoint", map[string]any{"sessionId": sessionID, "beforeTurn": beforeTurn}, &r); err != nil {
		return "", err
	}
	if !atToken.MatchString(r.At) {
		return "", errors.New("acp: the agent gave a fork point that is not usable")
	}
	return r.At, nil
}

// ForkSession makes a new session from the session sourceID in cwd and returns
// it with the new id. at is a value from ForkPoint, or empty for the whole
// conversation. It returns ErrUnsupported when the agent did not say at
// initialize that it can fork, and the agent's own error when it cannot.
func (c *Client) ForkSession(ctx context.Context, sourceID, cwd string, mcpServers []MCPServer, at string) (SessionNew, error) {
	if !c.Capabilities().Fork {
		return SessionNew{}, ErrUnsupported
	}
	if sourceID == "" {
		return SessionNew{}, errors.New("acp: no session id to fork")
	}
	if !filepath.IsAbs(cwd) {
		return SessionNew{}, fmt.Errorf("acp: the session cwd %q is not absolute", cwd)
	}
	if at != "" && (!c.Capabilities().Rewind || !atToken.MatchString(at)) {
		return SessionNew{}, ErrNoRewind
	}
	if mcpServers == nil {
		mcpServers = []MCPServer{}
	}
	params := map[string]any{"sessionId": sourceID, "cwd": cwd, "mcpServers": mcpServers}
	if at != "" {
		params["_meta"] = map[string]any{ExtKey: map[string]any{"at": at}}
	}
	ctx, cancel := c.setup(ctx)
	defer cancel()
	var raw json.RawMessage
	if err := c.call(ctx, "session/fork", params, &raw); err != nil {
		return SessionNew{}, err
	}
	r := decodeSessionNew(raw)
	if r.SessionID == "" {
		return SessionNew{}, errors.New("acp: session/fork returned no sessionId")
	}
	return r, nil
}
