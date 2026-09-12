package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

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
	// devices, when set, is the roster's devices, as JSON, in place of the
	// one phone.
	devices string
	// selfName, when set, is what the relay calls this machine, in place of
	// "desk": the name a paired device gave it.
	selfName string
	// renames is each rename asked for, as its path and the name sent, and
	// noRename answers one as a relay from before renaming does.
	renames  []string
	noRename bool
}

func (f *fakeRelayAPI) renamed(call string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.renames {
		if c == call {
			return true
		}
	}
	return false
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
	devices := f.devices
	selfName, noRename := f.selfName, f.noRename
	f.mu.Unlock()
	if devices == "" {
		devices = `[{"id":"d1","name":"phone","created":"2030-01-01T00:00:00Z","lastSeen":"2030-01-01T00:00:00Z"}]`
	}
	if selfName == "" {
		selfName = "desk"
	}
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/healthz" {
		// As a relay answers anybody asking whether it is there.
		_, _ = io.WriteString(w, "ok\n")
		return
	}
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
		_, _ = io.WriteString(w, `{"devices":`+devices+`,`+
			`"hosts":[{"id":"h-desk","name":"`+selfName+`","online":true,"self":true,"url":"`+f.URL+`/h/h-desk/"}]}`)
	case r.Method == http.MethodPatch && noRename:
		// As a relay from before renaming answers: the path is one it has,
		// for another method.
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = io.WriteString(w, `{"error":"PATCH is not something this address takes"}`)
	case (r.Method == http.MethodDelete || r.Method == http.MethodPatch) && strings.HasPrefix(r.URL.Path, "/api/v1/host/devices/") &&
		!strings.Contains(devices, `"id":"`+strings.TrimPrefix(r.URL.Path, "/api/v1/host/devices/")+`"`):
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"there is no such device"}`)
	case r.Method == http.MethodPatch:
		var req struct{ Name string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		f.renames = append(f.renames, r.URL.Path+" "+req.Name)
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
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

// A value status cannot know until it prints it is broken to fit 80 columns,
// carrying on under the value, with every word kept and in order.
func TestLabelled(t *testing.T) {
	const long = "could not ask the relay: reach the relay at http://127.0.0.1:60421: nothing is answering there (the connection was refused); check the address, and that the relay is running"
	for _, c := range []struct{ label, value, first string }{
		{"state:", long, "state:   could not ask the relay:"},
		{"machine:", "desk (h1)", "machine: desk (h1)"},
		{"relay:", strings.Repeat("x", 90), "relay:   " + strings.Repeat("x", 90)},
	} {
		got := labelled(c.label, c.value)
		lines := strings.Split(got, "\n")
		if !strings.HasPrefix(lines[0], c.first) {
			t.Errorf("labelled(%q, …) begins %q, want %q", c.label, lines[0], c.first)
		}
		for i, l := range lines {
			// A line may run over only when it holds one word of the value,
			// too long to break; the label is not one of them.
			value := l
			if i == 0 {
				value = strings.TrimPrefix(l, c.label)
			}
			if n := len([]rune(l)); n > 80 && len(strings.Fields(value)) > 1 {
				t.Errorf("labelled(%q, …) line %d is %d wide: %q", c.label, i, n, l)
			}
			if i > 0 && !strings.HasPrefix(l, strings.Repeat(" ", 9)) {
				t.Errorf("labelled(%q, …) line %d does not carry on under the value: %q", c.label, i, l)
			}
		}
		if words := strings.Fields(got)[1:]; strings.Join(words, " ") != strings.Join(strings.Fields(c.value), " ") {
			t.Errorf("labelled(%q, …) lost or moved words: %q", c.label, got)
		}
	}
}

// status names the paired devices, as many as fit, and says how many more.
func TestPairedSummary(t *testing.T) {
	devices := func(names ...string) []remote.Device {
		ds := make([]remote.Device, len(names))
		for i, n := range names {
			ds[i] = remote.Device{ID: string(rune('a' + i)), Name: n}
		}
		return ds
	}
	for _, c := range []struct {
		ds    []remote.Device
		width int
		want  string
	}{
		{devices("phone"), 71, "1 paired: phone"},
		{devices("phone", "laptop", "tablet"), 71, "3 paired: phone, laptop, tablet"},
		{devices("phone", "laptop", "tablet"), 26, "3 paired: phone and 2 more"},
		{devices(" "), 71, "1 paired: (unnamed)"},
		{devices("phone", "laptop"), 10, "2 paired"},
	} {
		if got := pairedSummary(c.ds, c.width); got != c.want {
			t.Errorf("pairedSummary(%d devices, %d) = %q, want %q", len(c.ds), c.width, got, c.want)
		}
		if n := len([]rune(pairedSummary(c.ds, c.width))); n > c.width && c.width >= len("N paired") {
			t.Errorf("pairedSummary(%d devices, %d) is %d wide", len(c.ds), c.width, n)
		}
	}
}

// revoke takes a device by its id or its name, and does not guess between two
// devices with one name.
func TestPickDevice(t *testing.T) {
	r := &remote.Roster{Devices: []remote.Device{
		{ID: "d1", Name: "phone"}, {ID: "d2", Name: " Laptop "}, {ID: "d3", Name: "tablet"}, {ID: "d4", Name: "Tablet"}, {ID: "d5"},
	}}
	for _, c := range []struct{ arg, id, name, err string }{
		{arg: "d1", id: "d1", name: "phone"},
		{arg: "D1", id: "d1", name: "phone"}, // an id typed in capitals
		{arg: "laptop", id: "d2", name: "Laptop"},
		{arg: "d5", id: "d5"},
		{arg: "d9", id: "d9"},
		{arg: "", id: ""},
		{arg: "tablet", err: `2 devices are called "tablet"; say which by its id: d3, d4`},
	} {
		id, name, err := pickDevice(r, c.arg)
		if id != c.id || name != c.name || (err == nil) != (c.err == "") || (err != nil && err.Error() != c.err) {
			t.Errorf("pickDevice(%q) = %q, %q, %v; want %q, %q, %q", c.arg, id, name, err, c.id, c.name, c.err)
		}
	}
	// Without a roster, it is an id for the relay to judge.
	if id, name, err := pickDevice(nil, "phone"); id != "phone" || name != "" || err != nil {
		t.Errorf("pickDevice without a roster = %q, %q, %v", id, name, err)
	}
}

// revoke given one of the account's machines, by name or id, says what it is
// and what takes a machine off, rather than sending it as a device; a device
// with the same name as a machine is still the device.
func TestPickDeviceRefusesAMachine(t *testing.T) {
	r := &remote.Roster{
		Devices: []remote.Device{{ID: "d1", Name: "phone"}, {ID: "d2", Name: "study"}},
		Hosts:   []remote.Host{{ID: "h1", Name: "desk", Self: true}, {ID: "h2", Name: "old laptop"}, {ID: "h3", Name: "study"}},
	}
	for arg, want := range map[string]string{
		"desk":       "that is this machine, not a device; `flockdeck remote disable` takes it off the relay",
		"H1":         "that is this machine, not a device",
		"Old Laptop": `"old laptop" is another of the account's machines, not a device; to take it off, remove it from the Devices page of a paired device, or run ` + "`flockdeck remote disable`" + ` on it`,
		"h2":         `"old laptop" is another of the account's machines`,
	} {
		if _, _, err := pickDevice(r, arg); err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("pickDevice(%q) = %v, want it to begin %q", arg, err, want)
		}
	}
	if id, _, err := pickDevice(r, "study"); id != "d2" || err != nil {
		t.Errorf("pickDevice(study), a device and a machine's name = %q, %v; want the device, d2", id, err)
	}
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
	} else if !strings.Contains(err.Error(), "unpairs its devices if it is the account's only machine") {
		t.Errorf("enabling twice = %v, want it to say what enrolling again costs", err)
	}
	// A join code on an enrolled machine is asking for another account, not
	// something with nothing to do.
	if _, _, err := runRemoteCmd(t, "enable", "-relay", f.URL, "-join", "fdp_other"); err == nil ||
		!strings.Contains(err.Error(), "to join that account instead") || strings.Contains(err.Error(), "nothing to do") {
		t.Errorf("enabling with a join code over an enrolment = %v, want it to say how to join", err)
	}
	// Naming another relay, one that is there, is asking to move to it, and
	// the refusal says how.
	other := newFakeRelayAPI(t)
	if _, _, err := runRemoteCmd(t, "enable", "-relay", other.URL); err == nil ||
		!strings.Contains(err.Error(), "to move this machine to "+other.URL+", run `flockdeck remote move "+other.URL+"`") ||
		!strings.Contains(err.Error(), "pair again") {
		t.Errorf("enabling with another relay = %v, want it to say how to move, and what it costs", err)
	}

	out, _, err = runRemoteCmd(t, "pair")
	if err != nil || !strings.Contains(out, f.URL+"/pair#fdp_code") || !strings.Contains(out, "█") {
		t.Errorf("pair = %q, %v; want the link and a QR code", out, err)
	}
	// The code comes last, so a terminal too short for everything pair says
	// still shows all of it at the bottom, and the link before it.
	if lines := strings.Split(strings.TrimRight(out, "\n"), "\n"); !strings.ContainsAny(lines[len(lines)-1], "█▀▄") ||
		strings.Index(out, "/pair#fdp_code") > strings.Index(out, "█") {
		t.Errorf("pair does not end with the code, after the link: %q", out)
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
	// What joining does is said before the code goes anywhere.
	if !strings.Contains(out, "a device paired with either reaches both") {
		t.Errorf("pair -desktop does not say what joining does: %q", out)
	}

	out, _, err = runRemoteCmd(t, "devices")
	if err != nil || !strings.Contains(out, "d1") || !strings.Contains(out, "phone") || !strings.Contains(out, "(this one)") {
		t.Errorf("devices = %q, %v", out, err)
	}
	// The ids and names are there to be unpaired by, so the list says how.
	if !strings.Contains(out, "flockdeck remote revoke <id or name>") {
		t.Errorf("devices does not say how to unpair one: %q", out)
	}
	out, _, err = runRemoteCmd(t, "status")
	if err != nil || !strings.Contains(out, "connected") || !strings.Contains(out, "1 paired") {
		t.Errorf("status = %q, %v", out, err)
	}
	// Where a paired device opens this machine is worth knowing without one.
	if !strings.Contains(out, "open at: "+f.URL+"/h/h-desk/\n") {
		t.Errorf("status does not say where a device opens this machine: %q", out)
	}
	// The devices are named, for a glance.
	if !strings.Contains(out, "devices: 1 paired: phone\n") {
		t.Errorf("status does not name the paired device: %q", out)
	}

	out, _, err = runRemoteCmd(t, "revoke", "d1")
	if err != nil || !f.saw("DELETE /api/v1/host/devices/d1") {
		t.Errorf("revoke = %v; relay saw it: %v", err, f.saw("DELETE /api/v1/host/devices/d1"))
	}
	// The name is what says the right device went.
	if !strings.Contains(out, "unpaired phone (d1)") {
		t.Errorf("revoke does not name the device it unpaired: %q", out)
	}
	// The name the list shows unpairs it too.
	f.mu.Lock()
	f.calls = nil
	f.mu.Unlock()
	out, _, err = runRemoteCmd(t, "revoke", "Phone")
	if err != nil || !f.saw("DELETE /api/v1/host/devices/d1") || !strings.Contains(out, "unpaired phone (d1)") {
		t.Errorf("revoke by name = %q, %v; relay saw it: %v", out, err, f.saw("DELETE /api/v1/host/devices/d1"))
	}

	out, reloads, err = runRemoteCmd(t, "disable")
	if err != nil || !f.saw("DELETE /api/v1/host") || reloads != 1 {
		t.Errorf("disable = %q, %v, %d reloads", out, err, reloads)
	}
	// The account's only machine takes its devices with it, and says so.
	if !strings.Contains(out, "its paired device has been unpaired too") {
		t.Errorf("disable of the account's only machine does not say its device went too: %q", out)
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
	_, _, err := runRemoteCmd(t, "disable")
	if err == nil || !strings.Contains(err.Error(), "-force") {
		t.Errorf("disable with the relay gone = %v, want it to suggest -force", err)
	} else if !strings.Contains(err.Error(), "list this machine, offline, until it is removed from the Devices page of a paired device") {
		// The cost of -force is that the machine stays listed, until a paired
		// device removes it.
		t.Errorf("disable with the relay gone = %v, want it to give the real cost of -force", err)
	} else if !strings.Contains(err.Error(), "try again once the relay can be reached, or, if it is gone for good, run again with -force") {
		// A relay out of reach is most often a network down for now, and
		// -force is the step nothing can undo.
		t.Errorf("disable with the relay gone = %v, want it to say to try again before forcing", err)
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

// A device paired while flockdeck is not running here finds the machine
// offline, so pair says so before the link is opened.
func TestRemotePairWhileNotRunning(t *testing.T) {
	isolateKeys(t)
	f := newFakeRelayAPI(t)
	if _, _, err := runRemoteCmd(t, "enable", "-relay", f.URL, "-name", "desk"); err != nil {
		t.Fatal(err)
	}
	for running, want := range map[bool]bool{false: true, true: false} {
		var out bytes.Buffer
		if err := remoteCmd([]string{"pair"}, remoteIO{out: &out, running: func() bool { return running }}); err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(out.String(), "this machine is offline until it starts"); got != want {
			t.Errorf("pair with flockdeck running=%v said it is offline: %v, want %v", running, got, want)
		}
	}
}

// devices says why this machine is offline when the reason is here to see:
// flockdeck is not running.
func TestRemoteDevicesWhileNotRunning(t *testing.T) {
	isolateKeys(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"devices":[],"hosts":[{"id":"h1","name":"desk","online":false,"self":true}]}`)
	}))
	defer srv.Close()
	if err := (&remote.Config{Relay: srv.URL, HostID: "h1", Token: "fdh_desk", Name: "desk"}).Save(); err != nil {
		t.Fatal(err)
	}
	for running, want := range map[bool]bool{false: true, true: false} {
		var out bytes.Buffer
		if err := remoteCmd([]string{"devices"}, remoteIO{out: &out, running: func() bool { return running }}); err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(out.String(), "desk (this one) — offline — flockdeck is not running here"); got != want {
			t.Errorf("devices with flockdeck running=%v said why it is offline: %v, want %v\n%s", running, got, want, out.String())
		}
	}
}

// An enrolment that cannot be used stops every command that needs it, and
// each says that disable -force clears it, which is less to do than finding
// the file to delete.
func TestRemoteCommandsOnAnUnusableEnrolment(t *testing.T) {
	isolateKeys(t)
	// One Load refuses (195): a relay off this machine without TLS.
	if err := (&remote.Config{Relay: "http://relay.example", HostID: "h1", Token: "fdh_desk"}).Save(); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"status"}, {"pair"}, {"devices"}, {"revoke", "d1"}} {
		if _, _, err := runRemoteCmd(t, args...); err == nil || !strings.HasSuffix(err.Error(), "(`flockdeck remote disable -force` removes it)") {
			t.Errorf("remote %v on an unusable enrolment = %v, want it to say disable -force removes it", args, err)
		}
	}
}

// disable -force with nothing enrolled has nothing to turn off, and says so
// rather than that remote access has been disabled.
func TestRemoteDisableForceWithNothingEnrolled(t *testing.T) {
	isolateKeys(t)
	out, reloads, err := runRemoteCmd(t, "disable", "-force")
	if err != nil || !strings.Contains(out, "remote access is not enabled") || strings.Contains(out, "disabled") || reloads != 0 {
		t.Errorf("disable -force with nothing enrolled = %q, %v, telling the instance %d times; want it to say remote access is not enabled", out, err, reloads)
	}
}

// A relay that answers with an error was reached; status says it could not
// be asked, rather than that it could not be reached.
func TestRemoteStatusWhenTheRelayErrs(t *testing.T) {
	isolateKeys(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	if err := (&remote.Config{Relay: srv.URL, HostID: "h1", Token: "fdh_desk", Name: "desk"}).Save(); err != nil {
		t.Fatal(err)
	}
	out, _, err := runRemoteCmd(t, "status")
	if err != nil || !strings.Contains(out, "state:   could not ask the relay: the relay answered 503") {
		t.Errorf("status with the relay answering 503 = %q, %v", out, err)
	}
}

// Each subcommand's -h says how it is written and what it is for, not only
// flag's "Usage of remote status:", which for a command with no flags was
// all it printed.
func TestRemoteSubcommandHelp(t *testing.T) {
	for _, name := range []string{"enable", "pair", "status", "devices", "revoke", "disable"} {
		// Through remoteHelp, which is how it is asked for, so that the
		// flags of enable, pair and disable are in it; a bare flag set of
		// the same name has none to show.
		var b bytes.Buffer
		if err := remoteHelp(name, remoteIO{out: &b}); err != nil {
			t.Fatalf("remote help %s: %v", name, err)
		}
		lines := strings.Split(b.String(), "\n")
		if !strings.HasPrefix(lines[0], "Usage: flockdeck remote "+name) || len(lines) < 3 || strings.TrimSpace(lines[2]) == "" {
			t.Errorf("remote %s -h = %q, want its usage line and what it is for", name, b.String())
		}
		for _, l := range lines {
			// A tab takes a terminal on to its next multiple of eight
			// columns, which is where flag starts every description.
			n := 0
			for _, r := range l {
				if r == '\t' {
					n = (n/8 + 1) * 8
				} else {
					n++
				}
			}
			if n > 80 {
				t.Errorf("remote %s -h has a line %d wide: %q", name, n, l)
			}
		}
	}
}

// Every flag of enable names what it takes, as -relay URL and -join code do,
// rather than leaving flag to print the type.
func TestRemoteEnableFlagsNameWhatTheyTake(t *testing.T) {
	var b bytes.Buffer
	fs := remoteEnableFlagSet(&remoteEnableFlags{})
	fs.SetOutput(&b)
	fs.PrintDefaults()
	if strings.Contains(b.String(), " string\n") || !strings.Contains(b.String(), "-name name") {
		t.Errorf("enable's flags = %q, want each to name what it takes", b.String())
	}
}

// The environment wins over the built-in relay whenever it is set, so
// -relay's help puts it first.
func TestRemoteRelayFlagSaysWhatWins(t *testing.T) {
	var b bytes.Buffer
	fs := remoteEnableFlagSet(&remoteEnableFlags{})
	fs.SetOutput(&b)
	fs.PrintDefaults()
	if want := "(default: $" + remote.RelayEnv + " if set, else " + remote.DefaultRelay + ")"; !strings.Contains(b.String(), want) {
		t.Errorf("enable's flags = %q, want -relay to say %q", b.String(), want)
	}
}

// A code's expiry is a clock time when it falls today, and names the day
// when it does not, as a relay with a long pairing time can make it.
func TestDescribeExpiry(t *testing.T) {
	now := time.Date(2030, 1, 1, 12, 0, 0, 0, time.Local)
	for _, tc := range []struct {
		at   time.Time
		want string
	}{
		{now.Add(10 * time.Minute), "until 12:10 (10 minutes from now)"},
		{now.Add(50 * time.Hour), "until Thu 3 Jan 14:00 (2 days from now)"},
		{now.Add(20 * time.Second), "for less than a minute"},
		{time.Time{}, "for a short while"},
	} {
		if got := describeExpiry(tc.at, now); got != tc.want {
			t.Errorf("describeExpiry(%v) = %q, want %q", tc.at.Sub(now), got, tc.want)
		}
	}
}

// A relay that takes new accounts only with an invitation says it needs an
// invite code; enable says which flag takes one.
func TestRemoteEnableNamesTheInviteFlag(t *testing.T) {
	isolateKeys(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":"this relay needs an invite code to register"}`)
	}))
	defer srv.Close()
	_, _, err := runRemoteCmd(t, "enable", "-relay", srv.URL, "-name", "desk")
	if err == nil || !strings.HasSuffix(err.Error(), "pass it with -invite CODE") {
		t.Errorf("enable on a relay that needs an invite = %v, want it to name -invite", err)
	}
}

