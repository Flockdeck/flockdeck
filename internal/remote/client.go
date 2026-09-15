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
	"syscall"
	"time"
)

// requestTimeout bounds one call to the relay's API. Every one of them is a
// small JSON exchange, so a relay that takes longer than this is not going to
// answer, and the person waiting on it should be told so. It is a variable so
// that a test does not have to sit through it.
var requestTimeout = 20 * time.Second

// relayHTTP is how the relay is called, its API and its tunnel alike, and it
// follows no redirect. The relay never answers a desktop with one — it
// answers on any of its names — and Go carries the Authorization header
// across a redirect to the same host whatever the scheme, so following one
// to http:// would send this machine's token in the clear.
var relayHTTP = &http.Client{
	CheckRedirect: func(req *http.Request, _ []*http.Request) error {
		return fmt.Errorf("it answered with a redirect to %s://%s, which is not followed with this machine's token", req.URL.Scheme, req.URL.Host)
	},
}

// probeHTTP is how Probe asks, following no redirect either: a relay answers
// on its own address, so one that redirects was given some other address,
// perhaps the one it redirects to. No token goes with a probe, so that, and
// not the token, is what its refusal says.
var probeHTTP = &http.Client{
	CheckRedirect: func(req *http.Request, _ []*http.Request) error {
		return fmt.Errorf("it answered with a redirect to %s://%s; if that is the relay's address, give that instead", req.URL.Scheme, req.URL.Host)
	},
}

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
// because the host was taken off the relay, by something holding that token
// or by another member of its account, a paired device or another of its
// machines, or the relay no longer has it. There is nothing to retry: the
// enrolment is spent.
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

// hostGoneMessage is what the relay answers a token whose host it no longer
// has any record of at all -- which it says the same way whether the host
// went for having never connected, gone unheard from for 30 days, or been
// removed from another device: nothing is kept to tell those apart. It is
// mirrored here only so RevokedReason does not repeat it in parentheses when
// it would add nothing past its own sentence.
const hostGoneMessage = "this desktop is no longer registered with the relay"

// RevokedReason explains, in one plain sentence, what err (which must satisfy
// IsRevoked) means: this machine is no longer paired, and the two most likely
// reasons why. When the relay said more than the boilerplate refusal above --
// because it still had the tunnel open, and so knew, or the token was refused
// with a reason of its own -- that is passed on too.
func RevokedReason(err error) string {
	reason := "this machine is no longer paired with the relay — it may have been removed after 30 days offline, or removed from another device"
	var api *APIError
	if errors.As(err, &api) && api.Message != "" && api.Message != hostGoneMessage {
		reason += " (" + api.Message + ")"
	}
	return reason
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
// ExpiresAt is by this machine's clock, however far the relay's is from it.
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
	// Plan is the account's plan, from a relay that asks for a subscription;
	// any other relay sends none.
	Plan *Plan `json:"plan,omitempty"`
}

// Plan is an account's plan as its relay reports it. Flockdeck shows it and
// decides nothing by it: whether remote access works is the relay's to say,
// and it says so by refusing the tunnel.
type Plan struct {
	// Plan is trial, subscribed, or lapsed once either has run out.
	Plan string `json:"plan"`
	// Name is what the relay calls it.
	Name   string `json:"name"`
	Active bool   `json:"active"`
	// Ends is when the trial or subscription runs out, or ran out.
	Ends time.Time `json:"ends,omitzero"`
	// Was is, for a lapsed account, the plan that ran out.
	Was string `json:"was,omitempty"`
	// DeleteAt is when a lapsed account is deleted unless it subscribes.
	DeleteAt time.Time `json:"deleteAt,omitzero"`
	// Message is, for a lapsed account, the relay's own words about it.
	Message string `json:"message,omitempty"`
}

// Pairing kinds.
const (
	KindDevice = "device"
	KindHost   = "host"
)

// Register enrols this machine with a relay. It is made without a token,
// because it is the call that gets one; Probe, the only other made without,
// asks no more than whether a relay is there.
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

// Probe asks the relay at relay whether it is there, without a token. Moving
// a machine is leaving one relay and then enrolling with another, and a
// mistyped or unreachable address is best found out before the first step,
// while the machine still has a relay to be on.
func Probe(ctx context.Context, relay string) error {
	// The address may come as typed, into the dialog's field say, and is
	// checked as every relay address here is: given a scheme and no trailing
	// slash, and refused, without being sent anywhere, when it is one of the
	// relay's codes, which asked after as a host would go to the resolver.
	relay, err := CheckRelay(relay)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, relay+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := probeHTTP.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("no answer within %s", requestTimeout)
		}
		return transportError(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("it answered %s, which is not what a Flockdeck relay answers", resp.Status)
	}
	return nil
}

// Pair asks for a one-time pairing code of the given kind.
func (c *Client) Pair(ctx context.Context, kind string) (*Pairing, error) {
	var out Pairing
	var relayNow time.Time
	if err := c.call(ctx, http.MethodPost, "/api/v1/host/pairings", map[string]string{"kind": kind}, &out, relayDate(&relayNow)); err != nil {
		return nil, err
	}
	if out.Code == "" {
		return nil, errors.New("the relay sent back no pairing code")
	}
	// The expiry is by the relay's clock, and this machine's may not agree —
	// a dual-booted PC's can be an hour out until it next syncs — so it is
	// given back by this machine's, as long after its now as it is after the
	// relay's.
	if !relayNow.IsZero() {
		out.ExpiresAt = byHere(out.ExpiresAt, time.Since(relayNow))
	}
	return &out, nil
}

