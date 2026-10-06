package help

import (
	"strings"
	"testing"
)

// The relay keeps an account when its last desktop is removed, and only
// erases one on request. The pages that used to say removing deleted it, or
// that a quiet desktop is removed after 30 days, must not say so any more, and
// the command that erases an account must be on both pages that list commands.
func TestPagesSayRemovingADesktopLeavesTheAccount(t *testing.T) {
	pages, err := Pages()
	if err != nil {
		t.Fatal(err)
	}
	text := map[string]string{}
	for _, p := range pages {
		text[p.Slug] = squash(p.Text)
	}
	for _, slug := range []string{"remote", "cli"} {
		if text[slug] == "" {
			t.Fatalf("no %s page", slug)
		}
		if !strings.Contains(text[slug], "flockdeck remote delete-account") {
			t.Errorf("the %s page does not mention flockdeck remote delete-account", slug)
		}
	}
	for _, want := range []string{
		"type delete my account",
		`-yes -confirm "delete my account"`,
		"even if this was its last machine",
		"removing a machine never deletes the account",
		"an account with a subscription is refused",
		"confirmation code",
		"never in the email",
	} {
		if !strings.Contains(text["remote"], want) {
			t.Errorf("the remote page does not say %q", want)
		}
	}
	for _, slug := range []string{"remote", "cli", "settings"} {
		for _, stale := range []string{
			"the account goes too",
			"the account goes with it",
			"account is deleted too",
			"account is removed",
			"without being heard from",
			"is not seen for 30 days",
			"starts a new account, with a new trial",
			"show a join code first",
		} {
			if strings.Contains(text[slug], stale) {
				t.Errorf("the %s page still says %q", slug, stale)
			}
		}
	}
}