// `remote help <command>` gives that command's own help, as most commands'
// help does, rather than the general usage again.
func TestRemoteHelpForOneCommand(t *testing.T) {
	out, _, err := runRemoteCmd(t, "help", "disable")
	if err != nil || !strings.HasPrefix(out, "Usage: flockdeck remote disable") || !strings.Contains(out, "-force") {
		t.Errorf("remote help disable = %q, %v; want disable's own help", out, err)
	}
	// Each says what the command shows and takes now: status the address and
	// the devices' names (172, 193), devices that revoke takes names (157).
	for cmd, want := range map[string]string{
		"status":  "the address a paired device opens it at, and which devices are paired",
		"devices": "with the ids and names revoke takes",
	} {
		if out, _, err := runRemoteCmd(t, "help", cmd); err != nil || !strings.Contains(strings.ReplaceAll(out, "\n", " "), want) {
			t.Errorf("remote help %s = %q, %v; want it to say %q", cmd, out, err, want)
		}
	}
	if _, _, err := runRemoteCmd(t, "help", "nonsense"); err == nil {
		t.Error("help for an unknown command was accepted")
	}
}

// What every command prints is read in a terminal, which is often 80 columns
// wide, in each state it can find the enrolment in. The one line exempt is
// the command pair -desktop gives to copy: no break in it would work in
// every shell (175).
func TestRemoteOutputFitsATerminal(t *testing.T) {
	isolateKeys(t)
	t.Setenv(remote.RelayEnv, "")
	f := newFakeRelayAPI(t)
	check := func(running bool, args ...string) {
		t.Helper()
		var out bytes.Buffer
		_ = remoteCmd(args, remoteIO{out: &out, reload: func() (bool, error) { return running, nil }, running: func() bool { return running }})
		for _, l := range strings.Split(out.String(), "\n") {
			if strings.HasPrefix(l, "  flockdeck remote enable ") {
				continue
			}
			if n := len([]rune(l)); n > 80 {
				t.Errorf("remote %v (running=%v) printed a line %d wide: %q", args, running, n, l)
			}
		}
	}
	check(false, "status")
	check(false, "enable", "-relay", f.URL, "-name", "desk")
	for _, running := range []bool{false, true} {
		check(running, "status")
		check(running, "pair")
		check(running, "pair", "-desktop")
		check(running, "devices")
		check(running, "revoke", "d1")
	}
	f.mu.Lock()
	f.revoked = true
	f.mu.Unlock()
	check(true, "status")
	f.mu.Lock()
	f.revoked = false
	f.mu.Unlock()
	check(true, "disable")
	check(false, "status")
	// A relay that does not see this machine, with flockdeck running here
	// and without.
	offline := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"devices":[],"hosts":[{"id":"h1","name":"desk","online":false,"self":true}]}`)
	}))
	defer offline.Close()
	if err := (&remote.Config{Relay: offline.URL, HostID: "h1", Token: "fdh_desk", Name: "desk"}).Save(); err != nil {
		t.Fatal(err)
	}
	for _, running := range []bool{false, true} {
		check(running, "status")
		check(running, "devices")
	}
	// A relay that cannot be reached at all, whose reason is a sentence of
	// its own after status's label.
	gone := httptest.NewServer(http.NotFoundHandler())
	goneURL := gone.URL
	gone.Close()
	if err := (&remote.Config{Relay: goneURL, HostID: "h1", Token: "fdh_desk", Name: "desk"}).Save(); err != nil {
		t.Fatal(err)
	}
	check(false, "status")
	// And, with nothing enrolled, a relay in the environment that cannot be
	// used, whose refusal status gives in place of the relay it would use.
	if err := remote.Clear(); err != nil {
		t.Fatal(err)
	}
	t.Setenv(remote.RelayEnv, "http://relay.example")
	check(false, "status")
	t.Setenv(remote.RelayEnv, "")

	// A name as long as Windows gives a machine by default, a running
	// instance that could not be told, whose reason is a whole error, and a
	// relay gone by the time the machine is taken off it by force.
	told := func(args ...string) {
		t.Helper()
		var out bytes.Buffer
		_ = remoteCmd(args, remoteIO{out: &out, running: func() bool { return true }, reload: func() (bool, error) {
			return true, errors.New(`reload remote access: Post "http://127.0.0.1:52110/remote/reload?t=…": dial tcp 127.0.0.1:52110: connectex: No connection could be made because the target machine actively refused it.`)
		}})
		for _, l := range strings.Split(out.String(), "\n") {
			if n := len([]rune(l)); n > 80 {
				t.Errorf("remote %v printed a line %d wide: %q", args, n, l)
			}
		}
	}
	f2 := newFakeRelayAPI(t)
	told("enable", "-relay", f2.URL, "-name", "DESKTOP-4F2K9LQ")
	f2.Close()
	told("disable", "-force")

	// Names as long as the relay keeps, 64 characters, of several words: a
	// device's, and machines', this one offline.
	long := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"devices":[{"id":"meqf4vmfersvxg4q","name":"the tablet in the kitchen that everybody in the house uses"}],`+
			`"hosts":[{"id":"h1","name":"the workstation under the desk in the study upstairs at home","online":false,"self":true},`+
			`{"id":"h2","name":"the old laptop that lives in the drawer of the hall table now","online":false}]}`)
	}))
	defer long.Close()
	if err := (&remote.Config{Relay: long.URL, HostID: "h1", Token: "fdh_desk", Name: "desk"}).Save(); err != nil {
		t.Fatal(err)
	}
	for _, running := range []bool{false, true} {
		check(running, "devices")
	}
	// revoke's confirmation names the device it unpaired, name and id.
	check(false, "revoke", "meqf4vmfersvxg4q")
}

