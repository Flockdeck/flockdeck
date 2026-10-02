package artifacts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// junction makes a directory junction, which any user may make without a
// privilege and which points anywhere on the machine. filepath.EvalSymlinks
// does not follow one, which is why a check built on it alone is not enough.
func junction(t *testing.T, link, target string) {
	t.Helper()
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		cannot(t, "cannot make a junction here:", err, string(out))
	}
}

// Threat: a junction inside the project that leads out of it, or to a secret
// folder inside it. Both are refused, and nothing is read through them.
func TestJunctionIsRefused(t *testing.T) {
	tr := newTree(t)
	junction(t, filepath.Join(tr.root, "jct"), tr.outside)
	junction(t, filepath.Join(tr.root, "gitjct"), filepath.Join(tr.root, ".git"))
	tr.mustRefuse("jct/payload.txt", ReasonLink, ReasonOutside)
	tr.mustRefuse(`jct\payload.txt`, ReasonLink, ReasonOutside)
	tr.mustRefuse(filepath.Join(tr.root, "jct", "payload.txt"), ReasonLink, ReasonOutside)
	tr.mustRefuse("gitjct/config", ReasonLink)
}

// Threat: a symbolic link (needs a privilege or developer mode on Windows, so
// skipped without), to a file outside and to a secret inside.
func TestSymlinkIsRefused(t *testing.T) {
	tr := newTree(t)
	if err := os.Symlink(filepath.Join(tr.outside, "payload.txt"), filepath.Join(tr.root, "report.html")); err != nil {
		cannot(t, "cannot make symlinks here:", err)
	}
	if err := os.Symlink(".env", filepath.Join(tr.root, "notes.txt")); err != nil {
		t.Fatal(err)
	}
	tr.mustRefuse("report.html", ReasonLink)
	tr.mustRefuse("notes.txt", ReasonLink)
}

// Threat: NTFS alternate data streams. "file.txt:hidden" and
// "file.txt::$DATA" name streams of a file, and a stream of .env is a way to
// reach .env under a name that does not end in it.
func TestAlternateDataStreamsAreRefused(t *testing.T) {
	tr := newTree(t)
	for _, c := range []string{
		"ok.txt:s", "ok.txt::$DATA", ".env::$DATA", ".env:s", "sub:s/deep.md", "sub::$INDEX_ALLOCATION",
		filepath.Join(tr.root, "ok.txt") + ":s",
	} {
		tr.mustRefuse(c, ReasonLexical)
	}
}

// Threat: reserved device names. Opening CON, NUL, AUX or COM1 -- with any
// extension, in any case -- reaches a device and not a file, and can hang a
// read for ever.
func TestReservedDeviceNamesAreRefused(t *testing.T) {
	tr := newTree(t)
	for _, c := range []string{"con", "CON", "nul", "NUL.txt", "aux", "prn", "com1", "COM9.log", "lpt1", "sub/con.md", "CONIN$"} {
		tr.mustRefuse(c, ReasonLexical)
	}
	// The device namespace, spelled out: not under the root at all.
	for _, c := range []string{`\\.\COM1`, `\\.\C:\Windows\win.ini`} {
		tr.mustRefuse(c, ReasonOutside, ReasonLexical)
	}
}

