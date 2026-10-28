package structured

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/acp/acptest"
)

// setupPane starts a fake agent that wants a login and lists the sign-in method
// as mode says, and waits for the setup state.
func setupPane(t *testing.T, mode, url string) *Runtime {
	t.Helper()
	opts := []acptest.Option{acptest.WithAuth("prompt")}
	if mode != "" {
		opts = append(opts, acptest.WithSignIn(mode, url))
	}
	r := begin(t, fakeOptions(t, opts...))
	if err := r.Send("plain"); err != nil {
		t.Fatal(err)
	}
	until(t, r, "the setup state", func(s Snapshot) bool { return s.Phase == PhaseSetup })
	return r
}

func TestSignInShowsTheAddressThenCallsOnDone(t *testing.T) {
	r := setupPane(t, "ok", "")
	if s := r.Snapshot(); !s.CanSignIn || s.SignIn != nil {
		t.Fatalf("before: can=%v signin=%+v", s.CanSignIn, s.SignIn)
	}
	done := make(chan struct{})
	if err := r.SignIn(func() { close(done) }); err != nil {
		t.Fatal(err)
	}
	s := until(t, r, "the address", func(s Snapshot) bool { return s.SignIn != nil && s.SignIn.URL != "" })
	if !strings.HasPrefix(s.SignIn.URL, "https://claude.ai/oauth/authorize?") {
		t.Errorf("url = %q", s.SignIn.URL)
	}
	until(t, r, "done", func(s Snapshot) bool { return s.SignIn != nil && s.SignIn.State == SignInDone })
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("onDone was not called")
	}
	if r.Snapshot().Phase != PhaseSetup {
		t.Error("the runtime changed phase itself; restarting is the owner's")
	}
}

func TestSignInDropsAnAddressThatFailsTheShapeCheck(t *testing.T) {
	r := setupPane(t, "badurl", "")
	if err := r.SignIn(nil); err != nil {
		t.Fatal(err)
	}
	s := until(t, r, "the end", func(s Snapshot) bool { return s.SignIn != nil && s.SignIn.State == SignInDone })
	if s.SignIn.URL != "" {
		t.Errorf("url = %q", s.SignIn.URL)
	}
}

func TestSignInFailureShowsNoAgentText(t *testing.T) {
	r := setupPane(t, "fail", "")
	if err := r.SignIn(func() { t.Error("onDone after a failure") }); err != nil {
		t.Fatal(err)
	}
	s := until(t, r, "failure", func(s Snapshot) bool { return s.SignIn != nil && s.SignIn.State == SignInFailed })
	if strings.Contains(fmt.Sprintf("%+v", s.SignIn)+s.Err, "SECRET") {
		t.Errorf("agent text in the state: %+v err %q", s.SignIn, s.Err)
	}
	// A failed sign-in can be tried again.
	if err := r.SignIn(nil); err != nil {
		t.Errorf("second try: %v", err)
	}
}

func TestOnlyOneSignInAtATimeAndItCancels(t *testing.T) {
	r := setupPane(t, "hang", "")
	if err := r.SignIn(func() { t.Error("onDone after a cancel") }); err != nil {
		t.Fatal(err)
	}
	until(t, r, "the address", func(s Snapshot) bool { return s.SignIn != nil && s.SignIn.State == SignInWaiting })
	if err := r.SignIn(nil); !errors.Is(err, ErrSignInBusy) {
		t.Fatalf("second SignIn: %v", err)
	}
	start := time.Now()
	if err := r.SignInCancel(); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("cancel took %v", time.Since(start))
	}
	if s := r.Snapshot(); s.SignIn != nil {
		t.Errorf("state after cancel = %+v", s.SignIn)
	}
	if err := r.SignInCancel(); !errors.Is(err, ErrNoSignIn) {
		t.Errorf("cancel with nothing running: %v", err)
	}
	if err := r.SignIn(nil); err != nil {
		t.Errorf("sign-in after a cancel: %v", err)
	}
}

func TestClosingThePaneEndsTheSignIn(t *testing.T) {
	r := setupPane(t, "hang", "")
	if err := r.SignIn(nil); err != nil {
		t.Fatal(err)
	}
	until(t, r, "the address", func(s Snapshot) bool { return s.SignIn != nil && s.SignIn.State == SignInWaiting })
	run := func() *signInRun { r.mu.Lock(); defer r.mu.Unlock(); return r.signin }()
	start := time.Now()
	_ = r.Close()
	select {
	case <-run.done:
	default:
		t.Fatal("Close returned while the sign-in still ran")
	}
	if time.Since(start) > 15*time.Second {
		t.Errorf("close took %v", time.Since(start))
	}
	if err := r.SignIn(nil); !errors.Is(err, ErrNotRunning) {
		t.Errorf("sign-in on a closed pane: %v", err)
	}
}

func TestSignInNeedsTheAdaptersLoginMethod(t *testing.T) {
	r := setupPane(t, "", "")
	if s := r.Snapshot(); s.CanSignIn {
		t.Error("CanSignIn without the method")
	}
	if err := r.SignIn(nil); !errors.Is(err, ErrNoSignIn) {
		t.Errorf("SignIn: %v", err)
	}
}

func TestSignInNeedsTheSetupPhase(t *testing.T) {
	r := begin(t, fakeOptions(t, acptest.WithSignIn("ok", "")))
	until(t, r, "ready", func(s Snapshot) bool { return s.Phase == PhaseReady })
	if err := r.SignIn(nil); !errors.Is(err, ErrNoSignIn) {
		t.Errorf("SignIn in a ready pane: %v", err)
	}
}