// The general usage is read in a terminal, which is often 80 columns wide, as
// each command's own help is.
func TestRemoteUsageFitsATerminal(t *testing.T) {
	out, _, err := runRemoteCmd(t)
	if err != nil || !strings.HasPrefix(out, "Usage: flockdeck remote") {
		t.Fatalf("remote with no command = %q, %v", out, err)
	}
	for _, l := range strings.Split(out, "\n") {
		if n := len([]rune(l)); n > 80 {
			t.Errorf("the usage has a line %d wide: %q", n, l)
		}
	}
}

// A device's name of several words, typed without quotes, is taken whole when
// it names a device; several words that do not are answered with the usage.
func TestRemoteRevokeANameOfSeveralWords(t *testing.T) {
	isolateKeys(t)
	f := newFakeRelayAPI(t)
	f.devices = `[{"id":"d7","name":"Chrome on Windows"}]`
	if _, _, err := runRemoteCmd(t, "enable", "-relay", f.URL, "-name", "desk"); err != nil {
		t.Fatal(err)
	}
	out, _, err := runRemoteCmd(t, "revoke", "chrome", "on", "windows")
	if err != nil || !f.saw("DELETE /api/v1/host/devices/d7") || !strings.Contains(out, "unpaired Chrome on Windows (d7)") {
		t.Errorf("revoke chrome on windows = %q, %v; relay saw it: %v", out, err, f.saw("DELETE /api/v1/host/devices/d7"))
	}
	if _, _, err := runRemoteCmd(t, "revoke", "d7", "d8"); err == nil {
		t.Error("revoke of two ids was accepted")
	}
	// Several words that name no device are told so, from the roster, rather
	// than shown the usage, which does not say what went wrong.
	if _, _, err := runRemoteCmd(t, "revoke", "chrome", "on", "windos"); err == nil || !strings.Contains(err.Error(), `no paired device is called "chrome on windos"`) {
		t.Errorf("revoke of a name no device has = %v, want it to say so", err)
	}
	if f.saw("DELETE /api/v1/host/devices/d7 d8") {
		t.Error("two ids were sent to the relay as one")
	}
}

