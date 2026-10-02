package artifacts

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/jmwri/flockdeck/internal/secretname"
)

// maxPasswdBytes bounds the read of the password file.
const maxPasswdBytes = 1 << 20

// guardEnv is everything forbiddenRoot asks of the machine, so that a test can
// give it another one without touching the process's environment.
type guardEnv struct {
	get     func(string) string // the environment
	uid     int                 // the process's user, or -1 if the platform has none
	passwd  string              // the password file, or "" for none
	profile func() string       // the system's own answer to "where is my home" (Windows), or nil
}

// osGuardEnv is the machine this process runs on.
func osGuardEnv() guardEnv {
	return guardEnv{get: os.Getenv, uid: os.Getuid(), passwd: "/etc/passwd", profile: osProfileDir}
}

// homes are the places taken to be the user's home: from the environment, from
// the parents of the settings folders the environment names, from the password
// file entry of the process's user, and from the system's own answer. Only a
// candidate that is an absolute path to an existing folder, and where the
// platform tells owners, owned by the process's user, is one: a value that is
// unset, empty, relative, wrong or someone else's is no home.
func (ge guardEnv) homes() []string {
	var cands []string
	for _, e := range []string{"HOME", "USERPROFILE"} {
		cands = append(cands, ge.get(e))
	}
	if hd, hp := ge.get("HOMEDRIVE"), ge.get("HOMEPATH"); hd != "" && hp != "" {
		cands = append(cands, hd+hp)
	}
	// %APPDATA% is <home>\AppData\Roaming and %LOCALAPPDATA% <home>\AppData\Local.
	for _, e := range []string{"APPDATA", "LOCALAPPDATA"} {
		if v := ge.get(e); v != "" {
			p := filepath.Dir(filepath.Dir(filepath.Clean(v)))
			if strings.EqualFold(filepath.Base(filepath.Dir(filepath.Clean(v))), "AppData") {
				cands = append(cands, p)
			}
		}
	}
	if h := passwdHome(ge.passwd, ge.uid); h != "" {
		cands = append(cands, h)
	}
	if ge.profile != nil {
		cands = append(cands, ge.profile())
	}
	var out []string
	seen := map[string]bool{}
	for _, h := range cands {
		if h == "" || !filepath.IsAbs(h) {
			continue
		}
		fi, err := os.Stat(h)
		if err != nil || !fi.IsDir() || !ownedBy(fi, ge.uid) {
			continue
		}
		h = filepath.ToSlash(filepath.Clean(h))
		if !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	return out
}

// passwdHome is the home directory the password file gives uid, or "". It reads
// the file (at most maxPasswdBytes of it); it does not use os/user, which this
// package may not import.
func passwdHome(path string, uid int) string {
	if path == "" || uid < 0 || runtime.GOOS == "windows" {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	b := make([]byte, maxPasswdBytes)
	n, _ := f.Read(b)
	return parsePasswd(b[:n], uid)
}

// parsePasswd is the home of uid in the text of a password file, or "".
func parsePasswd(data []byte, uid int) string {
	want := strconv.Itoa(uid)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r \t")
		f := strings.Split(line, ":")
		if len(f) >= 6 && f[2] == want && f[5] != "" {
			return strings.TrimSpace(f[5])
		}
	}
	return ""
}

// forbiddenRoot says why real, a root's real directory, may not be a root, or
// "" if it may. A root is where every file below is a candidate, so it must not
// be a place that is not a project: a whole filesystem or drive; a home
// directory, the user's or anyone's, or a folder that holds homes; the folders
// that keep secrets or a program's private settings; a system folder; or a
// place with a folder named as a secret on the way (/run/secrets, a ".ssh"
// anywhere above), which the name check below a root never sees. It is a list of
// what is known; the caller still chooses roots only from paths the host itself
// picked.
//
// It fails closed: unless at least one home directory is found (see homes),
// nothing can be judged against it and every root is refused.
func forbiddenRoot(real string, ge guardEnv) string {
	real = filepath.Clean(real)
	if filepath.Dir(real) == real || real == filepath.VolumeName(real)+string(filepath.Separator) {
		return "a filesystem or drive root"
	}
	homes := ge.homes()
	if len(homes) == 0 {
		return "no home directory could be found, so a root cannot be judged"
	}
	if why := namedAsSecret(real); why != "" {
		return why
	}
	if why := homeShape(real, homes); why != "" {
		return why
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
		for _, sub := range settingsUnderHome {
			if same(h+"/"+sub, real) {
				return "a folder that holds every program's settings (" + sub + ")"
			}
		}
	}
	for _, e := range []string{"APPDATA", "LOCALAPPDATA", "PROGRAMDATA", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME"} {
		if v := ge.get(e); v != "" && filepath.IsAbs(v) && same(v, real) {
			return "a folder that holds every program's settings"
		}
	}
	for _, s := range systemDirs(ge) {
		if sameOrUnder(s, real) {
			return "a system folder"
		}
	}
	return ""
}

// secretsUnderHome are folders below the home directory that keep secrets, a
// browser's or a mail program's profile among them. Root equal to one, or below
// it, is refused.
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

// settingsUnderHome are folders below the home directory that hold every
// program's settings or the user's whole documents: a root equal to one is
// refused, a project below one is not.
var settingsUnderHome = []string{
	".config", ".local", ".local/share", ".cache", "Library", "Library/Application Support", "Library/Preferences",
	"AppData", "AppData/Roaming", "AppData/Local", "AppData/LocalLow", "Documents", "Desktop", "Downloads",
}

// namedAsSecret is why real has a folder named as a secret on the way: the
// root itself by any of the names secretname knows as a secret folder
// (secrets, private, .ssh, User Data ...), and any folder above it by the names
// that are secret wherever they are (.ssh, .git, .aws ...) -- not the plain
// words, which are ordinary above a project (/private/var on macOS, ~/repos/private/app).
func namedAsSecret(real string) string {
	vol := filepath.VolumeName(real)
	parts := strings.FieldsFunc(real[len(vol):], func(r rune) bool { return r == '/' || r == '\\' })
	for i, c := range parts {
		last := i == len(parts)-1
		if (last && secretname.Folder(c)) || (!last && secretname.StrictFolder(c)) {
			return "a folder that is itself named as a secret (" + c + ")"
		}
		if !last && secretname.FolderPair(c, parts[i+1]) {
			return "a folder that keeps secrets (" + c + "/" + parts[i+1] + ")"
		}
	}
	return ""
}

// homeShape judges a path by where it is, without asking what the user's home
// is: whatever HOME says, /home/bob is a home. It refuses a folder that holds
// homes (/home, /Users, C:\Users, /mnt/c/Users), any home that is not one of
// homes (another user's, or one this process does not know as its own), a home
// itself, and a place in one that keeps secrets or settings (a project is not
// there).
func homeShape(real string, homes []string) string {
	vol := filepath.VolumeName(real)
	parts := strings.FieldsFunc(real[len(vol):], func(r rune) bool { return r == '/' || r == '\\' })
	lower := make([]string, len(parts))
	for i, p := range parts {
		lower[i] = strings.ToLower(p)
	}
	n := 0
	switch {
	case len(lower) == 1 && (lower[0] == "home" || lower[0] == "users" || lower[0] == "mnt"):
		return "a folder that holds homes or drives"
	case len(lower) == 2 && lower[0] == "mnt" && len(lower[1]) == 1:
		return "a mounted drive"
	case len(lower) == 3 && lower[0] == "mnt" && len(lower[1]) == 1 && lower[2] == "users":
		return "a folder that holds homes"
	case len(lower) >= 2 && (lower[0] == "home" || lower[0] == "users"):
		n = 2
	case len(lower) >= 4 && lower[0] == "mnt" && len(lower[1]) == 1 && lower[2] == "users":
		n = 4
	}
	if n == 0 {
		return ""
	}
	prefix := vol + string(filepath.Separator) + strings.Join(parts[:n], string(filepath.Separator))
	mine := false
	for _, h := range homes {
		if same(h, prefix) {
			mine = true
			break
		}
	}
	if !mine {
		return "another user's home directory, or one this process does not know as its own"
	}
	if len(lower) == n {
		return "the home directory itself"
	}
	return restForbidden(lower[n:])
}

// restForbidden is why a place below a home, given by its folders in lower
// case, may not be a root. A cloud folder that redirects the user's folders
// (OneDrive) is looked through.
func restForbidden(rest []string) string {
	if strings.HasPrefix(rest[0], "onedrive") {
		if len(rest) == 1 {
			return "a cloud folder that holds the user's folders"
		}
		rest = rest[1:]
	}
	joined := strings.Join(rest, "/")
	for _, sub := range secretsUnderHome {
		s := strings.ToLower(sub)
		if joined == s || strings.HasPrefix(joined, s+"/") {
			return "a folder that keeps secrets (" + sub + ")"
		}
	}
	for _, sub := range settingsUnderHome {
		if joined == strings.ToLower(sub) {
			return "a folder that holds every program's settings (" + sub + ")"
		}
	}
	return ""
}

func systemDirs(ge guardEnv) []string {
	var out []string
	for _, d := range []string{"/etc", "/proc", "/sys", "/dev", "/root", "/boot", "/private/etc", "/System", "/Library", "/run/secrets"} {
		if filepath.IsAbs(d) { // an absolute path only on a platform that has it
			out = append(out, d)
		}
	}
	for _, e := range []string{"SystemRoot", "ProgramFiles", "ProgramFiles(x86)", "ProgramW6432", "ProgramData"} {
		if v := ge.get(e); v != "" && filepath.IsAbs(v) {
			out = append(out, v)
		}
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
// base (a UNC path to a drive's share, \\localhost\C$\Users, another alias) is
// still found to be below it.
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
