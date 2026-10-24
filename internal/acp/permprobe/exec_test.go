package permprobe

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The test binary stands in for the program Exec runs: when
// FLOCKDECK_PERMPROBE_HELPER is set it does what the variable says and exits,
// and otherwise the tests run as usual.
//
// A child that has FLOCKDECK_PERMPROBE_DEPTH but no helper mode has lost the
// variable that makes it a helper, and would run the whole suite and start
// another child without end. It exits at once instead. The depth variable is
// set in the test process itself (so a child that inherits the parent's
// environment carries it) and in every environment helper builds.
func init() {
	mode := os.Getenv("FLOCKDECK_PERMPROBE_HELPER")
	if mode == "" {
		if os.Getenv("FLOCKDECK_PERMPROBE_DEPTH") != "" {
			os.Exit(2)
		}
		os.Setenv("FLOCKDECK_PERMPROBE_DEPTH", "1")
		return
	}
	switch mode {
	case "print":
		fmt.Println("hello")
		for _, kv := range os.Environ() {
			fmt.Println("env", kv)
		}
		fmt.Println("dir", func() string { d, _ := os.Getwd(); return d }())
	case "fail":
		fmt.Println("partial")
		os.Exit(3)
	case "hang":
		time.Sleep(time.Minute)
	case "flood":
		chunk := strings.Repeat("x", 1<<20)
		for i := 0; i < maxOutput>>20+2; i++ {
			fmt.Print(chunk)
		}
	}
	os.Exit(0)
}

func helper(t *testing.T, mode string, more ...string) (string, []string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exe, append([]string{"FLOCKDECK_PERMPROBE_HELPER=" + mode, "FLOCKDECK_PERMPROBE_DEPTH=1"}, more...)
}

func TestExecReturnsWhatTheProgramPrintedInTheGivenDirectoryWithOnlyTheGivenEnvironment(t *testing.T) {
	t.Setenv("FLOCKDECK_NOT_GIVEN", "leaked")
	exe, env := helper(t, "print", "FLOCKDECK_GIVEN=yes")
	dir := t.TempDir()
	out, err := Exec(context.Background(), exe, nil, dir, env)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.HasPrefix(got, "hello") || !strings.Contains(got, "env FLOCKDECK_GIVEN=yes") {
		t.Errorf("output %q", got)
	}
	if strings.Contains(got, "FLOCKDECK_NOT_GIVEN") || strings.Contains(got, "leaked") {
		t.Errorf("the parent's environment reached the program: %q", got)
	}
	// macOS reports its temporary folder through a symlink (/private/var), so
	// compare the resolved paths.
	var printed string
	for _, line := range strings.Split(strings.ReplaceAll(got, "\r", ""), "\n") {
		if p, ok := strings.CutPrefix(line, "dir "); ok {
			printed = p
		}
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	have, err := filepath.EvalSymlinks(printed)
	if err != nil {
		t.Fatalf("the program printed the directory %q: %v", printed, err)
	}
	if !strings.EqualFold(have, want) {
		t.Errorf("the program did not run in %s (printed %s)", dir, printed)
	}
}

func TestExecFailsForAMissingProgramAndANonZeroExit(t *testing.T) {
	if _, err := Exec(context.Background(), "/no/such/program", nil, t.TempDir(), []string{"A=1"}); err == nil {
		t.Error("a missing program succeeded")
	}
	exe, env := helper(t, "fail")
	if out, err := Exec(context.Background(), exe, nil, t.TempDir(), env); err == nil {
		t.Errorf("a failing program succeeded: %q", out)
	}
}

func TestExecStopsAProgramThatHangsAtTheTimeout(t *testing.T) {
	was := execTimeout
	execTimeout = 300 * time.Millisecond
	t.Cleanup(func() { execTimeout = was })
	exe, env := helper(t, "hang")
	start := time.Now()
	if _, err := Exec(context.Background(), exe, nil, t.TempDir(), env); err == nil {
		t.Error("a hanging program succeeded")
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("took %v", took)
	}
	if Timeout != 30*time.Second {
		t.Errorf("Timeout = %v, the bound is 30 s", Timeout)
	}
}

func TestExecStopsWhenTheCallersContextIsCancelled(t *testing.T) {
	exe, env := helper(t, "hang")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := Exec(ctx, exe, nil, t.TempDir(), env)
		done <- err
	}()
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a cancelled run succeeded")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the run did not stop when the context was cancelled")
	}
}

func TestExecRefusesOutputOverTheLimit(t *testing.T) {
	exe, env := helper(t, "flood")
	out, err := Exec(context.Background(), exe, nil, t.TempDir(), env)
	if err == nil || !strings.Contains(err.Error(), "too long") {
		t.Fatalf("err = %v, %d bytes", err, len(out))
	}
}

// The child's environment is exactly the one given: nothing is added to it by
// Exec, and the parent's is not passed on. (On Windows the runtime adds
// SYSTEMROOT when a child has none; that is the only addition allowed.)
func TestExecGivesTheProgramExactlyTheEnvironmentItWasGiven(t *testing.T) {
	t.Setenv("FLOCKDECK_NOT_GIVEN", "leaked")
	exe, env := helper(t, "print", "FLOCKDECK_A=1", "FLOCKDECK_B=two")
	out, err := Exec(context.Background(), exe, nil, t.TempDir(), env)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for _, kv := range env {
		want[kv] = true
	}
	got := map[string]bool{}
	for _, line := range strings.Split(strings.ReplaceAll(string(out), "\r", ""), "\n") {
		if kv, ok := strings.CutPrefix(line, "env "); ok {
			got[kv] = true
		}
	}
	for kv := range got {
		if !want[kv] && !strings.HasPrefix(strings.ToUpper(kv), "SYSTEMROOT=") {
			t.Errorf("the program has %q, which it was not given", kv)
		}
	}
	for kv := range want {
		if !got[kv] {
			t.Errorf("the program lacks %q", kv)
		}
	}
}
