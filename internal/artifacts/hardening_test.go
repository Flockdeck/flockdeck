package artifacts

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Threat: the Unicode case-folding bypass. File systems that ignore case (APFS
// by default, ext4 with casefold, vfat, NTFS, SMB mounts) resolve ".ſsh" (a long
// s) to ".ssh", "ſecrets" to "secrets", "paßword.txt" to "password.txt", the
// Kelvin sign to k. The name check ran on the spelling asked for and read them
// as innocent. Each is refused on every OS by the name alone; these files exist
// in the tree so that on a folding file system the open would have succeeded.
func TestFoldedSpellingsOfSecretsAreRefused(t *testing.T) {
	tr := newTree(t)
	for _, f := range []string{".ssh/known", "secrets/x.txt", "terraform.tfstate", ".zsh_history", "password.txt", "tls.key"} {
		tr.write("proj/"+f, envSecret)
	}
	const long, kelvin, sharp = "ſ", "K", "ß"
	for _, c := range []string{
		"." + long + "sh/known",
		long + "ecrets/x.txt",
		"terraform.tf" + long + "tate",
		".z" + long + "h_history",
		"pa" + long + long + "word.txt",
		"pa" + sharp + "word.txt",
		"tls." + kelvin + "ey",
		"TLS." + kelvin + "EY",
		filepath.Join(tr.root, "."+long+"sh", "known"),
	} {
		tr.mustRefuse(c, ReasonDenied)
	}
}

// Threat: a name that is not ASCII but is innocent still opens, and one that
// only a folding file system would resolve to another entry does not open as
// that entry. (On a file system that does not fold, the second is simply not
// there; on one that does, the exact-entry check refuses it.)
func TestNonASCIINamesAreExactEntries(t *testing.T) {
	tr := newTree(t)
	tr.write("proj/café.txt", "cafe")
	tr.write("proj/日本語/メモ.md", "memo")
	for _, c := range []string{"café.txt", "日本語/メモ.md"} {
		if got, err := tr.open(c); err != nil || got == "" {
			t.Fatalf("Open(%q) = %q, %v; an ordinary non-ASCII name should open", c, got, err)
		}
	}
	for _, c := range []struct{ ask, entry string }{
		{"CAFÉ.txt", "café.txt"},
		{"café.txt", ""},
		{"日本語/メモ.MD", "日本語/メモ.md"},
		{"café.txt‍", ""},
	} {
		got, err := tr.open(c.ask)
		if err != nil {
			continue
		}
		// Only Windows may accept a spelling that is not the entry's own, and only
		// one that differs by case: it reads back the real name and compares.
		if !(isWindows && c.entry != "" && strings.EqualFold(c.ask, c.entry)) {
			t.Errorf("Open(%q) read %q; it is not the entry's own spelling", c.ask, got)
		}
	}
}

// Threat: the exact-entry test being a lookup. It reads the folder's names, so a
// spelling the file system would resolve is still not an entry.
func TestHasEntryIsByteExact(t *testing.T) {
	tr := newTree(t)
	tr.write("proj/naïve.txt", "x")
	tr.write("proj/d/日本.md", "x")
	for _, c := range []struct {
		dir, name string
		want      bool
	}{
		{".", "naïve.txt", true},
		{".", "NAÏVE.TXT", false},
		{".", "naïve.txt", false}, // the same word, decomposed
		{".", "ok.txt", true},
		{".", "OK.TXT", false},
		{"d", "日本.md", true},
		{"d", "日本.m", false},
		{".", "d", true},
	} {
		got, err := tr.r.hasEntry(c.dir, c.name)
		if err != nil || got != c.want {
			t.Errorf("hasEntry(%q, %q) = %v, %v; want %v", c.dir, c.name, got, err, c.want)
		}
	}
	if _, err := tr.r.hasEntry("missing", "x"); err == nil {
		t.Error("hasEntry in a folder that is not there did not fail")
	}
	if _, err := tr.r.hasEntry("../outside", "payload.txt"); err == nil {
		t.Error("hasEntry looked outside the root")
	}
}

// Threat: a root that is not a project -- the process's current directory
// ("" or "."), a whole filesystem or drive, the home directory or a folder above
// it, a folder that keeps secrets -- which would make every file below it a
// candidate. Refused; a project and the app's own folder below ~/.config are not.
func TestNewRootRefusesWhatIsNotAProject(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	dirs := map[string]string{
		"ssh": ".ssh", "ssh-sub": ".ssh/keys", "aws": ".aws", "gnupg": ".gnupg", "config": ".config",
		"gh": ".config/gh", "kube": ".kube", "proj": "proj", "app": ".config/flockdeck/recordings",
		"appdata": "AppData/Roaming", "local": "AppData/Local", "plain": "work/x",
	}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(home, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))

	root := string(filepath.Separator)
	if v := filepath.VolumeName(os.TempDir()); v != "" {
		root = v + root
	}
	refused := []string{
		"", ".", "..", "proj", "./proj", filepath.Join("home", "proj"), // empty or relative
		root,                        // the whole filesystem or drive
		home,                        // the home directory itself
		base,                        // a folder above it
		filepath.Dir(base),          // and further above
		filepath.Join(home, ".ssh"), // folders that keep secrets
		filepath.Join(home, ".ssh", "keys"),
		filepath.Join(home, ".aws"),
		filepath.Join(home, ".gnupg"),
		filepath.Join(home, ".kube"),
		filepath.Join(home, ".config", "gh"),
		filepath.Join(home, ".config"), // every program's settings
		filepath.Join(home, "AppData", "Roaming"),
		filepath.Join(home, "AppData", "Local"),
		filepath.Join(home, "AppData", "Roaming") + string(filepath.Separator), // spelled with a trailing slash
	}
	if isWindows || foldCase {
		refused = append(refused, strings.ToUpper(filepath.Join(home, ".ssh")), strings.ToUpper(home))
	}
	for _, d := range refused {
		r, err := NewRoot(d)
		if err == nil {
			r.Close()
			t.Errorf("NewRoot(%q) succeeded, want a refusal", d)
		}
	}
	for _, d := range []string{"proj", ".config/flockdeck/recordings", "work/x"} {
		r, err := NewRoot(filepath.Join(home, filepath.FromSlash(d)))
		if err != nil {
			t.Errorf("NewRoot(%q) = %v, want a root", d, err)
			continue
		}
		r.Close()
	}
}

