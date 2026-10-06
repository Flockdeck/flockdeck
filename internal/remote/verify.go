package remote

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Verifying an email before this machine may register a new account on a
// relay that requires one (flockdeck-relay's own internal/relay/magiclink.go)
// -- the same "open a browser, or print something, and wait" shape
// internal/ghcli/auth.go's Login already uses for `gh auth login`, except
// what is waited on is flockdeck-relay's own HTTP API, polled, rather than a
// subprocess's output read as it streams. The poll code is never typed: the
// URL a relay hands back already carries it, in a fragment, for its own
// verification page to read. What a person does type, on that page, is the
// match code a relay may hand back as well. It is shown here and never
// emailed, so that someone who only receives the emailed link, and did not
// start this, has no code to enter. The email address is typed there too, and
// never reaches this machine.
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
	// MatchCode is the short code the person enters on the verification page
	// once they open the emailed link. A relay from before match codes sends
	// none, and there is then nothing to show.
	MatchCode string `json:"matchCode,omitempty"`
}

// startRequest is what starting a registration sends. MatchCode asks the
// relay to make a match code. A relay that has none ignores the field, as it
// ignores every JSON field it does not know, so it is always sent rather than
// only to relays known to want it: no relay version refuses it, and a relay
// that requires the code answers 409 without it.
type startRequest struct {
	MatchCode bool `json:"matchCode"`
}

// FormatMatchCode is a match code as it is shown to a person: an eight
// character code in two groups of four, so that it is easier to read out and
// to copy by eye. A code that is another length, or already has a separator,
// is shown as the relay sent it.
func FormatMatchCode(code string) string {
	code = strings.TrimSpace(code)
	if len(code) == 8 && !strings.ContainsAny(code, "- ") {
		return code[:4] + "-" + code[4:]
	}
	return code
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
// also carries the Line it was announced in, and the MatchCode if there is one.
type VerifyEvent struct {
	// URL is the address to open, or print, once, at the very start.
	URL string
	// MatchCode is, on the URL event, the code the person enters on the page
	// that opens from the emailed link, already formatted for showing. Empty
	// from a relay that makes none.
	MatchCode string
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
// today, the window's own Flockdeck Remote… dialog -- gets this instead of
// hanging.
var ErrNeedsInteractiveVerify = errors.New("this relay needs a verified email before registering a new machine; run `flockdeck remote enable` from a terminal, which opens a browser for it")

// verifyRefusal is the relay refusing a step of the email verification for a reason a person
// can act on, said in words for them instead of as a status or the relay's JSON. It keeps the
// APIError it came from, for errors.As.
type verifyRefusal struct {
	msg string
	api *APIError
}

func (e *verifyRefusal) Error() string { return e.msg }
func (e *verifyRefusal) Unwrap() error { return e.api }

// oldFlockdeckAdvice is said after a relay's 409 on starting a registration, which a relay
// that requires the match code answers to a desktop that did not ask for one. This version
// does ask, so it is only met from a relay that says 409 for some other reason; the way out
// is the same either way.
const oldFlockdeckAdvice = "Update Flockdeck and run `flockdeck remote enable` again, or run `flockdeck remote pair -desktop` on a machine already on your account and pass the code it prints with -join"

// verifyError words the refusals that starting a registration can answer with: 429, the
// relay's rate limit (wait), 500, the relay failing (try later), and 409, a relay that will
// not enrol this version of Flockdeck (update, or join with a code). The address is typed on
// the relay's own page in the browser, so a rejected or blocked address (422) and a failed
// email send (502) are shown there and never reach this machine. Any other error is returned
// as it is.
func verifyError(err error) error {
	var api *APIError
	if !errors.As(err, &api) {
		return err
	}
	switch api.Status {
	case http.StatusTooManyRequests:
		return &verifyRefusal{"too many verification attempts from here. Wait a while, then try again", api}
	case http.StatusInternalServerError:
		return &verifyRefusal{"the relay could not start registration. Try again later", api}
	case http.StatusConflict:
		// The relay's words say what it wants, when it says; they are followed by what to
		// do unless they already say it.
		msg := strings.TrimSpace(api.Message)
		if msg == "" {
			msg = "this relay will not enrol this version of Flockdeck by email"
		}
		if !strings.Contains(msg, "-join") {
			msg = strings.TrimRight(msg, ".") + ". " + oldFlockdeckAdvice
		}
		return &verifyRefusal{msg, api}
	}
	return err
}

// IsVerifyRefusal reports whether err is the relay refusing a step of the
// email verification, already said in words for a person, rather than the
// registration after it. A caller that adds advice to the relay's refusals
// leaves these alone.
func IsVerifyRefusal(err error) bool {
	var v *verifyRefusal
	return errors.As(err, &v)
}

// registrationGone is what is said when the relay no longer has the registration being waited
// on. It has expired, or the match code was entered wrongly too many times on the relay's page,
// which deletes the registration; the relay does not say which, and either way the answer is to
// start again.
func registrationGone(api *APIError) error {
	return &verifyRefusal{"the relay no longer has this registration. It may have expired, or the confirmation code may have been entered wrongly too many times. Run `flockdeck remote enable` again", api}
}

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
	ev := VerifyEvent{URL: started.VerifyURL, Line: "Verify your email: " + started.VerifyURL}
	if started.MatchCode != "" {
		ev.MatchCode = FormatMatchCode(started.MatchCode)
		ev.Line += " (confirmation code " + ev.MatchCode + ")"
	}
	onEvent(ev)

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
					err = registrationGone(api)
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
	var relayNow time.Time
	err := c.call(ctx, http.MethodPost, "/api/v1/register/start", startRequest{MatchCode: true}, &out, relayDate(&relayNow))
	var api *APIError
	if errors.As(err, &api) && api.Status == http.StatusNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, verifyError(err)
	}
	if out.Code == "" || out.VerifyURL == "" {
		return nil, errors.New("the relay started a registration but sent back no code or no verification URL")
	}
	// The expiry is by the relay's clock, and is waited for by this
	// machine's, as a pairing code's is (see Pair).
	if !relayNow.IsZero() {
		out.ExpiresAt = byHere(out.ExpiresAt, time.Since(relayNow))
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