// A command that is not one is answered with the one most likely meant, a
// word for it or a letter or two off, and never run as it.
func TestRemoteUnknownCommandSuggests(t *testing.T) {
	isolateKeys(t)
	for arg, want := range map[string]string{
		"unpair": "revoke", "off": "disable", "on": "enable", "Enable": "enable",
		"stauts": "status", "devcies": "devices", "piar": "pair", "disabel": "disable",
	} {
		for _, args := range [][]string{{arg}, {"help", arg}} {
			if _, _, err := runRemoteCmd(t, args...); err == nil || !strings.HasSuffix(err.Error(), "; did you mean "+want+"?") {
				t.Errorf("remote %v = %v, want it to suggest %s", args, err, want)
			}
		}
	}
	if _, _, err := runRemoteCmd(t, "nonsense"); err == nil || strings.Contains(err.Error(), "did you mean") {
		t.Errorf("remote nonsense = %v, want it refused with no guess", err)
	}
	if cfg, _ := remote.Load(); cfg != nil {
		t.Error("a guessed command was run")
	}
}

// A relay closed to new accounts still takes a machine into an account it
// has, and the refusal says how.
func TestRemoteEnableOnAClosedRelay(t *testing.T) {
	isolateKeys(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":"this relay is not accepting new accounts"}`)
	}))
	defer srv.Close()
	_, _, err := runRemoteCmd(t, "enable", "-relay", srv.URL, "-name", "desk")
	if want := "; a machine already on it can take this one into its account: `flockdeck remote pair -desktop` there prints the command to run here"; err == nil || !strings.HasSuffix(err.Error(), want) {
		t.Errorf("enable on a closed relay = %v, want it to end %q", err, want)
	}
}

