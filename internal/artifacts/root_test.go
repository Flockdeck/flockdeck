package artifacts

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
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
	tr.write("outside/payload.txt", outsideSecret)
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

// cannot says a thing a test needs -- making a link, a junction, a FIFO -- is
// not possible here. On a developer's machine that is a skip. Under CI it is a
// failure: a runner is expected to be able to, and a skip there would lose the
// coverage without anyone noticing.
func cannot(t testing.TB, args ...any) {
	t.Helper()
	if os.Getenv("CI") != "" {
		t.Fatal(append([]any{"CI must be able to run this test, but: "}, args...)...)
	}
	t.Skip(args...)
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
		"../outside/payload.txt",
		"sub/../../outside/payload.txt",
		"./../outside/payload.txt",
		"sub/../../proj/../outside/payload.txt",
		filepath.Join(tr.outside, "payload.txt"),
		filepath.Join(tr.root, "..", "outside", "payload.txt"),
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
	for _, c := range []string{"missing.txt", ".env", "../outside/payload.txt", "sub", "con", "ok.txt."} {
		tr.mustRefuse(c)
	}
}

// Threat: reading without bound. One reader is cut at the cap however large a
// request is made of it; and -- the case that was missed -- so is the file as a
// whole: a second and a third reader do not each get another allowance, they
// share the one total, served in order from the start.
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
	first, _ := io.ReadAll(f.Limited(1 << 40))
	second, _ := io.ReadAll(f.Limited(1 << 40))
	third, _ := io.ReadAll(f.Limited(10))
	if len(first) != MaxViewBytes || len(second) != 0 || len(third) != 0 {
		t.Fatalf("read %d, %d and %d bytes; want %d, 0 and 0 (one total for the file)", len(first), len(second), len(third), MaxViewBytes)
	}
}

