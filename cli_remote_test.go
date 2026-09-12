package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jmwri/flockdeck/internal/remote"
)

// fakeRelayAPI answers the relay's REST calls well enough for the subcommand
// to be driven end to end, and records what it was asked.
type fakeRelayAPI struct {
	*httptest.Server
	mu    sync.Mutex
	calls []string
	// revoked makes every authenticated call answer as if the host had been
	// removed.
	revoked bool
}

func newFakeRelayAPI(t *testing.T) *fakeRelayAPI {
	t.Helper()
	f := &fakeRelayAPI{}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeRelayAPI) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	revoked := f.revoked
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodPost && r.URL.Path == "/api/v1/hosts" {
		var req remote.RegisterRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"hostId": "h-" + req.Name, "accountId": "a1", "token": "fdh_" + req.Name,
		})
		return
	}
	if revoked || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer fdh_") {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"unknown host"}`)
		return
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/host/pairings":
		var req struct{ Kind string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		url := f.URL + "/pair#fdp_code"
		if req.Kind == remote.KindHost {
			url = ""
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"code": "fdp_code", "url": url, "expiresAt": "2099-01-01T00:10:00Z"})
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/host/devices":
		_, _ = io.WriteString(w, `{"devices":[{"id":"d1","name":"phone","created":"2030-01-01T00:00:00Z","lastSeen":"2030-01-01T00:00:00Z"}],`+
			`"hosts":[{"id":"h-desk","name":"desk","online":true,"self":true}]}`)
	case r.Method == http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeRelayAPI) saw(call string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == call {
			return true
		}
	}
	return false
}

// runRemoteCmd drives the subcommand with no running instance to tell, and
// returns what it printed and how many times it would have told one.
func runRemoteCmd(t *testing.T, args ...string) (string, int, error) {
	t.Helper()
	var out bytes.Buffer
	reloads := 0
	err := remoteCmd(args, remoteIO{out: &out, reload: func() (bool, error) { reloads++; return true, nil }})
	return out.String(), reloads, err
}

