// Package apikey finds the API key stored or exported for an agent and asks
// the agent's endpoint whether it takes one.
//
// It is what was left of the removed `flockdeck chat` client that
// `flockdeck keys` and Settings still use. Nothing here starts a pane: a pane's
// agent is given its stored key by the structured runtime (creds.StoredKey),
// and `flockdeck keys check` asks CheckKey whether the endpoint takes it.
package apikey

import (
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/jmwri/flockdeck/internal/agent"
)

// KeyStore is asked for an API key when the environment does not have one. It
// is a variable so that the credential store can supply it without this
// package depending on it.
var KeyStore func(agent string) string

// Target is an agent's endpoint as far as finding its key goes.
type Target struct {
	Agent   string   // catalog id; "" when started by hand at an address
	Wire    string   // "anthropic", "openai" or "gemini"
	BaseURL string   // "" for the vendor's own
	KeyEnv  []string // the entry's own variables
}

// storeID is the id a stored key is filed under. A target pointed at somebody
// else's address with no id has none: the key stored for the built-in is the
// user's key for the vendor and is not sent to a gateway.
func storeID(t Target) string {
	if t.Agent != "" {
		return t.Agent
	}
	if !agent.VendorsOwn(agent.APISpec{Wire: t.Wire, BaseURL: t.BaseURL}) {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(t.Wire)) {
	case "openai", "openai-compatible":
		return "openai"
	case "gemini", "google":
		return "google"
	default:
		return "anthropic"
	}
}

// KeyFor is the key a pane for this target would send, and where it comes from
// in words that name the place and never the key. The key is "" with a place
// when none is needed, and both are "" when there is none to be found.
// KeyStore must be set for a stored key to count.
func KeyFor(t Target) (key, from string) {
	first, last := keyNames(t)
	if v, name := envKey(first); v != "" {
		return v, "from " + name
	}
	if id := storeID(t); KeyStore != nil && id != "" {
		if v := strings.TrimSpace(KeyStore(id)); v != "" {
			return v, "stored with `flockdeck keys set " + id + "`"
		}
	}
	if v, name := envKey(last); v != "" {
		return v, "from " + name
	}
	if isLoopback(t.BaseURL) {
		return "", "none, which a local endpoint does not need"
	}
	return "", ""
}

// keyNames are the variables a key is looked for in, split where the stored
// key is looked at: first the entry's own and the wire's conventional one,
// last Flockdeck's. The wire's conventional variable is only read where the
// target is that vendor (agent.VendorsOwn), so a key exported for OpenAI is
// not sent to a gateway.
func keyNames(t Target) (first, last []string) {
	api := agent.APISpec{Wire: t.Wire, BaseURL: t.BaseURL, KeyEnv: t.KeyEnv}
	candidates := agent.OwnKeyEnv(api)
	if agent.VendorsOwn(api) {
		candidates = append(candidates, defaultKeyEnv(t.Wire)...)
	}
	seen := map[string]bool{}
	for _, name := range candidates {
		if !seen[name] {
			seen[name] = true
			first = append(first, name)
		}
	}
	if seen[agent.FallbackKeyEnv] {
		return first, nil
	}
	return first, []string{agent.FallbackKeyEnv}
}

func envKey(names []string) (key, name string) {
	for _, name := range names {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v, name
		}
	}
	return "", ""
}

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
	if strings.EqualFold(u.Hostname(), "localhost") {
		return true
	}
	ip := net.ParseIP(u.Hostname())
	return ip != nil && ip.IsLoopback()
}
