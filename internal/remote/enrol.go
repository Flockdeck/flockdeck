package remote

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode"
)

// Enrolling this machine and taking it off again, for the command line and the
// window alike. Both change the enrolment on disk; a running instance brings
// its tunnel in line with it through Reload, which Manager.Enable and
// Manager.Disable do on the window's behalf.

// EnableRequest is what enrolling takes. Every field may be left empty: the
// relay is then the one RelayURL settles on, and the name the host name.
type EnableRequest struct {
	Relay  string
	Name   string
	Join   string
	Invite string
	// OnVerify is sent VerifyEmail's events, on a relay that requires a
	// verified email before a new account may be created. A relay that does
	// not require one, and Join, which needs no verification of its own,
	// never send it anything. Left nil on a relay that does require one,
	// Enable and Move fail fast with ErrNeedsInteractiveVerify rather than
	// wait with nobody able to show the URL to open. The command line always
	// gives one; a caller with no way yet to show a URL and wait -- today,
	// the window's own Flockdeck Remote… dialog -- leaves it nil and gets that
	// error instead.
	OnVerify func(VerifyEvent)
	// OnJoined is called once, after registering, when the relay says the
	// verified email already had an account and this machine was added to it,
	// with what the relay said about that account. A relay that makes a new
	// account, or none that says anything, never calls it.
	OnJoined func(Registration)
}

// AlreadyEnabledError refuses to enrol over an enrolment the relay may still
// hold. Enrolling a second time would leave the first host on the relay with
// nobody holding its token, listed on every device as a machine that is never
// online. Err is why the relay could not be asked whether it still holds it;
// nil means it answered that it does.
type AlreadyEnabledError struct {
	Relay string
	Err   error
}

func (e *AlreadyEnabledError) Error() string {
	if e.Err == nil {
		return "Flockdeck Remote is already enabled, with " + e.Relay
	}
	return fmt.Sprintf("Flockdeck Remote is already enabled with %s, which could not be asked whether it still is (%v)", e.Relay, e.Err)
}

func (e *AlreadyEnabledError) Unwrap() error { return e.Err }

// RelayUntoldError is taking this machine off a relay that could not be told,
// without being asked to forget the enrolment regardless. Forgetting it here
// would leave the machine listed on the relay, offline, until somebody removes
// it from a paired device.
type RelayUntoldError struct{ Err error }

func (e *RelayUntoldError) Error() string {
	return fmt.Sprintf("could not tell the relay (%v)", e.Err)
}

func (e *RelayUntoldError) Unwrap() error { return e.Err }

// Enable enrols this machine with a relay and saves the enrolment.
//
// An enrolment already here is an *AlreadyEnabledError, except when its relay
// has forgotten it: that is exactly when enrolling again is right, and replaced
// says it happened.
//
// An enrolment that Disable turned off is turned on again, and that is all:
// it is the same host on the same account, with the same token and
// end-to-end key, so nothing is registered and no email is verified. A
// request that asks for something else of it (a join code, an invitation,
// another relay or another name) is refused rather than quietly ignored, and
// points at Remove, which leaves the account, and Move. If the relay has
// forgotten the host in the meantime, though, the enrolment is dead, and
// enrolling afresh is right as it is for an enabled one.
func Enable(ctx context.Context, version string, req EnableRequest) (cfg *Config, replaced bool, err error) {
	if err := checkCodes(req); err != nil {
		return nil, false, err
	}
	relay, err := RelayURL(req.Relay)
	if err != nil {
		return nil, false, err
	}
	existing, err := Load()
	if err != nil {
		return nil, false, err
	}
	if existing != nil {
		if existing.Disabled {
			if _, err := NewClient(existing, version).Devices(ctx); !IsRevoked(err) {
				if err := resumable(existing, req); err != nil {
					return nil, false, err
				}
				existing.Disabled = false
				if err := saveConfig(existing); err != nil {
					return nil, false, err
				}
				return existing, false, nil
			}
			replaced = true
			relay = againRelay(req, relay, existing)
			existing = nil
		}
	}
	if existing != nil {
		if _, err := NewClient(existing, version).Devices(ctx); !IsRevoked(err) {
			return nil, false, &AlreadyEnabledError{Relay: existing.Relay, Err: err}
		}
		replaced = true
		relay = againRelay(req, relay, existing)
	}

	name := enrolName(req.Name, "")
	// Joining an already-registered account needs no verification of its
	// own: that account was already verified when its first machine
	// registered (see magiclink.go's own comment on this, relay-side).
	code := ""
	if strings.TrimSpace(req.Join) == "" {
		if code, err = VerifyEmail(ctx, relay, version, req.OnVerify); err != nil {
			return nil, false, err
		}
	}
	reg, err := Register(ctx, relay, version, RegisterRequest{
		Name: name, Join: strings.TrimSpace(req.Join), Invite: strings.TrimSpace(req.Invite),
		VerificationCode: code,
	})
	if err != nil {
		return nil, false, err
	}
	if reg.Joined && req.OnJoined != nil {
		req.OnJoined(*reg)
	}
	cfg = &Config{Relay: relay, HostID: reg.HostID, AccountID: reg.AccountID, Token: reg.Token, Name: name}
	if err := saveConfig(cfg); err != nil {
		// The relay now has a host that nothing here can speak for. Take it
		// back off rather than leave it listed on every device for good.
		//
		// Not on ctx alone: the window gives the whole of an Enable one
		// budget, and a relay slow to register can leave none of it for
		// this, when the request failed at once and the host stayed listed.
		// The call still has its own requestTimeout.
		_ = NewClient(cfg, version).Unregister(context.WithoutCancel(ctx))
		return nil, false, err
	}
	return cfg, replaced, nil
}

