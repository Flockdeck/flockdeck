package agentlogin

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/testiso"
	"github.com/jmwri/flockdeck/internal/testiso/depth"
)

// The fake login command is this test binary run again; FD_LOGIN_FAKE says what
// it does. The chain is the test, the fake, and for "tree" a heartbeat process
// the fake starts: two ancestors at most.
const (
	envFake  = "FD_LOGIN_FAKE"
	envBeat  = "FD_LOGIN_BEAT"
	envChild = "FD_LOGIN_CHILD"
	envOut   = "FD_LOGIN_OUT"
)

func TestMain(m *testing.M) {
	depth.Guard()
	if os.Getenv(envChild) == "1" {
		beat(os.Getenv(envBeat))
		return
	}
	if mode := os.Getenv(envFake); mode != "" {
		runFake(mode)
		return
	}
	os.Exit(testiso.Main(m))
}

// beat appends a byte to path every 20 ms until the process is killed.
func beat(path string) {
	for {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = f.Write([]byte("x"))
			_ = f.Close()
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func runFake(mode string) {
	const good = "https://claude.ai/oauth/authorize?code=true&client_id=abc&state=s1"
	switch mode {
	case "ok":
		os.Stdout.WriteString("Opening browser to sign in...\n")
		os.Stdout.WriteString("If the browser did not open, visit: " + good + "\n")
		os.Stderr.WriteString("https://evil.example/second\n")
		os.Stdout.WriteString("Login successful.\n")
	case "fail":
		os.Stdout.WriteString(good + "\n")
		os.Stderr.WriteString("secret-token-123 something went wrong\n")
		os.Exit(1)
	case "nourl":
		os.Stdout.WriteString("nothing to see\n")
	case "hostile":
		os.Stdout.WriteString("http://claude.ai/plain https://user:pw@claude.ai/x https://evil.example/y https://claude.ai.evil.example/z\n")
		os.Stdout.WriteString("\x1b]8;;https://claude.ai/osc8?x=1\x1b\\link\x1b]8;;\x1b\\\n")
	case "noline":
		// A prompt that ends without a line break and waits.
		os.Stdout.WriteString("Visit this page: " + good)
		time.Sleep(2 * time.Second)
	case "record":
		// Where the command runs and which variables it was given.
		out := os.Getenv(envOut)
		wd, _ := os.Getwd()
		var names []string
		for _, kv := range os.Environ() {
			n, _, _ := strings.Cut(kv, "=")
			names = append(names, n)
		}
		_ = os.WriteFile(out, []byte(wd+"\n"+strings.Join(names, "\n")), 0o600)
	case "tree":
		os.Stdout.WriteString(good + "\n")
		exe, _ := os.Executable()
		c := exec.Command(exe)
		c.Env = append(os.Environ(), envChild+"=1", envFake+"=")
		_ = c.Start()
		time.Sleep(time.Hour)
	}
}

func fakeSpec(t *testing.T, mode string, extra ...string) Spec {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Spec{Path: exe, Hosts: []string{"claude.ai"}, Timeout: 20 * time.Second,
		Env: append(append([]string{}, os.Environ()...), append(extra, envFake+"="+mode)...)}
}

func collect() (func(string), func() []string) {
	var mu sync.Mutex
	var got []string
	return func(u string) { mu.Lock(); got = append(got, u); mu.Unlock() },
		func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), got...) }
}

func TestRunReportsOnlyTheFirstValidURLAndSucceeds(t *testing.T) {
	on, got := collect()
	if err := Run(context.Background(), fakeSpec(t, "ok"), on); err != nil {
		t.Fatal(err)
	}
	u := got()
	if len(u) != 1 || !strings.HasPrefix(u[0], "https://claude.ai/oauth/authorize?") {
		t.Fatalf("urls = %q", u)
	}
}

func TestRunFailureErrorHasNoOutput(t *testing.T) {
	on, got := collect()
	err := Run(context.Background(), fakeSpec(t, "fail"), on)
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("the command's output reached the error: %v", err)
	}
	if len(got()) != 1 {
		t.Fatalf("urls = %q", got())
	}
}

func TestRunStartFailureIsFixed(t *testing.T) {
	sp := fakeSpec(t, "ok")
	sp.Path = filepath.Join(t.TempDir(), "no-such-program")
	if err := Run(context.Background(), sp, func(string) {}); !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v", err)
	}
}

func TestRunHostileOutputOnlyYieldsAllowedURL(t *testing.T) {
	on, got := collect()
	if err := Run(context.Background(), fakeSpec(t, "hostile"), on); err != nil {
		t.Fatal(err)
	}
	u := got()
	if len(u) != 1 || u[0] != "https://claude.ai/osc8?x=1" {
		t.Fatalf("urls = %q", u)
	}
}

func TestRunWithoutURLStillWaitsForExit(t *testing.T) {
	on, got := collect()
	if err := Run(context.Background(), fakeSpec(t, "nourl"), on); err != nil {
		t.Fatal(err)
	}
	if len(got()) != 0 {
		t.Fatalf("urls = %q", got())
	}
}

