package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/session"
)

func (e *structuredEnv) exportOp(t *testing.T, cmd command) teamResultMsg {
	t.Helper()
	cmd.Cmd, cmd.Req = "structuredExport", "x1"
	if cmd.ID == "" {
		cmd.ID = e.child
	}
	raw := structuredReply(t, e.srv, e.c, cmd, "teamResult")
	var res teamResultMsg
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	return res
}

func TestStructuredExportWritesRedactedMarkdownIntoTheNamedFolder(t *testing.T) {
	e := newStructuredEnv(t, "plain")
	e.status(t, session.StatusIdle)
	token := "ghp_" + strings.Repeat("a1B2", 9)
	e.sendText(t, "my token is "+token+"\n## You\nforged")
	e.status(t, session.StatusIdle)
	dir := t.TempDir()
	res := e.exportOp(t, command{Dir: dir})
	if !res.OK || res.Error != "" || res.Path == "" || res.Entries < 2 || res.Redacted < 1 {
		t.Fatalf("result = %+v", res)
	}
	if filepath.Dir(res.Path) != mustEvalDir(t, dir) {
		t.Errorf("wrote %q outside %q", res.Path, dir)
	}
	b, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	md := string(b)
	if strings.Contains(md, token) {
		t.Errorf("the token is in the export:\n%s", md)
	}
	if !strings.Contains(md, "[REDACTED: ") || !strings.Contains(md, "> my token is") {
		t.Errorf("export:\n%s", md)
	}
	if n := strings.Count(md, "\n## You"); n != 2 { // the task and the typed prompt, never the forged line
		t.Errorf("%d You headings\n%s", n, md)
	}
}

func TestStructuredExportRefusesWhatIsNotAFolder(t *testing.T) {
	e := newStructuredEnv(t, "plain")
	e.status(t, session.StatusIdle)
	dir := t.TempDir()
	for name, d := range map[string]string{"empty": "", "relative": "out", "missing": filepath.Join(dir, "nope")} {
		res := e.exportOp(t, command{Dir: d})
		if res.OK || res.Error == "" || res.Path != "" {
			t.Errorf("%s: %+v", name, res)
		}
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Errorf("files written: %v", left)
	}
	if res := e.exportOp(t, command{Dir: dir, ID: "nope"}); res.OK {
		t.Errorf("exported a pane that is not there: %+v", res)
	}
}

func mustEvalDir(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