// againRelay is the relay to enrol afresh with, when the relay has forgotten an
// enrolment. A request that names no relay enrols with the relay the old
// enrolment was on, not the default one: somebody on a company's relay must not
// quietly end up registered on the public one. settled is what RelayURL made of
// the request.
func againRelay(req EnableRequest, settled string, old *Config) string {
	if strings.TrimSpace(req.Relay) == "" {
		return old.Relay
	}
	return settled
}

// DisabledEnrolmentError is what turning remote access on again with options
// that belong to a different enrolment is told. The enrolment that is kept is
// for one host on one account on one relay, and none of these change it.
type DisabledEnrolmentError struct{ Relay, What string }

func (e *DisabledEnrolmentError) Error() string {
	return fmt.Sprintf("Flockdeck Remote is turned off here but this machine is still enrolled with %s, and %s would not change that; turn it on without options to use the same account again, run `flockdeck remote remove` first to leave the account and enrol afresh, or `flockdeck remote move` to go to another relay", e.Relay, e.What)
}

// resumable refuses a request to turn a kept enrolment back on that asks for
// something other than turning it back on.
func resumable(existing *Config, req EnableRequest) error {
	switch {
	case strings.TrimSpace(req.Join) != "":
		return &DisabledEnrolmentError{Relay: existing.Relay, What: "a join code"}
	case strings.TrimSpace(req.Invite) != "":
		return &DisabledEnrolmentError{Relay: existing.Relay, What: "an invitation code"}
	case strings.TrimSpace(req.Relay) != "" && !SameRelay(req.Relay, existing.Relay):
		return &DisabledEnrolmentError{Relay: existing.Relay, What: "naming another relay"}
	case cleanName(req.Name) != "" && cleanName(req.Name) != existing.Name:
		return &DisabledEnrolmentError{Relay: existing.Relay, What: "a different name (`flockdeck remote rename` changes it)"}
	}
	return nil
}

// checkCodes refuses, before anything reaches a relay, a request whose codes
// or name are plainly something else: a pairing link, one code given for the
// other, a credential, or a code given as the name.
func checkCodes(req EnableRequest) error {
	// A pairing link is for a phone or a browser, and given as a join code it
	// would reach the relay only to be refused in words that do not say why.
	if strings.Contains(req.Join, "/pair#") {
		return errors.New("that is a pairing link, for a phone or browser to open; a machine joins another's account with a code made for that, which `flockdeck remote pair -desktop` prints")
	}
	// The relay's codes say what they are in their prefixes (fdp_ a join or
	// pairing code, fdi_ an invitation), so one given for the other is told
	// so here, rather than by a relay that answers as if none had been given.
	// The window shows these as well as the command line, so they name the
	// code, which both ask for by that name, and not a flag only one has.
	if strings.HasPrefix(strings.TrimSpace(req.Join), "fdi_") {
		return errors.New("that is an invitation, not a join code; give it as the invitation code instead")
	}
	if strings.HasPrefix(strings.TrimSpace(req.Invite), "fdp_") {
		return errors.New("that is a join code, not an invitation; give it as the join code instead")
	}
	// A machine's own credential (fdh_, as remote.json holds) or a browser's
	// session (fdd_) is a secret, not a code for joining or registering, and
	// somebody copying one across has a code made for the purpose to use.
	for _, code := range []string{req.Join, req.Invite} {
		if c := strings.TrimSpace(code); strings.HasPrefix(c, "fdh_") || strings.HasPrefix(c, "fdd_") {
			return errors.New("that is a credential, not a code to join or register with, and it is not for passing on; a machine joins another's account with a code `flockdeck remote pair -desktop` prints")
		}
	}
	// A code given as the name would be registered as it, and shown on every
	// paired device as what this machine is called.
	if isRelayCode(req.Name) {
		return errors.New("that is one of the relay's codes, not a name; the name is what this machine is called on your devices")
	}
	return nil
}