// The whole life of an enrolment, from the command line.
func TestRemoteLifecycle(t *testing.T) {
	isolateKeys(t)
	t.Setenv(remote.RelayEnv, "")
	f := newFakeRelayAPI(t)

	out, reloads, err := runRemoteCmd(t, "status")
	if err != nil || !strings.Contains(out, "not enabled") {
		t.Fatalf("status before enabling = %q, %v", out, err)
	}
	// The relay enabling would use is the choice it makes, so it is shown first.
	if !strings.Contains(out, "relay:   "+remote.DefaultRelay+", unless enable is given -relay") {
		t.Errorf("status before enabling does not say which relay enable would use: %q", out)
	}

	out, reloads, err = runRemoteCmd(t, "enable", "-relay", f.URL, "-name", "desk")
	if err != nil {
		t.Fatalf("enable: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"desk"`) || reloads != 1 || !strings.Contains(out, "picked this up") || !strings.Contains(out, "Remote access… in the window") {
		t.Errorf("enable printed %q and told the instance %d times", out, reloads)
	}
	cfg, err := remote.Load()
	if err != nil || cfg == nil || cfg.HostID != "h-desk" || cfg.Token != "fdh_desk" || cfg.Relay != f.URL {
		t.Fatalf("enable saved %+v, %v", cfg, err)
	}
	if strings.Contains(out, cfg.Token) {
		t.Error("enable printed the host token")
	}

	// A second enrolment over a live one would orphan the first.
	if _, _, err := runRemoteCmd(t, "enable", "-relay", f.URL, "-name", "other"); err == nil ||
		!strings.Contains(err.Error(), "already enabled") {
		t.Errorf("enabling twice = %v, want it refused", err)
	}
	// Naming another relay is asking to move there, and the refusal says how.
	if _, _, err := runRemoteCmd(t, "enable", "-relay", "https://other.example"); err == nil ||
		!strings.Contains(err.Error(), "to move this machine to https://other.example, run `flockdeck remote disable`, then `flockdeck remote enable -relay https://other.example`") {
		t.Errorf("enabling with another relay = %v, want it to say how to move", err)
	}

	out, _, err = runRemoteCmd(t, "pair")
	if err != nil || !strings.Contains(out, f.URL+"/pair#fdp_code") || !strings.Contains(out, "█") {
		t.Errorf("pair = %q, %v; want the link and a QR code", out, err)
	}
	// A light terminal draws the code inverted, so it says where one that
	// scans can be had.
	if !strings.Contains(out, "Remote access… in the window shows it dark on white") {
		t.Errorf("pair does not say where a code that scans can be had: %q", out)
	}
	// It is read in a terminal, which is often 80 columns wide.
	for _, l := range strings.Split(out, "\n") {
		if n := len([]rune(l)); n > 80 {
			t.Errorf("pair printed a line %d wide: %q", n, l)
		}
	}
	out, _, err = runRemoteCmd(t, "pair", "-desktop")
	if err != nil || !strings.Contains(out, "-join fdp_code") || strings.Contains(out, "█") {
		t.Errorf("pair -desktop = %q, %v; want the enable command to run elsewhere", out, err)
	}

	out, _, err = runRemoteCmd(t, "devices")
	if err != nil || !strings.Contains(out, "d1") || !strings.Contains(out, "phone") || !strings.Contains(out, "(this one)") {
		t.Errorf("devices = %q, %v", out, err)
	}
	// The ids are there to be unpaired by, so the list says how.
	if !strings.Contains(out, "flockdeck remote revoke <id>") {
		t.Errorf("devices does not say how to unpair one: %q", out)
	}
	out, _, err = runRemoteCmd(t, "status")
	if err != nil || !strings.Contains(out, "connected") || !strings.Contains(out, "1 paired") {
		t.Errorf("status = %q, %v", out, err)
	}

	if _, _, err := runRemoteCmd(t, "revoke", "d1"); err != nil || !f.saw("DELETE /api/v1/host/devices/d1") {
		t.Errorf("revoke = %v; relay saw it: %v", err, f.saw("DELETE /api/v1/host/devices/d1"))
	}

	out, reloads, err = runRemoteCmd(t, "disable")
	if err != nil || !f.saw("DELETE /api/v1/host") || reloads != 1 {
		t.Errorf("disable = %q, %v, %d reloads", out, err, reloads)
	}
	if cfg, _ := remote.Load(); cfg != nil {
		t.Error("disable left the enrolment behind")
	}
}

// A relay that cannot be told leaves the enrolment alone unless asked, since
// forgetting it here leaves a machine listed there that nothing can remove
// but a paired device.
func TestRemoteDisableNeedsForceWhenTheRelayIsGone(t *testing.T) {
	isolateKeys(t)
	f := newFakeRelayAPI(t)
	if _, _, err := runRemoteCmd(t, "enable", "-relay", f.URL, "-name", "desk"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, _, err := runRemoteCmd(t, "disable"); err == nil || !strings.Contains(err.Error(), "-force") {
		t.Errorf("disable with the relay gone = %v, want it to suggest -force", err)
	}
	if cfg, _ := remote.Load(); cfg == nil {
		t.Fatal("a failed disable forgot the enrolment")
	}
	if _, _, err := runRemoteCmd(t, "disable", "-force"); err != nil {
		t.Errorf("disable -force: %v", err)
	}
	if cfg, _ := remote.Load(); cfg != nil {
		t.Error("disable -force left the enrolment behind")
	}
}

// An enrolment the relay has already forgotten is no reason to refuse a new
// one: that is exactly when enrolling again is right.
func TestRemoteEnableReplacesARevokedEnrolment(t *testing.T) {
	isolateKeys(t)
	f := newFakeRelayAPI(t)
	if _, _, err := runRemoteCmd(t, "enable", "-relay", f.URL, "-name", "desk"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.revoked = true
	f.mu.Unlock()
	// Enabling again is all it takes, so that is all status says to do.
	out, _, err := runRemoteCmd(t, "status")
	if err != nil || !strings.Contains(out, "`flockdeck remote enable` enrols it again") || strings.Contains(out, "-force") {
		t.Errorf("status of a revoked enrolment = %q, %v", out, err)
	}
	for _, args := range [][]string{{"pair"}, {"devices"}, {"revoke", "d1"}} {
		if _, _, err := runRemoteCmd(t, args...); err == nil || !strings.Contains(err.Error(), "`flockdeck remote enable` enrols this machine again") {
			t.Errorf("remote %v on a revoked enrolment = %v, want it to say enable puts it right", args, err)
		}
	}
	out, _, err = runRemoteCmd(t, "enable", "-relay", f.URL, "-name", "again")
	if err != nil || !strings.Contains(out, "no longer knew") {
		t.Fatalf("enable over a revoked enrolment = %q, %v", out, err)
	}
	if cfg, _ := remote.Load(); cfg == nil || cfg.HostID != "h-again" {
		t.Errorf("the new enrolment was not saved: %+v", cfg)
	}
}

// A machine the relay does not see is told that flockdeck connects while it
// runs only when it is not running. When it is, the window says why it has
// not connected, and status says to look there.
func TestRemoteStatusWhenTheRelayDoesNotSeeThisMachine(t *testing.T) {
	isolateKeys(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"devices":[],"hosts":[{"id":"h1","name":"desk","online":false,"self":true}]}`)
	}))
	defer srv.Close()
	if err := (&remote.Config{Relay: srv.URL, HostID: "h1", Token: "fdh_desk", Name: "desk"}).Save(); err != nil {
		t.Fatal(err)
	}
	for running, want := range map[bool]string{
		true:  "flockdeck is running",
		false: "flockdeck connects while it is running",
	} {
		var out bytes.Buffer
		err := remoteCmd([]string{"status"}, remoteIO{out: &out, running: func() bool { return running }})
		if err != nil || !strings.Contains(out.String(), want) {
			t.Errorf("status with flockdeck running=%v = %q, %v; want it to say %q", running, out.String(), err, want)
		}
		// Nothing is paired, so the next step is pairing something.
		if !strings.Contains(out.String(), "`flockdeck remote pair` pairs one") {
			t.Errorf("status with nothing paired does not say how to pair: %q", out.String())
		}
	}
}

func TestRemoteCommandsNeedAnEnrolment(t *testing.T) {
	isolateKeys(t)
	for _, args := range [][]string{{"pair"}, {"devices"}, {"revoke", "d1"}} {
		if _, _, err := runRemoteCmd(t, args...); err == nil || !strings.Contains(err.Error(), "remote enable") {
			t.Errorf("remote %v with no enrolment = %v, want it to say how to enable", args, err)
		}
	}
}

func TestRemoteUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"help"}, {"-h"}} {
		out, _, err := runRemoteCmd(t, args...)
		if err != nil || !strings.Contains(out, "Usage: flockdeck remote") {
			t.Errorf("remote %v = %q, %v", args, out, err)
		}
		// What the relay can see is said plainly, as the README says it.
		if !strings.Contains(out, "To move to another relay") {
			t.Errorf("remote %v does not say how to change relay: %q", args, out)
		}
		if !strings.Contains(out, "decrypts") {
			t.Errorf("remote %v does not say the relay decrypts the traffic: %q", args, out)
		}
	}
	if _, _, err := runRemoteCmd(t, "nonsense"); err == nil {
		t.Error("an unknown command was accepted")
	}
	// Plain HTTP to anywhere but this machine would send the token in the clear.
	isolateKeys(t)
	if _, _, err := runRemoteCmd(t, "enable", "-relay", "http://relay.example"); err == nil {
		t.Error("enable accepted a relay without TLS")
	}
	// -h asks how revoke is used; it is not a device to send the relay.
	if _, _, err := runRemoteCmd(t, "revoke", "-h"); err != nil {
		t.Errorf("remote revoke -h = %v, want its usage", err)
	}
	if _, _, err := runRemoteCmd(t, "revoke"); err == nil {
		t.Error("revoke with no device was accepted")
	}
}
