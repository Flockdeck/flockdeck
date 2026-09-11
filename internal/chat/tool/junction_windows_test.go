package tool

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A junction is Windows' other kind of link: any user can make one without the
// privilege a symbolic link needs, and it points anywhere on the machine. The
// confinement has to hold through one as it does through a symbolic link.
func TestAJunctionDoesNotLeadOutOfTheRoot(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("the secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(root, "link"), outside).CombinedOutput(); err != nil {
		t.Skipf("cannot make a junction here: %v %s", err, out)
	}
	set, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ tool, args string }{
		{"grep", `{"pattern":"secret"}`},
		{"glob", `{"pattern":"**/*.txt"}`},
		{"read_file", `{"path":"link/secret.txt"}`},
		{"list_dir", `{"path":"link"}`},
		{"write_file", `{"path":"link/planted.txt","content":"x"}`},
	} {
		tool, _ := set.Lookup(c.tool)
		out, err := tool.Run(context.Background(), json.RawMessage(c.args))
		if err == nil && (strings.Contains(out, "the secret") || strings.Contains(out, "secret.txt")) {
			t.Errorf("%s %s reached through the junction:\n%s", c.tool, c.args, out)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "planted.txt")); err == nil {
		t.Error("write_file wrote through the junction to a file outside the root")
	}
}
