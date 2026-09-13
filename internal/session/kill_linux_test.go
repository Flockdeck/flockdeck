package session

import (
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestClosingAPaneWhoseProcessHasEndedEndsWhatItLeftRunning covers a pane
// whose own process has exited -- a shell told `exit` -- leaving a job
// running that ignored the hangup its terminal sent. Close did nothing once
// the pane's process had been reaped, so the job ran on after the pane was
// closed.
func TestClosingAPaneWhoseProcessHasEndedEndsWhatItLeftRunning(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("no sh: %v", err)
	}
	pidFile := filepath.Join(t.TempDir(), "job.pid")
	// The shell waits for the job to have set its trap before it exits.
	script := "sh -c 'trap \"\" HUP; echo $$ > " + pidFile + ".tmp; mv " + pidFile + ".tmp " + pidFile + "; exec sleep 3600' & " +
		"while [ ! -s " + pidFile + " ]; do sleep 0.05; done"
	s, err := Start(Config{ID: "left", Kind: KindShell, Cwd: t.TempDir(), Argv: []string{sh, "-c", script}, Env: Env(), Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	pid := waitForPID(t, s, pidFile)

	waitClosed(s.reaped, 10*time.Second)
	if !closedChan(s.reaped) {
		t.Fatal("the pane's process did not exit")
	}
	if !alive(pid) {
		t.Fatal("the job had gone with the pane's process")
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	for deadline := time.Now().Add(5 * time.Second); alive(pid); time.Sleep(20 * time.Millisecond) {
		if !time.Now().Before(deadline) {
			t.Fatal("a job left running by a pane whose process had ended outlived closing the pane")
		}
	}
}

// TestClosingAShellPaneEndsAJobThatIgnoresTheHangup covers a pane running an
// interactive shell, which puts each job in a process group of its own. Close
// signalled only the pane's group, which is the shell's, so a job that ignores
// the hangup -- nohup, a trap -- ran on after the pane was gone.
func TestClosingAShellPaneEndsAJobThatIgnoresTheHangup(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("no sh: %v", err)
	}
	pidFile := filepath.Join(t.TempDir(), "job.pid")
	s, err := Start(Config{ID: "job", Kind: KindShell, Cwd: t.TempDir(), Argv: []string{sh, "-i"}, Env: Env(), Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	job := "sh -c 'trap \"\" HUP; echo $$ > " + pidFile + ".tmp; mv " + pidFile + ".tmp " + pidFile + "; exec sleep 3600' &\n"
	if err := s.WriteString(job); err != nil {
		t.Fatalf("write: %v", err)
	}
	pid := waitForPID(t, s, pidFile)

	// The case is only the one it is meant to be if the shell gave the job a
	// group of its own.
	if pgid, err := syscall.Getpgid(pid); err != nil || pgid == s.cmd.Process.Pid {
		t.Fatalf("the job is in group %d (%v), the pane's own; the shell ran it without job control", pgid, err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	for deadline := time.Now().Add(5 * time.Second); alive(pid); time.Sleep(20 * time.Millisecond) {
		if !time.Now().Before(deadline) {
			t.Fatal("a shell job that ignores the hangup outlived closing the pane")
		}
	}
}