func TestRunTimeoutEndsTree(t *testing.T) {
	beatFile := filepath.Join(t.TempDir(), "beat")
	sp := fakeSpec(t, "tree", envBeat+"="+beatFile)
	sp.Timeout = 1500 * time.Millisecond
	start := time.Now()
	err := Run(context.Background(), sp, func(string) {})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 15*time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
	assertSilent(t, beatFile)
}

func TestRunCancelEndsTree(t *testing.T) {
	beatFile := filepath.Join(t.TempDir(), "beat")
	sp := fakeSpec(t, "tree", envBeat+"="+beatFile)
	ctx, cancel := context.WithCancel(context.Background())
	saw := make(chan struct{})
	var once sync.Once
	go func() {
		// Cancel once the child has started and its helper is beating.
		<-saw
		waitGrowth(t, beatFile)
		cancel()
	}()
	err := Run(ctx, sp, func(string) { once.Do(func() { close(saw) }) })
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("err = %v", err)
	}
	assertSilent(t, beatFile)
}

func waitGrowth(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if fi, err := os.Stat(path); err == nil && fi.Size() >= 3 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the helper process never started beating")
}

// assertSilent fails when the file still grows: the helper the command started
// is alive.
func assertSilent(t *testing.T, path string) {
	t.Helper()
	time.Sleep(300 * time.Millisecond)
	a, _ := os.Stat(path)
	time.Sleep(400 * time.Millisecond)
	b, _ := os.Stat(path)
	if a == nil || b == nil {
		t.Fatal("the helper process never wrote")
	}
	if b.Size() != a.Size() {
		t.Fatalf("the command's child is still running (%d -> %d)", a.Size(), b.Size())
	}
}

func TestClean(t *testing.T) {
	hosts := []string{"claude.ai", "console.anthropic.com"}
	ok := []string{
		"https://claude.ai/oauth/authorize?a=b",
		"https://console.anthropic.com/x#frag",
		"https://CLAUDE.AI/x",
	}
	for _, s := range ok {
		if _, good := Clean(s, hosts); !good {
			t.Errorf("refused %q", s)
		}
	}
	bad := map[string]string{
		"http":             "http://claude.ai/x",
		"userinfo":         "https://u@claude.ai/x",
		"userinfo+pw":      "https://u:p@claude.ai/x",
		"other host":       "https://evil.example/x",
		"suffix trick":     "https://claude.ai.evil.example/x",
		"subdomain":        "https://www.claude.ai/x",
		"other subdomain":  "https://x.console.anthropic.com/x",
		"whole domain":     "https://anthropic.com/x",
		"hyphen lookalike": "https://claude.ai-evil.com/x",
		"percent host":     "https://claude%2eai/x",
		"percent dot":      "https://claude.ai%2eevil.com/x",
		"percent escape":   "https://%63laude.ai/x",
		"at sign":          "https://claude.ai@evil.example/x",
		"encoded at":       "https://claude.ai%40evil.example/x",
		"prefix trick":     "https://notclaude.ai/x",
		"port":             "https://claude.ai:8443/x",
		"ip":               "https://127.0.0.1/x",
		"trailing dot":     "https://claude.ai./x",
		"space":            "https://claude.ai/a b",
		"control":          "https://claude.ai/a\x01b",
		"newline":          "https://claude.ai/a\nb",
		"bidi":             "https://claude.ai/a‮b",
		"non ascii host":   "https://clаude.ai/x",
		"javascript":       "javascript:alert(1)",
		"opaque":           "https:claude.ai/x",
		"empty":            "",
		"too long":         "https://claude.ai/" + strings.Repeat("a", MaxURL),
		"no host":          "https:///x",
		"backslash":        "https://claude.ai\\@evil.example/",
		"scheme relative":  "//claude.ai/x",
	}
	for name, s := range bad {
		if u, good := Clean(s, hosts); good {
			t.Errorf("%s: accepted %q as %q", name, s, u)
		}
	}
	if _, good := Clean("https://claude.ai/x", nil); good {
		t.Error("an empty allowlist accepted an address")
	}
	if u, _ := Clean("https://claude.ai/"+strings.Repeat("a", MaxURL-len("https://claude.ai/")), hosts); u == "" {
		t.Error("an address of exactly the cap was refused")
	}
}

func TestFindURLStripsEscapesAndPunctuation(t *testing.T) {
	hosts := []string{"claude.ai"}
	cases := map[string]string{
		"visit https://claude.ai/a?b=c.":                   "https://claude.ai/a?b=c",
		"\x1b[1;34mhttps://claude.ai/a\x1b[0m":             "https://claude.ai/a",
		"(https://claude.ai/p)":                            "https://claude.ai/p",
		"https://evil.example/x then https://claude.ai/ok": "https://claude.ai/ok",
		"\x1b]8;;https://claude.ai/h\x07text\x1b]8;;\x07":  "https://claude.ai/h",
	}
	for in, want := range cases {
		got, ok := FindURL(in, hosts)
		if !ok || got != want {
			t.Errorf("%q: got %q %v, want %q", in, got, ok, want)
		}
	}
	if _, ok := FindURL("no address here", hosts); ok {
		t.Error("found an address in plain text")
	}
}

