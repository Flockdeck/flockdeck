//go:build windows

package agent

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

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
