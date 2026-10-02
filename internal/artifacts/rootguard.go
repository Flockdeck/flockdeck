package artifacts

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
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
func (r *Root) hasEntry(dir, name string) (bool, error) {
	d, err := r.r.Open(dir)
	if err != nil {
		return false, err
	}
	defer d.Close()
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

// forbiddenRoot says why real, a root's real directory, may not be a root, or
// "" if it may. A root is where every file below is a candidate, so it must not
// be a place that is not a project: a whole filesystem or drive, the user's home
// directory or any folder above it (home holds the secrets' folders, and a
// name check below a root cannot see the root's own name), the folders that
// exist to keep secrets or a program's private settings, or a system folder.
// It is a list of what is known; the caller still chooses roots only from
// paths the host itself picked.
func forbiddenRoot(real string) string {
	real = filepath.Clean(real)
	if filepath.Dir(real) == real || real == filepath.VolumeName(real)+string(filepath.Separator) {
		return "a filesystem or drive root"
	}
	homes := homeDirs()
	for _, h := range homes {
		if sameOrUnder(real, h) { // real is h or a folder above it
			return "the home directory or a folder above it"
		}
	}
	// Equal or below: these hold secrets and nothing else.
	for _, h := range homes {
		for _, sub := range []string{
			".ssh", ".aws", ".gnupg", ".kube", ".docker", ".azure", ".gcloud", ".password-store", ".vault", ".secrets",
			".m2", ".terraform.d", ".config/gh", ".config/gcloud", ".config/op", ".config/rclone", ".config/git",
			"Library/Keychains", "Library/Cookies", "AppData/Roaming/gnupg", "AppData/Roaming/Microsoft/Credentials",
			"AppData/Roaming/Microsoft/Protect", "AppData/Local/Microsoft/Credentials",
		} {
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

// homeDirs are the places this process takes to be the user's home.
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
	return out
}

func systemDirs() []string {
	var out []string
	for _, d := range []string{"/etc", "/proc", "/sys", "/dev", "/root", "/boot", "/private/etc", "/System", "/Library"} {
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

// sameOrUnder is whether p is base, or lies below it.
func sameOrUnder(base, p string) bool {
	if same(base, p) {
		return true
	}
	_, ok := under(canonOr(base), canonOr(p))
	return ok
}
