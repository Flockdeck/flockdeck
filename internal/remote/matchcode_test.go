package remote

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// The match code is made by the relay and only shown here, so what this side
// owns is how it is written down for a person.
func TestFormatMatchCode(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"K7QM42XD", "K7QM-42XD"},
		{"  K7QM42XD\n", "K7QM-42XD"},
		{"K7QM-42XD", "K7QM-42XD"},
		{"K7QM 42XD", "K7QM 42XD"},
		{"K7QM42X", "K7QM42X"},
		{"K7QM42XDE", "K7QM42XDE"},
		{"", ""},
	} {
		if got := FormatMatchCode(tc.in); got != tc.want {
			t.Errorf("FormatMatchCode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Starting always asks for a match code. A relay from before match codes
// ignores the field and sends none back, and the wait then goes on exactly as
// it did, with no code to show.
func TestStartAsksForAMatchCodeAndToleratesARelayThatSendsNone(t *testing.T) {
	quickPoll(t)
	f := newFakeRelay(t)
	f.requireVerify = true
	f.setVerified(true)

	var events []VerifyEvent
	code, err := VerifyEmail(context.Background(), f.URL, "v", func(ev VerifyEvent) { events = append(events, ev) })
	if err != nil || code != f.verifyCode {
		t.Fatalf("VerifyEmail against a relay with no match codes = %q, %v", code, err)
	}
	f.mu.Lock()
	body := f.startBody
	f.mu.Unlock()
	if strings.ReplaceAll(body, " ", "") != `{"matchCode":true}` {
		t.Errorf("register/start was sent %q, want {\"matchCode\":true}", body)
	}
	if events[0].MatchCode != "" || strings.Contains(events[0].Line, "confirmation code") {
		t.Errorf("first event = %+v, want no match code from a relay that sent none", events[0])
	}
}

// A relay that sends a match code has it shown with the URL, in groups of four.
func TestStartShowsTheRelaysMatchCode(t *testing.T) {
	quickPoll(t)
	f := newFakeRelay(t)
	f.requireVerify = true
	f.matchCode = "K7QM42XD"
	f.setVerified(true)

	var events []VerifyEvent
	if _, err := VerifyEmail(context.Background(), f.URL, "v", func(ev VerifyEvent) { events = append(events, ev) }); err != nil {
		t.Fatal(err)
	}
	first := events[0]
	if first.URL == "" || first.MatchCode != "K7QM-42XD" {
		t.Errorf("first event = %+v, want the URL and the code K7QM-42XD", first)
	}
	if !strings.Contains(first.Line, "K7QM-42XD") {
		t.Errorf("the line %q does not carry the code", first.Line)
	}
	// The code is not part of the URL the browser opens: it is typed, not carried.
	if strings.Contains(first.URL, "K7QM") {
		t.Errorf("the URL %q carries the match code", first.URL)
	}
}

// A relay that refuses to start for want of the match code says so in its own
// words, followed by what to do, whichever way it phrases it.
func TestStartConflictSaysUpdateOrJoin(t *testing.T) {
	for _, tc := range []struct {
		name, relaySays string
		want            []string
		notWant         []string
	}{
		{"no words", "", []string{"will not enrol this version", "Update Flockdeck", "-join"}, nil},
		{"words without a way out", "this relay checks who asked", []string{"this relay checks who asked", "Update Flockdeck", "-join"}, nil},
		{"words with a way out", "update Flockdeck or use -join", []string{"update Flockdeck or use -join"}, []string{"Update Flockdeck and run"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeRelay(t)
			f.requireVerify = true
			f.startStatus, f.startError = http.StatusConflict, tc.relaySays
			_, err := VerifyEmail(context.Background(), f.URL, "v", func(VerifyEvent) {})
			if err == nil {
				t.Fatal("no error from a 409")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("%q does not say %q", err, w)
				}
			}
			for _, w := range tc.notWant {
				if strings.Contains(err.Error(), w) {
					t.Errorf("%q says %q, which repeats the relay", err, w)
				}
			}
			var api *APIError
			if !errors.As(err, &api) || api.Status != http.StatusConflict || !IsVerifyRefusal(err) {
				t.Errorf("the 409 is not kept for errors.As, or not a verify refusal: %v", err)
			}
		})
	}
}

// Five wrong codes on the relay's page delete the registration, which the poll
// then finds gone. The person is told what to do, not shown a bare 404.
func TestWaitSaysWhatAGoneRegistrationMeans(t *testing.T) {
	quickPoll(t)
	f := newFakeRelay(t)
	f.requireVerify = true
	f.goneAfterStart = true
	var last error
	_, err := VerifyEmail(context.Background(), f.URL, "v", func(ev VerifyEvent) {
		if ev.Err != nil {
			last = ev.Err
		}
	})
	for _, got := range []error{err, last} {
		if got == nil || !strings.Contains(got.Error(), "entered wrongly too many times") || !strings.Contains(got.Error(), "flockdeck remote enable") {
			t.Errorf("error = %v, want it to say the code may have been entered wrongly and to run enable again", got)
		}
		var api *APIError
		if !errors.As(got, &api) || api.Status != http.StatusNotFound {
			t.Errorf("the 404 is not kept for errors.As: %v", got)
		}
	}
}

// What a relay says of a join comes through Enable to OnJoined, and a relay
// that makes a new account, or says nothing, never calls it.
func TestEnableReportsAJoin(t *testing.T) {
	isolate(t)
	quickPoll(t)
	f := newFakeRelay(t)
	f.requireVerify = true
	f.matchCode = "K7QM42XD"
	f.setVerified(true)
	f.hostsReply = `{"hostId":"h2","accountId":"a9","token":"fdh_test","joined":true,"desktops":3,` +
		`"plan":{"plan":"subscribed","name":"Subscription","active":true,"ends":"2027-03-12T00:00:00Z"}}`

	var joined []Registration
	cfg, _, err := Enable(context.Background(), "v", EnableRequest{
		Relay: f.URL, Name: "desk", OnVerify: func(VerifyEvent) {},
		OnJoined: func(r Registration) { joined = append(joined, r) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(joined) != 1 || !joined[0].Joined || joined[0].Desktops != 3 || joined[0].Plan == nil || joined[0].Plan.Name != "Subscription" {
		t.Fatalf("OnJoined got %+v, want one join of 3 desktops on Subscription", joined)
	}
	if cfg.AccountID != "a9" || cfg.HostID != "h2" {
		t.Errorf("the enrolment is %+v, want the account the relay put this machine in", cfg)
	}

	// A new account: no join reported.
	if err := Clear(); err != nil {
		t.Fatal(err)
	}
	f.hostsReply = ""
	joined = nil
	if _, _, err := Enable(context.Background(), "v", EnableRequest{
		Relay: f.URL, Name: "desk", OnVerify: func(VerifyEvent) {},
		OnJoined: func(r Registration) { joined = append(joined, r) },
	}); err != nil {
		t.Fatal(err)
	}
	if len(joined) != 0 {
		t.Errorf("OnJoined was called for a new account: %+v", joined)
	}
}

// A relay's refusals of the registration after verifying keep their status, so
// the command line can word them.
func TestRegisterRefusalsKeepTheirStatus(t *testing.T) {
	for _, status := range []int{http.StatusConflict, http.StatusTooManyRequests} {
		isolate(t)
		f := newFakeRelay(t)
		f.hostsStatus, f.hostsError = status, "no room"
		_, _, err := Enable(context.Background(), "v", EnableRequest{Relay: f.URL, Name: "desk"})
		var api *APIError
		if !errors.As(err, &api) || api.Status != status || api.Message != "no room" {
			t.Errorf("%d: Enable = %v, want the relay's refusal", status, err)
		}
		if IsVerifyRefusal(err) {
			t.Errorf("%d: a refusal of the registration is not a verify refusal", status)
		}
	}
}

// The preview and the deletion are the two calls delete-account makes.
func TestAccountPreviewAndDelete(t *testing.T) {
	f := newFakeRelay(t)
	f.accountPreview = `{"desktops":3,"devices":2,"subscribed":true,"email":"j***@example.com",` +
		`"plan":{"plan":"subscribed","name":"Subscription","active":true}}`
	cfg := f.config()
	c := NewClient(&cfg, "v")

	p, err := c.AccountPreview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p.Desktops != 3 || p.Devices != 2 || !p.Subscribed || p.Email != "j***@example.com" || p.Plan == nil || p.Plan.Name != "Subscription" {
		t.Errorf("preview = %+v", p)
	}
	if f.deleted != 0 {
		t.Error("previewing deleted something")
	}

	f.accountPreview = ""
	d, err := c.DeleteAccount(context.Background())
	if err != nil || !d.Deleted || d.Desktops != 2 || d.Devices != 1 {
		t.Fatalf("DeleteAccount = %+v, %v", d, err)
	}
	if f.deleted != 1 {
		t.Errorf("the relay saw %d deletions, want 1", f.deleted)
	}

	// 409 comes back in the relay's words.
	f.accountStatus, f.accountError = http.StatusConflict, "this account has a subscription"
	_, err = c.DeleteAccount(context.Background())
	var api *APIError
	if !errors.As(err, &api) || api.Status != http.StatusConflict || !strings.Contains(err.Error(), "this account has a subscription") {
		t.Errorf("DeleteAccount on a subscribed account = %v, want the relay's 409", err)
	}

	// A relay from before account deletion has no such route.
	f.accountStatus, f.accountError = 0, ""
	old := newFakeRelay(t)
	oc := NewClient(&Config{Relay: old.URL, Token: old.token}, "v")
	old.accountStatus, old.accountError = http.StatusNotFound, "no such thing"
	if _, err := oc.AccountPreview(context.Background()); !errors.As(err, &api) || api.Status != http.StatusNotFound {
		t.Errorf("preview on a relay without the route = %v, want a 404", err)
	}
}
