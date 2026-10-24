package server

import (
	"github.com/jmwri/flockdeck/internal/keytable"
	"os/exec"
	"strings"
	"testing"

	webui "github.com/jmwri/flockdeck/ui"
	"github.com/jmwri/flockdeck/ui/help"
)

// testConfig is the server's configuration in tests: the front end the program
// embeds. internal/server itself does not import it (see
// TestServerDoesNotImportTheFrontEnd), so only the tests do.
func testConfig() Config {
	return Config{Static: webui.FS(), Keys: keytable.From(help.Keys), HelpJSON: help.JSON}
}

// The server is given its front end and does not import it, so the UI can move
// out of the module without the server changing.
func TestServerDoesNotImportTheFrontEnd(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	for _, pkg := range strings.Fields(string(out)) {
		if pkg == "github.com/jmwri/flockdeck/ui" || strings.HasPrefix(pkg, "github.com/jmwri/flockdeck/ui/") {
			t.Errorf("internal/server depends on %s", pkg)
		}
	}
}

// With no front end given, nothing opens.
func TestNoStaticMeansNoFiles(t *testing.T) {
	s := &Server{}
	if _, err := s.staticFS().Open("index.html"); err == nil {
		t.Error("a server without a front end opened index.html")
	}
}
