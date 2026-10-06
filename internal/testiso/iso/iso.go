// Package iso keeps a package's tests away from the machine they run on: the
// real per-user config directory (agents.json, keys.json, the stored TypeSafe
// key) and the real network.
//
// It imports nothing of Flockdeck's, so any package's tests may use it, the
// store package's own included. Other packages should use internal/testiso,
// which does the same and also arms the guard in internal/store.
//
// Main points every variable os.UserConfigDir and os.UserHomeDir read at a
// temporary directory, clears the API keys in the environment, and replaces
// http.DefaultTransport with one that refuses any host but loopback. Anything
// refused is remembered, so a test that swallowed the error still fails the
// run.
package iso

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// ErrNonLoopback is what a refused request or connection fails with.
var ErrNonLoopback = errors.New("testiso: tests may only talk to loopback; use httptest.NewServer, not a real host")

// These carry the real directories down to a test binary that a test starts
// (the program under test re-run as a child), which would otherwise take the
// first one's temporary directories for the real ones.
const (
	realConfigEnv = "FLOCKDECK_TESTISO_REAL_CONFIG"
	realHomeEnv   = "FLOCKDECK_TESTISO_REAL_HOME"
)

var (
	mu         sync.Mutex
	violations []string

	realConfig string
	realHome   string
)

// RealConfigDir is the user's actual config directory, as it was before Main
// redirected it. Tests use it only as a path to prove the guard trips on it;
// nothing opens it.
func RealConfigDir() string { return realConfig }

// RealHomeDir is the user's actual home directory, as it was before Main.
func RealHomeDir() string { return realHome }

// ChildEnv is what a test that builds a test binary's environment by hand adds
// to it, so the child keeps the directories that environment names and still
// knows which one is the real one. A child given os.Environ() has it already.
func ChildEnv() []string {
	return []string{realConfigEnv + "=" + realConfig, realHomeEnv + "=" + realHome}
}

// Violations lists what the guards refused so far.
func Violations() []string {
	mu.Lock()
	defer mu.Unlock()
	return append([]string(nil), violations...)
}

// ResetViolations forgets them, for a test that trips a guard on purpose.
func ResetViolations() {
	mu.Lock()
	violations = nil
	mu.Unlock()
}

func record(format string, args ...any) string {
	msg := fmt.Sprintf(format, args...)
	mu.Lock()
	violations = append(violations, msg)
	mu.Unlock()
	return msg
}

// Main is a TestMain body: os.Exit(iso.Main(m)).
func Main(m *testing.M) int {
	if cfg, ok := os.LookupEnv(realConfigEnv); ok {
		// A test binary a test started, with the environment it chose for it:
		// that environment stays, and "real" is still what the first one found.
		realConfig, realHome = cfg, os.Getenv(realHomeEnv)
	} else {
		realConfig, _ = os.UserConfigDir()
		realHome, _ = os.UserHomeDir()
		os.Setenv(realConfigEnv, realConfig)
		os.Setenv(realHomeEnv, realHome)
		dir, err := os.MkdirTemp("", "flockdeck-test-home-")
		if err != nil {
			fmt.Fprintln(os.Stderr, "testiso:", err)
			return 1
		}
		defer os.RemoveAll(dir)
		redirect(dir)
		clearKeys()
	}
	InstallNetworkGuard()

	code := m.Run()
	if v := Violations(); len(v) > 0 && code == 0 {
		fmt.Fprintf(os.Stderr, "testiso: %d guarded access(es) were refused during the run, even if a test swallowed the error:\n  %s\n",
			len(v), strings.Join(v, "\n  "))
		code = 1
	}
	return code
}

// redirect points every variable the config and home directories are found
// through at dir, under every name each platform uses.
func redirect(dir string) {
	cfg := filepath.Join(dir, "config")
	local := filepath.Join(dir, "local")
	for _, d := range []string{cfg, local} {
		_ = os.MkdirAll(d, 0o700)
	}
	for k, v := range map[string]string{
		"APPDATA":         cfg,   // Windows
		"LOCALAPPDATA":    local, // Windows
		"XDG_CONFIG_HOME": cfg,   // Linux
		"HOME":            dir,   // Linux, macOS (Library/Application Support is under it)
		"USERPROFILE":     dir,   // Windows
	} {
		os.Setenv(k, v)
	}
	// Claude Code's own config directory is wherever CLAUDE_CONFIG_DIR says,
	// and under the home directory only when it is unset. Left alone, a
	// developer's value would send tests to their real one.
	os.Unsetenv("CLAUDE_CONFIG_DIR")
	if runtime.GOOS == "windows" {
		os.Unsetenv("HOMEDRIVE")
		os.Unsetenv("HOMEPATH")
	}
}

// clearKeys unsets every API key in the environment. A test that needs one
// sets it with t.Setenv; none gets the developer's own by inheriting it.
func clearKeys() {
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		u := strings.ToUpper(name)
		if strings.HasSuffix(u, "_API_KEY") || strings.HasSuffix(u, "_API_TOKEN") || strings.HasSuffix(u, "_AUTH_TOKEN") {
			os.Unsetenv(name)
		}
	}
}

// UnderRealConfig reports whether dir is the user's real config directory or
// anything below it.
func UnderRealConfig(dir string) bool {
	if realConfig == "" || dir == "" {
		return false
	}
	return within(realConfig, dir)
}

func within(root, dir string) bool {
	root, dir = filepath.Clean(root), filepath.Clean(dir)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		root, dir = strings.ToLower(root), strings.ToLower(dir)
	}
	return dir == root || strings.HasPrefix(dir, root+string(filepath.Separator))
}

// DirGuard is for store.DirGuard: it panics, which fails the test that did it
// with a stack pointing at the caller, when dir is the user's real config
// directory.
func DirGuard(dir string) {
	if UnderRealConfig(dir) {
		panic(record("testiso: a test resolved the real user config dir %q; point APPDATA, XDG_CONFIG_HOME and HOME at t.TempDir() (or let testiso.Main do it)", dir))
	}
}

// loopback reports whether host, with or without a port, is this machine.
func loopback(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// GuardTransport refuses a request to any host but loopback, before any name
// is looked up, and hands the rest to inner.
func GuardTransport(inner http.RoundTripper) http.RoundTripper {
	return guardRT{inner}
}

type guardRT struct{ inner http.RoundTripper }

func (g guardRT) RoundTrip(req *http.Request) (*http.Response, error) {
	if !loopback(req.URL.Host) {
		record("testiso: refused %s %s://%s", req.Method, req.URL.Scheme, req.URL.Host)
		return nil, fmt.Errorf("%s %s://%s: %w", req.Method, req.URL.Scheme, req.URL.Host, ErrNonLoopback)
	}
	return g.inner.RoundTrip(req)
}

// InstallNetworkGuard makes http.DefaultTransport, which http.DefaultClient and
// every client without a Transport of its own use, refuse non-loopback hosts,
// and refuses non-loopback dials under it as well. A client with a Transport of
// its own, or a raw net.Dial, is not covered.
func InstallNetworkGuard() {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return // already replaced
	}
	t := base.Clone()
	dial := t.DialContext
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if !loopback(addr) {
			record("testiso: refused dial %s %s", network, addr)
			return nil, fmt.Errorf("dial %s: %w", addr, ErrNonLoopback)
		}
		return dial(ctx, network, addr)
	}
	t.Proxy = nil // a proxy would make every host look like the proxy's
	http.DefaultTransport = GuardTransport(t)
}
