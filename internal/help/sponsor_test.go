package help

import (
	"os"
	"strings"
	"testing"
)

// The help's Settings page says where the sponsor line is and that it buys
// nothing, and the README says the same beside the licence: sponsoring is a
// thank-you, not a way to buy features, support or priority.
func TestTheHelpAndTheREADMESaySponsoringBuysNothing(t *testing.T) {
	pages, err := Pages()
	if err != nil {
		t.Fatal(err)
	}
	var settings Page
	for _, p := range pages {
		if p.Slug == "settings" {
			settings = p
		}
	}
	for _, want := range []string{"Sponsor Flockdeck", "Sponsoring buys nothing"} {
		if !strings.Contains(settings.Text, want) {
			t.Errorf("the settings page does not say %q", want)
		}
	}
	if !strings.Contains(settings.HTML, `<a href="https://github.com/sponsors/`) {
		t.Error("the settings page does not link GitHub Sponsors")
	}

	b, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	readme := strings.Join(strings.Fields(string(b)), " ")
	for _, want := range []string{"## Sponsoring", "(https://github.com/sponsors/", "It buys no features, support or priority"} {
		if !strings.Contains(readme, want) {
			t.Errorf("the README does not have %q", want)
		}
	}
}
