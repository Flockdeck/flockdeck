package structured

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/acp/acptest"
	"github.com/jmwri/flockdeck/internal/acp/manifest"
	"github.com/jmwri/flockdeck/internal/agent"
)

// setupPane starts a fake agent that wants a login and lists the sign-in method
// as mode says, and waits for the setup state.
func setupPane(t *testing.T, mode, url string) *Runtime { return setupPaneAs(t, mode, url, true) }

// setupPaneAs is setupPane with the adapter said to be Flockdeck's own, or not.
func setupPaneAs(t *testing.T, mode, url string, own bool) *Runtime {
	t.Helper()
	opts := []acptest.Option{acptest.WithAuth("prompt")}
	if mode != "" {
		opts = append(opts, acptest.WithSignIn(mode, url))
	}
	o := fakeOptions(t, opts...)
	o.Command.Own = own
	r := begin(t, o)
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

// An agent that is not one of Flockdeck's own adapters is not asked to run a
// sign-in, whatever methods it lists, and a notification from it gives no link.
func TestAForeignAdapterListingTheLoginMethodIsNotSignedInFromHere(t *testing.T) {
	r := setupPaneAs(t, "ok", "", false)
	if s := r.Snapshot(); s.CanSignIn {
		t.Error("CanSignIn for an adapter that is not Flockdeck's own")
	}
	if err := r.SignIn(nil); !errors.Is(err, ErrNoSignIn) {
		t.Fatalf("SignIn: %v", err)
	}
	if s := r.Snapshot(); s.SignIn != nil {
		t.Errorf("state = %+v", s.SignIn)
	}
}

// The address counts only when it follows our own authenticate call.
func TestAnAddressBeforeAuthenticateIsNotTaken(t *testing.T) {
	r := setupPane(t, "early", "")
	if err := r.SignIn(nil); err != nil {
		t.Fatal(err)
	}
	s := until(t, r, "the end", func(s Snapshot) bool { return s.SignIn != nil && s.SignIn.State == SignInDone })
	if s.SignIn.URL != "" {
		t.Errorf("url = %q", s.SignIn.URL)
	}
}

// A command that exited well is a sign-in even if a cancel came at the same time.
func TestASuccessThatRacedACancelStillRestarts(t *testing.T) {
	r := &Runtime{subs: map[int]chan struct{}{}}
	called := make(chan struct{}, 1)
	run := &signInRun{done: make(chan struct{}), state: SignInWaiting, cancelled: true}
	r.signin = run
	r.settleSignIn(run, nil, func() { called <- struct{}{} })
	select {
	case <-called:
	case <-time.After(5 * time.Second):
		t.Fatal("onDone not called for a success that raced a cancel")
	}
	if run.state != SignInDone {
		t.Errorf("state = %q", run.state)
	}
	// A cancel that really ended the command is not a success.
	run2 := &signInRun{done: make(chan struct{}), state: SignInWaiting, cancelled: true}
	r.signin = run2
	r.settleSignIn(run2, ErrCancelled2, func() { t.Error("onDone after a real cancel") })
	if r.signin != nil {
		t.Error("a cancelled sign-in is still shown")
	}
}

// ErrCancelled2 stands for the error a cancelled sign-in returns.
var ErrCancelled2 = errors.New("cancelled")

// The sign-in's adapter gets no permission probe, no stored key, and the
// proxy variables of Flockdeck's own environment.
func TestTheSignInHelperIsResolvedWithoutTheProbeOrTheKey(t *testing.T) {
	probed := 0
	run := func(_ context.Context, _ string, _ []string, _ string, env []string) ([]byte, error) {
		probed++
		return []byte("{}"), nil
	}
	dir := t.TempDir()
	prog := filepath.Join(dir, "opencode")
	if err := os.WriteFile(prog, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	r := &Runtime{ctx: context.Background(), opts: Options{
		Spec: keySpec("opencode"), Cwd: dir, Key: testKey,
		Environ:          []string{"KEEP_ME=kept", "HTTPS_PROXY=http://proxy.example:3128", "no_proxy=localhost", "OTHER=x"},
		PermissionRunner: run,
		Probe:            &manifest.Probe{LookPath: func(string) (string, error) { return prog, nil }},
	}}
	cmd, _, err := r.resolveFor(true)
	if err != nil {
		t.Fatal(err)
	}
	if probed != 0 {
		t.Errorf("the launch check ran %d times for a sign-in", probed)
	}
	if r.permRan || r.permNote != "" {
		t.Errorf("the pane's permission state was written: ran=%v note=%q", r.permRan, r.permNote)
	}
	for _, kv := range cmd.env {
		if strings.Contains(kv, testKey) {
			t.Errorf("the sign-in's adapter was given the key (%s)", name(kv))
		}
	}
	if v, ok := envHas(cmd.env, "HTTPS_PROXY"); !ok || v != "http://proxy.example:3128" {
		t.Errorf("HTTPS_PROXY = %q, %v", v, ok)
	}
	if _, ok := envHas(cmd.env, "NO_PROXY"); !ok {
		t.Error("NO_PROXY (given in lower case) was dropped")
	}
	if _, ok := envHas(cmd.env, "OTHER"); ok {
		t.Error("an unrelated variable was passed")
	}
	if cmd.Own {
		t.Error("an adapter named opencode was taken for Flockdeck's own")
	}
	// The pane's own run still gets the key.
	own, _, err := r.resolve()
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := envHas(own.env, "GEMINI_API_KEY"); !ok || v != testKey {
		t.Errorf("the pane's own run lost its key: %q %v", v, ok)
	}
}

// Flockdeck's own adapters (the manifest command is flockdeck itself) are the
// ones trusted with a sign-in; a program found on PATH is not.
func TestOnlyAdaptersBuiltIntoFlockdeckAreTrustedWithASignIn(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		adapter string
		want    bool
	}{{"claude", true}, {"codex-go", true}, {"opencode", false}, {"gemini", false}} {
		r := &Runtime{ctx: context.Background(), opts: Options{
			Spec:       agent.Spec{ID: "x", Runner: agent.RunnerACP, ACP: agent.ACPSpec{Adapter: c.adapter}},
			Cwd:        t.TempDir(),
			Executable: func() (string, error) { return exe, nil },
			Probe:      &manifest.Probe{LookPath: func(string) (string, error) { return exe, nil }},
		}}
		cmd, _, err := r.resolveFor(true)
		if err != nil {
			t.Fatalf("%s: %v", c.adapter, err)
		}
		if cmd.Own != c.want || r.ownAdapter() != c.want {
			t.Errorf("%s: own=%v ownAdapter=%v, want %v", c.adapter, cmd.Own, r.ownAdapter(), c.want)
		}
	}
}
