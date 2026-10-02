//go:build !windows

package artifacts

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Threat: a symbolic link in the project that leads out of it -- to a file or
// to a folder -- which an agent or another local process can make with one
// command. Refused, and nothing outside is read.
func TestSymlinkOutOfRootIsRefused(t *testing.T) {
	tr := newTree(t)
	if err := os.Symlink(filepath.Join(tr.outside, "payload.txt"), filepath.Join(tr.root, "report.html")); err != nil {
		cannot(t, "cannot make symlinks here:", err)
	}
	if err := os.Symlink(tr.outside, filepath.Join(tr.root, "dirlink")); err != nil {
		t.Fatal(err)
	}
	tr.mustRefuse("report.html", ReasonLink)
	tr.mustRefuse("dirlink/payload.txt", ReasonLink, ReasonOutside)
	tr.mustRefuse(filepath.Join(tr.root, "dirlink", "payload.txt"), ReasonLink, ReasonOutside)
}

// Threat: a link that stays inside the project but points at a secret there --
// notes.txt -> .env -- which would pass a check of the name it is asked for by.
// Every link is refused, not only the ones that leave.
func TestSymlinkToSecretInsideRootIsRefused(t *testing.T) {
	tr := newTree(t)
	if err := os.Symlink(".env", filepath.Join(tr.root, "notes.txt")); err != nil {
		cannot(t, "cannot make symlinks here:", err)
	}
	if err := os.Symlink(".git", filepath.Join(tr.root, "innocent")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("ok.txt", filepath.Join(tr.root, "alias.txt")); err != nil {
		t.Fatal(err)
	}
	tr.mustRefuse("notes.txt", ReasonLink)
	tr.mustRefuse("innocent/config", ReasonLink)
	tr.mustRefuse("alias.txt", ReasonLink) // harmless, but refused: the rule is no links
}

// Threat: a hardlink. A second name for a file outside the project, made inside
// it, is a plain file by every check of its path; only its link count gives it
// away. Also refused: a second name for a file inside, so that the one name
// that was vetted is the only one there is.
func TestHardlinkIsRefused(t *testing.T) {
	tr := newTree(t)
	if err := os.Link(filepath.Join(tr.outside, "payload.txt"), filepath.Join(tr.root, "innocent.txt")); err != nil {
		cannot(t, "cannot make hardlinks here:", err)
	}
	if err := os.Link(filepath.Join(tr.root, ".env"), filepath.Join(tr.root, "notes.txt")); err != nil {
		t.Fatal(err)
	}
	tr.mustRefuse("innocent.txt", ReasonHardlink)
	tr.mustRefuse("notes.txt", ReasonHardlink)
}

// Threat: a pipe where a file was. Opening a FIFO that nobody writes to blocks
// for ever; it would hold a request, and the socket, open. It must be refused
// at once and never opened.
func TestFifoIsRefusedWithoutBlocking(t *testing.T) {
	tr := newTree(t)
	if err := syscall.Mkfifo(filepath.Join(tr.root, "pipe"), 0o644); err != nil {
		t.Skip("cannot make a FIFO here:", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		tr.mustRefuse("pipe", ReasonNotRegular)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Open blocked on a FIFO")
	}
}

// Threat: a project folder that is itself reached through a link (a macOS
// /tmp, a home folder symlinked to another disk) is the caller's own choice and
// must still work, by either spelling of its path; links *inside* it still do
// not.
func TestRootReachedThroughALink(t *testing.T) {
	tr := newTree(t)
	alias := filepath.Join(tr.base, "alias")
	if err := os.Symlink(tr.root, alias); err != nil {
		cannot(t, "cannot make symlinks here:", err)
	}
	r, err := NewRoot(alias)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, c := range []string{"ok.txt", filepath.Join(alias, "ok.txt"), filepath.Join(r.Path(), "ok.txt")} {
		f, err := r.Open(c)
		if err != nil {
			t.Fatalf("Open(%q): %v", c, err)
		}
		f.Close()
	}
}

// Threat: a race. The path is a plain file when it is checked and a link to a
// secret when it is opened (swap after check). The open must not hand back the
// secret.
func TestSwapAfterCheckIsCaught(t *testing.T) {
	tr := newTree(t)
	secret := filepath.Join(tr.outside, "payload.txt")
	target := filepath.Join(tr.root, "ok.txt")
	raceHook = func(stage string) {
		if stage != "checked" {
			return
		}
		os.Remove(target)
		os.Symlink(secret, target)
	}
	defer func() { raceHook = func(string) {} }()
	if err := os.Symlink(secret, filepath.Join(tr.base, "probe")); err != nil {
		cannot(t, "cannot make symlinks here:", err)
	}
	tr.mustRefuse("ok.txt")
}

// Threat: the same race one step later: the file is open and vetted-in-waiting,
// and the path is swapped for a link before the second look. The second look is
// the one that counts.
func TestSwapAfterOpenIsCaught(t *testing.T) {
	tr := newTree(t)
	secret := filepath.Join(tr.outside, "payload.txt")
	target := filepath.Join(tr.root, "ok.txt")
	raceHook = func(stage string) {
		if stage != "opened" {
			return
		}
		os.Remove(target)
		os.Symlink(secret, target)
	}
	defer func() { raceHook = func(string) {} }()
	if err := os.Symlink(secret, filepath.Join(tr.base, "probe")); err != nil {
		cannot(t, "cannot make symlinks here:", err)
	}
	tr.mustRefuse("ok.txt", ReasonLink)
}

// Threat: the path is swapped for a *different plain file* after the open -- not
// a link at all -- so what was opened is not what the path now names. Caught by
// comparing the open file with the path's own, not by looking for links.
func TestSwapForAnotherFileAfterOpenIsCaught(t *testing.T) {
	tr := newTree(t)
	target := filepath.Join(tr.root, "ok.txt")
	other := tr.write("proj/other.txt", "other")
	raceHook = func(stage string) {
		if stage != "opened" {
			return
		}
		os.Rename(other, target)
	}
	defer func() { raceHook = func(string) {} }()
	tr.mustRefuse("ok.txt", ReasonChanged)
}

// Threat: a file the process may not read must not turn into a different kind
// of answer. Skipped when run as root, which can read it.
func TestUnreadableFileIsRefused(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read anything")
	}
	tr := newTree(t)
	p := tr.write("proj/locked.txt", "x")
	if err := os.Chmod(p, 0); err != nil {
		t.Fatal(err)
	}
	tr.mustRefuse("locked.txt", ReasonMissing)
}

// Threat: a backslash is an ordinary file-name character on Unix, which a
// Windows-minded check would take for a separator; a path using one is refused
// rather than judged twice, two ways.
func TestBackslashIsRefusedOnUnix(t *testing.T) {
	tr := newTree(t)
	tr.write("proj/a\\b.txt", "x")
	tr.mustRefuse(`a\b.txt`, ReasonLexical)
	tr.mustRefuse(`sub\..\ok.txt`, ReasonLexical)
}

// replaceDirWithLink swaps a folder for a symbolic link to target, and says
// whether it did.
func replaceDirWithLink(t testing.TB, dir, target string) bool {
	t.Helper()
	if err := os.Rename(dir, dir+".moved"); err != nil {
		t.Error(err)
		return false
	}
	if err := os.Symlink(target, dir); err != nil {
		cannot(t, "cannot make symlinks here:", err)
		return false
	}
	return true
}

// plantLinks puts the links a fuzzed Open must never be led through into the
// tree: a symlink to a file outside, one to a file inside, one to a folder
// outside, and a hardlink to a file outside. A link that cannot be made is a
// failure under CI, a skip of the fuzz run elsewhere.
func plantLinks(tb testing.TB, base string) {
	tb.Helper()
	proj, outside := filepath.Join(base, "proj"), filepath.Join(base, "outside")
	for _, l := range [][2]string{
		{filepath.Join(outside, "payload.txt"), filepath.Join(proj, "lnk-out")},
		{"ok.txt", filepath.Join(proj, "lnk-in")},
		{outside, filepath.Join(proj, "lnk-dir")},
	} {
		if err := os.Symlink(l[0], l[1]); err != nil {
			cannot(tb, "cannot make symlinks here:", err)
		}
	}
	if err := os.Link(filepath.Join(outside, "payload.txt"), filepath.Join(proj, "hard.txt")); err != nil {
		cannot(tb, "cannot make hardlinks here:", err)
	}
}
