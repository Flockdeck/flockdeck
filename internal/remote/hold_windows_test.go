//go:build windows

package remote

import (
	"syscall"
	"testing"
	"time"
)

// TestAnEnrolmentIsReadWhileASaveHasItOpen checks the enrolment loads while
// something else holds the file for a moment, as a save replacing it or the
// virus scanner reading it after one does.
//
// Read straight, that moment's sharing violation came back as an error, and
// remote access was reported broken for a file that was perfectly sound.
func TestAnEnrolmentIsReadWhileASaveHasItOpen(t *testing.T) {
	isolate(t)
	c := &Config{Relay: "https://relay.example", HostID: "h1", AccountID: "a1", Token: "t1"}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	p, err := path()
	if err != nil {
		t.Fatal(err)
	}

	// Held with no sharing at all, which lets nobody else in until it closes.
	name, err := syscall.UTF16PtrFromString(p)
	if err != nil {
		t.Fatal(err)
	}
	h, err := syscall.CreateFile(name, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("hold the enrolment: %v", err)
	}
	const hold = 200 * time.Millisecond
	released := make(chan struct{})
	go func() {
		time.Sleep(hold)
		syscall.CloseHandle(h)
		close(released)
	}()
	defer func() { <-released }()

	got, err := Load()
	if err != nil {
		t.Fatalf("load while the file was held for %v: %v", hold, err)
	}
	if got == nil || got.HostID != "h1" {
		t.Errorf("loaded %+v, want the enrolment saved", got)
	}
}
