package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/remote"
)

// The match code is printed before the link is opened, in plain words that say
// where to enter it and that it is not in the email.
func TestVerifyEventPrinterShowsTheMatchCode(t *testing.T) {
	stubBrowser(t, nil)
	var out bytes.Buffer
	verifyEventPrinter(&out)(remote.VerifyEvent{URL: "http://127.0.0.1:1/auth/verify#fdv_test", MatchCode: "K7QM-42XD"})
	got := out.String()
	for _, want := range []string{
		"needs a verified email before it will add a machine",
		"Your confirmation code is K7QM-42XD.",
		"Enter it on the page that opens from the email link.",
		"It is not in the email",
		"if you started this just now",
		"Waiting for it to be verified",
	} {
		if !strings.Contains(squashSpaces(got), want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "K7QM-42XD") > strings.Index(got, "/auth/verify#") {
		t.Errorf("the code comes after the link, so it may scroll away or be missed:\n%s", got)
	}
	for _, line := range strings.Split(got, "\n") {
		if len([]rune(line)) > 80 {
			t.Errorf("line over 80 columns: %q", line)
		}
	}
}

// A relay that makes no match code gets the old output, with no word of one.
func TestVerifyEventPrinterWithoutAMatchCode(t *testing.T) {
	stubBrowser(t, nil)
	var out bytes.Buffer
	verifyEventPrinter(&out)(remote.VerifyEvent{URL: "http://127.0.0.1:1/auth/verify#fdv_test"})
	if strings.Contains(strings.ToLower(out.String()), "code") {
		t.Errorf("a relay with no match code printed a word of one:\n%s", out.String())
	}
}

func squashSpaces(s string) string { return strings.Join(strings.Fields(s), " ") }

// Enrolling on a relay that makes match codes prints the code, and when the
// relay says the email already had an account, says this machine joined it.
func TestRemoteEnableShowsTheMatchCodeAndTheJoin(t *testing.T) {
	isolateKeys(t)
	f := newFakeRelayAPI(t)
	f.requireVerify = true
	f.matchCode = "K7QM42XD"
	f.setVerified(true)
	f.hostsReply = `{"hostId":"h-NAME","accountId":"a9","token":"fdh_NAME","joined":true,"desktops":3,` +
		`"plan":{"plan":"subscribed","name":"Subscription","active":true,"ends":"2027-03-12T12:00:00Z"}}`

	out, _, err := runRemoteCmd(t, "enable", "-relay", f.URL, "-name", "Studio")
	if err != nil {
		t.Fatalf("enable: %v\n%s", err, out)
	}
	flat := squashSpaces(out)
	for _, want := range []string{
		"Your confirmation code is K7QM-42XD.",
		`Flockdeck Remote enabled: this machine is "Studio"`,
		"That email already had a Flockdeck Remote account, so this machine joined it: 3 desktops, Subscription until 12 March 2027.",
		"Devices already paired with the account reach this machine too.",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("enable printed no %q:\n%s", want, out)
		}
	}
	// Joining by email is not joining with a code.
	if strings.Contains(out, "Joined the other machine's account") {
		t.Errorf("enable by email said it joined with a code:\n%s", out)
	}
	cfg, err := remote.Load()
	if err != nil || cfg == nil || cfg.AccountID != "a9" {
		t.Fatalf("saved %+v, %v; want the account the relay put this machine in", cfg, err)
	}
}

// A relay that has not got joining yet, or no match codes, prints neither.
func TestRemoteEnableOnARelayWithNeitherPrintsNeither(t *testing.T) {
	isolateKeys(t)
	f := newFakeRelayAPI(t)
	f.requireVerify = true
	f.setVerified(true)
	out, _, err := runRemoteCmd(t, "enable", "-relay", f.URL, "-name", "desk")
	if err != nil {
		t.Fatalf("enable: %v\n%s", err, out)
	}
	for _, not := range []string{"confirmation code", "joined it", "already paired"} {
		if strings.Contains(out, not) {
			t.Errorf("a relay with no match code or join printed %q:\n%s", not, out)
		}
	}
}