func TestCleanShapeKeepsEveryRuleButTheHostList(t *testing.T) {
	if _, ok := CleanShape("https://anything.example/x"); !ok {
		t.Error("a well-formed address on any host was refused")
	}
	for _, s := range []string{"http://a.example/x", "https://u:p@a.example/x", "https://a.example:444/x", "https://10.0.0.1/x", "https://a.example/a b", "https://a.example/‮"} {
		if u, ok := CleanShape(s); ok {
			t.Errorf("accepted %q as %q", s, u)
		}
	}
}

func TestRunnerCloseReturnsOnlyAfterTheCommandIsGone(t *testing.T) {
	beatFile := filepath.Join(t.TempDir(), "beat")
	sp := fakeSpec(t, "tree", envBeat+"="+beatFile)
	var r Runner
	saw := make(chan struct{})
	var once sync.Once
	finished := make(chan error, 1)
	go func() {
		finished <- r.Do(context.Background(), sp, func(string) { once.Do(func() { close(saw) }) })
	}()
	<-saw
	waitGrowth(t, beatFile)
	r.Close()
	select {
	case err := <-finished:
		if !errors.Is(err, ErrCancelled) {
			t.Errorf("Do returned %v", err)
		}
	default:
		t.Fatal("Close returned while Do was still running")
	}
	if err := r.Do(context.Background(), sp, func(string) {}); !errors.Is(err, ErrCancelled) {
		t.Errorf("Do after Close: %v", err)
	}
	if err := (&Runner{}).Do(context.Background(), fakeSpec(t, "ok"), func(string) {}); err != nil {
		t.Errorf("a fresh Runner: %v", err)
	}
}

func TestNoLineBreakStillYieldsTheAddress(t *testing.T) {
	on, got := collect()
	start := time.Now()
	sp := fakeSpec(t, "noline")
	if err := Run(context.Background(), sp, on); err != nil {
		t.Fatal(err)
	}
	if u := got(); len(u) != 1 || !strings.HasPrefix(u[0], "https://claude.ai/oauth/authorize?") {
		t.Fatalf("urls = %q", u)
	}
	_ = start
}

// The address must arrive while the command is still waiting, not when it ends.
func TestNoLineBreakAddressArrivesBeforeTheCommandEnds(t *testing.T) {
	sp := fakeSpec(t, "noline")
	first := make(chan time.Duration, 1)
	start := time.Now()
	if err := Run(context.Background(), sp, func(string) { first <- time.Since(start) }); err != nil {
		t.Fatal(err)
	}
	if d := <-first; d > 1500*time.Millisecond {
		t.Errorf("the address came after %v; the command waited 2s", d)
	}
}

func TestLoginRunsInTheTempDirWithoutKeysOrFlockdeckVariables(t *testing.T) {
	out := filepath.Join(t.TempDir(), "rec")
	cwd := t.TempDir() // the "project" directory the adapter itself runs in
	t.Chdir(cwd)
	sp := fakeSpec(t, "record", envOut+"="+out,
		"FLOCKDECK_TOKEN=x", "PERCH_SESSION=x", "ANTHROPIC_API_KEY=k", "OPENAI_API_KEY=k", "ANTHROPIC_AUTH_TOKEN=k",
		"HTTPS_PROXY=http://proxy.example:3128", "NO_PROXY=localhost", "HOME=/h", "USERPROFILE=C:/h", "APPDATA=C:/a")
	if err := Run(context.Background(), sp, func(string) {}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(b), "\n")
	want, _ := filepath.EvalSymlinks(os.TempDir())
	got, _ := filepath.EvalSymlinks(lines[0])
	if got != want {
		t.Errorf("the command ran in %q, want %q", got, want)
	}
	have := map[string]bool{}
	for _, n := range lines[1:] {
		have[strings.ToUpper(n)] = true
	}
	for n := range have {
		if strings.HasPrefix(n, "FLOCKDECK_") || strings.HasPrefix(n, "PERCH_") || strings.Contains(n, "API_KEY") || strings.Contains(n, "AUTH_TOKEN") {
			t.Errorf("the command was given %s", n)
		}
	}
	for _, n := range []string{"HTTPS_PROXY", "NO_PROXY", "HOME", "USERPROFILE", "APPDATA"} {
		if !have[n] {
			t.Errorf("the command was not given %s", n)
		}
	}
}

func TestLoginEnv(t *testing.T) {
	got := LoginEnv([]string{"A=1", "flockdeck_x=1", "FLOCKDECK_Y=1", "CODEX_API_KEY=k", "x_auth_token=k", "PERCH_Z=1", "PATH=/bin"})
	if strings.Join(got, ",") != "A=1,PATH=/bin" {
		t.Errorf("env = %v", got)
	}
}