// A device the relay does not know is refused in its words, and the refusal
// says where the ones it does know are listed.
func TestRemoteRevokeAnUnknownDevice(t *testing.T) {
	isolateKeys(t)
	f := newFakeRelayAPI(t)
	if _, _, err := runRemoteCmd(t, "enable", "-relay", f.URL, "-name", "desk"); err != nil {
		t.Fatal(err)
	}
	_, _, err := runRemoteCmd(t, "revoke", "d9")
	if want := "there is no such device; `flockdeck remote devices` lists the paired ones, by id and name"; err == nil || !strings.HasSuffix(err.Error(), want) {
		t.Errorf("revoke of an unknown device = %v, want it to end %q", err, want)
	}
	if !f.saw("DELETE /api/v1/host/devices/d9") {
		t.Error("the relay was not asked")
	}
}

// A new name given to an enrolled machine is something to do, and the refusal
// names the command that does it; the name it already has is nothing to do.
func TestRemoteEnableWithANewName(t *testing.T) {
	isolateKeys(t)
	f := newFakeRelayAPI(t)
	if _, _, err := runRemoteCmd(t, "enable", "-relay", f.URL, "-name", "desk"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runRemoteCmd(t, "enable", "-name", "new desk"); err == nil || !strings.Contains(err.Error(), "; to give this machine a new name, run `flockdeck remote rename new desk`, which keeps what is paired") {
		t.Errorf("enable with a new name = %v, want it to name the rename that does it", err)
	}
	if _, _, err := runRemoteCmd(t, "enable", "-name", "desk"); err == nil || !strings.Contains(err.Error(), "nothing to do") || strings.Contains(err.Error(), "rename") {
		t.Errorf("enable with the name it has = %v, want nothing to do", err)
	}
}

// A machine is renamed without enrolling it again: the relay is told, the
// name is saved here as the relay keeps it, and a running instance is told to
// show it. A name of several words needs no quotes.
func TestRemoteRenameThisMachine(t *testing.T) {
	isolateKeys(t)
	f := newFakeRelayAPI(t)
	if _, _, err := runRemoteCmd(t, "enable", "-relay", f.URL, "-name", "desk"); err != nil {
		t.Fatal(err)
	}
	out, reloads, err := runRemoteCmd(t, "rename", "Work", "PC ")
	if err != nil || !f.renamed("/api/v1/host Work PC") {
		t.Fatalf("rename Work PC = %q, %v; the relay was asked %v", out, err, f.renames)
	}
	if !strings.Contains(out, `this machine is now "Work PC" on every paired device`) || reloads != 1 {
		t.Errorf("rename printed %q and told the instance %d times", out, reloads)
	}
	if cfg, _ := remote.Load(); cfg == nil || cfg.Name != "Work PC" || cfg.HostID != "h-desk" || cfg.Token != "fdh_desk" {
		t.Errorf("rename saved %+v, want the new name and the same enrolment", cfg)
	}
	// No name is the usage, not a request.
	if _, _, err := runRemoteCmd(t, "rename"); err == nil {
		t.Error("rename with no name was accepted")
	}
	// One of the relay's codes is not a name, and is not sent as one.
	if _, _, err := runRemoteCmd(t, "rename", "fdp_code"); err == nil || !strings.Contains(err.Error(), "not a name") {
		t.Errorf("rename to a code = %v, want it refused", err)
	}
	// A relay from before renaming says so, rather than only refusing.
	f.mu.Lock()
	f.noRename = true
	f.mu.Unlock()
	if _, _, err := runRemoteCmd(t, "rename", "desk"); err == nil || !strings.HasSuffix(err.Error(), "this relay is older than renaming, so a new name has to wait until it is updated") {
		t.Errorf("rename on an older relay = %v, want it to say the relay is older", err)
	}
	if cfg, _ := remote.Load(); cfg == nil || cfg.Name != "Work PC" {
		t.Errorf("a refused rename saved %+v", cfg)
	}
}

// A device is renamed by its id or its name, with -device before or after the
// new name; a machine given as the device, or a device the relay does not
// know, is answered with where to look.
func TestRemoteRenameADevice(t *testing.T) {
	isolateKeys(t)
	f := newFakeRelayAPI(t)
	if _, _, err := runRemoteCmd(t, "enable", "-relay", f.URL, "-name", "desk"); err != nil {
		t.Fatal(err)
	}
	out, _, err := runRemoteCmd(t, "rename", "Sam's", "phone", "-device", "Phone")
	if err != nil || !f.renamed("/api/v1/host/devices/d1 Sam's phone") || !strings.Contains(out, `renamed phone (d1) to "Sam's phone"`) {
		t.Errorf("rename -device Phone = %q, %v; the relay was asked %v", out, err, f.renames)
	}
	if _, _, err := runRemoteCmd(t, "rename", "-device", "d1", "phone"); err != nil || !f.renamed("/api/v1/host/devices/d1 phone") {
		t.Errorf("rename -device d1 = %v; the relay was asked %v", err, f.renames)
	}
	if _, _, err := runRemoteCmd(t, "rename", "-device", "desk", "x"); err == nil || !strings.Contains(err.Error(), "without -device, renames it") {
		t.Errorf("rename -device of this machine = %v, want it to say how this machine is renamed", err)
	}
	if _, _, err := runRemoteCmd(t, "rename", "-device", "d9", "x"); err == nil || !strings.HasSuffix(err.Error(), "`flockdeck remote devices` lists the paired ones, by id and name") {
		t.Errorf("rename of an unknown device = %v, want it to say where the devices are listed", err)
	}
}

// A machine renamed from a paired device is shown by status under its new
// name, which the relay has and the enrolment here does not.
func TestRemoteStatusUsesTheRelaysName(t *testing.T) {
	isolateKeys(t)
	f := newFakeRelayAPI(t)
	if _, _, err := runRemoteCmd(t, "enable", "-relay", f.URL, "-name", "desk"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.selfName = "Work PC"
	f.mu.Unlock()
	out, _, err := runRemoteCmd(t, "status")
	if err != nil || !strings.Contains(out, "machine: Work PC (h-desk)\n") {
		t.Errorf("status = %q, %v; want the name the relay has", out, err)
	}
}

// Enabling with a join code says the machine has joined the other's account,
// which is what the code was for; enabling without one does not.
func TestRemoteEnableByJoiningSaysSo(t *testing.T) {
	isolateKeys(t)
	f := newFakeRelayAPI(t)
	const joined = "Joined the other machine's account: a device paired with either reaches both.\n"
	out, _, err := runRemoteCmd(t, "enable", "-relay", f.URL, "-name", "desk", "-join", "fdp_code")
	if err != nil || !strings.Contains(out, joined) {
		t.Errorf("enable -join = %q, %v; want it to say it joined the other machine's account", out, err)
	}
	if _, _, err := runRemoteCmd(t, "disable"); err != nil {
		t.Fatal(err)
	}
	if out, _, err := runRemoteCmd(t, "enable", "-relay", f.URL, "-name", "desk"); err != nil || strings.Contains(out, "Joined") {
		t.Errorf("enable without a join code = %q, %v; want no word of joining", out, err)
	}
}

// An invitation that has been used, or was never one, is refused by the relay
// in its own words, and the refusal says who can make another.
func TestRemoteEnableWithASpentInvitation(t *testing.T) {
	isolateKeys(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":"that invite code is not valid, or has already been used"}`)
	}))
	defer srv.Close()
	_, _, err := runRemoteCmd(t, "enable", "-relay", srv.URL, "-name", "desk", "-invite", "fdi_code")
	if want := "already been used; whoever runs the relay makes invitations, and can make another"; err == nil || !strings.HasSuffix(err.Error(), want) {
		t.Errorf("enabling with a spent invitation = %v, want it to end %q", err, want)
	}
}

