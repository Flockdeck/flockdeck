package store

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// A profile an earlier build kept elsewhere is moved across once, and one
// already where this build keeps it is never written over.
func TestAdoptProfileMovesTheOldProfileOnce(t *testing.T) {
	base := t.TempDir()
	old, dir := filepath.Join(base, "roaming", "window"), filepath.Join(base, "local", "flockdeck", "window")
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "Local State"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	adoptProfile(old, dir)
	if got, err := os.ReadFile(filepath.Join(dir, "Local State")); err != nil || string(got) != "old" {
		t.Fatalf("moved profile = %q, %v; want the old one's contents", got, err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("the old profile is still there after the move: %v", err)
	}

	// Another one turning up in the old place later is left alone.
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	adoptProfile(old, dir)
	if _, err := os.Stat(old); err != nil {
		t.Errorf("a profile was moved over one already in place: %v", err)
	}
}

// On Windows the window's profile lives in the local application data folder,
// not in the roaming one beside the rest of the state, and one left in the
// roaming folder by an earlier build comes with it.
func TestBrowserProfileDirIsLocalOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows roams the folder the state is kept in")
	}
	roaming, local := t.TempDir(), t.TempDir()
	t.Setenv("APPDATA", roaming)
	t.Setenv("LOCALAPPDATA", local)
	old := filepath.Join(roaming, "flockdeck", "window")
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "Local State"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := BrowserProfileDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(local, "flockdeck", "window"); got != want {
		t.Errorf("BrowserProfileDir = %s, want %s", got, want)
	}
	if b, err := os.ReadFile(filepath.Join(got, "Local State")); err != nil || string(b) != "old" {
		t.Errorf("the roaming profile was not carried over: %q, %v", b, err)
	}
}
