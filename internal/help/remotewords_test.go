package help

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

const appJSPath = "../webui/assets/app.js"

// remoteBanned are words that describe Flockdeck Remote as something it is not.
// A payment reviewer classed it as a VPN or proxy, and a paired device opening
// the Flockdeck window is not that. The site pages are held to the same list in
// cmd/sitegen. "proxy" and "from anywhere" are left out here because the help
// and the README use them truthfully about agent gateways and the help key.
var remoteBanned = []string{
	"remote access", "vpn", "tunnel", "no ports", "no port ", "remote desktop",
	"remote support", "remote assistance", "remote-assistance",
}

// The old name stays in the Remote page once, so that someone who remembers it
// finds the page, and nowhere else the reader sees.
const oldNameSentence = "Earlier versions called this page Remote access."

func squash(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

func TestRemoteIsNotNamedOrDescribedAsAccessOrATunnel(t *testing.T) {
	check := func(where, text string) {
		t.Helper()
		l := squash(text)
		for _, b := range remoteBanned {
			if strings.Contains(l, b) {
				t.Errorf("%s contains %q", where, b)
			}
		}
	}

	pages, err := Pages()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pages {
		text := p.Text
		if p.Slug == "remote" {
			if !strings.Contains(text, oldNameSentence) {
				t.Errorf("the remote page no longer carries %q", oldNameSentence)
			}
			text = strings.Replace(text, oldNameSentence, "", 1)
		}
		check("help page "+p.Slug, text)
	}

	readme, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	check("the README", string(readme))

	js, err := os.ReadFile(appJSPath)
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	for i, line := range strings.Split(string(js), "\n") {
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, "//") || strings.HasPrefix(s, "*") || strings.HasPrefix(s, "/*") {
			continue // comments are not shown to anyone
		}
		// The palette's search words keep the old name findable. They are
		// typed into a box, never displayed.
		if strings.Contains(s, "words: \"") || strings.HasPrefix(s, "const also =") {
			continue
		}
		check("app.js line "+strconv.Itoa(i+1), line)
	}
}
