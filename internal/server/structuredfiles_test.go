package server

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/session"
)

// The @ picker lists names under the pane's directory, bounded, without
// following links out of it and without reading any file.

func writeTree(t *testing.T, root string, files ...string) {
	t.Helper()
	for _, f := range files {
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
}

func listed(t *testing.T, dir, q string) []string {
	t.Helper()
	got, _, err := listPaneFiles(dir, q)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestFilesListingHonoursGitignore(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	writeTree(t, dir, "src/main.go", "src/util.go", "README.md", "build/out.bin", "notes.log")
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("build/\n*.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := listed(t, dir, "")
	for _, want := range []string{"src/main.go", "src/util.go", "README.md"} {
		if !slices.Contains(got, want) {
			t.Errorf("%s is not listed: %v", want, got)
		}
	}
	for _, bad := range []string{"build/out.bin", "notes.log"} {
		if slices.Contains(got, bad) {
			t.Errorf("an ignored file is listed: %s", bad)
		}
	}
	if got := listed(t, dir, "MAIN"); len(got) != 1 || got[0] != "src/main.go" {
		t.Errorf("query: %v", got)
	}
}

func TestFilesListingOfASubdirectoryStaysInIt(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	writeTree(t, dir, "top.txt", "sub/in.txt")
	got := listed(t, filepath.Join(dir, "sub"), "")
	if len(got) != 1 || got[0] != "in.txt" {
		t.Errorf("got %v", got)
	}
}

func TestFilesWalkWithoutGitSkipsHiddenAndDependencyDirs(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, "a.txt", "deep/er/b.txt", ".secret/c.txt", "node_modules/d/e.js")
	got := listed(t, dir, "")
	if !slices.Equal(got, []string{"a.txt", "deep/er/b.txt"}) {
		t.Errorf("got %v", got)
	}
}

func TestFilesListingNeverLeavesTheDirectoryThroughALink(t *testing.T) {
	outside := t.TempDir()
	writeTree(t, outside, "secret.txt", "nested/deeper.txt")
	dir := t.TempDir()
	writeTree(t, dir, "ok.txt")
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(dir, "filelink.txt")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "dirlink")); err != nil {
		t.Skipf("no directory symlinks here: %v", err)
	}
	check := func(what string) {
		t.Helper()
		got := listed(t, dir, "")
		if !slices.Equal(got, []string{"ok.txt"}) {
			t.Errorf("%s: %v", what, got)
		}
		if got := listed(t, dir, "secret"); len(got) != 0 {
			t.Errorf("%s: a query found %v", what, got)
		}
	}
	check("walk")
	gitInit(t, dir)
	check("git")
}

func TestFilesCandidatesWithATraversalAreRefused(t *testing.T) {
	outside := t.TempDir()
	writeTree(t, outside, "x.txt")
	dir := t.TempDir()
	writeTree(t, dir, "ok.txt")
	rel, err := filepath.Rel(dir, filepath.Join(outside, "x.txt"))
	if err != nil {
		t.Skip("no relative path on this volume")
	}
	got, _ := pickFiles(dir, []string{filepath.ToSlash(rel), "../x.txt", "a/../../x.txt", "/etc/passwd", "C:/Windows/win.ini", `a\..\x`, "a//b", "./ok.txt", "ok.txt"}, "")
	if !slices.Equal(got, []string{"ok.txt"}) {
		t.Errorf("got %v", got)
	}
}

func TestFilesNamesAreRefusedUnlessPlain(t *testing.T) {
	for _, bad := range []string{"", "a\nb", "a\x1b[31m", "a\x00b", strings.Repeat("x", structuredFilesPathMax+1), "a\xffb"} {
		if plainPath(bad) {
			t.Errorf("%q is plain", bad)
		}
	}
	if !plainPath("dir/name with space.txt") || !plainPath("ünï/cödé.go") {
		t.Error("an ordinary name was refused")
	}
}