// Threat: the same by a link: a root named by an alias of the home directory or
// of ~/.ssh is as bad as the folder itself (NewRoot follows links, so the root
// is judged by where it really is).
func TestNewRootRefusesALinkToWhatIsNotAProject(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for name, target := range map[string]string{"homelink": home, "sshlink": filepath.Join(home, ".ssh")} {
		link := filepath.Join(base, name)
		dirLink(t, link, target)
		if r, err := NewRoot(link); err == nil {
			r.Close()
			t.Errorf("NewRoot(%s) succeeded", name)
		}
	}
}

// Threat: a read that fails for a reason of the system -- a locked byte range, a
// disk error, a share that went away, a closed file -- returning the system's
// own error, a *fs.PathError that names the file's absolute path on this
// machine. Every read and close error is the one generic refusal; the detail is
// kept for the log.
func TestReadAndCloseErrorsAreGeneric(t *testing.T) {
	tr := newTree(t)
	f, err := tr.r.Open("ok.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.f.Close(); err != nil { // the handle goes away under the File
		t.Fatal(err)
	}
	_, err = f.Limited(10).Read(make([]byte, 4))
	mustBeGeneric(t, "read", err, tr)
	if CauseOf(err) == nil {
		t.Error("the detail of the failed read was not kept for the log")
	}
	mustBeGeneric(t, "close", f.Close(), tr)
	if err := f.Close(); !errors.Is(err, os.ErrClosed) {
		t.Errorf("closing twice = %v, want os.ErrClosed", err)
	}
	if _, err := f.Limited(1).Read(make([]byte, 1)); !errors.Is(err, os.ErrClosed) {
		t.Errorf("read after close = %v, want os.ErrClosed", err)
	}
}

// mustBeGeneric asserts err says nothing the host did not choose to say.
func mustBeGeneric(t *testing.T, what string, err error, tr *tree) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: no error", what)
	}
	var pe *fs.PathError
	if errors.As(err, &pe) {
		t.Fatalf("%s: the error is a *fs.PathError naming %q", what, pe.Path)
	}
	if !errors.Is(err, ErrUnavailable) || err.Error() != ErrUnavailable.Error() {
		t.Fatalf("%s: %v is not the generic refusal", what, err)
	}
	if errors.Is(err, fs.ErrPermission) || errors.Is(err, os.ErrClosed) || errors.Unwrap(err) != nil {
		t.Fatalf("%s: the refusal unwraps to the system's error", what)
	}
	for _, s := range []string{tr.root, tr.base, filepath.Base(tr.base), "ok.txt"} {
		for _, v := range []string{err.Error(), fmt.Sprintf("%+v %#v", err, err)} {
			if strings.Contains(v, s) {
				t.Fatalf("%s: %q names %q", what, v, s)
			}
		}
	}
}

// Threat: copying a File to get a second budget. A copy shares the original's
// state, so what one reads the other does not read again, and the total holds.
func TestACopyOfAFileSharesItsBudget(t *testing.T) {
	tr := newTree(t)
	tr.write("proj/ten.txt", "0123456789")
	f, err := tr.r.Open("ten.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cp := reflect.New(reflect.TypeOf(File{})).Elem() // a copy, made without a copy vet would report
	cp.Set(reflect.ValueOf(f).Elem())
	g := cp.Addr().Interface().(*File)
	a, _ := io.ReadAll(f.Limited(3))
	b, _ := io.ReadAll(g.Limited(100))
	if string(a) != "012" || string(b) != "3456789" {
		t.Fatalf("original read %q, copy read %q; the copy must continue where the original stopped", a, b)
	}
	if err := g.Close(); err != nil { // closing the copy closes the file for both
		t.Fatal(err)
	}
	if _, err := f.Limited(1).Read(make([]byte, 1)); err == nil {
		t.Error("the original still reads after its copy was closed")
	}
	var zero File // the zero value reads nothing and closes without a panic
	if _, err := zero.Limited(1).Read(make([]byte, 1)); err == nil {
		t.Error("a zero File read")
	}
}

// Threat: a long path made to cost quadratic work (each component is looked at
// again from the root, twice) or to hide a secret deep in it. Over
// MaxComponents is refused before any disk is touched; exactly that many opens.
func TestTooManyComponentsAreRefused(t *testing.T) {
	tr := newTree(t)
	deep := strings.Repeat("d/", MaxComponents-1) + "f.txt"
	tr.write("proj/"+deep, "deep")
	if got, err := tr.open(deep); err != nil || got != "deep" {
		t.Fatalf("Open at %d components = %q, %v", MaxComponents, got, err)
	}
	tr.mustRefuse("d/"+deep, ReasonLexical)
	tr.mustRefuse(strings.Repeat("a/", 2000)+"f", ReasonLexical, ReasonOutside)
	tr.mustRefuse(strings.Repeat("../", 100)+"ok.txt", ReasonOutside, ReasonLexical)
}
