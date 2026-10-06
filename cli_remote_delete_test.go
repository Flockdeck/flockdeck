package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/remote"
)

// runRemoteTyped drives the subcommand as somebody at a terminal who types
// what typed returns, when asked to type anything.
func runRemoteTyped(t *testing.T, typed func(prompt string) string, args ...string) (string, int, error) {
	t.Helper()
	var out bytes.Buffer
	reloads := 0
	err := remoteCmd(args, remoteIO{out: &out, typed: typed, reload: func() (bool, error) { reloads++; return true, nil }})
	return out.String(), reloads, err
}

// enrolledOn enrols this machine on a fake relay, ready to delete its account.
func enrolledOn(t *testing.T) *fakeRelayAPI {
	t.Helper()
	isolateKeys(t)
	f := newFakeRelayAPI(t)
	if _, _, err := runRemoteCmd(t, "enable", "-relay", f.URL, "-name", "desk"); err != nil {
		t.Fatal(err)
	}
	return f
}

func stillEnrolled(t *testing.T) bool {
	t.Helper()
	cfg, err := remote.Load()
	if err != nil {
		t.Fatal(err)
	}
	return cfg != nil
}

func (f *fakeRelayAPI) deleted() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.deletes
}

func TestRemoteDeleteAccountAsksForThePhraseThenDeletes(t *testing.T) {
	f := enrolledOn(t)
	f.account = `{"desktops":2,"devices":1,"subscribed":false,"email":"j***@example.com",` +
		`"plan":{"plan":"trial","name":"Free trial","active":true,"ends":"2027-03-12T12:00:00Z"}}`

	var prompts []string
	out, reloads, err := runRemoteTyped(t, func(p string) string {
		prompts = append(prompts, p)
		return "  delete my account \n"
	}, "delete-account")
	if err != nil {
		t.Fatalf("delete-account: %v\n%s", err, out)
	}
	if len(prompts) != 1 || !strings.Contains(prompts[0], "`delete my account`") {
		t.Errorf("asked %q, want one prompt naming the phrase", prompts)
	}
	flat := squashSpaces(out)
	for _, want := range []string{
		"This deletes the Flockdeck Remote account for j***@example.com on " + f.URL,
		"2 machines, this one included, which lose their enrolment",
		"1 paired device, which are unpaired",
		"the plan: Free trial until 12 March 2027",
		"the link between the verified email and the account",
		"Records of payments are kept for tax",
		"The account was deleted: 2 machines and 1 paired device are gone.",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	// What goes is said before it goes.
	if strings.Index(out, "This deletes") > strings.Index(out, "was deleted") {
		t.Errorf("the account was reported deleted before it was said what goes:\n%s", out)
	}
	if f.deleted() != 1 {
		t.Errorf("the relay saw %d deletions, want 1", f.deleted())
	}
	if stillEnrolled(t) {
		t.Error("the enrolment is still here after the account was deleted")
	}
	if reloads != 1 {
		t.Errorf("a running instance was told %d times, want once", reloads)
	}
	for _, line := range strings.Split(out, "\n") {
		if len([]rune(line)) > 80 {
			t.Errorf("line over 80 columns: %q", line)
		}
	}
}

func TestRemoteDeleteAccountWithoutTheRightPhraseDeletesNothing(t *testing.T) {
	f := enrolledOn(t)
	// Anything but the exact phrase is an error, so a script's exit status says
	// nothing was deleted, including the right words in another case or spacing.
	for _, typed := range []string{"yes", "Delete My Account", "delete  my account", "delete my account now", ""} {
		_, reloads, err := runRemoteTyped(t, func(string) string { return typed }, "delete-account")
		if err == nil || !strings.Contains(err.Error(), "nothing has been deleted") {
			t.Errorf("typing %q = %v; want an error saying nothing was deleted", typed, err)
		}
		if f.deleted() != 0 || !stillEnrolled(t) || reloads != 0 {
			t.Errorf("typing %q deleted %d accounts, enrolled=%v, reloads=%d", typed, f.deleted(), stillEnrolled(t), reloads)
		}
	}
}

// Piped into, with no flags, there is nobody to type the phrase.
func TestRemoteDeleteAccountWithNobodyToAsk(t *testing.T) {
	f := enrolledOn(t)
	_, _, err := runRemoteCmd(t, "delete-account")
	if err == nil || !strings.Contains(err.Error(), "nothing has been deleted") || !strings.Contains(err.Error(), "-yes -confirm") {
		t.Errorf("delete-account with no terminal = %v, want it to say nothing was deleted and what a script gives", err)
	}
	if f.deleted() != 0 || !stillEnrolled(t) {
		t.Error("something was deleted with nobody to ask")
	}
}

// A script gives both -yes and the phrase. Either alone, or another phrase, is
// refused before the relay is asked anything.
func TestRemoteDeleteAccountScriptNeedsBothFlags(t *testing.T) {
	f := enrolledOn(t)
	for _, args := range [][]string{
		{"delete-account", "-yes"},
		{"delete-account", "-confirm", "delete my account"},
		{"delete-account", "-yes", "-confirm", "delete"},
		{"delete-account", "-yes", "-confirm", ""},
	} {
		f.mu.Lock()
		f.calls = nil
		f.mu.Unlock()
		_, _, err := runRemoteCmd(t, args...)
		if err == nil || !strings.Contains(err.Error(), "-yes and -confirm go together") || !strings.Contains(err.Error(), "nothing has been deleted") {
			t.Errorf("%v = %v, want it refused", args, err)
		}
		if f.saw("GET /api/v1/host/account") || f.deleted() != 0 || !stillEnrolled(t) {
			t.Errorf("%v reached the relay or deleted something", args)
		}
	}

	out, _, err := runRemoteCmd(t, "delete-account", "-yes", "-confirm", "delete my account")
	if err != nil || !strings.Contains(out, "The account was deleted") {
		t.Fatalf("delete-account -yes -confirm = %q, %v", out, err)
	}
	if f.deleted() != 1 || stillEnrolled(t) {
		t.Errorf("deleted=%d enrolled=%v after a script's delete", f.deleted(), stillEnrolled(t))
	}
}

// An account with a subscription is said so, and the person is not asked to
// type a phrase for something the relay will refuse.
func TestRemoteDeleteAccountSubscribedIsNotOffered(t *testing.T) {
	f := enrolledOn(t)
	f.account = `{"desktops":1,"devices":0,"subscribed":true,"plan":{"plan":"subscribed","name":"Subscription","active":true}}`
	out, _, err := runRemoteTyped(t, func(string) string {
		t.Error("asked for the phrase on a subscribed account")
		return deleteAccountWords
	}, "delete-account")
	if err == nil || err.Error() != "nothing has been deleted" {
		t.Errorf("err = %v, want \"nothing has been deleted\"", err)
	}
	flat := squashSpaces(out)
	for _, want := range []string{"has a subscription", "does not cancel it", "customer portal", "run this again", "the plan: Subscription"} {
		if !strings.Contains(flat, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "for ") && strings.Contains(out, "@") {
		t.Errorf("an account with no email printed one:\n%s", out)
	}
	if f.deleted() != 0 || !stillEnrolled(t) {
		t.Error("a subscribed account was deleted, or the enrolment cleared")
	}
}

// The relay's 409 on the delete itself, for a subscription taken out between
// the preview and the delete, is shown in its words and clears nothing.
func TestRemoteDeleteAccountShowsTheRelays409(t *testing.T) {
	f := enrolledOn(t)
	// The preview works; the delete is refused.
	f.mu.Lock()
	f.deleteStatus, f.deleteError = 409, "this account has a subscription, and deleting the account does not cancel it"
	f.mu.Unlock()
	_, reloads, err := runRemoteTyped(t, func(string) string { return deleteAccountWords }, "delete-account")
	if err == nil || !strings.Contains(err.Error(), "the relay said: this account has a subscription, and deleting the account does not cancel it") ||
		!strings.Contains(err.Error(), "nothing has been deleted") {
		t.Errorf("err = %v, want the relay's words and that nothing was deleted", err)
	}
	if !stillEnrolled(t) || reloads != 0 {
		t.Errorf("after a refusal: enrolled=%v reloads=%d, want the enrolment kept", stillEnrolled(t), reloads)
	}

	// Another failure of the delete is not taken for a refusal: the relay may or
	// may not have done it.
	f.mu.Lock()
	f.deleteStatus, f.deleteError = 500, "the relay could not delete"
	f.mu.Unlock()
	_, _, err = runRemoteTyped(t, func(string) string { return deleteAccountWords }, "delete-account")
	if err == nil || !strings.Contains(err.Error(), "may already be gone") || !strings.Contains(err.Error(), "flockdeck remote remove") || !stillEnrolled(t) {
		t.Errorf("a 500 on the delete = %v, enrolled=%v", err, stillEnrolled(t))
	}
}

// A relay from before account deletion has no such address.
func TestRemoteDeleteAccountOnAnOlderRelay(t *testing.T) {
	for _, status := range []int{404, 405} {
		f := enrolledOn(t)
		f.accountStatus = status
		_, _, err := runRemoteTyped(t, func(string) string { return deleteAccountWords }, "delete-account")
		if err == nil || !strings.Contains(err.Error(), "older than that") || !strings.Contains(err.Error(), "Nothing has been deleted") {
			t.Errorf("%d: err = %v, want it to say the relay is older", status, err)
		}
		if !stillEnrolled(t) {
			t.Errorf("%d: the enrolment was cleared", status)
		}
		if _, _, err := runRemoteCmd(t, "remove", "-yes"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRemoteDeleteAccountNeedsAnEnrolment(t *testing.T) {
	isolateKeys(t)
	_, _, err := runRemoteCmd(t, "delete-account", "-yes", "-confirm", "delete my account")
	if err == nil || !strings.Contains(err.Error(), "not enabled") {
		t.Errorf("delete-account with nothing enrolled = %v", err)
	}
}

func TestPhraseTyped(t *testing.T) {
	for in, want := range map[string]bool{
		"delete my account":     true,
		"  delete my account\n": true,
		"DELETE my account":     false,
		"delete  my account":    false,
		"delete my":             false,
		"delete my account now": false,
		"yes":                   false,
		"":                      false,
	} {
		if got := phraseTyped(in); got != want {
			t.Errorf("phraseTyped(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestRemoteDeleteAccountIsHelpedAndSuggested(t *testing.T) {
	var out bytes.Buffer
	if err := remoteCmd([]string{"help", "delete-account"}, remoteIO{out: &out}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Usage: flockdeck remote delete-account", "delete my account", "-yes", "-confirm", "subscription"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help lacks %q:\n%s", want, out.String())
		}
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if len([]rune(line)) > 80 {
			t.Errorf("help line over 80 columns: %q", line)
		}
	}
	for _, word := range []string{"delete", "erase"} {
		err := unknownRemote(word)
		if !strings.Contains(err.Error(), "did you mean delete-account?") {
			t.Errorf("%q: %v", word, err)
		}
	}
	var usage bytes.Buffer
	remoteUsage(&usage)
	if !strings.Contains(usage.String(), "delete-account") {
		t.Error("the usage does not list delete-account")
	}
}