// enrolName is the name a machine enrols under: the one given, else the one
// it already goes by, else its host name, as the relay will keep it.
func enrolName(given, current string) string {
	name := cleanName(given)
	if name == "" {
		name = cleanName(current)
	}
	if name == "" {
		host, _ := hostname()
		name = cleanName(hostName(host))
	}
	if name == "" {
		// What the relay calls a machine that gave it no name, which is then
		// what every device lists; saved blank, status and the window would
		// show nothing where the devices show that.
		name = "Desktop"
	}
	return name
}

// ErrSameRelay is a move to the relay this machine is already on.
var ErrSameRelay = errors.New("this machine is already on that relay, so there is nowhere to move it")

// Move enrols this machine with another relay and then takes it off the one
// it is on: what a company moving its desktops off the shared relay, or off
// an old relay of its own, does.
//
// The order is the point. The machine is enrolled with the new relay first,
// and the new relay is asked, with the new credential, to answer for it; only
// then is the enrolment here replaced and the old relay told. A new relay
// that refuses, needs an invitation not given, or is not there leaves the
// machine where it was, still reachable by every device paired with it.
//
// Every device has to pair again afterwards, with the new relay: a device's
// session is a cookie on the relay's own origin, which no other relay can
// read. The caller says so before the move, since it cannot be undone by
// moving back.
//
// switched, if given, is called once the new enrolment is saved and before
// the old relay is told, for a running instance to move its tunnel across
// then, rather than see it cut by the old relay first.
//
// untold is why the old relay could not be told. The move is done then, and
// the old relay goes on listing this machine, offline, until a device paired
// there removes it.
func Move(ctx context.Context, version string, req EnableRequest, switched func()) (cfg *Config, untold error, err error) {
	if strings.TrimSpace(req.Relay) == "" {
		return nil, nil, errors.New("name the relay to move this machine to")
	}
	if err := checkCodes(req); err != nil {
		return nil, nil, err
	}
	relay, err := CheckRelay(strings.TrimSpace(req.Relay))
	if err != nil {
		return nil, nil, err
	}
	old, err := Load()
	if err != nil {
		return nil, nil, err
	}
	if old == nil {
		return nil, nil, ErrNotEnabled
	}
	if SameRelay(relay, old.Relay) {
		return nil, nil, ErrSameRelay
	}

	name := enrolName(req.Name, old.Name)
	code := ""
	if strings.TrimSpace(req.Join) == "" {
		if code, err = VerifyEmail(ctx, relay, version, req.OnVerify); err != nil {
			return nil, nil, err
		}
	}
	reg, err := Register(ctx, relay, version, RegisterRequest{
		Name: name, Join: strings.TrimSpace(req.Join), Invite: strings.TrimSpace(req.Invite),
		VerificationCode: code,
	})
	if err != nil {
		return nil, nil, err
	}
	if reg.Joined && req.OnJoined != nil {
		req.OnJoined(*reg)
	}
	next := &Config{Relay: relay, HostID: reg.HostID, AccountID: reg.AccountID, Token: reg.Token, Name: name}
	// Registering is the new relay answering once. Answering to the new
	// credential is what the tunnel will ask of it, and a relay behind a proxy
	// that lets registration through and nothing else is found out here,
	// while the old relay still has the machine.
	if _, err := NewClient(next, version).Devices(ctx); err != nil {
		// Not on ctx alone, for the reason Enable's own cleanup gives: a
		// relay this slow can leave none of the caller's budget for it.
		_ = NewClient(next, version).Unregister(context.WithoutCancel(ctx))
		return nil, nil, fmt.Errorf("%s took this machine but then did not answer for it (%v); nothing has changed, and it is still on %s", relay, err, old.Relay)
	}
	if err := next.Save(); err != nil {
		// Nothing here can speak for the new host, so it is taken back off.
		_ = NewClient(next, version).Unregister(context.WithoutCancel(ctx))
		return nil, nil, err
	}
	if switched != nil {
		switched()
	}
	if err := NewClient(old, version).Unregister(ctx); err != nil && !IsRevoked(err) {
		untold = err
	}
	return next, untold, nil
}

