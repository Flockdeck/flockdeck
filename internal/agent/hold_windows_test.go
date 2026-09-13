//go:build windows

package agent

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// The picker asking whether an agent has a key while a save is replacing the
// store waits the save out and finds the key. A read refused as a sharing
// violation used to read as no key, greying the agent out for as long as the
// answer was trusted.
func TestAKeyIsFoundWhileTheStoreIsBeingReplaced(t *testing.T) {
	keys := filepath.Join(t.TempDir(), KeysName)
	if err := os.WriteFile(keys, []byte(`{"openai":"sk-test"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stubProbes(t, nil, keys)

	// A save part-way through replacing the file, which lets nobody else in.
	p, err := syscall.UTF16PtrFromString(keys)
	if err != nil {
		t.Fatal(err)
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("hold the store: %v", err)
	}
	const hold = 400 * time.Millisecond
	released := make(chan struct{})
	go func() {
		time.Sleep(hold)
		syscall.CloseHandle(h)
		close(released)
	}()
	defer func() { <-released }()

	if !keyIsSet(Spec{ID: "openai", Runner: RunnerAPI}) {
		t.Errorf("the key read as missing while a save held the store for %v", hold)
	}
}

// A default saved while agents.json is open elsewhere -- a picker reading the
// catalog, the virus scanner after the last save -- waits for it to be let go.
// A plain rename onto a file somebody has open is refused on the spot with
// "Access is denied", and the default was lost.
func TestADefaultIsSavedWhileAgentsJSONIsHeldOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ConfigName)
	if err := os.WriteFile(path, []byte(`{"version": 1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// Open as a reader opens it: others may read it, but not replace it.
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("hold agents.json: %v", err)
	}
	const hold = 400 * time.Millisecond
	released := make(chan struct{})
	go func() {
		time.Sleep(hold)
		syscall.CloseHandle(h)
		close(released)
	}()
	defer func() { <-released }()

	if err := SetDefaults(dir, "", Defaults{Agent: "anthropic"}); err != nil {
		t.Fatalf("saving a default while agents.json was open for %v: %v", hold, err)
	}
	<-released
	if f, err := ReadConfig(dir); err != nil || f.Defaults.Agent != "anthropic" {
		t.Errorf("the default saved is %+v (%v), want anthropic", f, err)
	}
}
