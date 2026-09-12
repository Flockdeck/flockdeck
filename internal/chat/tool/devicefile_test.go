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