// Devices lists the account's devices and hosts.
func (c *Client) Devices(ctx context.Context) (*Roster, error) {
	var out Roster
	var relayNow time.Time
	if err := c.call(ctx, http.MethodGet, "/api/v1/host/devices", nil, &out, relayDate(&relayNow)); err != nil {
		return nil, err
	}
	// The times are by the relay's clock, as a pairing code's expiry is, and
	// are given back by this machine's for the same reason: "last seen just
	// now" is to be said of a device the relay saw just now.
	if !relayNow.IsZero() {
		shift := time.Since(relayNow)
		for i := range out.Devices {
			out.Devices[i].Created = byHere(out.Devices[i].Created, shift)
			out.Devices[i].LastSeen = byHere(out.Devices[i].LastSeen, shift)
		}
		for i := range out.Hosts {
			out.Hosts[i].LastSeen = byHere(out.Hosts[i].LastSeen, shift)
		}
		if p := out.Plan; p != nil {
			p.Ends, p.DeleteAt = byHere(p.Ends, shift), byHere(p.DeleteAt, shift)
		}
	}
	return &out, nil
}

// relayDate is a look at an answer's headers that keeps the relay's now,
// from its Date header, which Go's server puts on every answer.
func relayDate(now *time.Time) func(http.Header) {
	return func(h http.Header) { *now, _ = http.ParseTime(h.Get("Date")) }
}

// byHere is a time by the relay's clock given by this machine's, shift being
// how far this machine's is ahead. A time that was never set stays unset.
func byHere(t time.Time, shift time.Duration) time.Time {
	if t.IsZero() {
		return t
	}
	return t.Add(shift)
}

// Revoke unpairs a device. Its session ends at once, including any window it
// has open.
func (c *Client) Revoke(ctx context.Context, deviceID string) error {
	if isRelayCode(deviceID) {
		return errCodeForDevice
	}
	return c.call(ctx, http.MethodDelete, "/api/v1/host/devices/"+url.PathEscape(deviceID), nil, nil)
}

// errCodeForDevice refuses one of the relay's codes or credentials, a
// browser's session say, typed where a device's id goes. An id is not a
// secret, and goes in the request's path, which a relay may log, to be
// answered "no such device".
var errCodeForDevice = errors.New("that is one of the relay's codes or credentials, not a device's id; `flockdeck remote devices` lists the ids")

// RenameHost gives this machine a new name, which is what every paired device
// and the account's other machines list it as. Rename, which also saves it in
// the enrolment, is what the command line and the window use.
func (c *Client) RenameHost(ctx context.Context, name string) error {
	return c.call(ctx, http.MethodPatch, "/api/v1/host", map[string]string{"name": name}, nil)
}

// RenameDevice gives one of the account's paired devices a new name.
func (c *Client) RenameDevice(ctx context.Context, deviceID, name string) error {
	if isRelayCode(deviceID) {
		return errCodeForDevice
	}
	return c.call(ctx, http.MethodPatch, "/api/v1/host/devices/"+url.PathEscape(deviceID), map[string]string{"name": name}, nil)
}

// Unregister removes this host from the relay, which spends its token.
func (c *Client) Unregister(ctx context.Context) error {
	return c.call(ctx, http.MethodDelete, "/api/v1/host", nil, nil)
}

// call makes one request and decodes the answer into out, if there is one.
// seen, if given, is shown a successful answer's headers.
func (c *Client) call(ctx context.Context, method, path string, in, out any, seen ...func(http.Header)) error {
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

	resp, err := relayHTTP.Do(req)
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
	for _, f := range seen {
		f(resp.Header)
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
// one line is the part people stop reading at. Four failures are said in
// words rather than the library's: a certificate this machine does not
// trust, which retrying will not mend, with the verifier's reason after it;
// a name that cannot be looked up, which is how no network looks; a
// connection refused, which is nothing listening at the address; and an
// answer in plain HTTP to a relay reached as https://.
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
	case refused(err):
		// On Windows the system's own words are "connectex: No connection
		// could be made because the target machine actively refused it."
		return errors.New("nothing is answering there (the connection was refused); check the address, and that the relay is running")
	case errors.Is(err, http.ErrSchemeMismatch):
		// An address typed without a scheme is taken as https://, and a
		// relay of one's own may be serving plain HTTP on the port given.
		return errors.New("it answers in plain HTTP, not HTTPS; a relay off this machine has to be reached over TLS, and one on this machine can be given as http://")
	}
	return err
}

// refused reports whether err is a connection refused: nothing listening at
// the address. syscall.ECONNREFUSED is Unix's; on Windows the socket error is
// WSAECONNREFUSED, 10061, which Go neither is nor relates to it.
func refused(err error) bool {
	var errno syscall.Errno
	return errors.As(err, &errno) && (errno == syscall.ECONNREFUSED || errno == 10061)
}
