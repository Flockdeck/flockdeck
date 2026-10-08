//go:build !windows

package channel

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func socketPathFor(stateDir string, pid int, started time.Time) string {
	return filepath.Join(stateDir, "c", ID(pid, started)+".sock")
}

func TestPrimaryPlacementIsInTheStateDirectory(t *testing.T) {
	state := t.TempDir()
	tmp := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	c := start(t, func(cfg *Config) { cfg.StateDir = state; cfg.Seams.Tmp = tmp })
	if got, want := filepath.Dir(c.Path()), filepath.Join(state, "c"); got != want {
		t.Fatalf("socket is in %s, want %s", got, want)
	}
	if !strings.HasSuffix(c.Path(), ID(os.Getpid(), c.cfg.Started)+".sock") {
		t.Fatalf("name %s does not carry the instance hash", c.Path())
	}
	if rt := os.Getenv("XDG_RUNTIME_DIR"); strings.HasPrefix(c.Path(), rt) {
		t.Fatalf("socket %s is under XDG_RUNTIME_DIR", c.Path())
	}
}

func TestDirectoryAndSocketModes(t *testing.T) {
	c := start(t, nil)
	di, err := os.Lstat(filepath.Dir(c.Path()))
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 || !di.IsDir() || ownerOf(di) != uint32(os.Geteuid()) {
		t.Errorf("directory: mode %v owner %d", di.Mode(), ownerOf(di))
	}
	si, err := os.Lstat(c.Path())
	if err != nil {
		t.Fatal(err)
	}
	if si.Mode()&os.ModeSocket == 0 || si.Mode().Perm() != 0o600 || ownerOf(si) != uint32(os.Geteuid()) {
		t.Errorf("socket: mode %v owner %d", si.Mode(), ownerOf(si))
	}
}