// Threat: the same, in the way a viewer reads: chunk by chunk. The chunks
// continue where the last stopped, join up to the file, and stop at the cap.
func TestLimitedChunksContinueAndShareTheCap(t *testing.T) {
	tr := newTree(t)
	var sb strings.Builder
	for i := 0; sb.Len() < MaxViewBytes+ChunkBytes; i++ {
		sb.WriteString(strconv.Itoa(i))
		sb.WriteByte(' ')
	}
	big := sb.String()
	tr.write("proj/big.txt", big)
	f, err := tr.r.Open("big.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var got []byte
	for {
		chunk, err := io.ReadAll(f.Limited(ChunkBytes))
		if err != nil {
			t.Fatal(err)
		}
		if len(chunk) == 0 {
			break
		}
		got = append(got, chunk...)
	}
	if len(got) != MaxViewBytes || string(got) != big[:MaxViewBytes] {
		t.Fatalf("chunks made %d bytes (want %d) or did not equal the start of the file", len(got), MaxViewBytes)
	}
}

// Threat: getting at the raw file handle through the reader -- by type
// assertion to *io.LimitedReader (whose R field is the *os.File), to an
// io.SectionReader, an Unwrap, or a method that hands it out -- and then
// seeking, reading past the cap or writing. The reader is a private type with
// one method, Read.
func TestLimitedReaderLeaksNoHandle(t *testing.T) {
	tr := newTree(t)
	f, err := tr.r.Open("ok.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r := f.Limited(10)
	if _, ok := r.(*io.LimitedReader); ok {
		t.Error("the reader is an *io.LimitedReader, whose R field is the file")
	}
	if _, ok := r.(*io.SectionReader); ok {
		t.Error("the reader is an *io.SectionReader")
	}
	for name, ok := range map[string]bool{
		"Fd":     implements[interface{ Fd() uintptr }](r),
		"Unwrap": implements[interface{ Unwrap() io.Reader }](r),
		"Outer": implements[interface {
			Outer() (io.ReaderAt, int64, int64)
		}](r),
		"Seek":   implements[io.Seeker](r),
		"ReadAt": implements[io.ReaderAt](r),
		"Write":  implements[io.Writer](r),
		"Close":  implements[io.Closer](r),
		"File":   implements[interface{ File() *os.File }](r),
	} {
		if ok {
			t.Errorf("the reader has a %s method", name)
		}
	}
	if n := reflect.TypeOf(r).NumMethod(); n != 1 {
		t.Errorf("the reader has %d exported methods, want only Read", n)
	}
	if reflect.TypeOf(r).Kind() == reflect.Ptr && reflect.TypeOf(r).Elem().PkgPath() != reflect.TypeOf(f).Elem().PkgPath() {
		t.Errorf("the reader's type is from %s, not this package", reflect.TypeOf(r).Elem().PkgPath())
	}
	// And the File's only exported way to its bytes is Limited.
	for i := 0; i < reflect.TypeOf(f).NumMethod(); i++ {
		switch m := reflect.TypeOf(f).Method(i).Name; m {
		case "Limited", "Close":
		default:
			t.Errorf("File has an exported method %s", m)
		}
	}
}

func implements[T any](v any) bool { _, ok := v.(T); return ok }

// Threat: reading after the file is closed, from a reader made earlier.
func TestLimitedAfterCloseFails(t *testing.T) {
	tr := newTree(t)
	f, err := tr.r.Open("ok.txt")
	if err != nil {
		t.Fatal(err)
	}
	r := f.Limited(10)
	f.Close()
	if _, err := r.Read(make([]byte, 4)); err == nil {
		t.Fatal("read from a closed file")
	}
}

// Threat: an error that is sent to a client by mistake -- printed, formatted
// with any verb, or marshalled to JSON -- naming why a path was refused. It
// must say only what a client may be told, whatever the reason was.
func TestRefusalNeverPrintsItsReason(t *testing.T) {
	tr := newTree(t)
	for _, c := range []struct {
		path   string
		reason Reason
	}{{"../outside/payload.txt", ReasonOutside}, {".env", ReasonDenied}, {"missing.txt", ReasonMissing}} {
		_, err := tr.r.Open(c.path)
		if ReasonOf(err) != c.reason {
			t.Fatalf("Open(%q) refused for %q, want %q", c.path, ReasonOf(err), c.reason)
		}
		js, jerr := json.Marshal(err)
		if jerr != nil {
			t.Fatal(jerr)
		}
		wrapped := fmt.Errorf("opening: %w", err)
		for _, out := range []string{
			fmt.Sprint(err), fmt.Sprintf("%v", err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err),
			fmt.Sprintf("%s", err), fmt.Sprintf("%q", err), string(js), fmt.Sprint(wrapped), err.Error(),
			fmt.Sprintf("%#v", *err.(*Refusal)),
		} {
			for _, why := range []Reason{ReasonOutside, ReasonDenied, ReasonMissing, ReasonLink, ReasonLexical} {
				if strings.Contains(out, string(why)) {
					t.Errorf("a refusal for %q printed as %q, which holds %q", c.path, out, why)
				}
			}
		}
	}
}

// Threat: a path long enough to make the rest of the checks slow, or to
// overflow something below them. Refused before any work is done on it.
func TestLongCandidatesAreRefused(t *testing.T) {
	tr := newTree(t)
	tr.mustRefuse(strings.Repeat("a/", MaxCandidateBytes), ReasonLexical)
	tr.mustRefuse(strings.Repeat("a", MaxCandidateBytes+1), ReasonLexical)
}

// Threat: secrets under names a file-name check must know, in the file or in
// any folder on the way: key files whose names start with "-" or end in "=",
// dotenv variants, state and credential stores, and folders that exist to keep
// secrets.
func TestOpenRefusesTheNamesThatWereMissed(t *testing.T) {
	tr := newTree(t)
	var made []string
	for _, rel := range []string{
		"-prod.pem", "-id_rsa", "key.pem=", ".envrc", "secrets.yml", "secrets.json", "prod.tfvars", "terraform.tfstate",
		".terraform/x.json", ".htpasswd", ".pypirc", ".dockercfg", ".yarnrc.yml", ".boto", ".s3cfg", ".vault-token",
		"kubeconfig", "service-account.json", "vault.kdbx", "backup.gpg", ".bash_history", ".azure/accessTokens.json",
		"wp-config.php", "config/secrets.yml.enc", "secrets/readme.md", "private/readme.md", "a/Private/b/readme.md",
		".config/gh/hosts.yml",
	} {
		tr.write("proj/"+rel, envSecret)
		made = append(made, rel)
	}
	for _, rel := range made {
		tr.mustRefuse(rel, ReasonDenied)
	}
	// What is not a secret by name is still shown.
	tr.write("proj/docs/guide.md", "guide")
	if got, err := tr.open("docs/guide.md"); err != nil || got != "guide" {
		t.Fatalf("an ordinary file was refused: %q, %v", got, err)
	}
}

// Threat: the viewer changing anything. A battery of opens, good and hostile,
// leaves every file, folder, size, time and content as it was.
func TestOpenNeverWrites(t *testing.T) {
	tr := newTree(t)
	before := snapshot(t, tr.base)
	for _, c := range []string{"ok.txt", "sub/deep.md", ".env", "../outside/payload.txt", "sub", "missing", "con", "ok.txt:s", "ok.txt."} {
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

// Fuzzing the whole of Open with whatever bytes, against a tree that is full of
// traps: a symlink and a junction out, a symlink in, a hardlink to a file
// outside, a .git folder, secrets by name, a secrets/ folder, a file with an
// 8.3 alias. It must never panic and never hang, and the oracle is strict: of
// everything in the tree exactly two files may ever be returned, ok.txt and
// sub/deep.md, with their own content and size, under their own names
// (ignoring case on a file system that folds it). Anything else is a refusal.
func FuzzOpen(f *testing.F) {
	for _, s := range []string{
		"ok.txt", "../outside/payload.txt", ".env", "sub/../../outside/payload.txt", "con", "ok.txt:s",
		"ok.txt.", "SECRET~1.TXT", `..\outside\payload.txt`, "/etc/passwd", `\\?\C:\Windows\win.ini`,
		"sub/deep.md", "sub//deep.md", "sub/./deep.md", ".git/config", "id_rsa", "a\x00b", "%2e%2e/outside/payload.txt",
		"lnk-out", "lnk-in", "lnk-dir/payload.txt", "jct/payload.txt", "hard.txt", "secrets/x.txt", "private/x.txt",
		"secret-credentials.txt", "-prod.pem", "key.pem=", "ENVX.TXT", "sub/../ok.txt", "SUB/DEEP.MD", "OK.TXT",
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
	mk("proj/.git/config", envSecret)
	mk("proj/id_rsa", envSecret)
	mk("proj/-prod.pem", envSecret)
	mk("proj/key.pem=", envSecret)
	mk("proj/secrets/x.txt", envSecret)
	mk("proj/private/x.txt", envSecret)
	mk("proj/secret-credentials.txt", envSecret)
	mk("outside/payload.txt", outsideSecret)
	plantLinks(f, base)
	r, err := NewRoot(filepath.Join(base, "proj"))
	if err != nil {
		f.Fatal(err)
	}
	defer r.Close()
	allowed := map[string]string{"ok.txt": "ok", "sub/deep.md": "# deep"}
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
		var want string
		var ok bool
		for rel, content := range allowed {
			if file.Rel == rel || (foldCase && strings.EqualFold(file.Rel, rel)) {
				want, ok = content, true
			}
		}
		if !ok {
			t.Fatalf("Open(%q) returned %q, which is not one of the two files the tree means to show", candidate, file.Rel)
		}
		if string(b) != want || file.Size != int64(len(want)) {
			t.Fatalf("Open(%q) gave %q (size %d) for %q, want %q", candidate, b, file.Size, file.Rel, want)
		}
	})
}

// Threat: a race on a FOLDER on the way, not on the file. "sub" is a plain
// folder when the path is checked and a link (a symlink, or a junction on
// Windows) when the file is opened -- to a folder outside the root that holds a
// file of the same name, or to another folder inside it. The file must not be
// handed back, in either case, at either moment.
func TestDirectorySwapRace(t *testing.T) {
	for _, stage := range []string{"checked", "opened"} {
		for _, where := range []string{"outside", "inside"} {
			t.Run(stage+"/"+where, func(t *testing.T) {
				tr := newTree(t)
				tr.write("outside/sub/deep.md", outsideSecret)
				tr.write("proj/other/deep.md", "# other")
				target := filepath.Join(tr.outside, "sub")
				if where == "inside" {
					target = filepath.Join(tr.root, "other")
				}
				swapped := false
				raceHook = func(s string) {
					if s == stage {
						swapped = replaceDirWithLink(t, filepath.Join(tr.root, "sub"), target)
					}
				}
				defer func() { raceHook = func(string) {} }()
				got, err := tr.open("sub/deep.md")
				if !swapped {
					// Windows will not rename a folder with a file open in it:
					// the race cannot happen there once the file is open, and
					// what was opened is the plain file the path named.
					if err != nil || got != "# deep" {
						t.Fatalf("no swap was possible, but Open gave %q, %v", got, err)
					}
					return
				}
				if err == nil {
					t.Fatalf("a path whose folder was swapped for a link opened and read %q", got)
				}
				if !errors.Is(err, ErrUnavailable) {
					t.Fatal(err)
				}
				if strings.Contains(got, "SECRET") || got == "# other" {
					t.Fatalf("read %q through a swapped folder", got)
				}
			})
		}
	}
}