// Threat: an 8.3 alias with no tilde in it. NTFS lets an administrator set any
// short name for a file (fsutil file setshortname), so "ENVX.TXT" can be
// another name for secret-credentials.txt, and the spelling check on "~digit"
// does not see it. Open itself must refuse it, by the real name read back from
// the open file -- not only the helper, called on its own.
func TestOpenCatchesAShortNameWithNoTilde(t *testing.T) {
	tr := newTree(t)
	long := filepath.Join(tr.root, "secret-credentials.txt")
	if out, err := exec.Command("fsutil", "file", "setshortname", long, "ENVX.TXT").CombinedOutput(); err != nil {
		cannot(t, "cannot set a short name here (needs administrator and 8.3 names on):", err, string(out))
	}
	tr.mustRefuse("ENVX.TXT", ReasonDenied)
	tr.mustRefuse(filepath.Join(tr.root, "ENVX.TXT"), ReasonDenied)
	// A harmless file with a tilde-less alias is judged by its real name too: it
	// is refused as a different name from the one asked for, never shown under
	// two names.
	ok := tr.write("proj/a-harmless-long-name.txt", "ok") // a name that is not itself 8.3, so it can have an alias
	if out, err := exec.Command("fsutil", "file", "setshortname", ok, "OKALIAS.TXT").CombinedOutput(); err != nil {
		t.Fatalf("setting a second short name: %v: %s", err, out)
	}
	tr.mustRefuse("OKALIAS.TXT", ReasonChanged)
	if got, err := tr.open("a-harmless-long-name.txt"); err != nil || got != "ok" {
		t.Fatalf("the file under its real name: %q, %v", got, err)
	}
}

// Threat: Windows drops trailing dots and spaces from a name, so "id_rsa." and
// ".env " are .env and id_rsa to the file system but not to a name check. And
// "ok.txt." is, to Go's own root, the file ok.txt.
func TestTrailingDotsAndSpacesAreRefused(t *testing.T) {
	tr := newTree(t)
	for _, c := range []string{"ok.txt.", "ok.txt ", ".env.", ".env ", "id_rsa.", "sub./deep.md", "sub /deep.md", "ok.txt. ."} {
		tr.mustRefuse(c, ReasonLexical)
	}
}

