package main

import (
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/store"
)

// A launch close behind one that is starting waits for it rather than
// starting a rival, and goes ahead as soon as the first lets the lock go.
func TestHoldStartLockWaitsItsTurn(t *testing.T) {
	isolateState(t)
	first, err := store.TryLockStart()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		first()
	}()

	var warned string
	begun := time.Now()
	release := holdStartLock(5*time.Second, func(s string) { warned = s })
	defer release()
	if waited := time.Since(begun); waited < 150*time.Millisecond {
		t.Errorf("the lock was taken after %v, while the first launch still held it", waited)
	}
	if warned != "" {
		t.Errorf("waiting its turn was reported as %q", warned)
	}
}

// A launch that has been starting for longer than the wait is stuck, and the
// next one starts anyway, saying so.
func TestHoldStartLockGivesUpOnAStuckStart(t *testing.T) {
	isolateState(t)
	stuck, err := store.TryLockStart()
	if err != nil {
		t.Fatal(err)
	}
	defer stuck()

	var warned string
	holdStartLock(100*time.Millisecond, func(s string) { warned = s })()
	if warned == "" {
		t.Error("gave up on the lock without a word")
	}
	// -quit waits for the lock too, and starts nothing: the word has to be
	// true for both.
	if strings.Contains(warned, "starting anyway") {
		t.Errorf("the warning %q says something is being started", warned)
	}
}
