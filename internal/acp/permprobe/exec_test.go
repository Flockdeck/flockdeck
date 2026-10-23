package permprobe

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// The test binary stands in for the program Exec runs: when
// FLOCKDECK_PERMPROBE_HELPER is set it does what the variable says and exits,
// and otherwise the tests run as usual.
func init() {
	mode := os.Getenv("FLOCKDECK_PERMPROBE_HELPER")
	if mode == "" {
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
	return exe, append([]string{"FLOCKDECK_PERMPROBE_HELPER=" + mode}, more...)
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
	if !strings.Contains(strings.ToLower(got), "dir "+strings.ToLower(dir)) {
		t.Errorf("the program did not run in %s: %q", dir, got)
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
