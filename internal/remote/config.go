// Package remote connects a running Flockdeck to a relay, so the window can be
// opened from another device.
//
// The desktop never listens for anyone. It dials out to the relay, holds one
// WebSocket open, and carries a stream multiplexer over it; the relay opens a
// stream for each connection a paired browser makes, and each stream is served
// here by the very handlers the local window uses. Nothing on this machine is
// reachable from the network that was not before, and no port is opened.
//
// What the relay is, and what it may see, is described in the relay's own
// design notes: traffic is TLS on both legs and the relay decrypts it to route
// it. It is trusted, and the help says so rather than implying otherwise.
package remote

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/jmwri/flockdeck/internal/store"
)

// DefaultRelay is the relay a machine is enrolled with when nobody names one.
const DefaultRelay = "https://relay.flockdeck.ai"

// RelayEnv overrides DefaultRelay, for somebody running a relay of their own
// or developing against one.
const RelayEnv = "FLOCKDECK_RELAY"

// File is where the enrolment is kept, in the state directory.
const File = "remote.json"

// Config is this machine's enrolment with a relay.
//
// Token is the credential the relay knows this host by. It is a secret with
// the same reach as the window itself — whoever holds it can mint pairing
// codes, and a paired device can type into every agent — so it lives in a
// 0600 file in the state directory beside the local server's own token, is
// never printed, and never travels anywhere but to the relay that issued it.
type Config struct {
	Relay     string `json:"relay"`
	HostID    string `json:"hostId"`
	AccountID string `json:"accountId"`
	Token     string `json:"token"`
	Name      string `json:"name,omitempty"`
}

// path is where the enrolment is kept.
func path() (string, error) {
	dir, err := store.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, File), nil
}

// Load returns the enrolment, or nil when this machine has none.
//
// A file that is there and cannot be understood is an error rather than
// "not enabled": answering that would tell the user remote access is off while
// a relay still holds a live host for this machine, and invite them to enrol it
// a second time.
func Load() (*Config, error) {
	p, err := path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", File, err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON (%v); delete it to start again", p, err)
	}
	if c.Relay == "" || c.HostID == "" || c.Token == "" {
		return nil, fmt.Errorf("%s is missing the relay, the host or the token; delete it to start again", p)
	}
	return &c, nil
}

// Save writes the enrolment, readable by this user only.
func (c *Config) Save() error {
	p, err := path()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", File, err)
	}
	if err := store.WriteAtomic(p, data); err != nil {
		return fmt.Errorf("write %s: %w", File, err)
	}
	return nil
}

// Clear forgets the enrolment. It is not an error for there to be none.
func Clear() error {
	p, err := path()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", File, err)
	}
	return nil
}

// RelayURL decides which relay is meant: the one named on the command line,
// else the one in the environment, else the default. It is checked and
// normalised on the way out, so that everything downstream can join paths on
// to it without thinking.
func RelayURL(named string) (string, error) {
	raw := strings.TrimSpace(named)
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv(RelayEnv))
	}
	if raw == "" {
		raw = DefaultRelay
	}
	return CheckRelay(raw)
}

// CheckRelay insists on a relay address that can be trusted with the token.
//
// That means HTTPS. The host token and everything a paired device types cross
// this connection, and the relay being trusted is no help if the hop to it is
// not. Plain HTTP is allowed only to this machine itself, which is where a
// relay being developed or tested runs, and where there is no network for the
// traffic to cross.
func CheckRelay(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("%q is not a relay address; it should look like %s", raw, DefaultRelay)
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopback(u.Hostname()) {
			return "", fmt.Errorf("the relay at %s would be reached without TLS; use https://", u.Host)
		}
	default:
		return "", fmt.Errorf("%q is not a relay address; it should start https://", raw)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("a relay address has no query or fragment: %q", raw)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String(), nil
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
