package tool

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A link to a directory inside the pane is listed as the directory it leads
// to, rather than as a file of a few bytes the model then tries to read.
func TestAListingShowsALinkToADirectoryAsADirectory(t *testing.T) {
	root := newRoot(t)
	write(t, root, "packages/real/index.js", "x")
	target := filepath.Join(root.Dir(), "packages", "real")
	link := filepath.Join(root.Dir(), "linked")
	if runtime.GOOS == "windows" {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
			t.Skipf("cannot make a junction here: %v %s", err, out)
		}
	} else if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot make a link here: %v", err)
	}

	got, err := call(t, &listDir{root: root}, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "\nlinked/\n") {
		t.Errorf("the link to a directory is not listed as one:\n%s", got)
	}
	if !strings.HasPrefix(got, "the working directory: 2 directories, 0 files") {
		t.Errorf("the link is not counted among the directories:\n%s", got)
	}
}