func TestFilesAHugeDirectoryIsReadOnlyPartlyAndTheAnswerIsCapped(t *testing.T) {
	old := structuredFilesScanMax
	structuredFilesScanMax = 300
	defer func() { structuredFilesScanMax = old }()
	dir := t.TempDir()
	for i := 0; i < 700; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%04d.txt", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, more, err := listPaneFiles(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != structuredFilesMax || !more {
		t.Errorf("%d names, more %v", len(got), more)
	}
	gitInit(t, dir)
	got, more, err = listPaneFiles(dir, "f0")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != structuredFilesMax || !more {
		t.Errorf("git: %d names, more %v", len(got), more)
	}
}

func TestFilesRankingPutsTheFileNameFirst(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, "docs/server.md", "server/x.go", "internal/server/handler.go", "srv.go")
	got := listed(t, dir, "server")
	if len(got) < 3 || got[0] != "docs/server.md" {
		t.Errorf("got %v", got)
	}
	if got := listed(t, dir, "sg"); !slices.Contains(got, "srv.go") {
		t.Errorf("letters in order: %v", got)
	}
}

func TestFilesNoDirectoryIsAnError(t *testing.T) {
	for _, d := range []string{"", "  ", filepath.Join(t.TempDir(), "gone")} {
		if _, _, err := listPaneFiles(d, ""); err == nil {
			t.Errorf("%q: no error", d)
		}
	}
}

func TestStructuredFilesCommand(t *testing.T) {
	cwd := t.TempDir()
	writeTree(t, cwd, "pkg/alpha.go", "beta.txt")
	e := newStructuredEnvWith(t, "plain", structuredEnvOpts{Cwd: cwd})
	e.status(t, session.StatusIdle)
	c := &controlClient{out: make(chan []byte, 32)}

	raw := structuredReply(t, e.srv, c, command{Cmd: "structuredFiles", ID: e.child, Q: "alp", Req: "f1"}, "structuredFiles", "teamResult")
	var m structuredFilesMsg
	if err := json.Unmarshal(raw, &m); err != nil || m.Type != "structuredFiles" || m.Req != "f1" || m.Q != "alp" || m.Error != "" || !slices.Equal(m.Files, []string{"pkg/alpha.go"}) {
		t.Fatalf("%s", raw)
	}

	// A pane that is not a structured pane is refused in the words every structured command uses.
	raw = structuredReply(t, e.srv, c, command{Cmd: "structuredFiles", ID: e.boss, Req: "f2"}, "structuredFiles", "teamResult")
	var res teamResultMsg
	if err := json.Unmarshal(raw, &res); err != nil || res.Type != "teamResult" || res.Error != structuredNotPane {
		t.Errorf("%s", raw)
	}

	// A window through the relay gets no names, and no message of the type is sent to one.
	remote := &controlClient{out: make(chan []byte, 32), remote: true, device: "full-dev"}
	raw = structuredReply(t, e.srv, remote, command{Cmd: "structuredFiles", ID: e.child, Req: "f3"}, "structuredFiles", "teamResult")
	if err := json.Unmarshal(raw, &res); err != nil || res.Type != "teamResult" || res.Error != structuredDeskOnly || strings.Contains(string(raw), "alpha") {
		t.Errorf("%s", raw)
	}
	if scrubForRemote([]byte(`{"type":"structuredFiles","files":["a"]}`)) != nil {
		t.Error("a relay window would be sent file names")
	}
}

func TestStructuredFilesAreRateLimited(t *testing.T) {
	e := newStructuredEnvWith(t, "plain", structuredEnvOpts{Cwd: t.TempDir()})
	e.status(t, session.StatusIdle)
	c := &controlClient{out: make(chan []byte, 256)}
	var last structuredFilesMsg
	for i := 0; i < structuredFilesRateLimit+5; i++ {
		raw := structuredReply(t, e.srv, c, command{Cmd: "structuredFiles", ID: e.child, Req: "r"}, "structuredFiles")
		if err := json.Unmarshal(raw, &last); err != nil {
			t.Fatal(err)
		}
	}
	if last.Error != structuredFilesBusy {
		t.Errorf("the last answer: %+v", last)
	}
}