// The path-length rule and the bind-error rule, one row each.
func TestPlacementTable(t *testing.T) {
	eperm := &net.OpError{Op: "listen", Err: os.NewSyscallError("bind", syscall.EPERM)}
	eopn := &net.OpError{Op: "listen", Err: os.NewSyscallError("bind", syscall.EOPNOTSUPP)}
	long := func(t *testing.T) string {
		dir := t.TempDir()
		for len(dir) < 110 {
			dir = filepath.Join(dir, strings.Repeat("d", 20))
		}
		return dir
	}
	realBind := func(path string) (net.Listener, error) { return net.Listen("unix", path) }

	cases := []struct {
		name     string
		state    func(*testing.T) string
		bind     func(path string) (net.Listener, error)
		maxPath  int
		wantFall bool // the socket is in the fallback location
		wantFail bool
	}{
		{name: "short path uses the state directory", state: func(t *testing.T) string { return t.TempDir() }},
		{name: "path over 100 bytes uses the fallback", state: long, wantFall: true},
		{name: "path at the limit stays", state: func(t *testing.T) string { return t.TempDir() }, maxPath: -1},
		{name: "path one over the limit falls back", state: func(t *testing.T) string { return t.TempDir() }, maxPath: -2, wantFall: true},
		{
			name: "bind refused with EPERM falls back", state: func(t *testing.T) string { return t.TempDir() },
			bind: func(path string) (net.Listener, error) {
				if strings.Contains(path, string(filepath.Separator)+"c"+string(filepath.Separator)) {
					return nil, eperm
				}
				return realBind(path)
			},
			wantFall: true,
		},
		{
			name: "bind refused with EOPNOTSUPP falls back", state: func(t *testing.T) string { return t.TempDir() },
			bind: func(path string) (net.Listener, error) {
				if strings.Contains(path, string(filepath.Separator)+"c"+string(filepath.Separator)) {
					return nil, eopn
				}
				return realBind(path)
			},
			wantFall: true,
		},
		{
			name: "both places refuse", state: func(t *testing.T) string { return t.TempDir() },
			bind:     func(string) (net.Listener, error) { return nil, eperm },
			wantFail: true,
		},
		{name: "both places too long", state: long, maxPath: 30, wantFail: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state, tmp := tc.state(t), t.TempDir()
			started := time.Now()
			cfg := Config{
				StateDir: state, PID: os.Getpid(), Started: started,
				Window: func() string { return "x" }, Logf: t.Logf,
				Seams: Seams{Tmp: tmp, Bind: tc.bind, MaxPath: tc.maxPath},
			}
			switch {
			case tc.maxPath == -1:
				cfg.Seams.MaxPath = len(socketPathFor(state, os.Getpid(), started))
			case tc.maxPath == -2:
				cfg.Seams.MaxPath = len(socketPathFor(state, os.Getpid(), started)) - 1
			}
			c, err := Start(cfg)
			if tc.wantFail {
				if err == nil {
					c.Close()
					t.Fatal("started although no place was fit")
				}
				for _, dir := range []string{filepath.Join(state, "c"), filepath.Join(tmp, "flockdeck-"+strconv.Itoa(os.Geteuid()))} {
					entries, _ := os.ReadDir(dir)
					for _, e := range entries {
						t.Errorf("left %s behind", filepath.Join(dir, e.Name()))
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			fb := filepath.Join(tmp, "flockdeck-"+strconv.Itoa(os.Geteuid()))
			if got := filepath.Dir(c.Path()) == fb; got != tc.wantFall {
				t.Fatalf("socket %s: in fallback = %v, want %v", c.Path(), got, tc.wantFall)
			}
			if code, _ := post(t, c, "/identify", ""); code != 200 {
				t.Fatalf("identify = %d", code)
			}
			if tc.wantFall {
				if len(c.Path()) > 100 {
					t.Errorf("fallback path is %d bytes", len(c.Path()))
				}
			}
		})
	}
}

func TestDirectoryChecks(t *testing.T) {
	uid := os.Geteuid()
	mk := func(t *testing.T, mode os.FileMode) string {
		dir := filepath.Join(t.TempDir(), "d")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, mode); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	t.Run("0700 owned by us is fine", func(t *testing.T) {
		if err := checkDir(mk(t, 0o700), uid); err != nil {
			t.Fatal(err)
		}
	})
	for _, mode := range []os.FileMode{0o755, 0o770, 0o707, 0o777, 0o750, 0o500, 0o701} {
		t.Run("mode "+strconv.FormatUint(uint64(mode), 8), func(t *testing.T) {
			if err := checkDir(mk(t, mode), uid); err == nil {
				t.Fatal("accepted")
			}
		})
	}
	t.Run("a link to a good directory", func(t *testing.T) {
		good := mk(t, 0o700)
		link := filepath.Join(t.TempDir(), "l")
		if err := os.Symlink(good, link); err != nil {
			t.Skip("no symlinks:", err)
		}
		if err := checkDir(link, uid); err == nil {
			t.Fatal("followed a link")
		}
	})
	t.Run("a file", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "f")
		os.WriteFile(f, nil, 0o700)
		if err := checkDir(f, uid); err == nil {
			t.Fatal("accepted a file")
		}
	})
	t.Run("owned by another user", func(t *testing.T) {
		if err := checkDir(mk(t, 0o700), uid+1); err == nil {
			t.Fatal("accepted a directory owned by someone else")
		}
	})
	t.Run("ensureDir makes 0700 and checks it", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "x", "c")
		if err := ensureDir(dir, uid); err != nil {
			t.Fatal(err)
		}
		fi, _ := os.Lstat(dir)
		if fi.Mode().Perm() != 0o700 {
			t.Fatalf("mode %v", fi.Mode())
		}
	})
	t.Run("ensureDir does not repair a loose directory", func(t *testing.T) {
		dir := mk(t, 0o755)
		if err := ensureDir(dir, uid); err == nil {
			t.Fatal("accepted")
		}
		if fi, _ := os.Lstat(dir); fi.Mode().Perm() != 0o755 {
			t.Fatal("changed a directory that is not ours to change")
		}
	})
}

