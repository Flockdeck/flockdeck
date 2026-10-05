//go:build windows

package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// freeLetter is a drive letter with nothing on it, or "".
func freeLetter() string {
	for c := 'Z'; c >= 'G'; c-- {
		if _, err := os.Stat(string(c) + `:\`); err != nil {
			return string(c) + ":"
		}
	}
	return ""
}

func TestLocalVolumeAcceptsTheSystemDrive(t *testing.T) {
	dir := t.TempDir()
	if err := localVolume(filepath.Join(dir, "notes.md")); err != nil {
		t.Errorf("localVolume(temp dir) = %v", err)
	}
	f := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(f, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	o, err := os.Open(f)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	if err := verifyOpened(o); err != nil {
		t.Errorf("verifyOpened(local file) = %v", err)
	}
	if err := checkBatonPath(f); err != nil {
		t.Errorf("checkBatonPath(local file) = %v", err)
	}
}

func TestLocalVolumeRefusesADriveLetterWithNothingOnIt(t *testing.T) {
	l := freeLetter()
	if l == "" {
		t.Skip("no free drive letter")
	}
	if err := localVolume(l + `\notes.md`); err == nil {
		t.Errorf("localVolume(%s\notes.md) accepted a drive that is not there", l)
	}
}

func TestLocalVolumeRefusesASubstDrive(t *testing.T) {
	l := freeLetter()
	if l == "" {
		t.Skip("no free drive letter")
	}
	dir := t.TempDir()
	if out, err := exec.Command("subst", l, dir).CombinedOutput(); err != nil {
		t.Skipf("subst is not available here: %v %s", err, out)
	}
	defer exec.Command("subst", l, "/d").Run()
	f := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(f, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := checkBatonPath(l + `\notes.md`)
	if err == nil || !strings.Contains(err.Error(), "subst") {
		t.Errorf("checkBatonPath through a subst drive = %v", err)
	}
	// The real path of the same file is fine.
	if err := checkBatonPath(f); err != nil {
		t.Errorf("checkBatonPath(real path) = %v", err)
	}
}

// A file whose path is longer than the first buffer asked for is judged by where
// it is and not refused for the length.
func TestVerifyOpenedAcceptsALongLocalPath(t *testing.T) {
	dir := t.TempDir()
	for len(dir) < 1300 {
		dir = filepath.Join(dir, strings.Repeat("d", 200))
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Skipf("this machine does not allow long paths: %v", err)
	}
	f := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(f, []byte("hello"), 0o600); err != nil {
		t.Skipf("this machine does not allow long paths: %v", err)
	}
	o, err := os.Open(f)
	if err != nil {
		t.Skipf("this machine does not allow long paths: %v", err)
	}
	defer o.Close()
	if err := verifyOpened(o); err != nil {
		t.Errorf("verifyOpened(long local path) = %v", err)
	}
}
