package artifacts

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/jmwri/flockdeck/internal/secretname"
)

// maxDirEntries bounds the look for one entry in a folder, so that a folder with
// an absurd number of names cannot hold a request.
const maxDirEntries = 1 << 20

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// hasEntry is whether the folder dir (a slash path below the root, "." for the
// root) has an entry named exactly name, byte for byte. It reads the folder's
// names and trusts no lookup, which is the point: a file system that ignores
// case or composition resolves a lookup of another spelling, and lists only the
// spelling the entry was made with.
//
// The folder is looked at without following a link first, and the folder that
// is then opened must be the same one: a folder swapped for a link in between
// is not read.
func (r *Root) hasEntry(dir, name string) (bool, error) {
	before, err := r.r.Lstat(dir)
	if err != nil {
		return false, err
	}
	if before.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 || !before.IsDir() {
		return false, errors.New("not a plain folder")
	}
	d, err := r.r.Open(dir)
	if err != nil {
		return false, err
	}
	defer d.Close()
	opened, err := d.Stat()
	if err != nil {
		return false, err
	}
	if !os.SameFile(before, opened) {
		return false, errors.New("the folder changed")
	}
	for seen := 0; ; {
		names, err := d.Readdirnames(256)
		for _, n := range names {
			if n == name {
				return true, nil
			}
		}
		seen += len(names)
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if seen > maxDirEntries {
			return false, errors.New("too many entries")
		}
	}
}

// passwdPath is where a Unix home directory is looked up when the environment
// does not say. A variable so that a test can point it elsewhere.
var passwdPath = "/etc/passwd"

// forbiddenRoot says why real, a root's real directory, may not be a root, or
// "" if it may. A root is where every file below is a candidate, so it must not
// be a place that is not a project: a whole filesystem or drive, the user's home
// directory or any folder above it (home holds the secrets' folders), the
// folders that exist to keep secrets or a program's private settings, a system
// folder, or a folder that is itself named as a secret (/run/secrets, a
// ".ssh"), which the name check below a root never sees. It is a list of what is
// known; the caller still chooses roots only from paths the host itself picked.
//
// It fails closed: if no home directory can be found at all (no HOME, no
// USERPROFILE, no entry for the process's user), nothing can be judged against
// it, and every root is refused.
func forbiddenRoot(real string) string {
	real = filepath.Clean(real)
	if filepath.Dir(real) == real || real == filepath.VolumeName(real)+string(filepath.Separator) {
		return "a filesystem or drive root"
	}
	homes := homeDirs()
	if len(homes) == 0 {
		return "the user's home directory is not known, so a root cannot be judged"
	}
	if lastPartsSecret(real) {
		return "a folder that is itself named as a secret"
	}
	for _, h := range homes {
		if sameOrUnder(real, h) { // real is h or a folder above it
			return "the home directory or a folder above it"
		}
	}
	// Equal or below: these hold secrets and nothing else.
	for _, h := range homes {
		for _, sub := range secretsUnderHome {
			if sameOrUnder(h+"/"+sub, real) {
				return "a folder that keeps secrets (" + sub + ")"
			}
		}
		// Equal only: a program's settings live below these, and the folder
		// itself holds every program's.
		for _, sub := range []string{
			".config", ".local", ".local/share", ".cache", "Library", "Library/Application Support", "Library/Preferences",
			"AppData", "AppData/Roaming", "AppData/Local", "AppData/LocalLow", "Documents", "Desktop", "Downloads",
		} {
			if same(h+"/"+sub, real) {
				return "a folder that holds every program's settings (" + sub + ")"
			}
		}
	}
	for _, e := range []string{"APPDATA", "LOCALAPPDATA", "PROGRAMDATA", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME"} {
		if v := os.Getenv(e); v != "" && filepath.IsAbs(v) && same(v, real) {
			return "a folder that holds every program's settings"
		}
	}
	if d, err := os.UserConfigDir(); err == nil && filepath.IsAbs(d) && same(d, real) {
		return "a folder that holds every program's settings"
	}
	if d, err := os.UserCacheDir(); err == nil && filepath.IsAbs(d) && same(d, real) {
		return "a folder that holds every program's settings"
	}
	for _, s := range systemDirs() {
		if sameOrUnder(s, real) {
			return "a system folder"
		}
	}
	return ""
}

