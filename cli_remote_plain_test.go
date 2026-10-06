package main

import (
	"bytes"
	"strings"
	"testing"
	"unicode"

	"github.com/jmwri/flockdeck/internal/remote"
)

// evilJSON is JSON string text holding ESC with a CSI sequence, an OSC title,
// a bidirectional override and a zero-width space.
const evilJSON = `x\u001b[2J\u001b]0;pwned\u0007\u202ey\u200bz`

func assertNoControls(t *testing.T, where, out string) {
	t.Helper()
	for _, r := range out {
		if r == '\n' {
			continue
		}
		if unicode.IsControl(r) || !unicode.IsPrint(r) {
			t.Errorf("%s holds %U:\n%q", where, r, out)
			return
		}
	}
}

// What a relay sends is printed into a terminal, so it is cleaned first: the
// preview of an account deletion, a join's plan, and the printer's own link.
func TestRemoteOutputOfRelayTextHoldsNoControls(t *testing.T) {
	f := enrolledOn(t)
	f.account = `{"desktops":1,"devices":1,"subscribed":false,"email":"j` + evilJSON + `@example.com",` +
		`"plan":{"plan":"trial","name":"` + evilJSON + `","active":true}}`
	out, _, err := runRemoteTyped(t, func(string) string { return "no" }, "delete-account")
	if err == nil {
		t.Fatal("a wrong phrase deleted")
	}
	assertNoControls(t, "delete-account output", out)
	assertNoControls(t, "delete-account error", err.Error())
	if !strings.Contains(out, "x[2J]0;pwnedyz") {
		t.Errorf("the relay's printable words were lost:\n%s", out)
	}

	// A subscribed preview, whose words come from the relay too.
	f.account = `{"desktops":1,"devices":0,"subscribed":true,"plan":{"plan":"subscribed","name":"` + evilJSON + `","active":true}}`
	out, _, _ = runRemoteTyped(t, func(string) string { return "no" }, "delete-account")
	assertNoControls(t, "subscribed preview", out)

	// A refusal on the delete.
	f.account = ""
	f.mu.Lock()
	f.deleteStatus, f.deleteError = 409, evilJSON
	f.mu.Unlock()
	_, _, err = runRemoteTyped(t, func(string) string { return deleteAccountWords }, "delete-account")
	if err == nil {
		t.Fatal("a refused delete returned nil")
	}
	assertNoControls(t, "the 409 on delete", err.Error())
}

func TestEnableWithHostileRelayTextHoldsNoControls(t *testing.T) {
	isolateKeys(t)
	f := newFakeRelayAPI(t)
	f.requireVerify = true
	f.matchCode = `K7QM\u001b[2J42XD`
	f.setVerified(true)
	f.hostsReply = `{"hostId":"h-NAME","accountId":"a9","token":"fdh_NAME","joined":true,"desktops":2,` +
		`"plan":{"plan":"subscribed","name":"` + evilJSON + `","active":true,"ends":"2027-03-12T12:00:00Z"}}`
	out, _, err := runRemoteCmd(t, "enable", "-relay", f.URL, "-name", "desk")
	if err != nil {
		t.Fatalf("enable: %v\n%s", err, out)
	}
	assertNoControls(t, "enable output", out)
	if strings.Contains(out, "confirmation code") {
		t.Errorf("a match code that is not a code was printed:\n%s", out)
	}
	if !strings.Contains(out, "x[2J]0;pwnedyz until 12 March 2027") {
		t.Errorf("the join line lost the plan's printable words:\n%s", out)
	}
}

func TestVerifyEventPrinterAndPlanPhraseCleanTheirInput(t *testing.T) {
	stubBrowser(t, nil)
	var out bytes.Buffer
	verifyEventPrinter(&out)(remote.VerifyEvent{URL: "https://relay.example/auth/verify#fdv", MatchCode: "K7QM-42XD"})
	assertNoControls(t, "printer output", out.String())
	got := planPhrase(&remote.Plan{Plan: "trial", Name: "A\x1b[31mB\u202e", Active: true})
	if got != "A[31mB" {
		t.Errorf("planPhrase = %q", got)
	}
	if got := orUnnamed("a\x1b]0;t\x07b"); got != "a]0;tb" {
		t.Errorf("orUnnamed = %q", got)
	}
	if got := orUnnamed("\x1b\u202e"); got != "(unnamed)" {
		t.Errorf("orUnnamed of nothing printable = %q", got)
	}
}
