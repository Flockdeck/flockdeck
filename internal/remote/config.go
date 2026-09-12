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
//
// It was https://relay.flockdeck.ai in v0.2.0, and a machine enrolled then
// keeps that address in its remote.json. That is fine: the relay still answers
// desktops on its old name, and sends a browser that arrives there to this
// one.
const DefaultRelay = "https://remote.flockdeck.ai"

// oldDefaultRelay is DefaultRelay's name in v0.2.0.
const oldDefaultRelay = "https://relay.flockdeck.ai"

// SameRelay reports whether two relay addresses name the same relay, however
// each was typed: the hosted relay by either of its names is one relay, and
// telling somebody enrolled under the old name to move to the new one would
// have them unpair every device to arrive where they already are.
func SameRelay(a, b string) bool {
	canonical := func(s string) string {
		if c, err := CheckRelay(s); err == nil {
			return c
		}
		return s
	}
	return canonical(a) == canonical(b)
}

// RelayEnv overrides DefaultRelay, for pointing Flockdeck at a development or
// staging relay.
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
	// Nothing Flockdeck has ever saved names a relay off this machine
	// without TLS, since enrolling has always refused one; a file that does
	// was edited by hand, and used, it would send the token across the
	// network in the clear. Only this rule of CheckRelay's is applied here:
	// a later one about what an address looks like must not stop an
	// enrolment already made from loading.
	if u, err := url.Parse(c.Relay); err == nil && u.Scheme == "http" && !isLoopback(u.Hostname()) {
		return nil, fmt.Errorf("%s names a relay at %s without TLS, which this machine's token would cross in the clear; put https:// in its place, or delete it to start again", p, u.Host)
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

// RelayURL decides which relay is meant: the one named, with -relay or in the
// window, else the one in the environment, else the default. It is checked and
// normalised on the way out, so that everything downstream can join paths on
// to it without thinking.
func RelayURL(named string) (string, error) {
	raw, from := strings.TrimSpace(named), ""
	if raw == "" {
		raw, from = strings.TrimSpace(os.Getenv(RelayEnv)), RelayEnv
	}
	if raw == "" {
		raw, from = DefaultRelay, ""
	}
	relay, err := CheckRelay(raw)
	// An address nobody typed just now, because it was set in the
	// environment long ago, is no use refused without saying where it is.
	if err != nil && from != "" {
		return "", fmt.Errorf("%w (from %s)", err, from)
	}
	return relay, err
}

// CheckRelay insists on a relay address that can be trusted with the token.
//
// That means HTTPS. The host token and everything a paired device types cross
// this connection, and the relay being trusted is no help if the hop to it is
// not. Plain HTTP is allowed only to this machine itself, which is where a
// relay being developed or tested runs, and where there is no network for the
// traffic to cross.
func CheckRelay(raw string) (string, error) {
	// One of the relay's own codes or credentials typed where its address
	// goes would, taken below for a host name, be sent to the DNS resolver to
	// look up, in the clear, so it is refused as what it is and not repeated.
	if isRelayCode(raw) {
		return "", errors.New("that is one of the relay's codes or credentials, not its address; a relay address looks like " + DefaultRelay)
	}
	// An address typed without a scheme, as addresses mostly are, is taken
	// to be HTTPS: that is the only one a relay off this machine may use.
	addr := raw
	if !strings.Contains(addr, "://") {
		addr = "https://" + addr
	}
	u, err := url.Parse(addr)
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
	// A pairing link is the relay-looking thing most likely to be to hand, and
	// the wrong one: it is opened on a device, and a machine joins an account
	// with a code from `pair -desktop`. Being a one-time secret, it is not
	// repeated back. The window says this too, so "here" is the join code
	// wherever it is being asked for, not a flag only the command line has.
	if strings.HasSuffix(u.Path, "/pair") && strings.HasPrefix(u.Fragment, "fdp_") {
		return "", errors.New("that is a pairing link, to open on the phone or browser being paired; to add this machine to that account, run `flockdeck remote pair -desktop` on the other machine and give the code it prints as the join code here")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("a relay address has no query or fragment: %q", raw)
	}
	// A relay is reached with this machine's own token, never a password, and
	// one written into the address would be saved with it and printed back by
	// status, pair -desktop and the window. So it is refused, without being
	// repeated here.
	if u.User != nil {
		return "", fmt.Errorf("a relay address has no user or password in it; give it as %s://%s", u.Scheme, u.Host)
	}
	// One relay is one address however it was typed, so that naming the relay
	// this machine is already on is not taken for asking to move to another.
	u.Host = strings.ToLower(u.Host)
	if p := u.Port(); (u.Scheme == "https" && p == "443") || (u.Scheme == "http" && p == "80") {
		u.Host = strings.TrimSuffix(u.Host, ":"+p)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	// A machine's own address on the relay, <relay>/h/<id>, is what status
	// shows as where a paired device opens it, and so the address most to
	// hand on a machine being set up beside it. It is a page, not a relay,
	// and taken for one it would have registration posted beneath it.
	if i := strings.LastIndex(u.Path, "/h/"); i >= 0 && u.Path[i+3:] != "" && !strings.Contains(u.Path[i+3:], "/") {
		relay := u.Scheme + "://" + u.Host + u.Path[:i]
		if relay == oldDefaultRelay {
			relay = DefaultRelay
		}
		return "", fmt.Errorf("that is a machine's address on the relay, for a paired device to open; the relay's own address is %s", relay)
	}
	// The hosted relay's old name is the same relay, and a machine enrolled
	// now is given the current one, whichever was typed.
	if s := u.String(); s != oldDefaultRelay {
		return s, nil
	}
	return DefaultRelay, nil
}

// isRelayCode reports whether s is one of the relay's own codes or
// credentials, which say what they are in their prefixes: fdh_ a machine's
// credential, fdd_ a browser's session, fdp_ a pairing or join code, and
// fdi_ an invitation.
func isRelayCode(s string) bool {
	s = strings.TrimSpace(s)
	for _, prefix := range []string{"fdh_", "fdd_", "fdp_", "fdi_"} {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