func TestSocketChecks(t *testing.T) {
	uid := os.Geteuid()
	dir := t.TempDir()
	reg := filepath.Join(dir, "r")
	os.WriteFile(reg, nil, 0o600)
	if err := checkSocket(reg, uid); err == nil {
		t.Error("accepted a regular file")
	}
	sock := filepath.Join(dir, "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	os.Chmod(sock, 0o666)
	if err := checkSocket(sock, uid); err == nil {
		t.Error("accepted mode 0666")
	}
	os.Chmod(sock, 0o600)
	if err := checkSocket(sock, uid); err != nil {
		t.Error(err)
	}
	if err := checkSocket(sock, uid+1); err == nil {
		t.Error("accepted a socket owned by another user")
	}
	if err := checkSocket(filepath.Join(dir, "none"), uid); err == nil {
		t.Error("accepted nothing")
	}
}

// squat makes ownerOf say that anything with this base name is owned by
// somebody else, which is what a directory pre-created by another user looks
// like. A test cannot make one without being that user.
func squat(t *testing.T, names ...string) {
	t.Helper()
	old := ownerOf
	ownerOf = func(fi os.FileInfo) uint32 {
		for _, n := range names {
			if fi.Name() == n {
				return uint32(os.Geteuid()) + 1
			}
		}
		return old(fi)
	}
	t.Cleanup(func() { ownerOf = old })
}

func TestSquattedPrimaryDirectoryFallsBackWithTheSameChecks(t *testing.T) {
	state, tmp := t.TempDir(), t.TempDir()
	os.Mkdir(filepath.Join(state, "c"), 0o700)
	squat(t, "c")
	c := start(t, func(cfg *Config) { cfg.StateDir = state; cfg.Seams.Tmp = tmp })
	if want := filepath.Join(tmp, "flockdeck-"+strconv.Itoa(os.Geteuid())); filepath.Dir(c.Path()) != want {
		t.Fatalf("socket in %s, want %s", filepath.Dir(c.Path()), want)
	}
	if entries, _ := os.ReadDir(filepath.Join(state, "c")); len(entries) != 0 {
		t.Fatal("put something in a directory owned by someone else")
	}
}

func TestSquattedFallbackDirectoryIsRefusedToo(t *testing.T) {
	state, tmp := t.TempDir(), t.TempDir()
	name := "flockdeck-" + strconv.Itoa(os.Geteuid())
	os.Mkdir(filepath.Join(tmp, name), 0o700)
	squat(t, "c", name)
	os.Mkdir(filepath.Join(state, "c"), 0o700)
	_, err := Start(Config{
		StateDir: state, PID: os.Getpid(), Started: time.Now(),
		Window: func() string { return "x" }, Seams: Seams{Tmp: tmp},
	})
	if err == nil {
		t.Fatal("started in a directory owned by someone else")
	}
	for _, d := range []string{filepath.Join(state, "c"), filepath.Join(tmp, name)} {
		if entries, _ := os.ReadDir(d); len(entries) != 0 {
			t.Errorf("%s was written to", d)
		}
	}
}

func TestSymlinkedFallbackDirectoryIsRefused(t *testing.T) {
	state, tmp, elsewhere := t.TempDir(), t.TempDir(), t.TempDir()
	os.Chmod(elsewhere, 0o700)
	if err := os.Symlink(elsewhere, filepath.Join(tmp, "flockdeck-"+strconv.Itoa(os.Geteuid()))); err != nil {
		t.Skip("no symlinks:", err)
	}
	// Make the primary fail so that the fallback is the only way in.
	_, err := Start(Config{
		StateDir: state, PID: os.Getpid(), Started: time.Now(),
		Window: func() string { return "x" },
		Seams: Seams{Tmp: tmp, Bind: func(p string) (net.Listener, error) {
			if strings.HasPrefix(p, state) {
				return nil, syscall.EPERM
			}
			return net.Listen("unix", p)
		}},
	})
	if err == nil {
		t.Fatal("followed a link in the temporary directory")
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Fatal("wrote through the link")
	}
}

func TestLeftoverSocketOfOursIsReplaced(t *testing.T) {
	state := t.TempDir()
	started := time.Now()
	path := socketPathFor(state, os.Getpid(), started)
	if err := ensureDir(filepath.Dir(path), os.Geteuid()); err != nil {
		t.Fatal(err)
	}
	old, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	old.(*net.UnixListener).SetUnlinkOnClose(false)
	old.Close()
	c := start(t, func(cfg *Config) { cfg.StateDir = state; cfg.Started = started })
	if code, _ := post(t, c, "/identify", ""); code != 200 {
		t.Fatalf("identify = %d", code)
	}
}

func TestSomethingElseAtTheSocketPathIsNotRemoved(t *testing.T) {
	state := t.TempDir()
	started := time.Now()
	path := socketPathFor(state, os.Getpid(), started)
	ensureDir(filepath.Dir(path), os.Geteuid())
	os.WriteFile(path, []byte("keep"), 0o600)
	tmp := t.TempDir()
	c := start(t, func(cfg *Config) { cfg.StateDir = state; cfg.Started = started; cfg.Seams.Tmp = tmp })
	if filepath.Dir(c.Path()) == filepath.Dir(path) {
		t.Fatal("bound over a regular file")
	}
	if b, _ := os.ReadFile(path); string(b) != "keep" {
		t.Fatal("removed a file that was not a socket")
	}
}

func TestCloseRemovesTheSocket(t *testing.T) {
	c := start(t, nil)
	path := c.Path()
	c.Close()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("socket left behind: %v", err)
	}
}

