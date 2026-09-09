package webui

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/jmwri/agent-wrapper/internal/help"
)

// readAsset returns one of the embedded front-end files.
func readAsset(t *testing.T, name string) string {
	t.Helper()
	data, err := fs.ReadFile(FS(), name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

// The palette, the keyboard and the help pages are all drawn from the key
// table, so an action the table names and the front end has not implemented
// would be listed and then do nothing when it was picked.
func TestFrontEndImplementsEveryAction(t *testing.T) {
	app := readAsset(t, "app.js")

	start := strings.Index(app, "const ACTIONS = {")
	if start < 0 {
		t.Fatal("app.js no longer has an ACTIONS table")
	}
	end := strings.Index(app[start:], "\n  };")
	if end < 0 {
		t.Fatal("the ACTIONS table in app.js is not terminated as expected")
	}
	block := app[start : start+end]

	for _, k := range help.Keys {
		if !strings.Contains(block, "\n    "+k.ID+":") {
			t.Errorf("action %q (%s) is in the key table but not implemented in app.js", k.ID, k.Label)
		}
	}
}

// A binding written into the interface by hand is the drift this whole
// arrangement exists to prevent: tooltips, hints and the palette are all
// filled in from the table once it arrives, so no binding the table owns
// should appear in the front end at all.
func TestNoBindingsAreWrittenIntoTheInterface(t *testing.T) {
	// A comment may talk about a key; nothing a user reads may.
	comment := regexp.MustCompile(`^\s*(//|\*|/\*|<!--)`)

	for _, name := range []string{"app.js", "index.html", "app.css"} {
		for i, line := range strings.Split(readAsset(t, name), "\n") {
			if comment.MatchString(line) {
				continue
			}
			for _, k := range help.Keys {
				if k.Keys == "" || !strings.Contains(line, k.Keys) {
					continue
				}
				t.Errorf("%s:%d writes %q out by hand; take it from the key table instead:\n\t%s",
					name, i+1, k.Keys, strings.TrimSpace(line))
			}
		}
	}
}
