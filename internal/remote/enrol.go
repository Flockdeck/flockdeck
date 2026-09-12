package remote

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
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
		return "remote access is already enabled, with " + e.Relay
	}
	return fmt.Sprintf("remote access is already enabled with %s, which could not be asked whether it still is (%v)", e.Relay, e.Err)
}

func (e *AlreadyEnabledError) Unwrap() error { return e.Err }

// RelayUntoldError is taking this machine off a relay that could not be told,
// without being asked to forget the enrolment regardless. Forgetting it here
// would leave the machine listed on the relay, offline, for good: nothing but
// this machine can take it off.
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
func Enable(ctx context.Context, version string, req EnableRequest) (cfg *Config, replaced bool, err error) {
	// A pairing link is for a phone or a browser, and given as a join code it
	// would reach the relay only to be refused in words that do not say why.
	if strings.Contains(req.Join, "/pair#") {
		return nil, false, errors.New("that is a pairing link, for a phone or browser to open; a machine joins another's account with a code made for that, which `flockdeck remote pair -desktop` prints")
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
		if _, err := NewClient(existing, version).Devices(ctx); !IsRevoked(err) {
			return nil, false, &AlreadyEnabledError{Relay: existing.Relay, Err: err}
		}
		replaced = true
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		host, _ := os.Hostname()
		name = hostName(host)
	}
	reg, err := Register(ctx, relay, version, RegisterRequest{
		Name: name, Join: strings.TrimSpace(req.Join), Invite: strings.TrimSpace(req.Invite),
	})
	if err != nil {
		return nil, false, err
	}
	cfg = &Config{Relay: relay, HostID: reg.HostID, AccountID: reg.AccountID, Token: reg.Token, Name: name}
	if err := cfg.Save(); err != nil {
		// The relay now has a host that nothing here can speak for. Take it
		// back off rather than leave it listed on every device for good.
		_ = NewClient(cfg, version).Unregister(ctx)
		return nil, false, err
	}
	return cfg, replaced, nil
}

// hostName is what a machine is called on devices when nobody says: its host
// name, without the ".local" a Mac adds for its own network, which is not
// part of what anybody calls it.
func hostName(host string) string {
	if n := len(host) - len(".local"); n > 0 && strings.EqualFold(host[n:], ".local") {
		return host[:n]
	}
	return host
}

// Disable takes this machine off its relay and forgets the enrolment, and
// reports whether there was one.
//
// A relay that cannot be told stops it with a *RelayUntoldError, and an
// enrolment that cannot be read with Load's error, unless force says to forget
// the enrolment here regardless. Then why the relay could not be told comes
// back as untold, for the caller to pass on.
func Disable(ctx context.Context, version string, force bool) (had bool, untold error, err error) {
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
