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
	key, from := lookupKey(o)
	if key != "" || from != "" {
		return key, nil
	}
	// The chat cannot start without one, so the error ends it -- and a pane
	// that has ended does not pick a key up later: it has to be started again,
	// which is the step people miss.
	then := "then run this again"
	if o.API != "" {
		then = "then restart this pane (Restart pane, in the command palette)"
	}
	return "", fmt.Errorf("no API key for %s: set %s, or run `flockdeck keys set %s`; %s",
		firstNonEmpty(o.Agent, o.Wire, "this agent"), strings.Join(keyNames(o), " or "),
		keyAgent(o), then)
}

// keyAgent is the agent whose stored key is looked for: the one the chat was
// started as, or -- started by hand with only a wire named -- the built-in
// agent that speaks that wire, which is the id `flockdeck keys set` would have
// been given. Looked for under no id at all, a stored key was never found,
// and the error said to run the very command that had stored it.
func keyAgent(o Options) string {
	if o.Agent != "" {
		return o.Agent
	}
	switch strings.ToLower(strings.TrimSpace(o.Wire)) {
	case "openai", "openai-compatible":
		return "openai"
	case "gemini", "google":
		return "google"
	default:
		return "anthropic"
	}
}

// lookupKey finds the key and says where it came from, in words that name the
// place and never the key. A key is "" with a place when none is needed.
//
// It is one lookup for starting the chat and for /status, so that what /status
// says is where the key the chat is using came from.
func lookupKey(o Options) (key, from string) {
	for _, name := range keyNames(o) {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v, "from " + name
		}
	}
	if KeyStore != nil {
		if v := strings.TrimSpace(KeyStore(keyAgent(o))); v != "" {
			return v, "stored with `flockdeck keys set " + keyAgent(o) + "`"
		}
	}
	// An endpoint on this machine is usually a local model, which wants no key
	// at all; refusing to start would be refusing over nothing.
	if isLoopback(o.BaseURL) {
		return "", "none, which a local endpoint does not need"
	}
	return "", ""
}

// keyNames are the environment variables a key is looked for in, in order: the
// agent's own, the wire's conventional one, and Flockdeck's. The spec usually
// names the conventional variable itself, and an error that tells somebody to
// set X or X is one they read twice.
func keyNames(o Options) []string {
	var names []string
	seen := map[string]bool{}
	for _, name := range append(append(append([]string{}, o.KeyEnv...), defaultKeyEnv(o.Wire)...), "FLOCKDECK_API_KEY") {
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}

// endpointOf is the address the chat talks to, as /status names it.
func endpointOf(o Options) string {
	if o.BaseURL != "" {
		return o.BaseURL
	}
	switch strings.ToLower(strings.TrimSpace(o.Wire)) {
	case "openai", "openai-compatible":
		return "https://api.openai.com (the vendor's own)"
	case "gemini", "google":
		return "https://generativelanguage.googleapis.com (the vendor's own)"
	default:
		return "https://api.anthropic.com (the vendor's own)"
	}
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
