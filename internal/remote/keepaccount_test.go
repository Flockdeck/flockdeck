package remote

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// callsTo counts the fake relay's recorded calls that begin with prefix.
func (f *fakeRelay) callsTo(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func (f *fakeRelay) forgetCalls() {
	f.mu.Lock()
	f.calls = nil
	f.mu.Unlock()
}

func keepAccountManager(t *testing.T) (*Manager, *fakeRelay) {
	t.Helper()
	isolate(t)
	quick(t)
	f := newFakeRelay(t)
	m := NewManager("v", func(l net.Listener) error { return http.Serve(l, http.NotFoundHandler()) }, nil)
	t.Cleanup(m.Close)
	return m, f
}

func mustPath(t *testing.T) string {
	t.Helper()
	p, err := path()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// Turning remote access off keeps the account: the same host, account, token
// and end-to-end key are kept, the relay is not told, and turning it on again
// reconnects to them without registering or verifying anything.
func TestDisableKeepsTheAccountAndEnableReturnsToIt(t *testing.T) {
	m, f := keepAccountManager(t)
	ctx := context.Background()
	// A relay that wants a verified email, so any second sign-up would show.
	f.requireVerify, f.verifyCode, f.verified = true, "fdv_test", true
	if _, err := m.Enable(ctx, EnableRequest{Relay: f.URL, Name: "desk", OnVerify: func(VerifyEvent) {}}); err != nil {
		t.Fatal(err)
	}
	f.session(t)
	waitFor(t, "connected", func() bool { st, _ := m.Status(); return st.State == StateConnected })
	// The end-to-end identity is made on the first connection; the relay's
	// answer to registering its public key does not matter here.
	_ = m.EnsureE2EKey(ctx)
	keyPath, _ := e2eKeyPath()
	keyBefore, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("no end-to-end identity was made: %v", err)
	}
	before, err := Load()
	if err != nil || before == nil || before.Disabled {
		t.Fatalf("enrolment before = %+v, %v", before, err)
	}

	f.forgetCalls()
	if err := m.Disable(); err != nil {
		t.Fatal(err)
	}
	st, ok := m.Status()
	if !ok || st.State != StateDisabled || st.HostID != before.HostID {
		t.Errorf("status while off = %+v, %v; want disabled, and still this host", st, ok)
	}
	off, err := Load()
	if err != nil || off == nil || !off.Disabled {
		t.Fatalf("the enrolment after Disable = %+v, %v; want it kept and disabled", off, err)
	}
	if off.Token != before.Token || off.HostID != before.HostID || off.AccountID != before.AccountID || off.Relay != before.Relay || off.Name != before.Name {
		t.Errorf("Disable changed the enrolment: %+v, was %+v", off, before)
	}
	if n := f.callsTo("DELETE "); n != 0 {
		t.Errorf("Disable made %d DELETE calls to the relay, want none", n)
	}
	raw, _ := os.ReadFile(mustPath(t))
	if !strings.Contains(string(raw), `"enabled": false`) {
		t.Errorf("remote.json does not say enabled is false:\n%s", raw)
	}
	if got, _ := os.ReadFile(keyPath); string(got) != string(keyBefore) {
		t.Error("Disable changed the end-to-end key")
	}
	if _, err := m.Client(); err != nil {
		t.Errorf("the roster is out of reach while off: %v", err)
	}
	if err := m.Reconnect(); err == nil {
		t.Error("Reconnect while off did not say it is off")
	}

	// Turn it on again: nothing is registered, verified or deleted.
	f.forgetCalls()
	if _, err := m.Enable(ctx, EnableRequest{OnVerify: func(VerifyEvent) { t.Error("a verification was asked for") }}); err != nil {
		t.Fatal(err)
	}
	f.session(t)
	waitFor(t, "connected again", func() bool { st, _ := m.Status(); return st.State == StateConnected })
	on, err := Load()
	if err != nil || on == nil || on.Disabled {
		t.Fatalf("the enrolment after Enable = %+v, %v; want it enabled", on, err)
	}
	if on.Token != before.Token || on.HostID != before.HostID || on.AccountID != before.AccountID || on.Relay != before.Relay || on.Name != before.Name {
		t.Errorf("Enable changed the account: %+v, was %+v", on, before)
	}
	for _, prefix := range []string{"POST /api/v1/hosts", "POST /api/v1/register", "GET /api/v1/register", "DELETE "} {
		if n := f.callsTo(prefix); n != 0 {
			t.Errorf("turning it back on made %d calls beginning %q, want none; saw %v", n, prefix, f.calls)
		}
	}
	if got, _ := os.ReadFile(keyPath); string(got) != string(keyBefore) {
		t.Error("Enable changed the end-to-end key")
	}
}

// Turning it on with options that belong to another enrolment is refused,
// and leaves the kept one off and untouched, rather than quietly ignoring
// them or enrolling a second host.
func TestEnableOfAKeptEnrolmentRefusesOptionsForAnotherOne(t *testing.T) {
	_, f := keepAccountManager(t)
	ctx := context.Background()
	if _, _, err := Enable(ctx, "v", EnableRequest{Relay: f.URL, Name: "desk"}); err != nil {
		t.Fatal(err)
	}
	if had, err := Disable(); !had || err != nil {
		t.Fatalf("Disable = %v, %v", had, err)
	}
	for name, req := range map[string]EnableRequest{
		"join":    {Join: "fdp_x"},
		"invite":  {Invite: "fdi_x"},
		"relay":   {Relay: "https://other.example"},
		"renamed": {Name: "laptop"},
	} {
		var kept *DisabledEnrolmentError
		if _, _, err := Enable(ctx, "v", req); !errors.As(err, &kept) {
			t.Errorf("%s: Enable = %v, want a DisabledEnrolmentError", name, err)
		}
	}
	if c, _ := Load(); c == nil || !c.Disabled {
		t.Errorf("a refused Enable changed the kept enrolment: %+v", c)
	}
	if n := f.callsTo("POST /api/v1/hosts"); n != 1 {
		t.Errorf("registered %d times, want only the first enrolment", n)
	}
	// The same options that match what is kept are fine.
	if _, _, err := Enable(ctx, "v", EnableRequest{Relay: f.URL, Name: "desk"}); err != nil {
		t.Errorf("Enable with the kept relay and name = %v", err)
	}
}

// A kept enrolment whose relay has since forgotten the host is dead, and
// turning it on enrols afresh, saying so, as it does for an enabled one.
func TestEnableOfAKeptEnrolmentTheRelayForgotEnrolsAgain(t *testing.T) {
	_, f := keepAccountManager(t)
	ctx := context.Background()
	cfg, _, err := Enable(ctx, "v", EnableRequest{Relay: f.URL, Name: "desk"})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Token, cfg.Disabled = "fdh_forgotten", true
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	got, replaced, err := Enable(ctx, "v", EnableRequest{Relay: f.URL, Name: "desk"})
	if err != nil || !replaced || got.Disabled || got.Token != f.token {
		t.Errorf("Enable = %+v, replaced %v, %v; want a fresh, enabled enrolment", got, replaced, err)
	}
}

// Remove is what Disable used to be: the relay is told, and the enrolment is
// cleared, even from a machine that was turned off first.
func TestRemoveTellsTheRelayAndClears(t *testing.T) {
	m, f := keepAccountManager(t)
	ctx := context.Background()
	if _, err := m.Enable(ctx, EnableRequest{Relay: f.URL, Name: "desk"}); err != nil {
		t.Fatal(err)
	}
	f.session(t)
	if err := m.Disable(); err != nil {
		t.Fatal(err)
	}
	if n := f.callsTo("DELETE "); n != 0 {
		t.Fatalf("Disable made %d DELETE calls", n)
	}
	if _, err := m.Remove(ctx, false); err != nil {
		t.Fatal(err)
	}
	if n := f.callsTo("DELETE /api/v1/host"); n != 1 {
		t.Errorf("Remove made %d DELETE calls, want 1", n)
	}
	if c, _ := Load(); c != nil {
		t.Errorf("Remove left the enrolment: %+v", c)
	}
	if _, ok := m.Status(); ok {
		t.Error("the status still shows an enrolment after Remove")
	}
}

// remote.json written before the field existed has no "enabled" in it, and
// is an enabled machine. Saving it adds the field.
func TestConfigWithoutEnabledIsEnabled(t *testing.T) {
	isolate(t)
	p := mustPath(t)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	old := `{"relay":"https://remote.flockdeck.ai","hostId":"h9","accountId":"a9","token":"fdh_old","name":"desk"}`
	if err := os.WriteFile(p, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load()
	if err != nil || c == nil || c.Disabled || c.HostID != "h9" || c.AccountID != "a9" || c.Token != "fdh_old" {
		t.Fatalf("Load of a file with no enabled field = %+v, %v; want an enabled enrolment", c, err)
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(p); !strings.Contains(string(raw), `"enabled": true`) {
		t.Errorf("saving did not write the field:\n%s", raw)
	}
	c.Disabled = true
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	if back, _ := Load(); back == nil || !back.Disabled {
		t.Errorf("a disabled file loaded as %+v", back)
	}
}