// A join code that has run out or been used is refused by the relay in its
// own words, and the refusal says where a new one comes from.
func TestRemoteJoinWithASpentCode(t *testing.T) {
	isolateKeys(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"that join code is not valid, or has expired or already been used"}`)
	}))
	defer srv.Close()
	_, _, err := runRemoteCmd(t, "enable", "-relay", srv.URL, "-join", "fdp_code")
	if want := "already been used; `flockdeck remote pair -desktop` on the other machine makes a new one"; err == nil || !strings.HasSuffix(err.Error(), want) {
		t.Errorf("joining with a spent code = %v, want it to end %q", err, want)
	}
}

// Joining an account that has all the machines it may is refused by the
// relay in its own words, and the refusal says which command here does what
// they ask.
func TestRemoteJoinAFullAccount(t *testing.T) {
	isolateKeys(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error":"this account already has as many desktops as it may; unregister one first"}`)
	}))
	defer srv.Close()
	_, _, err := runRemoteCmd(t, "enable", "-relay", srv.URL, "-join", "fdp_code")
	if want := "; running `flockdeck remote disable` on one of that account's machines does that"; err == nil || !strings.HasSuffix(err.Error(), want) {
		t.Errorf("joining a full account = %v, want it to end %q", err, want)
	}
}

// Moving to a relay that cannot be reached is found out before the machine
// leaves the relay it is on, which is the first step of moving, and nothing
// is said of taking it.
func TestRemoteMoveToAnUnreachableRelay(t *testing.T) {
	isolateKeys(t)
	f := newFakeRelayAPI(t)
	if _, _, err := runRemoteCmd(t, "enable", "-relay", f.URL, "-name", "desk"); err != nil {
		t.Fatal(err)
	}
	gone := httptest.NewServer(http.NotFoundHandler())
	goneURL := gone.URL
	gone.Close()
	_, _, err := runRemoteCmd(t, "enable", "-relay", goneURL)
	if err == nil || !strings.Contains(err.Error(), goneURL+" could not be reached") || strings.Contains(err.Error(), "run `flockdeck remote disable") {
		t.Errorf("moving to a relay that cannot be reached = %v, want it found out before any disabling", err)
	}
}