func waitFor(t *testing.T, what string, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !f() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func answers(c *Channel) bool {
	req, _ := http.NewRequest(http.MethodPost, "http://channel/identify", nil)
	resp, err := Client(c.Path()).Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

func TestRemovedSocketIsPutBack(t *testing.T) {
	c := start(t, func(cfg *Config) { cfg.Seams.Interval = 20 * time.Millisecond })
	path := c.Path()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the socket to come back", func() bool { return answers(c) })
	if c.Path() != path {
		t.Fatalf("moved from %s to %s", path, c.Path())
	}
	if err := checkSocket(path, os.Geteuid()); err != nil {
		t.Fatal(err)
	}
}

func TestRemovedDirectoryIsMadeAgain(t *testing.T) {
	c := start(t, func(cfg *Config) { cfg.Seams.Interval = 20 * time.Millisecond })
	os.RemoveAll(filepath.Dir(c.Path()))
	waitFor(t, "the directory and socket to come back", func() bool { return answers(c) })
	if err := checkDir(filepath.Dir(c.Path()), os.Geteuid()); err != nil {
		t.Fatal(err)
	}
}

// What replaces the socket may not be the old listener's to remove, so the
// old listener must not unlink on close. The replacement here is a file the
// rebind will refuse to touch, and it must still be there afterwards.
func TestRebindDoesNotRemoveWhatReplacedTheSocket(t *testing.T) {
	var lost atomic.Int32
	c := start(t, func(cfg *Config) {
		cfg.Seams.Interval = 10 * time.Millisecond
		cfg.Seams.Backoff = func(int) time.Duration { return 10 * time.Millisecond }
		cfg.OnLost = func(string) { lost.Add(1) }
	})
	path := c.Path()
	os.Remove(path)
	os.WriteFile(path, []byte("not yours"), 0o600)
	waitFor(t, "the windows to be told", func() bool { return lost.Load() == 1 })
	if b, err := os.ReadFile(path); err != nil || string(b) != "not yours" {
		t.Fatalf("the replacement was removed: %v %q", err, b)
	}
	if !c.Lost() {
		t.Error("not marked lost")
	}
	// Told once, however long it stays down.
	time.Sleep(100 * time.Millisecond)
	if lost.Load() != 1 {
		t.Errorf("told %d times", lost.Load())
	}
	// And it recovers when the way is clear.
	os.Remove(path)
	waitFor(t, "recovery", func() bool { return answers(c) })
	waitFor(t, "lost to clear", func() bool { return !c.Lost() })
}

func TestWindowsAreToldAfterThreeFailures(t *testing.T) {
	var fails atomic.Int32
	var told atomic.Int32
	c := start(t, func(cfg *Config) {
		cfg.Seams.Interval = 10 * time.Millisecond
		cfg.Seams.Backoff = func(int) time.Duration { return 5 * time.Millisecond }
		cfg.OnLost = func(string) { told.Add(1) }
		cfg.Seams.Bind = func(p string) (net.Listener, error) {
			if fails.Load() > 0 && fails.Add(1) <= 100 {
				return nil, syscall.EPERM
			}
			return net.Listen("unix", p)
		}
	})
	fails.Store(1)
	os.Remove(c.Path())
	waitFor(t, "three failures", func() bool { return fails.Load() >= 4 })
	if told.Load() != 1 {
		t.Fatalf("told %d times after the failures", told.Load())
	}
	fails.Store(0)
	waitFor(t, "recovery", func() bool { return answers(c) })
}

func TestTwoFailuresDoNotAlarmTheWindows(t *testing.T) {
	var fails atomic.Int32
	var told atomic.Int32
	c := start(t, func(cfg *Config) {
		cfg.Seams.Interval = 10 * time.Millisecond
		cfg.Seams.Backoff = func(int) time.Duration { return 5 * time.Millisecond }
		cfg.OnLost = func(string) { told.Add(1) }
		cfg.Seams.Bind = func(p string) (net.Listener, error) {
			if fails.Load() > 0 && fails.Add(1) <= 3 {
				return nil, syscall.EPERM
			}
			return net.Listen("unix", p)
		}
	})
	fails.Store(1)
	os.Remove(c.Path())
	waitFor(t, "recovery after two failures", func() bool { return answers(c) })
	if told.Load() != 0 {
		t.Fatal("told the windows after only two failures")
	}
}
