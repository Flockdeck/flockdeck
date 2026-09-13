package tool

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A path that is not a regular file -- NUL on Windows, where a write vanishes
// and is reported as written; a named pipe elsewhere, which a read waits on
// forever -- is refused by the file tools rather than read, written or edited.
func TestTheFileToolsRefuseWhatIsNotARegularFile(t *testing.T) {
	root := newRoot(t)
	name := "NUL"
	if runtime.GOOS != "windows" {
		name = "pipe"
		if err := exec.Command("mkfifo", filepath.Join(root.Dir(), name)).Run(); err != nil {
			t.Skipf("cannot make a named pipe here: %v", err)
		}
	}

	for _, c := range []struct {
		tool Tool
		args map[string]any
	}{
		{&readFile{root: root}, map[string]any{"path": name}},
		{&writeFile{root: root}, map[string]any{"path": name, "content": "gone"}},
		{&editFile{root: root}, map[string]any{"path": name, "old_string": "a", "new_string": "b"}},
	} {
		if q := c.tool.Approval(rawArgs(t, c.args)); q != "" {
			t.Errorf("%s: asked %q about something it will refuse", c.tool.Name(), q)
		}
		_, err := call(t, c.tool, c.args)
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Errorf("%s %s: %v", c.tool.Name(), name, err)
		}
	}
}

// In a directory write_file has yet to make there is nothing to look at, so a
// device's name there looked like a new file: newdir/NUL was asked about as
// one, and once newdir was made the write went to the device and was reported
// as written. Windows keeps the names, so they are refused by name.
func TestAWriteToADeviceNameInANewDirectoryIsRefused(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the device names are Windows'")
	}
	root := newRoot(t)
	w := &writeFile{root: root}
	for _, name := range []string{"newdir/NUL", "other/nul.", "third/con.txt", "fourth/COM1"} {
		args := map[string]any{"path": name, "content": "gone"}
		if q := w.Approval(rawArgs(t, args)); q != "" {
			t.Errorf("asked %q about a write it will refuse", q)
		}
		out, err := call(t, w, args)
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Errorf("write_file %s = %q, %v; want a refusal", name, out, err)
		}
	}
}