// Moving off a relay that can no longer be reached is move's to do as well:
// enable points at it, and move goes ahead, saying the old relay was not told.
func TestRemoteMoveFromAGoneRelay(t *testing.T) {
	isolateKeys(t)
	f := newFakeRelayAPI(t)
	if _, _, err := runRemoteCmd(t, "enable", "-relay", f.URL, "-name", "desk"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	other := newFakeRelayAPI(t)
	_, _, err := runRemoteCmd(t, "enable", "-relay", other.URL)
	if want := "run `flockdeck remote move " + other.URL + "`"; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("moving off a relay that is gone = %v, want it to say %q", err, want)
	}
	// Enabling again, with the relay out of reach, is most often a network
	// down for now: trying again comes first, and -force with what it costs.
	_, _, err = runRemoteCmd(t, "enable")
	if err == nil || !strings.Contains(err.Error(), "try again once the relay can be reached") || !strings.Contains(err.Error(), "leaves this machine listed on it") {
		t.Errorf("enabling again with the relay out of reach = %v, want it to say to try again, and what -force costs", err)
	}
	out, _, err := runRemoteCmd(t, "move", "-yes", other.URL)
	if err != nil || !strings.Contains(out, "moved: this machine is \"desk\" on "+other.URL) ||
		!strings.Contains(out, f.URL+" could not be told") {
		t.Errorf("moving off a relay that is gone = %q, %v; want it moved, saying the old relay was not told", out, err)
	}
	if cfg, _ := remote.Load(); cfg == nil || cfg.Relay != other.URL {
		t.Errorf("after moving off a relay that is gone, the enrolment is %+v", cfg)
	}
}