// Threat: 8.3 short names. secret-credentials.txt is also SECRET~1.TXT, a name
// no check for "credential" recognises. Refused by its spelling; and, as a
// second line, by what the system says the open file is really called.
func TestShortNamesAreRefused(t *testing.T) {
	tr := newTree(t)
	long := filepath.Join(tr.root, "secret-credentials.txt")
	short := shortPath(t, long)
	if strings.EqualFold(short, long) {
		t.Skip("8.3 names are turned off on this volume")
	}
	tr.mustRefuse(filepath.Base(short), ReasonLexical)
	// Whole path in its short form: outside by text (the root is spelled long),
	// or lexical where only the last part is short; refused either way.
	tr.mustRefuse(short, ReasonLexical, ReasonOutside)

	// The second line: pretend the spelling was let through, and ask what the
	// open file is really called.
	f, err := os.Open(short)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := tr.r.checkRealName(f, filepath.Base(short)); ReasonOf(err) != ReasonDenied {
		t.Fatalf("checkRealName on the short name: %v (%q), want denied", err, ReasonOf(err))
	}
	// And a harmless file under its real name passes the same check.
	ok, err := os.Open(filepath.Join(tr.root, "ok.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer ok.Close()
	if err := tr.r.checkRealName(ok, "ok.txt"); err != nil {
		t.Fatalf("checkRealName on a plain file: %v", err)
	}
	// A short name for a folder on the way is refused by its spelling too.
	tr.write("proj/a-long-folder-name/x.md", "x")
	if sd := shortPath(t, filepath.Join(tr.root, "a-long-folder-name")); !strings.EqualFold(sd, filepath.Join(tr.root, "a-long-folder-name")) {
		tr.mustRefuse(filepath.Join(sd, "x.md"), ReasonLexical, ReasonOutside)
		tr.mustRefuse(filepath.Base(sd)+"/x.md", ReasonLexical)
	}
}

func shortPath(t *testing.T, p string) string {
	t.Helper()
	u, err := syscall.UTF16PtrFromString(p)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, 520)
	n, err := syscall.GetShortPathName(u, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 {
		t.Skip("no short name available:", err)
	}
	return syscall.UTF16ToString(buf[:n])
}

// Threat: other namespaces. An extended-length path (\\?\C:\...) and a UNC path
// (\\localhost\c$\...) name the project's own files in a spelling the root's
// prefix does not match, and a drive-relative "C:foo" is relative to a
// directory that is not the project. All are outside, whatever they reach.
func TestUNCAndExtendedPathsAreRefused(t *testing.T) {
	tr := newTree(t)
	ok := filepath.Join(tr.root, "ok.txt")
	vol := filepath.VolumeName(ok)
	unc := `\\localhost\` + strings.Replace(vol, ":", "$", 1) + strings.TrimPrefix(ok, vol)
	for _, c := range []string{
		`\\?\` + ok, // "?" is refused as a character
		`\\?\UNC\localhost\` + strings.Replace(vol, ":", "$", 1) + strings.TrimPrefix(ok, vol),
	} {
		tr.mustRefuse(c, ReasonLexical)
	}
	for _, c := range []string{
		unc,
		`\\.\` + ok,
		vol + "ok.txt",
		`\ok.txt`,
		"/" + "ok.txt",
	} {
		tr.mustRefuse(c, ReasonOutside)
	}
}

// Threat: a second name for a file outside the project, made inside it
// (NTFS hardlink), is a plain file by every check of its path.
func TestHardlinkIsRefusedOnWindows(t *testing.T) {
	tr := newTree(t)
	if err := os.Link(filepath.Join(tr.outside, "payload.txt"), filepath.Join(tr.root, "innocent.txt")); err != nil {
		cannot(t, "cannot make hardlinks here:", err)
	}
	tr.mustRefuse("innocent.txt", ReasonHardlink)
}

// Threat: the drive letter and the folders in an absolute path are spelled in
// other capitals than the root's; that is the same place and judged the same.
func TestAbsolutePathCaseAndSeparators(t *testing.T) {
	tr := newTree(t)
	p := filepath.Join(tr.root, "ok.txt")
	for _, c := range []string{
		strings.ToLower(p), strings.ToUpper(p),
		strings.ReplaceAll(p, `\`, "/"),
	} {
		got, err := tr.open(c)
		if err != nil || got != "ok" {
			t.Errorf("Open(%q) = %q, %v", c, got, err)
		}
	}
	tr.mustRefuse(strings.ToUpper(filepath.Join(tr.root, ".env")), ReasonDenied)
}

// replaceDirWithLink swaps a folder for a junction to target, and says whether
// it did. Windows refuses to rename a folder that has a file open in it, which
// is itself a defence: the swap cannot be made after the file is open.
func replaceDirWithLink(t testing.TB, dir, target string) bool {
	t.Helper()
	if err := os.Rename(dir, dir+".moved"); err != nil {
		t.Log("the folder cannot be renamed while a file in it is open:", err)
		return false
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", dir, target).CombinedOutput(); err != nil {
		cannot(t, "cannot make a junction here:", err, string(out))
		return false
	}
	return true
}

// plantLinks puts the links a fuzzed Open must never be led through into the
// tree: a junction to a folder outside, a hardlink to a file outside, symbolic
// links where the privilege allows, and a file with a tilde-less 8.3 alias
// where it can be set.
func plantLinks(tb testing.TB, base string) {
	tb.Helper()
	proj, outside := filepath.Join(base, "proj"), filepath.Join(base, "outside")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(proj, "jct"), outside).CombinedOutput(); err != nil {
		cannot(tb, "cannot make a junction here:", err, string(out))
	}
	if err := os.Link(filepath.Join(outside, "payload.txt"), filepath.Join(proj, "hard.txt")); err != nil {
		cannot(tb, "cannot make hardlinks here:", err)
	}
	// Symbolic links need a privilege that a developer's machine may not have;
	// where it is missing the junction and the hardlink are what are tried.
	_ = os.Symlink(filepath.Join(outside, "payload.txt"), filepath.Join(proj, "lnk-out"))
	_ = os.Symlink("ok.txt", filepath.Join(proj, "lnk-in"))
	_ = os.Symlink(outside, filepath.Join(proj, "lnk-dir"))
	_ = exec.Command("fsutil", "file", "setshortname", filepath.Join(proj, "secret-credentials.txt"), "ENVX.TXT").Run()
}
