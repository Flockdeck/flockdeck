package shellrun

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func sleeper(secs int) string {
	if runtime.GOOS == "windows" {
		return "ping -n " + strconv.Itoa(secs+1) + " 127.0.0.1 >nul"
	}
	return "sleep " + strconv.Itoa(secs)
}

func envDump() string {
	if runtime.GOOS == "windows" {
		return "set"
	}
	return "env"
}

func baseEnv() []string {
	var env []string
	for _, k := range []string{"PATH", "Path", "SystemRoot", "ComSpec", "PATHEXT", "TEMP", "TMP", "HOME"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

func waitDone(t *testing.T, m *Manager, id int) Run {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if r, ok := m.Get(id); ok && r.State != Running {
			return r
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("run did not end")
	return Run{}
}

func TestRunCapturesOutputAndExitCode(t *testing.T) {
	m := New(t.TempDir(), baseEnv(), nil)
	defer m.Close()
	id, err := m.Start("echo hello")
	if err != nil {
		t.Fatal(err)
	}
	r := waitDone(t, m, id)
	if r.State != Exited || r.Code != 0 || !strings.Contains(Text(r.Output), "hello") {
		t.Fatalf("run = %+v %q", r, Text(r.Output))
	}
	id, _ = m.Start("exit 3")
	if r := waitDone(t, m, id); r.State != Exited || r.Code != 3 {
		t.Fatalf("run = %+v", r)
	}
}

func TestRunsInThePaneDirectory(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	m := New(dir, baseEnv(), nil)
	defer m.Close()
	cmd := "pwd"
	if runtime.GOOS == "windows" {
		cmd = "cd"
	}
	id, _ := m.Start(cmd)
	got := strings.TrimSpace(Text(waitDone(t, m, id).Output))
	if !strings.EqualFold(filepath.Clean(got), filepath.Clean(dir)) {
		t.Fatalf("ran in %q, want %q", got, dir)
	}
}

func TestEnvHoldsNoFlockdeckVariableNorKey(t *testing.T) {
	secrets := []string{
		"FLOCKDECK_TOKEN=sekret-flock", "flockdeck_lower=sekret-lower", "ANTHROPIC_API_KEY=sekret-anth",
		"OPENAI_API_KEY=sekret-oai", "GEMINI_API_KEY=sekret-gem", "GITHUB_TOKEN=sekret-gh", "AWS_SECRET_ACCESS_KEY=sekret-aws",
	}
	env := append(baseEnv(), secrets...)
	for _, kv := range Env(env) {
		if strings.Contains(strings.ToLower(kv), "sekret") {
			t.Fatalf("Env passed %q", kv)
		}
	}
	// The same through a real run: nil environ is the process's own.
	for _, kv := range secrets {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	m := New(t.TempDir(), nil, nil)
	defer m.Close()
	id, _ := m.Start(envDump())
	out := Text(waitDone(t, m, id).Output)
	if strings.Contains(out, "sekret") || strings.Contains(strings.ToUpper(out), "FLOCKDECK_") {
		t.Fatalf("a command saw Flockdeck's environment:\n%s", out)
	}
	if !strings.Contains(strings.ToUpper(out), "PATH=") {
		t.Fatalf("PATH is missing, so commands cannot be found:\n%s", out)
	}
}

func TestCloseKillsRunningCommandsAndWhatTheyStarted(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	line := "sleep 3; echo x > marker"
	if runtime.GOOS == "windows" {
		line = "ping -n 4 127.0.0.1 >nul & echo x > marker"
	}
	m := New(dir, baseEnv(), nil)
	id, err := m.Start(line)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	start := time.Now()
	m.Close()
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("Close took %v", d)
	}
	if r, _ := m.Get(id); r.State != Stopped {
		t.Fatalf("state = %q, want stopped", r.State)
	}
	time.Sleep(4500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the command went on after Close")
	}
	if _, err := m.Start("echo late"); err != ErrClosed {
		t.Fatalf("Start after Close = %v, want ErrClosed", err)
	}
}

func TestStopEndsOneRun(t *testing.T) {
	m := New(t.TempDir(), baseEnv(), nil)
	defer m.Close()
	a, _ := m.Start(sleeper(20))
	b, _ := m.Start(sleeper(20))
	time.Sleep(200 * time.Millisecond)
	if err := m.Stop(a); err != nil {
		t.Fatal(err)
	}
	if r := waitDone(t, m, a); r.State != Stopped {
		t.Fatalf("a = %q", r.State)
	}
	if r, _ := m.Get(b); r.State != Running {
		t.Fatalf("b = %q, want running", r.State)
	}
	if err := m.Stop(99); err != ErrNoRun {
		t.Fatalf("Stop(99) = %v", err)
	}
}

func TestTimeoutEndsARun(t *testing.T) {
	old := Timeout
	Timeout = 300 * time.Millisecond
	defer func() { Timeout = old }()
	m := New(t.TempDir(), baseEnv(), nil)
	defer m.Close()
	id, _ := m.Start(sleeper(20))
	if r := waitDone(t, m, id); r.State != TimedOut {
		t.Fatalf("state = %q, want timeout", r.State)
	}
}

func TestLimits(t *testing.T) {
	m := New(t.TempDir(), baseEnv(), nil)
	defer m.Close()
	if _, err := m.Start("  \n"); err != ErrEmpty {
		t.Fatalf("empty = %v", err)
	}
	if _, err := m.Start(strings.Repeat("a", MaxCmd+1)); err != ErrTooLong {
		t.Fatalf("long = %v", err)
	}
	var ids []int
	for i := 0; i < MaxRunning; i++ {
		id, err := m.Start(sleeper(20))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if _, err := m.Start("echo x"); err != ErrBusy {
		t.Fatalf("one over the running limit = %v", err)
	}
	if err := m.Remove(ids[0]); err != ErrRunning {
		t.Fatalf("Remove of a running run = %v", err)
	}
	for _, id := range ids {
		_ = m.Stop(id)
	}
	for _, id := range ids {
		waitDone(t, m, id)
	}
	// History is bounded: the oldest finished run makes room.
	for i := 0; i < MaxRuns+3; i++ {
		id, err := m.Start("echo x")
		if err != nil {
			t.Fatal(err)
		}
		waitDone(t, m, id)
	}
	if n := len(m.List()); n != MaxRuns {
		t.Fatalf("kept %d runs, want %d", n, MaxRuns)
	}
	last := m.List()[MaxRuns-1]
	if err := m.Remove(last.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove(last.ID); err != ErrNoRun {
		t.Fatalf("second Remove = %v", err)
	}
}

func TestOutputKeepsTheNewestBytes(t *testing.T) {
	m := New("", nil, nil)
	r := &run{Run: Run{ID: 1, State: Running}}
	s := &sink{m: m, r: r}
	s.Write([]byte(strings.Repeat("a", OutputCap-10)))
	s.Write([]byte(strings.Repeat("b", 30)))
	if len(r.Output) != OutputCap || r.Dropped != 20 || !strings.HasSuffix(string(r.Output), strings.Repeat("b", 30)) {
		t.Fatalf("len %d dropped %d", len(r.Output), r.Dropped)
	}
	s.Write([]byte(strings.Repeat("c", OutputCap*2)))
	if len(r.Output) != OutputCap || strings.Trim(string(r.Output), "c") != "" || r.Dropped != int64(OutputCap*3+20-OutputCap) {
		t.Fatalf("len %d dropped %d", len(r.Output), r.Dropped)
	}
}

func TestLargeOutputOfARealRunIsBounded(t *testing.T) {
	line := "yes aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa | head -c 300000"
	if runtime.GOOS == "windows" {
		line = "for /L %i in (1,1,6000) do @echo aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	}
	m := New(t.TempDir(), baseEnv(), nil)
	defer m.Close()
	id, _ := m.Start(line)
	r := waitDone(t, m, id)
	if len(r.Output) > OutputCap || r.Dropped == 0 {
		t.Fatalf("kept %d bytes, dropped %d", len(r.Output), r.Dropped)
	}
}

func TestTextIsPlain(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"colour", "\x1b[31mred\x1b[0m ok", "red ok"},
		{"osc title", "a\x1b]0;evil title\x07b", "ab"},
		{"osc link", "\x1b]8;;http://x\x1b\\click\x1b]8;;\x1b\\", "click"},
		{"cursor", "a\x1b[2K\x1b[1Ab", "ab"},
		{"progress", "10%\r50%\r100%\ndone\n", "100%\ndone\n"},
		{"crlf", "a\r\nb\r\n", "a\nb\n"},
		{"bidi", "abc\u202edef\u2066g\u200bh", "abcdefgh"},
		{"controls", "a\x00b\x07c\x08d\x7fe\u0085f\td", "abcdef\td"},
		{"separators", "a\u2028b\u2029c", "abc"},
		{"invalid utf8", "a\xffb", "a\ufffdb"},
		{"lone esc at end", "a\x1b", "a"},
	}
	for _, c := range cases {
		if got := Text([]byte(c.in)); got != c.want {
			t.Errorf("%s: Text(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
	// An introducer with no end is not allowed to swallow the rest of the output.
	long := "a\x1b]" + strings.Repeat("x", maxSeq*2) + "z"
	if got := Text([]byte(long)); !strings.HasSuffix(got, "z") || len(got) < maxSeq {
		t.Errorf("an unterminated sequence swallowed the output (%d bytes left)", len(got))
	}
}

func TestClip(t *testing.T) {
	if s, cut := Clip("héllo", 100, false); s != "héllo" || cut {
		t.Fatal(s, cut)
	}
	if s, cut := Clip("héllo", 2, false); s != "h" || !cut {
		t.Fatalf("%q %v", s, cut)
	}
	if s, cut := Clip("héllo", 4, true); s != "llo" || !cut {
		t.Fatalf("%q %v", s, cut)
	}
}

func TestEnvIsNeverNilSoTheChildDoesNotInheritEverything(t *testing.T) {
	got := Env([]string{"FLOCKDECK_TOKEN=sekret-x"})
	if got == nil {
		t.Fatal("Env returned nil: on Unix a nil Cmd.Env is the whole environment")
	}
	m := New(t.TempDir(), []string{"FLOCKDECK_TOKEN=sekret-x"}, nil)
	defer m.Close()
	id, _ := m.Start(envDump())
	if out := Text(waitDone(t, m, id).Output); strings.Contains(out, "sekret") || strings.Contains(strings.ToUpper(out), "FLOCKDECK_") {
		t.Fatalf("the child got the variable:\n%s", out)
	}
}
