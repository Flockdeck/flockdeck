package main

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/store"
)

// shuttingDownFor records an instance that has closed its port -- the first
// thing one shutting down does -- and makes its process read as still running
// for d, and then as gone.
func shuttingDownFor(t *testing.T, d time.Duration) {
	t.Helper()
	isolateState(t)
	if err := store.SaveInstance(&store.Instance{PID: os.Getpid() + 1, URL: "http://127.0.0.1:1", Token: "t", Started: time.Now()}); err != nil {
		t.Fatal(err)
	}
	was := instanceGoing
	t.Cleanup(func() { instanceGoing = was })
	until := time.Now().Add(d)
	instanceGoing = func(*store.Instance) bool { return time.Now().Before(until) }
}

// A launch made while the instance on record is shutting down found its port
// closed, took the record for a stale one and started a rival: restoring the
// projects before the first had saved them, and resuming conversations the
// first one's agents still had open. It waits for the first to finish.
func TestALaunchDuringAShutdownWaitsForItToFinish(t *testing.T) {
	const going = 500 * time.Millisecond
	shuttingDownFor(t, going)
	start := time.Now()
	inst, _, err := runningInstance()
	if err != nil || inst != nil {
		t.Fatalf("runningInstance = %v, %v; want nothing to join once it has gone", inst, err)
	}
	if took := time.Since(start); took < going {
		t.Errorf("the launch went ahead after %v, while the instance on its way out was still running", took)
	}
}

// -quit in the same gap said nothing was running, so `flockdeck -quit &&
// flockdeck` started a second instance beside the first one's agents. It waits
// for the instance to finish, and says it has stopped.
func TestAQuitDuringAShutdownWaitsForItToFinish(t *testing.T) {
	const going = 500 * time.Millisecond
	shuttingDownFor(t, going)
	start := time.Now()
	if err := quitRunning(); err != nil {
		t.Errorf("quitRunning = %v, want the instance seen out and reported stopped", err)
	}
	if took := time.Since(start); took < going {
		t.Errorf("-quit returned after %v, while the instance on its way out was still running", took)
	}
}

// An instance that stops listening and never exits is waited for only so long:
// the launch is told, rather than hanging.
func TestALaunchGivesUpOnAShutdownThatNeverFinishes(t *testing.T) {
	shuttingDownFor(t, time.Hour)
	was := exitWait
	t.Cleanup(func() { exitWait = was })
	exitWait = 300 * time.Millisecond
	if _, _, err := runningInstance(); err == nil {
		t.Error("runningInstance found nothing, want it to say the instance on record is still running")
	}
	if err := quitRunning(); err == nil || errors.Is(err, errNoneRunning) {
		t.Errorf("quitRunning = %v, want it to say the instance is still running", err)
	}
}