// What the relay's refusals of enrolling come out as.
func TestRemoteEnableWordsTheNewRefusals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(f *fakeRelayAPI)
		want  []string
		not   []string
	}{
		{
			name: "start 409, in the relay's words",
			setup: func(f *fakeRelayAPI) {
				f.requireVerify, f.startStatus, f.startError = true, 409, "update Flockdeck or use -join"
			},
			want: []string{"update Flockdeck or use -join"},
			not:  []string{"Update Flockdeck and run"},
		},
		{
			name:  "start 409, no words",
			setup: func(f *fakeRelayAPI) { f.requireVerify, f.startStatus = true, 409 },
			want:  []string{"Update Flockdeck", "-join"},
		},
		{
			name:  "start 429",
			setup: func(f *fakeRelayAPI) { f.requireVerify, f.startStatus, f.startError = true, 429, "x" },
			want:  []string{"too many verification attempts", "Wait a while"},
			not:   []string{"limiting how fast"},
		},
		{
			name: "account full",
			setup: func(f *fakeRelayAPI) {
				f.hostsStatus = 409
				f.hostsError = "this account already has 10 desktops"
			},
			want: []string{"this account already has 10 desktops", "flockdeck remote remove"},
		},
		{
			name:  "account full, no words",
			setup: func(f *fakeRelayAPI) { f.hostsStatus = 409 },
			want:  []string{"flockdeck remote remove"},
		},
		{
			name:  "joined too often, no words",
			setup: func(f *fakeRelayAPI) { f.hostsStatus = 429 },
			want:  []string{"limiting how fast machines can be added", "try again later"},
		},
		{
			name: "joined too often, in the relay's words",
			setup: func(f *fakeRelayAPI) {
				f.hostsStatus, f.hostsError = 429, "too many desktops joined this account today; try again in 3 hours"
			},
			want: []string{"too many desktops joined this account today; try again in 3 hours"},
			not:  []string{"limiting how fast"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateKeys(t)
			f := newFakeRelayAPI(t)
			f.setVerified(true)
			tc.setup(f)
			_, _, err := runRemoteCmd(t, "enable", "-relay", f.URL, "-name", "desk")
			if err == nil {
				t.Fatal("enable succeeded")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("%q does not say %q", err, w)
				}
			}
			for _, w := range tc.not {
				if strings.Contains(err.Error(), w) {
					t.Errorf("%q says %q", err, w)
				}
			}
			if cfg, _ := remote.Load(); cfg != nil {
				t.Errorf("a refused enrolment saved %+v", cfg)
			}
		})
	}
}

func TestPlanPhrase(t *testing.T) {
	ends := time.Date(2027, 3, 12, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		in   *remote.Plan
		want string
	}{
		{"none", nil, ""},
		{"subscription", &remote.Plan{Plan: "subscribed", Name: "Subscription", Active: true, Ends: ends}, "Subscription until 12 March 2027"},
		{"trial", &remote.Plan{Plan: "trial", Name: "Free trial", Active: true, Ends: ends}, "Free trial until 12 March 2027"},
		{"no end", &remote.Plan{Plan: "subscribed", Name: "Subscription", Active: true}, "Subscription"},
		{"no name", &remote.Plan{Plan: "trial", Active: true, Ends: ends}, "trial until 12 March 2027"},
		{"lapsed", &remote.Plan{Plan: "lapsed", Name: "Lapsed", Ends: ends}, "its plan has run out"},
		{"empty", &remote.Plan{}, ""},
	} {
		if got := planPhrase(tc.in); got != tc.want {
			t.Errorf("%s: planPhrase = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestPrintJoinedWithoutCounts(t *testing.T) {
	var out bytes.Buffer
	printJoined(&out, remote.Registration{Joined: true})
	if got := squashSpaces(out.String()); !strings.HasPrefix(got, "That email already had a Flockdeck Remote account, so this machine joined it. Devices already paired") {
		t.Errorf("printJoined with nothing to add = %q", got)
	}
}