// secretsUnderHome are folders below the home directory that keep secrets, a
// browser's or a mail program's profile among them.
var secretsUnderHome = []string{
	".ssh", ".aws", ".gnupg", ".kube", ".docker", ".azure", ".gcloud", ".password-store", ".vault", ".secrets",
	".m2", ".terraform.d", ".config/gh", ".config/gcloud", ".config/op", ".config/rclone", ".config/git",
	".local/share/keyrings", ".mozilla", ".thunderbird", ".config/google-chrome", ".config/chromium",
	".config/BraveSoftware", ".config/microsoft-edge", ".config/Bitwarden", ".config/1Password",
	"Library/Keychains", "Library/Cookies", "Library/Application Support/Google/Chrome",
	"Library/Application Support/Firefox", "Library/Application Support/BraveSoftware",
	"Library/Application Support/Microsoft Edge", "Library/Thunderbird",
	"AppData/Roaming/gnupg", "AppData/Roaming/Microsoft/Credentials", "AppData/Roaming/Microsoft/Protect",
	"AppData/Roaming/Mozilla", "AppData/Roaming/Thunderbird", "AppData/Local/Microsoft/Credentials",
	"AppData/Local/Google/Chrome", "AppData/Local/Microsoft/Edge", "AppData/Local/BraveSoftware",
}

// lastPartsSecret is whether the last one or two folders of real are a secret
// by name: the name check below a root only ever sees what is under it.
func lastPartsSecret(real string) bool {
	parts := strings.FieldsFunc(real, func(r rune) bool { return r == '/' || r == '\\' })
	if len(parts) > 2 {
		parts = parts[len(parts)-2:]
	}
	return secretname.Path(strings.Join(parts, "/"))
}

// homeDirs are the places taken to be the user's home: the environment's, and
// the entry of the process's user in the password file where there is one. An
// environment that is unset, empty, relative or points elsewhere does not hide
// the real one.
func homeDirs() []string {
	var out []string
	add := func(h string) {
		if h != "" && filepath.IsAbs(h) {
			out = append(out, filepath.ToSlash(filepath.Clean(h)))
		}
	}
	if h, err := os.UserHomeDir(); err == nil {
		add(h)
	}
	add(os.Getenv("HOME"))
	add(os.Getenv("USERPROFILE"))
	if hd, hp := os.Getenv("HOMEDRIVE"), os.Getenv("HOMEPATH"); hd != "" && hp != "" {
		add(hd + hp)
	}
	if h := passwdHome(os.Getuid()); h != "" {
		add(h)
	}
	return out
}

// passwdHome is the home directory the password file gives uid, or "". It reads
// the file; it does not use os/user, which this package may not import.
func passwdHome(uid int) string {
	if uid < 0 || runtime.GOOS == "windows" {
		return ""
	}
	b, err := os.ReadFile(passwdPath)
	if err != nil {
		return ""
	}
	want := strconv.Itoa(uid)
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Split(line, ":")
		if len(f) >= 6 && f[2] == want && f[5] != "" {
			return f[5]
		}
	}
	return ""
}

func systemDirs() []string {
	var out []string
	for _, d := range []string{"/etc", "/proc", "/sys", "/dev", "/root", "/boot", "/private/etc", "/System", "/Library", "/run/secrets"} {
		if filepath.IsAbs(d) { // an absolute path only on a platform that has it
			out = append(out, d)
		}
	}
	if v := os.Getenv("SystemRoot"); v != "" && filepath.IsAbs(v) {
		out = append(out, v)
	}
	return out
}

// canonOr is p with its links followed if it exists, else p as given.
func canonOr(p string) string {
	p = filepath.Clean(filepath.FromSlash(p))
	if c, err := canonical(p); err == nil {
		return c
	}
	return p
}

// same is whether a and b are the same folder, by name (ignoring case where the
// platform's file systems usually do) and, if both exist, by identity.
func same(a, b string) bool {
	a, b = canonOr(a), canonOr(b)
	if a == b || (foldCase && strings.EqualFold(a, b)) {
		return true
	}
	fa, errA := os.Stat(a)
	fb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}

// sameOrUnder is whether p is base, or lies below it. It is judged by text and
// by identity: every folder from p up to the top is compared with base by what
// the file system says they are, so a spelling of p that text cannot relate to
// base (a UNC path to a drive's share, \\localhost\C$\Users, a bind or another
// alias) is still found to be below it.
func sameOrUnder(base, p string) bool {
	cb := canonOr(base)
	bs, err := os.Stat(cb)
	if err != nil { // base is not there: only text can relate p to it
		cp := canonOr(p)
		_, ok := under(cb, cp)
		return ok || cb == cp || (foldCase && strings.EqualFold(cb, cp))
	}
	if same(base, p) {
		return true
	}
	if _, ok := under(cb, canonOr(p)); ok {
		return true
	}
	for q := filepath.Clean(filepath.FromSlash(p)); ; {
		if qs, err := os.Stat(q); err == nil && os.SameFile(qs, bs) {
			return true
		}
		parent := filepath.Dir(q)
		if parent == q {
			return false
		}
		q = parent
	}
}
