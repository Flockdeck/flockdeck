package chat

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

// KeyStore is asked for an API key when the environment does not have one. It
// is a variable so that the credential store can supply it without the chat
// client depending on it, and so that a key reaches exactly one process: this
// one.
var KeyStore func(agent string) string

// resolveKey finds the API key for this agent: the environment first, under the
// names the agent's spec gives and then the conventional ones, and the
// credential store after that.
//
// A key never appears in an error message, only the name of the place it was
// looked for, because an error is the one string in a program that gets pasted
// into a bug report.
func resolveKey(o Options) (string, error) {
	// The spec usually names the conventional variable itself, and an error
	// that tells somebody to set X or X is one they read twice.
	var names []string
	seen := map[string]bool{}
	for _, name := range append(append(append([]string{}, o.KeyEnv...), defaultKeyEnv(o.Wire)...), "FLOCKDECK_API_KEY") {
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	for _, name := range names {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v, nil
		}
	}
	if KeyStore != nil {
		if v := strings.TrimSpace(KeyStore(o.Agent)); v != "" {
			return v, nil
		}
	}
	// An endpoint on this machine is usually a local model, which wants no key
	// at all; refusing to start would be refusing over nothing.
	if isLoopback(o.BaseURL) {
		return "", nil
	}
	// The chat cannot start without one, so the error ends it -- and a pane
	// that has ended does not pick a key up later: it has to be started again,
	// which is the step people miss.
	then := "then run this again"
	if o.API != "" {
		then = "then restart this pane (Restart pane, in the command palette)"
	}
	return "", fmt.Errorf("no API key for %s: set %s, or run `flockdeck keys set %s`; %s",
		firstNonEmpty(o.Agent, o.Wire, "this agent"), strings.Join(names, " or "),
		firstNonEmpty(o.Agent, o.Wire, "<agent>"), then)
}

// rekey looks for the key again and, where a different one is set now than the
// wire was built with, builds the wire again with it. It reports whether it did.
func (s *session) rekey() bool {
	if s.opts.wire != nil {
		return false
	}
	key, err := resolveKey(s.opts)
	if err != nil || key == s.key {
		return false
	}
	wire, err := NewWire(s.opts.Wire, s.opts.BaseURL, key)
	if err != nil {
		return false
	}
	s.wire, s.key = wire, key
	return true
}

// defaultKeyEnv is the conventional variable for a wire, used when the agent's
// spec names none.
func defaultKeyEnv(wire string) []string {
	switch strings.ToLower(strings.TrimSpace(wire)) {
	case "openai", "openai-compatible":
		return []string{"OPENAI_API_KEY"}
	case "gemini", "google":
		return []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"}
	default:
		return []string{"ANTHROPIC_API_KEY"}
	}
}

// isLoopback reports whether a base URL points at this machine.
func isLoopback(base string) bool {
	if base == "" {
		return false
	}
	u, err := url.Parse(base)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
