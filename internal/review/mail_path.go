package review

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// MailEnv is where a mail read will run, as far as Flockdeck knows it: the
// PATH its first word is looked up on, and the application that is running.
// MailRead allows the word flockdeck only if it resolves, on Path, to Self or
// to a file in Self's directory.
//
// Path is the PATH the pane was started with. The agent can
// change its own shell's PATH afterwards (a virtual environment, a prompt that
// exports it), which Flockdeck cannot see, so this narrows what is allowed and
// does not close it: a shell function or alias named flockdeck is not visible
// here either.
type MailEnv struct {
	// Path is a PATH value, in the form of the platform's list separator.
	Path string
	// Self is the running application's executable. Empty allows nothing.
	Self string
}

// MailEnvFrom is the MailEnv for a command that will run with environ (a list
// of NAME=value strings) and the application whose executable is self. The
// name PATH is matched in any letter case on Windows, where it is usually Path.
func MailEnvFrom(environ []string, self string) MailEnv {
	var path string
	for _, kv := range environ {
		name, value, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if name == "PATH" || (runtime.GOOS == "windows" && strings.EqualFold(name, "PATH")) {
			// The last one wins, as in the environment a child is given.
			path = value
		}
	}
	return MailEnv{Path: path, Self: self}
}

// runsThisApp reports whether the word flockdeck, looked up on e.Path from cwd,
// is the running application. A relative entry in Path, and an empty one, are
// taken from cwd, where the command runs.
func (e MailEnv) runsThisApp(cwd string) bool {
	if e.Self == "" {
		return false
	}
	// A shell expands a leading ~ in a PATH entry to a home directory, which
	// is not known here. Ask rather than guess where it points.
	for _, dir := range filepath.SplitList(e.Path) {
		if strings.HasPrefix(dir, "~") {
			return false
		}
	}
	found, ok := lookOn("flockdeck", e.Path, cwd)
	if !ok {
		return false
	}
	self := realPath(e.Self)
	found = realPath(found)
	if fa, err := os.Stat(found); err == nil {
		if fb, err := os.Stat(self); err == nil && os.SameFile(fa, fb) {
			return true
		}
	}
	return sameDir(filepath.Dir(found), filepath.Dir(self))
}

// lookOn finds name on path the way a shell does: the first directory that
// holds a regular file of that name that can be run, with the platform's
// executable extensions added on Windows. It returns the path to it.
func lookOn(name, path, cwd string) (string, bool) {
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			dir = "."
		}
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(cwd, dir)
		}
		for _, candidate := range candidates(name) {
			p := filepath.Join(dir, candidate)
			if runnable(p) {
				return p, true
			}
		}
	}
	return "", false
}

// candidates are the file names that name can be run as in a directory.
func candidates(name string) []string {
	if runtime.GOOS != "windows" {
		return []string{name}
	}
	// The bare name comes first: Git Bash runs an extensionless script (an npm
	// shim, say) found on PATH, which PATHEXT does not list.
	out := []string{name}
	exts := os.Getenv("PATHEXT")
	if exts == "" {
		exts = ".COM;.EXE;.BAT;.CMD"
	}
	for _, ext := range strings.Split(exts, ";") {
		if ext != "" {
			out = append(out, name+ext)
		}
	}
	return out
}

// runnable reports whether p is a regular file a shell would run.
func runnable(p string) bool {
	fi, err := os.Stat(p)
	if err != nil || fi.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return fi.Mode()&0o111 != 0
}

// realPath is p with symbolic links resolved, or cleaned if that fails.
func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

// sameDir reports whether two directory paths name one place.
func sameDir(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	// Case-insensitive on macOS as well, where the default volume is; a
	// case-sensitive volume would only make this allow a directory differing
	// in case, which is theoretical.
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
