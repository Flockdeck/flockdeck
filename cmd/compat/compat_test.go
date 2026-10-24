package compat

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The compatibility matrix. A window and a server are built apart, so a window
// from the last release can meet this tree's server (a desk updated under an
// open window, a phone on an old page) and this tree's window can meet an older
// server (the desk behind a relay). The pushes the server writes are the
// fixtures in api/fixtures, and this runs every one through a front end:
//
//   - the previous release's front end against this tree's fixtures, and
//   - this tree's front end against the previous release's fixtures.
//
// It is off unless COMPAT_RUN=1, because it needs the repository's tags.
// COMPAT_REF names the release to compare with (any git ref); without
// it the newest tag matching v* is used. When that release has no front end in a
// layout this knows, or no fixtures, the half that needs it is skipped and says
// so: it starts to run by itself in the first release that has them.
//
// The front end is only fed the graph push and drawn, which is what the fixtures
// are. Other messages are held by the API catalog (internal/server).

var (
	uiDirs      = []string{"ui/src", "internal/webui/ui/assets", "internal/webui/next/assets"}
	fixtureDirs = []string{"api/fixtures", "internal/server/testdata/graph-fixtures"}
)

func TestCompatibilityMatrix(t *testing.T) {
	if os.Getenv("COMPAT_RUN") != "1" && os.Getenv("COMPAT_REF") == "" {
		t.Skip("off: set COMPAT_RUN=1 (and fetch the tags) to run the compatibility matrix")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	ref := os.Getenv("COMPAT_REF")
	if ref == "" {
		out, err := git(repo, "describe", "--tags", "--abbrev=0", "--match", "v*")
		if err != nil {
			t.Skipf("no release tag to compare with (fetch tags with fetch-depth 0): %v", err)
		}
		ref = strings.TrimSpace(out)
	}
	t.Logf("comparing with %s", ref)

	prev := t.TempDir()
	prevUI := extractFirst(t, repo, ref, uiDirs, filepath.Join(prev, "src"), "graph/model.js")
	prevFix := extractFirst(t, repo, ref, fixtureDirs, filepath.Join(prev, "fixtures"), "")

	// The render helper is this tree's, beside whichever front end it renders.
	helper, err := os.ReadFile(filepath.Join(repo, "ui", "test", "vendor", "render-to-string.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(prev, "test", "vendor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prev, "test", "vendor", "render-to-string.mjs"), helper, 0o644); err != nil {
		t.Fatal(err)
	}

	curRoot := filepath.Join(repo, "ui")
	curFix := filepath.Join(repo, "api", "fixtures")

	t.Run("previous front end, current fixtures", func(t *testing.T) {
		if !prevUI {
			t.Skipf("%s has no front end in a layout this knows (%v), so there is nothing to draw the fixtures with", ref, uiDirs)
		}
		runMatrix(t, repo, prev, curFix)
	})
	t.Run("current front end, previous fixtures", func(t *testing.T) {
		if !prevFix {
			t.Skipf("%s has no fixtures (%v)", ref, fixtureDirs)
		}
		runMatrix(t, repo, curRoot, filepath.Join(prev, "fixtures"))
	})
}

type result struct {
	Fixture string `json:"fixture"`
	OK      bool   `json:"ok"`
	Error   string `json:"error"`
}

func runMatrix(t *testing.T, repo, root, fixtures string) {
	t.Helper()
	cmd := exec.Command("node", filepath.Join(repo, "cmd", "compat", "run.mjs"), root, fixtures)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		var r result
		if json.Unmarshal([]byte(line), &r) != nil {
			t.Errorf("unreadable line from the runner: %q", line)
			continue
		}
		n++
		if !r.OK {
			t.Errorf("%s: %s", r.Fixture, r.Error)
		}
	}
	if err != nil {
		t.Fatalf("the runner failed: %v\n%s", err, stderr.String())
	}
	if n == 0 {
		t.Fatalf("no fixture was run from %s", fixtures)
	}
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", errors.New(strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

// extractFirst writes the first of dirs that ref has (and that holds must, if
// given) to dest, and says whether one was found. It reads git archive's tar
// itself, so it needs nothing of the system but git.
func extractFirst(t *testing.T, repo, ref string, dirs []string, dest, must string) bool {
	t.Helper()
	for _, dir := range dirs {
		cmd := exec.Command("git", "archive", "--format=tar", ref, "--", dir)
		cmd.Dir = repo
		out, err := cmd.Output()
		if err != nil {
			continue // not in that release
		}
		tmp := t.TempDir()
		if err := untar(out, tmp); err != nil {
			t.Fatalf("reading %s of %s: %v", dir, ref, err)
		}
		src := filepath.Join(tmp, filepath.FromSlash(dir))
		if must != "" {
			if _, err := os.Stat(filepath.Join(src, filepath.FromSlash(must))); err != nil {
				continue
			}
		}
		if err := os.RemoveAll(dest); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(src, dest); err != nil {
			t.Fatal(err)
		}
		return true
	}
	return false
}

func untar(data []byte, dest string) error {
	tr := tar.NewReader(bytes.NewReader(data))
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.Join(dest, filepath.FromSlash(h.Name))
		if rel, err := filepath.Rel(dest, name); err != nil || strings.HasPrefix(rel, "..") {
			return errors.New("a path leaves the folder: " + h.Name)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(name, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
				return err
			}
			b, err := io.ReadAll(tr)
			if err != nil {
				return err
			}
			if err := os.WriteFile(name, b, 0o644); err != nil {
				return err
			}
		}
	}
}
