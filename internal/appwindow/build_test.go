package appwindow

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// A real release build sets CGO_ENABLED per platform (cmd/release's build:
// 1 for linux and darwin, whose Wails backends need cgo for WebKitGTK and
// Cocoa; 0 for windows, whose backend reaches WebView2 through raw syscalls
// and needs none). A build constraint here that gave Windows the
// always-fails appwindow_nocgo.go instead of the real appwindow.go would
// compile cleanly either way -- both satisfy every caller's signature -- so
// nothing short of asking the go command which file it actually chose,
// exactly as it would for a real release of each platform, would have caught
// this before a release did. It did once: see appwindow.go's own comment on
// the release that shipped Windows the stub.
func TestReleaseBuildsGetTheRealWindowNotTheStub(t *testing.T) {
	for _, c := range []struct {
		goos, cgo string
	}{
		{"windows", "0"}, // cmd/release's own setting: no cgo needed at all
		{"windows", "1"},
		{"linux", "1"},  // cmd/release's own setting: WebKitGTK needs cgo
		{"darwin", "1"}, // cmd/release's own setting: Cocoa needs cgo
	} {
		t.Run(c.goos+"/cgo="+c.cgo, func(t *testing.T) {
			got := goFiles(t, c.goos, c.cgo)
			if !got["appwindow.go"] || got["appwindow_nocgo.go"] {
				t.Errorf("GOOS=%s CGO_ENABLED=%s builds %v, want appwindow.go alone", c.goos, c.cgo, got)
			}
		})
	}

	// The one build this package's split is actually for: no cgo, and not
	// Windows, which needs none regardless.
	got := goFiles(t, "linux", "0")
	if !got["appwindow_nocgo.go"] || got["appwindow.go"] {
		t.Errorf("GOOS=linux CGO_ENABLED=0 builds %v, want appwindow_nocgo.go alone", got)
	}
}

// goFiles is the base names of the .go files `go list` says GOOS/CGO_ENABLED
// compiles for this package, asking the real go command rather than
// re-implementing its build-constraint evaluation, which is the only way
// this can fail exactly as a real build would.
func goFiles(t *testing.T, goos, cgo string) map[string]bool {
	t.Helper()
	cmd := exec.Command("go", "list", "-f", "{{range .GoFiles}}{{.}}\n{{end}}", ".")
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH=amd64", "CGO_ENABLED="+cgo)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list GOOS=%s CGO_ENABLED=%s: %v", goos, cgo, err)
	}
	files := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			files[line] = true
		}
	}
	return files
}