// Moving says, before anything is done, that every device will pair again,
// and asks; it enrols with the new relay before leaving the old, and a
// running instance is told in between. Unasked, a script has to say -yes.
func TestRemoteMove(t *testing.T) {
	isolateKeys(t)
	f := newFakeRelayAPI(t)
	if _, _, err := runRemoteCmd(t, "enable", "-relay", f.URL, "-name", "desk"); err != nil {
		t.Fatal(err)
	}
	other := newFakeRelayAPI(t)

	// Nobody at a terminal, and no -yes: nothing is done.
	if _, _, err := runRemoteCmd(t, "move", other.URL); err == nil || !strings.Contains(err.Error(), "-yes") {
		t.Errorf("move without anyone to ask = %v, want it to say -yes", err)
	}
	// Asked, and answered no: nothing is done, and the warning came first.
	var out bytes.Buffer
	var saidFirst string
	err := remoteCmd([]string{"move", other.URL}, remoteIO{out: &out, confirm: func(q string) bool {
		saidFirst = out.String()
		return false
	}})
	if err != nil || !strings.Contains(out.String(), "nothing has changed") {
		t.Errorf("move answered no = %q, %v", out.String(), err)
	}
	if !strings.Contains(saidFirst, "have to pair again") || !strings.Contains(saidFirst, f.URL) || !strings.Contains(saidFirst, other.URL) {
		t.Errorf("before asking, move said %q; want where from, where to, and that every device pairs again", saidFirst)
	}
	// The one paired device goes with the account, the machine being its only one.
	if !strings.Contains(saidFirst, "its paired device with it") {
		t.Errorf("before asking, move said %q; want the account's paired device said to go with it", saidFirst)
	}
	if other.saw("POST /api/v1/hosts") || f.saw("DELETE /api/v1/host") {
		t.Fatal("a move that was not agreed to reached a relay")
	}

	// Answered yes, with the flags after the relay: moved, and told in order.
	out.Reset()
	var order []string
	err = remoteCmd([]string{"move", other.URL, "-name", "Work PC"}, remoteIO{out: &out,
		confirm: func(string) bool { return true },
		reload: func() (bool, error) {
			// The new relay has the machine, and the old one has not yet been
			// told, when a running instance is told to move its tunnel.
			order = append(order, fmt.Sprintf("reload new=%v oldTold=%v", other.saw("GET /api/v1/host/devices"), f.saw("DELETE /api/v1/host")))
			return true, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 1 || order[0] != "reload new=true oldTold=false" {
		t.Errorf("the running instance was told %q; want once, after the new relay answered and before the old one was told", order)
	}
	if !f.saw("DELETE /api/v1/host") {
		t.Error("the old relay was never told the machine had left")
	}
	cfg, err := remote.Load()
	if err != nil || cfg == nil || cfg.Relay != other.URL || cfg.Name != "Work PC" || cfg.Token != "fdh_Work PC" {
		t.Errorf("after moving, the enrolment is %+v, %v", cfg, err)
	}
	for _, want := range []string{"moved: this machine is \"Work PC\" on " + other.URL, "Pair each device again"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("move printed %q; want it to say %q", out.String(), want)
		}
	}
	for _, l := range strings.Split(out.String(), "\n") {
		if n := len([]rune(l)); n > 80 {
			t.Errorf("move printed a line %d wide: %q", n, l)
		}
	}

	// Where it already is, and with nowhere named.
	if _, _, err := runRemoteCmd(t, "move", "-yes", other.URL); err == nil || !strings.Contains(err.Error(), "already on") {
		t.Errorf("moving to the relay it is on = %v", err)
	}
	if _, _, err := runRemoteCmd(t, "move"); !errors.Is(err, errReported) {
		t.Errorf("move with no relay = %v, want its usage", err)
	}
}

// Moving a machine that is not enrolled has nothing to move, and says what
// enrols it there instead; moving to a relay that is not there is found out
// before anybody is asked.
func TestRemoteMoveRefusals(t *testing.T) {
	isolateKeys(t)
	other := newFakeRelayAPI(t)
	if _, _, err := runRemoteCmd(t, "move", "-yes", other.URL); err == nil || !strings.Contains(err.Error(), "`flockdeck remote enable -relay "+other.URL+"`") {
		t.Errorf("moving a machine that is not enrolled = %v", err)
	}
	f := newFakeRelayAPI(t)
	if _, _, err := runRemoteCmd(t, "enable", "-relay", f.URL, "-name", "desk"); err != nil {
		t.Fatal(err)
	}
	gone := httptest.NewServer(http.NotFoundHandler())
	goneURL := gone.URL
	gone.Close()
	asked := false
	err := remoteCmd([]string{"move", goneURL}, remoteIO{out: io.Discard, confirm: func(string) bool { asked = true; return true }})
	if err == nil || !strings.Contains(err.Error(), "nothing has changed") || asked {
		t.Errorf("moving to a relay that is not there = %v (asked: %v); want it refused before asking", err, asked)
	}
	if cfg, _ := remote.Load(); cfg == nil || cfg.Relay != f.URL {
		t.Errorf("a refused move changed the enrolment to %+v", cfg)
	}
}

// A machine enrolled under the hosted relay's old name tells a machine
// joining it the current one; any other relay is passed on as it is.
func TestRelayToJoin(t *testing.T) {
	for relay, want := range map[string]string{
		"https://relay.flockdeck.ai": remote.DefaultRelay,
		remote.DefaultRelay:          remote.DefaultRelay,
		"https://relay.example":      "https://relay.example",
	} {
		if got := relayToJoin(relay); got != want {
			t.Errorf("relayToJoin(%q) = %q, want %q", relay, got, want)
		}
	}
}

// A reload that got no answer comes back naming its URL, the local
// server's token in it, and what is printed keeps the reason but not the
// token.
func TestRedactToken(t *testing.T) {
	const token = "47e24fd0a26dbd27fa21525c7ae2add3"
	msg := `Post "http://127.0.0.1:63149/remote/reload?t=` + token + `": dial tcp 127.0.0.1:63149: connection refused`
	got := redactToken(msg, token)
	if strings.Contains(got, token) || !strings.Contains(got, "/remote/reload?t=…") || !strings.HasSuffix(got, "connection refused") {
		t.Errorf("redactToken = %q, want the token gone and the rest kept", got)
	}
	if got := redactToken(msg, ""); got != msg {
		t.Errorf("redactToken with no token = %q, want the message as it was", got)
	}
	if got := redactToken("reload remote access: 500 Internal Server Error", token); got != "reload remote access: 500 Internal Server Error" {
		t.Errorf("redactToken of a message without the token = %q", got)
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
		if !strings.Contains(out, "flockdeck remote help <command>") {
			t.Errorf("remote %v does not say how to get one command's help: %q", args, out)
		}
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
	// A word too many is answered with which command it was and where its
	// help is, not a bare "unexpected".
	if _, _, err := runRemoteCmd(t, "pair", "phone"); err == nil ||
		!strings.Contains(err.Error(), "remote pair takes no arguments") || !strings.Contains(err.Error(), "`flockdeck remote pair -h`") {
		t.Errorf("remote pair phone = %v, want it to name the command and its help", err)
	}
	// revoke with no id is answered with its own usage, not every command's.
	if out, _, err := runRemoteCmd(t, "revoke"); err == nil {
		t.Error("revoke with no device was accepted")
	} else if strings.Contains(out, "Usage: flockdeck remote <command>") {
		t.Errorf("revoke with no device printed the whole usage: %q", out)
	}
}
