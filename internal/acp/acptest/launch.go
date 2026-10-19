package acptest

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Option changes how the fake agent behaves.
type Option func(*launch)

type launch struct{ env []string }

// WithAuth makes the agent refuse until authenticate is called: at "new" it
// answers session/new with -32000 "Authentication required", at "prompt"
// session/prompt. It then also lists two auth methods at initialize.
func WithAuth(at string) Option {
	return func(l *launch) { l.env = append(l.env, envAuth+"="+at) }
}

// WithMarker makes the hold-perm scenario write the outcome it received to
// path.
func WithMarker(path string) Option {
	return func(l *launch) { l.env = append(l.env, envMarker+"="+path) }
}

// Command returns what to start the fake agent with: an absolute path, its
// arguments and a full environment. The environment is an allowlist, not the
// test's own, so nothing of the user's reaches the child.
func Command(t testing.TB, opts ...Option) (path string, args []string, env []string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("acptest: find the test binary: %v", err)
	}
	l := &launch{env: []string{envAgent + "=1"}}
	for _, o := range opts {
		o(l)
	}
	for _, k := range []string{"PATH", "SYSTEMROOT", "SystemRoot", "SYSTEMDRIVE", "COMSPEC", "PATHEXT", "TEMP", "TMP", "TMPDIR"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return exe, nil, append(env, l.env...)
}

// SessionDir returns a fresh directory to use as a session's cwd, outside the
// user's profile where it can be. An adapter walks up from the cwd looking for
// project settings, and with a cwd under the profile it found the user's own
// skills. On Windows the temp directory is under the profile, so the drive's
// root is used; if a directory cannot be made there, t.TempDir is the
// fallback. It is removed when the test ends.
func SessionDir(t testing.TB) string {
	t.Helper()
	// The profile cannot be asked for: the test isolation redirects it. The
	// temp directory is under it on Windows and outside it elsewhere (/tmp,
	// /var/folders), so Windows goes to the drive's root first.
	roots := []string{os.TempDir()}
	if runtime.GOOS == "windows" {
		roots = []string{volumeRoot(), os.TempDir()}
	}
	for _, root := range roots {
		if root == "" {
			continue
		}
		dir, err := os.MkdirTemp(root, "flockdeck-acp-")
		if err != nil {
			continue
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		abs, err := filepath.Abs(dir)
		if err != nil {
			return dir
		}
		return abs
	}
	return t.TempDir()
}

func volumeRoot() string {
	if v := filepath.VolumeName(os.TempDir()); v != "" {
		return v + string(filepath.Separator)
	}
	return ""
}

// WithMute makes the agent read everything and answer nothing.
func WithMute() Option {
	return func(l *launch) { l.env = append(l.env, envMute+"=1") }
}

// WithDeaf makes the agent never read its input, so writes to it fill the
// pipe and then block.
func WithDeaf() Option {
	return func(l *launch) { l.env = append(l.env, envDeaf+"=1") }
}
