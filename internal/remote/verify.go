package remote

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Verifying an email before this machine may register a new account on a
// relay that requires one (flockdeck-relay's own internal/relay/magiclink.go)
// -- the same "open a browser, or print something, and wait" shape
// internal/ghcli/auth.go's Login already uses for `gh auth login`, except
// what is waited on is flockdeck-relay's own HTTP API, polled, rather than a
// subprocess's output read as it streams, and there is no code for anyone to
// type anywhere: the URL a relay hands back already carries it, in a
// fragment, for its own verification page to read.
//
// Starting a registration and registering it, once verified, are still two
// separate calls to the relay (StartVerification is folded into VerifyEmail
// below; the registration itself is Register, given the code VerifyEmail
// returns as RegisterRequest.VerificationCode) -- exactly as flockdeck-relay
// itself keeps them apart, since nothing between them needs a browser
// session of its own (see magiclink.go's own package doc there).

// startVerification is what a registration is handed back the moment it
// starts: the poll code -- what Register is finally called with, and what
// VerifyEmail polls status with -- and the URL to open, or print, for a
// browser to give an email address to.
type startVerification struct {
	Code      string    `json:"code"`
	VerifyURL string    `json:"verifyUrl"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// verificationStatus is what polling answers with.
type verificationStatus struct {
	Verified  bool      `json:"verified"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// VerifyEvent is one thing that happens while VerifyEmail waits on a
// verified email, sent to onEvent as it happens -- the same shape
// ghcli.LoginEvent already uses for `gh auth login`'s own device flow, so a
// dialog that already knows how to show one of those can very likely be
// adapted rather than built from scratch.
//
// Exactly one of URL, Line, Done or Err is set on any event but URL, which
// also carries the Line it was announced in.
type VerifyEvent struct {
	// URL is the address to open, or print, once, at the very start.
	URL string
	// Line is one line of commentary, kept for a log a person can read if
	// something goes wrong partway through.
	Line string
	// Done is true on the last event, once the email has been confirmed. No
	// further events follow it.
	Done bool
	// Err is set instead of Done when the wait did not finish -- the
	// registration's own expiry passed, or ctx was cancelled.
	Err error
}

// ErrNeedsInteractiveVerify is VerifyEmail refusing to wait on a relay that
// requires a verified email when onEvent is nil: without it, nothing here
// can show anybody the URL to open, and waiting regardless would sit polling
// for up to the registration's own expiry with no way for anyone to ever
// finish it. The command line always gives an onEvent that opens or prints
// the URL; a caller that does not yet have a way to show one and wait --
// today, the window's own Remote access… dialog -- gets this instead of
// hanging.
var ErrNeedsInteractiveVerify = errors.New("this relay needs a verified email before registering a new machine; run `flockdeck remote enable` from a terminal, which opens a browser for it")

// verifyPollInterval is how often VerifyEmail checks whether the email has
// been confirmed yet. A person clicking a link in their inbox is not
// bothered by a couple of seconds' delay in noticing it, and it is a small,
// cheap request either way. A variable so a test does not have to sit
// through it.
var verifyPollInterval = 2 * time.Second

// VerifyEmail starts a registration that needs a verified email, if relay
// asks for one at all, and waits for it to be confirmed, sending onEvent
// every event along the way. The code it returns is what Register is then
// called with, as RegisterRequest.VerificationCode.
//
// A relay that does not require a verified email -- every relay unless told
// otherwise, and every relay from before this existed -- answers the very
// first call 404: there is nothing to wait on, code is "", and the caller
// registers exactly as it always has.
//
// ctx cancelled, or the registration's own expiry passing, stops the wait
// early with a non-nil error; either way onEvent is sent a final event with
// Err set, unless it had already verified.
func VerifyEmail(ctx context.Context, relay, version string, onEvent func(VerifyEvent)) (code string, err error) {
	started, err := beginVerification(ctx, relay, version)
	if err != nil {
		return "", err
	}
	if started == nil {
		return "", nil
	}
	if onEvent == nil {
		return "", ErrNeedsInteractiveVerify
	}
	onEvent(VerifyEvent{URL: started.VerifyURL, Line: "Verify your email: " + started.VerifyURL})

	ctx, cancel := context.WithDeadline(ctx, started.ExpiresAt)
	defer cancel()
	ticker := time.NewTicker(verifyPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			err := fmt.Errorf("timed out waiting for that email to be verified; the link may have expired -- run `flockdeck remote enable` again")
			if ctx.Err() == context.Canceled {
				err = ctx.Err()
			}
			onEvent(VerifyEvent{Err: err})
			return "", err
		case <-ticker.C:
			st, err := pollVerification(ctx, relay, version, started.Code)
			if err != nil {
				// A registration the relay no longer has -- expired, or
				// somehow already spent -- is worth stopping for; anything
				// else reaching the relay is more likely a blip than the
				// end of the wait, and is worth trying again for.
				var api *APIError
				if errors.As(err, &api) && (api.Status == http.StatusNotFound || api.Status == http.StatusGone) {
					onEvent(VerifyEvent{Err: err})
					return "", err
				}
				continue
			}
			if st.Verified {
				onEvent(VerifyEvent{Done: true})
				return started.Code, nil
			}
		}
	}
}

// beginVerification asks relay to start a registration that needs a
// verified email. nil, nil means the relay does not require one.
func beginVerification(ctx context.Context, relay, version string) (*startVerification, error) {
	c := &Client{Relay: relay, Version: version}
	var out startVerification
	err := c.call(ctx, http.MethodPost, "/api/v1/register/start", struct{}{}, &out)
	var api *APIError
	if errors.As(err, &api) && api.Status == http.StatusNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if out.Code == "" || out.VerifyURL == "" {
		return nil, errors.New("the relay started a registration but sent back no code or no verification URL")
	}
	return &out, nil
}

// pollVerification asks whether code has been verified yet, without
// consuming it.
func pollVerification(ctx context.Context, relay, version, code string) (verificationStatus, error) {
	c := &Client{Relay: relay, Version: version, Token: code}
	var out verificationStatus
	err := c.call(ctx, http.MethodGet, "/api/v1/register/status", nil, &out)
	return out, err
}