// hostname is os.Hostname, a variable so that a test can have it fail.
var hostname = os.Hostname

// saveConfig is Config.Save, a variable so that a test can have it fail.
var saveConfig = (*Config).Save

// hostName is what a machine is called on devices when nobody says: its host
// name, without the ".local" a Mac adds for its own network, which is not
// part of what anybody calls it.
func hostName(host string) string {
	if n := len(host) - len(".local"); n > 0 && strings.EqualFold(host[n:], ".local") {
		return host[:n]
	}
	return host
}

// maxName is the longest name the relay keeps, in characters.
const maxName = 64

// cleanName is a name as the relay will keep it: trimmed, without control
// characters, and cut to maxName characters, as flockdeck-relay's own
// cleanName does. Made so here, the name saved, which status and the window
// show, is the one every device lists.
func cleanName(name string) string {
	var b strings.Builder
	n := 0
	for _, r := range strings.TrimSpace(name) {
		if unicode.IsControl(r) || r == unicode.ReplacementChar {
			continue
		}
		if n == maxName {
			break
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}

// CheckName is a new name for this machine or a device as the relay will keep
// it, or why it cannot be one: nothing left once it is cleaned, or one of the
// relay's codes, which would be shown on every device as what it is called.
func CheckName(name string) (string, error) {
	if isRelayCode(name) {
		return "", errors.New("that is one of the relay's codes, not a name; the name is what it is called on your devices")
	}
	clean := cleanName(name)
	if clean == "" {
		return "", errors.New("a name is needed, and that one is empty")
	}
	return clean, nil
}

// Rename gives this machine a new name on its relay, and saves it in the
// enrolment, which status and the window show. It is saved as the relay keeps
// it, trimmed and cut to length, so that both say what every device lists.
func Rename(ctx context.Context, version, name string) (*Config, error) {
	clean, err := CheckName(name)
	if err != nil {
		return nil, err
	}
	cfg, err := Load()
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, ErrNotEnabled
	}
	if err := NewClient(cfg, version).RenameHost(ctx, clean); err != nil {
		return nil, err
	}
	cfg.Name = clean
	if err := cfg.Save(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Disable turns remote access off on this machine and keeps the enrolment: the
// tunnel closes and stays closed, and the relay is not told anything. Its
// account, with its devices, plan, trial and verified email, is untouched,
// and the host stays listed on the relay as offline. Enable turns it on again,
// to the same account and host. It reports whether there was an enrolment, and
// is not an error when it was already off.
func Disable() (had bool, err error) {
	cfg, err := Load()
	if err != nil {
		return true, err
	}
	if cfg == nil {
		return false, nil
	}
	if cfg.Disabled {
		return true, nil
	}
	cfg.Disabled = true
	return true, saveConfig(cfg)
}

// Remove takes this machine off its relay and forgets the enrolment, and
// reports whether there was one. The relay deletes this host and nothing else:
// the account, with its other desktops, paired devices, plan, trial and
// verified email, stays, even when this was its last desktop. DeleteAccount
// is what erases an account.
//
// A relay that cannot be told stops it with a *RelayUntoldError, and an
// enrolment that cannot be read with Load's error, unless force says to forget
// the enrolment here regardless. Then why the relay could not be told comes
// back as untold, for the caller to pass on.
func Remove(ctx context.Context, version string, force bool) (had bool, untold error, err error) {
	cfg, err := Load()
	if err != nil && !force {
		return true, nil, err
	}
	had = err != nil || cfg != nil
	if cfg != nil {
		err := NewClient(cfg, version).Unregister(ctx)
		switch {
		case err == nil, IsRevoked(err):
			// Gone either way: removed now, or already.
		case !force:
			return true, nil, &RelayUntoldError{Err: err}
		default:
			untold = err
		}
	}
	if !had && !force {
		return false, nil, nil
	}
	return had, untold, Clear()
}
