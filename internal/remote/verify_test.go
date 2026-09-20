package remote

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// quickPoll makes VerifyEmail's polling fast enough for a test to watch.
func quickPoll(t *testing.T) {
	t.Helper()
	old := verifyPollInterval
	verifyPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { verifyPollInterval = old })
}

// A relay that does not require a verified email -- every relay unless told
// otherwise, and every relay from before this existed -- answers the first
// call 404, and VerifyEmail says there is nothing to wait on: no event is
// ever sent, and code is "".
func TestVerifyEmailNotRequired(t *testing.T) {
	f := newFakeRelay(t)
	called := false
	code, err := VerifyEmail(context.Background(), f.URL, "v", func(VerifyEvent) { called = true })
	if err != nil || code != "" {
		t.Fatalf("VerifyEmail on a relay that does not require one = %q, %v; want \"\", nil", code, err)
	}
	if called {
		t.Error("an event was sent for a relay that needs no verification at all")
	}
}

// Without a way to show anybody the URL, waiting on a relay that requires
// one would sit polling for as long as the registration stays open with no
// way for anyone to ever finish it -- so VerifyEmail refuses immediately
// instead.
func TestVerifyEmailNeedsOnEvent(t *testing.T) {
	f := newFakeRelay(t)
	f.requireVerify = true
	_, err := VerifyEmail(context.Background(), f.URL, "v", nil)
	if !errors.Is(err, ErrNeedsInteractiveVerify) {
		t.Fatalf("VerifyEmail with no onEvent = %v, want ErrNeedsInteractiveVerify", err)
	}
}

// The whole path: the URL is sent once, at the start, and VerifyEmail keeps
// polling until another goroutine -- standing in for a person clicking the
// emailed link -- marks it verified.
func TestVerifyEmailWaitsThenSucceeds(t *testing.T) {
	quickPoll(t)
	f := newFakeRelay(t)
	f.requireVerify = true

	var events []VerifyEvent
	go func() {
		time.Sleep(50 * time.Millisecond)
		f.setVerified(true)
	}()
	code, err := VerifyEmail(context.Background(), f.URL, "v", func(ev VerifyEvent) { events = append(events, ev) })
	if err != nil {
		t.Fatalf("VerifyEmail = %v", err)
	}
	if code != f.verifyCode {
		t.Errorf("VerifyEmail returned code %q, want %q", code, f.verifyCode)
	}
	if len(events) < 2 {
		t.Fatalf("events = %+v, want at least a URL and a Done", events)
	}
	if events[0].URL == "" || !strings.Contains(events[0].URL, f.verifyCode) {
		t.Errorf("first event = %+v, want the verify URL, carrying the code", events[0])
	}
	last := events[len(events)-1]
	if !last.Done || last.Err != nil {
		t.Errorf("last event = %+v, want Done", last)
	}
}

// A registration the relay no longer has -- expired, or somehow already
// spent -- is worth stopping for at once, rather than polling until the
// registration's own (much longer) expiry passes.
func TestVerifyEmailStopsWhenTheRegistrationIsGone(t *testing.T) {
	quickPoll(t)
	f := newFakeRelay(t)
	f.requireVerify = true
	f.goneAfterStart = true

	start := time.Now()
	_, err := VerifyEmail(context.Background(), f.URL, "v", func(VerifyEvent) {})
	if err == nil {
		t.Fatal("VerifyEmail on a registration the relay does not have = nil, want an error")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("VerifyEmail took %s to notice the registration was gone; the fake answers 404 to any other bearer immediately", elapsed)
	}
}

// Cancelling ctx -- Ctrl+C, in the command line -- stops the wait promptly,
// rather than only once the registration's own expiry passes.
func TestVerifyEmailRespectsCancellation(t *testing.T) {
	quickPoll(t)
	f := newFakeRelay(t)
	f.requireVerify = true

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	var lastErr error
	start := time.Now()
	_, err := VerifyEmail(ctx, f.URL, "v", func(ev VerifyEvent) {
		if ev.Err != nil {
			lastErr = ev.Err
		}
	})
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("cancelling took %s to be noticed", elapsed)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("VerifyEmail after cancelling = %v, want context.Canceled", err)
	}
	if !errors.Is(lastErr, context.Canceled) {
		t.Errorf("final event's Err = %v, want context.Canceled", lastErr)
	}
}

// Enable threads the code VerifyEmail returns into the registration itself,
// and a Join -- which needs no verification of its own -- skips the whole
// dance, never even starting one.
func TestEnableWiresTheVerificationCodeIntoRegistering(t *testing.T) {
	isolate(t)
	quickPoll(t)
	f := newFakeRelay(t)
	f.requireVerify = true
	go func() {
		time.Sleep(30 * time.Millisecond)
		f.setVerified(true)
	}()

	var gotURL string
	cfg, _, err := Enable(context.Background(), "v", EnableRequest{
		Relay: f.URL, Name: "desk",
		OnVerify: func(ev VerifyEvent) {
			if ev.URL != "" {
				gotURL = ev.URL
			}
		},
	})
	if err != nil {
		t.Fatalf("Enable = %v", err)
	}
	if cfg.HostID != "h1" {
		t.Fatalf("Enable = %+v, want it to have registered", cfg)
	}
	if gotURL == "" {
		t.Error("Enable never surfaced a URL to verify")
	}

	// A relay that requires verification but is never told how to show
	// anybody a URL fails fast rather than hanging.
	if err := Clear(); err != nil {
		t.Fatal(err)
	}
	_, _, err = Enable(context.Background(), "v", EnableRequest{Relay: f.URL, Name: "desk2"})
	if !errors.Is(err, ErrNeedsInteractiveVerify) {
		t.Errorf("Enable with no OnVerify on a relay that requires one = %v, want ErrNeedsInteractiveVerify", err)
	}
}

// Joining an already-registered account needs no verification of its own,
// so it never even asks the relay to start one.
func TestJoinSkipsVerificationEntirely(t *testing.T) {
	isolate(t)
	f := newFakeRelay(t)
	f.requireVerify = true

	if _, _, err := Enable(context.Background(), "v", EnableRequest{
		Relay: f.URL, Name: "desk", Join: "fdp_somejoincode",
	}); err != nil {
		t.Fatalf("Enable with a join code = %v", err)
	}
	f.mu.Lock()
	calls := append([]string(nil), f.calls...)
	f.mu.Unlock()
	for _, c := range calls {
		if strings.Contains(c, "/register/") {
			t.Errorf("a join asked the relay to start a verification: %v", calls)
		}
	}
}
