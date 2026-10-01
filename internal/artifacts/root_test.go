package artifacts

import (
	"crypto/sha256"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

const (
	outsideSecret = "OUTSIDE-SECRET-DO-NOT-SHOW"
	envSecret     = "ENV-SECRET-DO-NOT-SHOW"
)

// tree is a project folder with a folder beside it that is not part of it.
// Everything is under t.TempDir; nothing reads a real home or config
// directory.
type tree struct {
	t       *testing.T
	base    string
	root    string // the project
	outside string // a sibling of the project, holding a secret
	r       *Root
}

func newTree(t *testing.T) *tree {
	t.Helper()
	base := t.TempDir()
	tr := &tree{t: t, base: base, root: filepath.Join(base, "proj"), outside: filepath.Join(base, "outside")}
	tr.write("proj/ok.txt", "ok")
	tr.write("proj/sub/deep.md", "# deep")
	tr.write("proj/.env", envSecret)
	tr.write("proj/.git/config", envSecret)
	tr.write("proj/id_rsa", envSecret)
	tr.write("proj/secret-credentials.txt", envSecret)
	tr.write("outside/secret.txt", outsideSecret)
	r, err := NewRoot(tr.root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	tr.r = r
	return tr
}

func (tr *tree) write(rel, content string) string {
	tr.t.Helper()
	p := filepath.Join(tr.base, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		tr.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		tr.t.Fatal(err)
	}
	return p
}

// open opens a candidate and reads all of it that may be read.
func (tr *tree) open(candidate string) (string, error) {
	f, err := tr.r.Open(candidate)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(f.Limited(MaxViewBytes))
	if err != nil {
		tr.t.Fatal(err)
	}
	return string(b), nil
}

// mustRefuse asserts a refusal, that it is the one thing a client is ever told,
// and that no secret came back with it.
func (tr *tree) mustRefuse(candidate string, want ...Reason) {
	tr.t.Helper()
	got, err := tr.open(candidate)
	if err == nil {
		tr.t.Fatalf("Open(%q) succeeded and read %q", candidate, got)
	}
	if !errors.Is(err, ErrUnavailable) {
		tr.t.Fatalf("Open(%q): %v is not ErrUnavailable", candidate, err)
	}
	if err.Error() != ErrUnavailable.Error() {
		tr.t.Fatalf("Open(%q): the error text %q says more than ErrUnavailable", candidate, err.Error())
	}
	if strings.Contains(got, "SECRET") {
		tr.t.Fatalf("Open(%q) leaked %q while refusing", candidate, got)
	}
	if len(want) > 0 {
		for _, w := range want {
			if ReasonOf(err) == w {
				return
			}
		}
		tr.t.Fatalf("Open(%q) refused for %q, want one of %v", candidate, ReasonOf(err), want)
	}
}

// Threat: the baseline -- a plain file in the project is shown, by a relative
// path, by the absolute path as the system names it and as the caller spelled
// it, and nothing but the project-relative name is reported.
func TestOpenPlainFile(t *testing.T) {
	tr := newTree(t)
	for _, c := range []string{
		"ok.txt",
		"./ok.txt",
		"sub/../ok.txt",
		filepath.Join(tr.root, "ok.txt"),
		filepath.Join(tr.r.Path(), "ok.txt"),
	} {
		f, err := tr.r.Open(c)
		if err != nil {
			t.Fatalf("Open(%q): %v", c, err)
		}
		if f.Rel != "ok.txt" || f.Size != 2 || f.ModTime.IsZero() {
			t.Errorf("Open(%q) = %+v", c, f)
		}
		f.Close()
	}
	f, err := tr.r.Open(filepath.Join("sub", "deep.md"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if f.Rel != "sub/deep.md" {
		t.Errorf("Rel = %q, want a forward-slash path below the root", f.Rel)
	}
}

// Threat: path traversal. "..", an absolute path elsewhere, the root itself, a
// folder, and the empty string never open anything, however they are
// spelled.
func TestOpenRefusesOutside(t *testing.T) {
	tr := newTree(t)
	for _, c := range []string{
		"../outside/secret.txt",
		"sub/../../outside/secret.txt",
		"./../outside/secret.txt",
		"sub/../../proj/../outside/secret.txt",
		filepath.Join(tr.outside, "secret.txt"),
		filepath.Join(tr.root, "..", "outside", "secret.txt"),
		"/etc/passwd",
		`\Windows\win.ini`,
		`C:\Windows\win.ini`,
		"..",
		"../",
		".",
		"",
		tr.root,
		"sub",
		filepath.Join(tr.root, "sub"),
		// A path that merely starts with the root's text is not inside it.
		tr.root + "-evil/x.txt",
	} {
		tr.mustRefuse(c)
	}
}

// Threat: reading a secret by name -- dotenv, private key, credentials, the
// .git folder -- including with other capitals on a file system that folds
// case. The same check recordings and auto-review use, plus denied folders.
func TestOpenRefusesSecrets(t *testing.T) {
	tr := newTree(t)
	for _, c := range []string{
		".env", ".ENV", "./.env", filepath.Join(tr.root, ".env"),
		".git/config", ".GIT/config", ".git",
		"id_rsa", "secret-credentials.txt",
	} {
		got, err := tr.open(c)
		if err == nil {
			t.Errorf("Open(%q) succeeded and read %q", c, got)
			continue
		}
		if r := ReasonOf(err); r != ReasonDenied && r != ReasonMissing {
			t.Errorf("Open(%q) refused for %q", c, r)
		}
	}
	tr.mustRefuse(".env", ReasonDenied)
	tr.mustRefuse("id_rsa", ReasonDenied)
	tr.mustRefuse(".git/config", ReasonDenied)
}

// Threat: every way of saying "no" looks the same to a client, so asking
// cannot be used to find out what is on the disk: a missing file, a forbidden
// one and a path out of the root are one error with one text.
func TestRefusalsAreUniform(t *testing.T) {
	tr := newTree(t)
	for _, c := range []string{"missing.txt", ".env", "../outside/secret.txt", "sub", "con", "ok.txt."} {
		tr.mustRefuse(c)
	}
}

// Threat: reading without bound. A file larger than the view limit is read
// only up to it, however large a request is made.
func TestLimitedNeverReadsPastTheCap(t *testing.T) {
	tr := newTree(t)
	big := strings.Repeat("x", MaxViewBytes+1000)
	tr.write("proj/big.txt", big)
	f, err := tr.r.Open("big.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if f.Size != int64(len(big)) {
		t.Errorf("Size = %d", f.Size)
	}
	b, _ := io.ReadAll(f.Limited(1 << 40))
	if len(b) != MaxViewBytes {
		t.Errorf("read %d bytes, want exactly %d", len(b), MaxViewBytes)
	}
}

// Threat: the viewer changing anything. A battery of opens, good and hostile,
// leaves every file, folder, size, time and content as it was.
func TestOpenNeverWrites(t *testing.T) {
	tr := newTree(t)
	before := snapshot(t, tr.base)
	for _, c := range []string{"ok.txt", "sub/deep.md", ".env", "../outside/secret.txt", "sub", "missing", "con", "ok.txt:s", "ok.txt."} {
		if f, err := tr.r.Open(c); err == nil {
			io.Copy(io.Discard, f.Limited(1<<20))
			f.Close()
		}
	}
	// A short pause so that a changed modification time could not hide in the
	// same tick.
	time.Sleep(20 * time.Millisecond)
	if after := snapshot(t, tr.base); after != before {
		t.Fatalf("the tree changed:\nbefore %s\nafter  %s", before, after)
	}
}

func snapshot(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := os.Lstat(p) // not d.Info(): on Windows that is the listing's cached copy, which lags
		if err != nil {
			return err
		}
		sum := ""
		if fi.Mode().IsRegular() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			h := sha256.Sum256(b)
			sum = string(h[:8])
		}
		lines = append(lines, p+"|"+fi.Mode().String()+"|"+fi.ModTime().String()+"|"+sum)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	h := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return string(h[:])
}

// Threat: a root that is not a directory, or a path that is not there, cannot
// become a root that opens things.
func TestNewRootRejectsNonDirectories(t *testing.T) {
	tr := newTree(t)
	if _, err := NewRoot(filepath.Join(tr.root, "ok.txt")); err == nil {
		t.Error("a file was accepted as a root")
	}
	if _, err := NewRoot(filepath.Join(tr.root, "missing")); err == nil {
		t.Error("a missing folder was accepted as a root")
	}
}

// Threat: a candidate in other capitals on Windows and macOS is the same place
// and is judged by the same rules: allowed if the file is, refused if it is a
// secret.
func TestCaseVariantsOnFoldingFileSystems(t *testing.T) {
	if !foldCase {
		t.Skip("this platform's file system is case-sensitive")
	}
	tr := newTree(t)
	got, err := tr.open(strings.ToUpper(filepath.Join(tr.root, "OK.TXT")))
	if err != nil || got != "ok" {
		t.Errorf("an upper-case spelling of a plain file: %q, %v", got, err)
	}
	tr.mustRefuse(strings.ToUpper(filepath.Join(tr.root, ".ENV")))
	tr.mustRefuse(".ENV")
}

// Fuzzing the whole of Open with whatever bytes: it must never panic, never
// hang, and a success must be a plain file inside the project that is not
// denied, whose content is one of the project's own -- never the secret that
// sits beside it.
func FuzzOpen(f *testing.F) {
	for _, s := range []string{
		"ok.txt", "../outside/secret.txt", ".env", "sub/../../outside/secret.txt", "con", "ok.txt:s",
		"ok.txt.", "SECRET~1.TXT", `..\outside\secret.txt`, "/etc/passwd", `\\?\C:\Windows\win.ini`,
		"sub/deep.md", "sub//deep.md", "sub/./deep.md", ".git/config", "id_rsa", "a\x00b", "%2e%2e/outside/secret.txt",
	} {
		f.Add(s)
	}
	base := f.TempDir()
	mk := func(rel, content string) {
		p := filepath.Join(base, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			f.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			f.Fatal(err)
		}
	}
	mk("proj/ok.txt", "ok")
	mk("proj/sub/deep.md", "# deep")
	mk("proj/.env", envSecret)
	mk("outside/secret.txt", outsideSecret)
	r, err := NewRoot(filepath.Join(base, "proj"))
	if err != nil {
		f.Fatal(err)
	}
	defer r.Close()
	f.Fuzz(func(t *testing.T, candidate string) {
		file, err := r.Open(candidate)
		if err != nil {
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("Open(%q): %v is not ErrUnavailable", candidate, err)
			}
			return
		}
		defer file.Close()
		b, _ := io.ReadAll(file.Limited(1 << 20))
		if s := string(b); s != "ok" && s != "# deep" {
			t.Fatalf("Open(%q) read %q, which is not a file the project meant to show", candidate, s)
		}
		if denied(file.Rel) || strings.HasPrefix(file.Rel, "..") || strings.Contains(file.Rel, `\`) {
			t.Fatalf("Open(%q) returned Rel %q", candidate, file.Rel)
		}
	})
}
