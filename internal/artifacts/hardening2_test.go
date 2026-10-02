package artifacts

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/secretname"
)

// Threat: one hostile component made to cost CPU (the name check was
// quadratic in the "=" of a name, and only the 4096-byte total was capped). A
// component over 255 bytes is refused before any check of its name, and the
// time for the worst ones that fit is bounded.
func TestLongComponentsAreRefusedCheaply(t *testing.T) {
	tr := newTree(t)
	for _, c := range []string{
		strings.Repeat("a", secretname.MaxName+1),
		strings.Repeat("a=", 2000),
		strings.Repeat("\u017f=", 1000),
		"sub/" + strings.Repeat("a", 300),
	} {
		start := time.Now()
		tr.mustRefuse(c, ReasonLexical)
		if d := time.Since(start); d > 500*time.Millisecond {
			t.Errorf("refusing a %d byte candidate took %v", len(c), d)
		}
	}
	start := time.Now()
	for i := 0; i < 100; i++ { // the worst name that is allowed to be looked at
		_, _ = tr.r.Open(strings.Repeat("\u017f=", secretname.MaxName/4))
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("100 worst-case opens took %v", d)
	}
}

// Threat: a root judged against a home directory that is not known -- HOME and
// USERPROFILE unset, empty or relative, or pointing elsewhere (a daemon, a
// launchd or systemd unit, env -i). With no home found every root is refused;
// with one found only through the password file it is still protected.
func TestNewRootFailsClosedWithoutAHome(t *testing.T) {
	base := t.TempDir()
	proj := filepath.Join(base, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, e := range []string{"HOME", "USERPROFILE", "HOMEDRIVE", "HOMEPATH"} {
		t.Setenv(e, "")
	}
	old := passwdPath
	passwdPath = filepath.Join(base, "no-such-passwd")
	t.Cleanup(func() { passwdPath = old })
	if r, err := NewRoot(proj); err == nil {
		r.Close()
		t.Fatal("NewRoot succeeded though no home directory can be found")
	}
	t.Setenv("HOME", "relative/home") // relative: no better than unset
	t.Setenv("USERPROFILE", "relative/home")
	if r, err := NewRoot(proj); err == nil {
		r.Close()
		t.Fatal("NewRoot succeeded with a relative home")
	}
	if runtime.GOOS == "windows" {
		return
	}
	// The password file says where the user's home really is, whatever HOME says.
	home := filepath.Join(base, "realhome")
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o755); err != nil {
		t.Fatal(err)
	}
	passwd := filepath.Join(base, "passwd")
	line := fmt.Sprintf("someone:x:%d:%d:Some One:%s:/bin/sh\n", os.Getuid(), os.Getgid(), home)
	if err := os.WriteFile(passwd, []byte("root:x:0:0:root:/root:/bin/sh\n"+line), 0o644); err != nil {
		t.Fatal(err)
	}
	passwdPath = passwd
	t.Setenv("HOME", proj) // a lie: HOME says the project is home
	for _, d := range []string{home, filepath.Join(home, ".ssh"), base} {
		if r, err := NewRoot(d); err == nil {
			r.Close()
			t.Errorf("NewRoot(%q) succeeded though the password file says it is or holds the home", d)
		}
	}
	if r, err := NewRoot(filepath.Join(home, "work")); err == nil {
		r.Close()
		t.Error("a folder that does not exist was made a root")
	}
	if err := os.MkdirAll(filepath.Join(home, "work"), 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := NewRoot(filepath.Join(home, "work"))
	if err != nil {
		t.Fatalf("a project below the home was refused: %v", err)
	}
	r.Close()
}

// Threat: a root whose own name is the secret (/run/secrets, /mnt/backup/.ssh,
// a keyring or a browser's "User Data"): the name check only sees what is
// below a root, so it would serve /run/secrets/db_password. Refused when the
// last one or two folders are secret by name.
func TestNewRootRefusesARootNamedAsASecret(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, d := range []string{
		"mnt/backup/.ssh", "run/secrets", "x/private/y", "etc-ssl/private", "a/keyrings", "b/User Data", "c/.config/gh", "d/.git",
		".local/share/keyrings", ".mozilla/firefox", "ok/proj", "ok/src",
	} {
		if err := os.MkdirAll(filepath.Join(base, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(home, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []string{
		"mnt/backup/.ssh", "run/secrets", "x/private/y", "etc-ssl/private", "a/keyrings", "b/User Data", "c/.config/gh", "d/.git",
	} {
		if r, err := NewRoot(filepath.Join(base, filepath.FromSlash(d))); err == nil {
			r.Close()
			t.Errorf("NewRoot(%q) succeeded, want a refusal", d)
		}
	}
	for _, d := range []string{".local/share/keyrings", ".mozilla/firefox", ".mozilla"} {
		if r, err := NewRoot(filepath.Join(home, filepath.FromSlash(d))); err == nil {
			r.Close()
			t.Errorf("NewRoot(~/%s) succeeded, want a refusal", d)
		}
	}
	for _, d := range []string{"ok/proj", "ok/src"} {
		r, err := NewRoot(filepath.Join(base, filepath.FromSlash(d)))
		if err != nil {
			t.Errorf("NewRoot(%q) = %v, want a root", d, err)
			continue
		}
		r.Close()
	}
}

// Threat: an error or a handle used in a way that leaks or panics: a Refusal
// formatted by a formatter that ignores methods (held in a struct field, or
// printed with a verb fmt rejects) must not print the reason; a nil File or a
// zero Root must not panic.
func TestAPIHygiene(t *testing.T) {
	tr := newTree(t)
	_, err := tr.r.Open(".env")
	if ReasonOf(err) != ReasonDenied {
		t.Fatalf("setup: %v", err)
	}
	held := struct{ E error }{err}
	heldValue := struct{ R Refusal }{*(err.(*Refusal))}
	for _, s := range []string{
		fmt.Sprintf("%v", held), fmt.Sprintf("%+v", held), fmt.Sprintf("%#v", held),
		fmt.Sprintf("%v", heldValue), fmt.Sprintf("%+v", heldValue), fmt.Sprintf("%#v", heldValue),
		fmt.Sprintf("%d", err), fmt.Sprintf("%x", err), fmt.Sprint(err), fmt.Sprintf("%v", []error{err}),
	} {
		if strings.Contains(s, string(ReasonDenied)) || strings.Contains(s, ".env") {
			t.Errorf("a formatted Refusal printed its reason: %s", s)
		}
	}
	var nilFile *File
	if _, err := nilFile.Limited(1).Read(make([]byte, 1)); err == nil {
		t.Error("a nil File read")
	}
	if err := nilFile.Close(); err == nil {
		t.Error("a nil File closed")
	}
	var zero Root
	if _, err := zero.Open("ok.txt"); err == nil {
		t.Error("a zero Root opened a file")
	}
	var nilRoot *Root
	if _, err := nilRoot.Open("ok.txt"); err == nil {
		t.Error("a nil Root opened a file")
	}
	_ = zero.Close()
}

// Threat: hasEntry opening a folder that was swapped for a link after the walk.
// A folder that is a link is not read.
func TestHasEntryDoesNotFollowALinkedFolder(t *testing.T) {
	tr := newTree(t)
	link := filepath.Join(tr.root, "linked")
	dirLink(t, link, tr.outside)
	if _, err := tr.r.hasEntry("linked", "payload.txt"); err == nil {
		t.Error("hasEntry read a folder that is a link")
	}
	if ok, err := tr.r.hasEntry("sub", "deep.md"); err != nil || !ok {
		t.Errorf("hasEntry of a plain folder = %v, %v", ok, err)
	}
}

// Threat (Windows): a root spelled so that text cannot relate it to the home
// directory above it -- a UNC path to the drive's administrative share -- is
// still judged by identity. Skipped where the share is not there.
func TestNewRootAncestryIsByIdentity(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("UNC spellings of a local folder are a Windows thing")
	}
	base := t.TempDir()
	home := filepath.Join(base, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	vol := filepath.VolumeName(base)
	if len(vol) != 2 || vol[1] != ':' {
		t.Skip("not a drive path")
	}
	unc := `\\localhost\` + vol[:1] + `$` + base[len(vol):]
	if _, err := os.Stat(unc); err != nil {
		t.Skip("the administrative share is not available:", err)
	}
	if r, err := NewRoot(unc); err == nil {
		r.Close()
		t.Errorf("NewRoot(%q) succeeded, but that is the folder that holds the home", unc)
	}
}
