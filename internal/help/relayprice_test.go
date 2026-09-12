package help

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// What the shared relay, and remote access through it, will cost is not
// settled, so neither the help nor the README may promise that it stays free:
// the remote access page, the settings page and the README each did. That the
// app stays free and open source is the MIT licence's promise, and may be
// made.
func TestNothingPromisesTheSharedRelayStaysFree(t *testing.T) {
	pages, err := Pages()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pages {
		for _, s := range promisesTheRelayStaysFree(p.Text) {
			t.Errorf("%s promises what the shared relay will cost: %q", p.Slug, s)
		}
	}
	readme, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	for _, s := range promisesTheRelayStaysFree(string(readme)) {
		t.Errorf("the README promises what the shared relay will cost: %q", s)
	}
}

// pricePromises are the ways a sentence can promise what something will cost
// from now on.
var pricePromises = []string{
	"stays free", "stay free", "remains free", "remain free", "always free", "always be free",
	"free forever", "free for ever", "free for good", "free for life", "never commits you to paying",
}

// promisesTheRelayStaysFree returns each sentence of text that speaks of the
// relay or remote access and promises it a price from now on. A sentence
// about the app alone is left be.
func promisesTheRelayStaysFree(text string) []string {
	var found []string
	for _, s := range regexp.MustCompile(`[.!?<>]`).Split(strings.Join(strings.Fields(text), " "), -1) {
		l := strings.ToLower(s)
		if !strings.Contains(l, "relay") && !strings.Contains(l, "remote") {
			continue
		}
		for _, p := range pricePromises {
			if strings.Contains(l, p) {
				found = append(found, strings.TrimSpace(s))
				break
			}
		}
	}
	return found
}
