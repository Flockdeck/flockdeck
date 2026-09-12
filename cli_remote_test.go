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
	f.mu.Unlock()
	if devices == "" {
		devices = `[{"id":"d1","name":"phone","created":"2030-01-01T00:00:00Z","lastSeen":"2030-01-01T00:00:00Z"}]`
	}
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
		_, _ = io.WriteString(w, `{"devices":`+devices+`,`+
			`"hosts":[{"id":"h-desk","name":"desk","online":true,"self":true}]}`)
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v1/host/devices/") &&
		!strings.Contains(devices, `"id":"`+strings.TrimPrefix(r.URL.Path, "/api/v1/host/devices/")+`"`):
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"there is no such device"}`)
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

// revoke takes a device by its id or its name, and does not guess between two
// devices with one name.
func TestPickDevice(t *testing.T) {
	r := &remote.Roster{Devices: []remote.Device{
		{ID: "d1", Name: "phone"}, {ID: "d2", Name: " Laptop "}, {ID: "d3", Name: "tablet"}, {ID: "d4", Name: "Tablet"}, {ID: "d5"},
	}}
	for _, c := range []struct{ arg, id, name, err string }{
		{arg: "d1", id: "d1", name: "phone"},
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
	// Naming another relay is asking to move there, and the refusal says how.
	if _, _, err := runRemoteCmd(t, "enable", "-relay", "https://other.example"); err == nil ||
		!strings.Contains(err.Error(), "to move this machine to https://other.example, run `flockdeck remote disable`, then `flockdeck remote enable -relay https://other.example`") {
		t.Errorf("enabling with another relay = %v, want it to say how to move", err)
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
	if _, _, err := runRemoteCmd(t, "disable"); err == nil || !strings.Contains(err.Error(), "-force") {
		t.Errorf("disable with the relay gone = %v, want it to suggest -force", err)
	} else if strings.Contains(err.Error(), "paired device") || !strings.Contains(err.Error(), "nothing but this machine can take it off") {
		// No device can remove a machine from the relay; the cost of -force
		// is that it stays listed.
		t.Errorf("disable with the relay gone = %v, want it to give the real cost of -force", err)
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
	if _, _, err := runRemoteCmd(t, "help", "nonsense"); err == nil {
		t.Error("help for an unknown command was accepted")
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

// Moving off a relay that can no longer be reached takes disable -force, and
// the refusal says so, not the plain disable, which would stop there too.
func TestRemoteMoveFromAGoneRelay(t *testing.T) {
	isolateKeys(t)
	f := newFakeRelayAPI(t)
	if _, _, err := runRemoteCmd(t, "enable", "-relay", f.URL, "-name", "desk"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	_, _, err := runRemoteCmd(t, "enable", "-relay", "https://other.example")
	if want := "run `flockdeck remote disable -force`, then `flockdeck remote enable -relay https://other.example`"; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("moving off a relay that is gone = %v, want it to say %q", err, want)
	}
	// Enabling again, with the relay out of reach, is most often a network
	// down for now: trying again comes first, and -force with what it costs.
	_, _, err = runRemoteCmd(t, "enable")
	if err == nil || !strings.Contains(err.Error(), "try again once the relay can be reached") || !strings.Contains(err.Error(), "leaves this machine listed on it") {
		t.Errorf("enabling again with the relay out of reach = %v, want it to say to try again, and what -force costs", err)
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
