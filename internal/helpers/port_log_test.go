package helpers

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestChoosePortReusesAFreePreferredPort(t *testing.T) {
	p := freePort(t)
	got, err := ChoosePort(p)
	if err != nil || got != p {
		t.Fatalf("ChoosePort(%d) = %d, %v", p, got, err)
	}
}

func TestChoosePortFallsBackWhenTaken(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	taken := ln.Addr().(*net.TCPAddr).Port
	got, err := ChoosePort(taken)
	if err != nil {
		t.Fatal(err)
	}
	if got == taken || got < 1024 {
		t.Fatalf("got %d with %d taken", got, taken)
	}
	// And the one it gave is bindable.
	l2, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", got))
	if err != nil {
		t.Fatalf("the chosen port is not free: %v", err)
	}
	l2.Close()
}

func TestChoosePortIgnoresUnusablePreferences(t *testing.T) {
	for _, p := range []int{0, -1, 80, 1023, 65536, 1 << 20} {
		got, err := ChoosePort(p)
		if err != nil || got < 1024 || got > 65535 {
			t.Errorf("ChoosePort(%d) = %d, %v", p, got, err)
		}
	}
}

func TestChoosePortOnlyBindsLoopback(t *testing.T) {
	var addrs []string
	listen := func(network, addr string) (net.Listener, error) {
		addrs = append(addrs, addr)
		return net.Listen(network, addr)
	}
	if _, err := choosePort(freePort(t), listen); err != nil {
		t.Fatal(err)
	}
	if _, err := choosePort(0, listen); err != nil {
		t.Fatal(err)
	}
	for _, a := range addrs {
		if !strings.HasPrefix(a, "127.0.0.1:") {
			t.Errorf("bound %s", a)
		}
	}
}

func TestChoosePortReportsFailure(t *testing.T) {
	fail := func(string, string) (net.Listener, error) { return nil, fmt.Errorf("no sockets") }
	if _, err := choosePort(5000, fail); err == nil {
		t.Fatal("no error when nothing can be bound")
	}
}

// log rotation ---------------------------------------------------------------

func logFiles(t *testing.T, path string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, suffix := range []string{"", ".1", ".2", ".3", ".4"} {
		if b, err := os.ReadFile(path + suffix); err == nil {
			out[suffix] = string(b)
		}
	}
	return out
}

func TestRotatingWriterRotatesAtTheLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "helper.log")
	w := &RotatingWriter{Path: path, Max: 20, Keep: 3}
	defer w.Close()
	for i := 1; i <= 8; i++ {
		// 10 bytes each: two to a file.
		if _, err := fmt.Fprintf(w, "line %04d\n", i); err != nil {
			t.Fatal(err)
		}
	}
	got := logFiles(t, path)
	want := map[string]string{
		"":   "line 0007\nline 0008\n",
		".1": "line 0005\nline 0006\n",
		".2": "line 0003\nline 0004\n",
		".3": "line 0001\nline 0002\n",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("helper.log%s = %q, want %q", k, got[k], v)
		}
	}
	// One more file's worth pushes the oldest off the end.
	_, _ = w.Write([]byte("line 0009\n"))
	_, _ = w.Write([]byte("line 0010\n"))
	_, _ = w.Write([]byte("line 0011\n"))
	got = logFiles(t, path)
	if _, ok := got[".4"]; ok {
		t.Error("a fourth old file was kept")
	}
	if got[".3"] != "line 0005\nline 0006\n" {
		t.Errorf("helper.log.3 = %q", got[".3"])
	}
}

func TestRotatingWriterDefaults(t *testing.T) {
	w := NewRotatingWriter("x")
	if w.Max != 5<<20 || w.Keep != 3 {
		t.Fatalf("defaults are %d bytes and %d old files", w.Max, w.Keep)
	}
}

func TestRotatingWriterKeepsALongLineWhole(t *testing.T) {
	path := filepath.Join(t.TempDir(), "helper.log")
	w := &RotatingWriter{Path: path, Max: 10, Keep: 2}
	defer w.Close()
	long := strings.Repeat("x", 50) + "\n"
	_, _ = w.Write([]byte("a\n"))
	if _, err := w.Write([]byte(long)); err != nil {
		t.Fatal(err)
	}
	got := logFiles(t, path)
	if got[""] != long || got[".1"] != "a\n" {
		t.Fatalf("files = %q", got)
	}
}

func TestRotatingWriterContinuesAnExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "helper.log")
	if err := os.WriteFile(path, []byte("123456789\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := &RotatingWriter{Path: path, Max: 15, Keep: 1}
	defer w.Close()
	_, _ = w.Write([]byte("abcdefgh\n"))
	got := logFiles(t, path)
	if got[".1"] != "123456789\n" || got[""] != "abcdefgh\n" {
		t.Fatalf("files = %q", got)
	}
}

func TestRotatingWriterIsSafeForConcurrentUse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "helper.log")
	w := &RotatingWriter{Path: path, Max: 200, Keep: 3}
	defer w.Close()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				_, _ = w.Write([]byte("0123456789 abcdefghij\n"))
			}
		}()
	}
	wg.Wait()
	for k, v := range logFiles(t, path) {
		for _, line := range strings.Split(strings.TrimSuffix(v, "\n"), "\n") {
			if line != "0123456789 abcdefghij" {
				t.Fatalf("helper.log%s has a torn line: %q", k, line)
			}
		}
	}
}

func TestRotatingWriterFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("modes are not meaningful on Windows")
	}
	path := filepath.Join(t.TempDir(), "helper.log")
	w := NewRotatingWriter(path)
	defer w.Close()
	_, _ = w.Write([]byte("x\n"))
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, %v", fi.Mode().Perm(), err)
	}
}

func TestTailLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "helper.log")
	var b strings.Builder
	for i := 1; i <= 50; i++ {
		fmt.Fprintf(&b, "line %d\r\n\n", i)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	got := TailLines(path, 3)
	if strings.Join(got, "|") != "line 48|line 49|line 50" {
		t.Fatalf("tail = %q", got)
	}
	if TailLines(filepath.Join(t.TempDir(), "none"), 3) != nil {
		t.Error("a missing file has lines")
	}
	// A file much larger than the read window still gives its end.
	big := strings.Repeat("filler filler filler\n", 10000) + "the end\n"
	_ = os.WriteFile(path, []byte(big), 0o600)
	if got := TailLines(path, 1); len(got) != 1 || got[0] != "the end" {
		t.Fatalf("tail of a big file = %q", got)
	}
}
