package tool

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A link that leads out of the pane is refused by every tool, so a listing
// says what it is rather than showing it as a file the model then reaches for.
func TestAListingMarksALinkThatLeadsOutside(t *testing.T) {
	root, outside := newRoot(t), t.TempDir()
	link := filepath.Join(root.Dir(), "elsewhere")
	if runtime.GOOS == "windows" {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, outside).CombinedOutput(); err != nil {
			t.Skipf("cannot make a junction here: %v %s", err, out)
		}
	} else if err := os.Symlink(outside, link); err != nil {
		t.Skipf("cannot make a link here: %v", err)
	}
	write(t, root, "a.txt", "x")

	got, err := call(t, &listDir{root: root}, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "elsewhere  (a link leading outside the working directory)") {
		t.Errorf("the link out was not marked:\n%s", got)
	}
	if !strings.Contains(got, "a.txt") {
		t.Errorf("the ordinary file is missing:\n%s", got)
	}
}
