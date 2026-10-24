package server

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/webui"
)

// testConfig is the server's configuration in tests: the front end the program
// embeds. internal/server itself does not import it (see
// TestServerDoesNotImportTheFrontEnd), so only the tests do.
func testConfig() Config { return Config{Static: webui.FS()} }

// The server is given its front end and does not import it, so the UI can move
// out of the module without the server changing.
func TestServerDoesNotImportTheFrontEnd(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	for _, pkg := range strings.Fields(string(out)) {
		if strings.HasSuffix(pkg, "/internal/webui") || strings.Contains(pkg, "/internal/webui/") {
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
