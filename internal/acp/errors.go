package acp

import (
	"encoding/json"
	"errors"
	"fmt"
)

// codeAuthRequired is the JSON-RPC code ACP gives to "Authentication
// required". The Claude and Codex adapters and Gemini all answered with it,
// at session/prompt, session/new and session/new, each with its own message.
const codeAuthRequired = -32000

// RPCError is an error response from the agent.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message) }

// AuthRequiredError is an RPCError the agent sent because it has no usable
// login. The caller shows it as a needs-you state and offers Methods, which
// are what the agent listed at initialize. Authenticate with one of their ids
// and try again.
type AuthRequiredError struct {
	// Method is the request that was refused: session/new, session/prompt and
	// so on.
	Method  string
	Message string
	Methods []AuthMethod
	Err     *RPCError
}

func (e *AuthRequiredError) Error() string {
	return fmt.Sprintf("authentication required (%s): %s", e.Method, e.Message)
}

func (e *AuthRequiredError) Unwrap() error { return e.Err }

// IsAuthRequired reports whether err is, or wraps, an AuthRequiredError.
func IsAuthRequired(err error) bool {
	var a *AuthRequiredError
	return errors.As(err, &a)
}

// ErrClosed is returned by calls on a client that has been closed.
var ErrClosed = errors.New("acp: client closed")

// ExitError is returned to calls that were waiting when the agent's output
// ended, which is what the agent exiting or crashing looks like. Stderr is the
// last of what the agent wrote there. It is for logs and tests: Error does not
// include it, so that it cannot reach a message shown to the user by way of an
// error's text.
type ExitError struct {
	Stderr string
}

func (e *ExitError) Error() string { return "acp: the agent closed its output" }

// UnsupportedVersionError is returned by Initialize when the agent answers with
// a protocol version this client does not speak.
type UnsupportedVersionError struct{ Version int }

func (e *UnsupportedVersionError) Error() string {
	return fmt.Sprintf("acp: the agent speaks protocol version %d, this client speaks %d", e.Version, ProtocolVersion)
}
