package chat

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"
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
	return "", fmt.Errorf("%s; %s", noKey(o), then)
}

// noKey says that there is no key, and where one goes.
func noKey(o Options) string {
	return fmt.Sprintf("no API key for %s: set %s, or run `flockdeck keys set %s`",
		firstNonEmpty(o.Agent, o.Wire, "this agent"), strings.Join(keyNames(o), " or "), keyAgent(o))
}

// keyPoll is how often a chat waiting for a key looks for one.
var keyPoll = 2 * time.Second

// waitForKey waits for a key to be stored, having said how to store one.
//
// Only the store is worth waiting on: a variable exported now reaches the
// processes started after it, not this one. The key comes back without being
// shown, and where it came from is said instead.
func waitForKey(ctx context.Context, o Options) (string, error) {
	fmt.Fprintf(o.Out, "%s in any terminal; this waits for it (Ctrl+C leaves)\n", noKey(o))
	// Waiting on the user is what turns a pane amber, the same as a tool's
	// question does: of a dozen panes, this is the one they have to look at.
	newReporter(o.API, o.Token, o.Session, o.Cwd).notification("")
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(keyPoll):
		}
		if key, from := lookupKey(o); key != "" {
			fmt.Fprintf(o.Out, "(found a key %s; carrying on)\n", from)
			return key, nil
		}
	}
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
	if s.refused == nil {
		s.refused = map[string]bool{}
	}
	s.refused[s.key] = true
	// The key found the usual way first, then the stored one. A pane is handed
	// its stored key in its environment when it starts, and the environment is
	// looked at first, so the usual way goes on finding that key -- the one
	// just refused -- however many times another is set: a key set since is
	// in the store and only there. A key refused once is not tried again, so
	// that the two cannot take turns being refused.
	type candidate struct{ key, from string }
	var candidates []candidate
	if key, from := lookupKey(s.opts); key != "" {
		candidates = append(candidates, candidate{key, from})
	}
	if KeyStore != nil {
		agent := keyAgent(s.opts)
		candidates = append(candidates, candidate{strings.TrimSpace(KeyStore(agent)), "stored with `flockdeck keys set " + agent + "`"})
	}
	for _, c := range candidates {
		if c.key == "" || s.refused[c.key] {
			continue
		}
		wire, err := NewWire(s.opts.Wire, s.opts.BaseURL, c.key)
		if err != nil {
			return false
		}
		s.wire, s.key, s.keyFrom = wire, c.key, c.from
		return true
	}
	return false
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
