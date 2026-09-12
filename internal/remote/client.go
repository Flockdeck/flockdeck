package remote

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// requestTimeout bounds one call to the relay's API. Every one of them is a
// small JSON exchange, so a relay that takes longer than this is not going to
// answer, and the person waiting on it should be told so. It is a variable so
// that a test does not have to sit through it.
var requestTimeout = 20 * time.Second

// Client makes the relay's REST calls on behalf of an enrolled host.
type Client struct {
	Relay   string
	Token   string
	Version string
}

// NewClient is a client for an enrolment.
func NewClient(c *Config, version string) *Client {
	return &Client{Relay: c.Relay, Token: c.Token, Version: version}
}

// APIError is the relay saying no, with its reason.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	switch {
	case e.Message != "":
		return "the relay said: " + e.Message
	case e.Status == http.StatusNotFound:
		// Nothing at the address knows the relay's API, which most likely
		// means it is not a relay's address at all.
		return "the relay answered 404 Not Found; is that a Flockdeck relay's address?"
	}
	return fmt.Sprintf("the relay answered %d %s", e.Status, http.StatusText(e.Status))
}

// Revoked reports whether the relay no longer accepts this host's token —
// because the host was taken off the relay by something holding that token,
// from here or elsewhere, or the relay no longer has it. Nothing else can
// remove a host: a device can remove only devices, and the relay's operator
// only makes invites. There is nothing to retry: the enrolment is spent.
//
// Only a refusal in the relay's own words counts. A 403 is also what a proxy
// or a firewall in front of the relay answers with, on a page of its own, and
// taking that for a revocation would stop the tunnel for good and have
// `remote enable` enrol the machine again over a host that is still live.
func (e *APIError) Revoked() bool {
	return e.Message != "" && (e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden)
}

// IsRevoked reports whether err is the relay refusing this host's token.
func IsRevoked(err error) bool {
	var api *APIError
	return errors.As(err, &api) && api.Revoked()
}

// RegisterRequest enrols a machine. Join puts it in the account a host-kind
// pairing code belongs to rather than a new one; Invite is what a relay that
// does not take registrations from just anyone asks for.
type RegisterRequest struct {
	Name   string `json:"name"`
	Join   string `json:"join,omitempty"`
	Invite string `json:"invite,omitempty"`
}

// Registration is what the relay hands back for a new host.
type Registration struct {
	HostID    string `json:"hostId"`
	AccountID string `json:"accountId"`
	Token     string `json:"token"`
}

// Pairing is a one-time code, and for a device the link that redeems it.
type Pairing struct {
	Code      string    `json:"code"`
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// Device is a browser paired with the account.
type Device struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Created  time.Time `json:"created"`
	LastSeen time.Time `json:"lastSeen"`
}

// Host is a desktop enrolled in the account; Self marks this one. URL is
// where a paired device opens it, when the relay says.
type Host struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Online   bool      `json:"online"`
	LastSeen time.Time `json:"lastSeen"`
	Self     bool      `json:"self"`
	URL      string    `json:"url,omitempty"`
}

// Roster is everything the account has paired and enrolled.
type Roster struct {
	Devices []Device `json:"devices"`
	Hosts   []Host   `json:"hosts"`
}

// Pairing kinds.
const (
	KindDevice = "device"
	KindHost   = "host"
)

// Register enrols this machine with a relay. It is the one call made without
// a token, because it is the one that gets one.
func Register(ctx context.Context, relay, version string, req RegisterRequest) (*Registration, error) {
	c := &Client{Relay: relay, Version: version}
	var out Registration
	if err := c.call(ctx, http.MethodPost, "/api/v1/hosts", req, &out); err != nil {
		return nil, err
	}
	if out.HostID == "" || out.Token == "" {
		return nil, errors.New("the relay accepted the registration but sent back no host or no token")
	}
	return &out, nil
}

// Pair asks for a one-time pairing code of the given kind.
func (c *Client) Pair(ctx context.Context, kind string) (*Pairing, error) {
	var out Pairing
	if err := c.call(ctx, http.MethodPost, "/api/v1/host/pairings", map[string]string{"kind": kind}, &out); err != nil {
		return nil, err
	}
	if out.Code == "" {
		return nil, errors.New("the relay sent back no pairing code")
	}
	return &out, nil
}

// Devices lists the account's devices and hosts.
func (c *Client) Devices(ctx context.Context) (*Roster, error) {
	var out Roster
	if err := c.call(ctx, http.MethodGet, "/api/v1/host/devices", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Revoke unpairs a device. Its session ends at once, including any window it
// has open.
func (c *Client) Revoke(ctx context.Context, deviceID string) error {
	// A device's id is not a secret. One of the relay's codes or credentials
	// typed in its place, a browser's session say, would travel in the
	// request's path, which a relay may log, to be answered "no such device".
	if isRelayCode(deviceID) {
		return errors.New("that is one of the relay's codes or credentials, not a device's id; `flockdeck remote devices` lists the ids")
	}
	return c.call(ctx, http.MethodDelete, "/api/v1/host/devices/"+url.PathEscape(deviceID), nil, nil)
}

// Unregister removes this host from the relay, which spends its token.
func (c *Client) Unregister(ctx context.Context) error {
	return c.call(ctx, http.MethodDelete, "/api/v1/host", nil, nil)
}

// call makes one request and decodes the answer into out, if there is one.
func (c *Client) call(ctx context.Context, method, path string, in, out any) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Relay+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if c.Version != "" {
		req.Header.Set("Flockdeck-Version", c.Version)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("reach the relay at %s: no answer within %s", c.Relay, requestTimeout)
		}
		return fmt.Errorf("reach the relay at %s: %w", c.Relay, transportError(err))
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read the relay's answer: %w", err)
	}
	if resp.StatusCode >= 300 {
		return decodeError(resp.StatusCode, data)
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("the relay's answer did not make sense (%v); is %s a Flockdeck relay?", err, c.Relay)
	}
	return nil
}

// decodeError turns a refusal into an APIError, keeping the relay's own words
// where it gave some. A page that is not the relay's JSON — a proxy's error
// page, most likely — is not repeated back whole: the status says enough.
func decodeError(status int, data []byte) error {
	var e struct {
		Error string `json:"error"`
	}
	msg := ""
	if json.Unmarshal(data, &e) == nil {
		msg = strings.TrimSpace(e.Error)
	}
	return &APIError{Status: status, Message: msg}
}

// transportError says why a relay could not be reached. It drops the
// "Get \"https://…\":" prefix net/http puts on every transport error, since
// the caller has already said which relay it was, and the same URL twice in
// one line is the part people stop reading at. Two failures are said in
// words rather than the library's: a certificate this machine does not
// trust, which retrying will not mend, with the verifier's reason after it;
// and a name that cannot be looked up, which is how no network looks.
func transportError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	var cert *tls.CertificateVerificationError
	if errors.As(err, &cert) {
		return fmt.Errorf("its certificate is not one this machine trusts (%v)", cert.Err)
	}
	// A name that cannot be looked up is the commonest failure of all — no
	// network, most often, or a mistyped address — and the resolver's own
	// account of it is no help: on Windows it is "getaddrinfow: The
	// requested name is valid, but no data of the requested type was found".
	var dns *net.DNSError
	switch {
	case errors.As(err, &dns) && dns.IsNotFound:
		return fmt.Errorf("%s could not be found; check the address, and that this machine is online", dns.Name)
	case errors.As(err, &dns) && dns.IsTimeout:
		return fmt.Errorf("looking up %s took too long; check that this machine is online", dns.Name)
	}
	return err
}
