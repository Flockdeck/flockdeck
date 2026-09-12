package store

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// A profile an earlier build kept elsewhere is moved across, and one found in
// the old place once there is one in the new place is deleted: it holds
// nothing the window could use, and it is a couple of hundred megabytes.
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

	// One turning up in the old place later, as when the move could not be
	// made while an earlier build's window had it open, is deleted, and the
	// one in place is not touched.
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "Local State"), []byte("left behind"), 0o600); err != nil {
		t.Fatal(err)
	}
	adoptProfile(old, dir)
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("the profile left in the old place is still there: %v", err)
	}
	if _, err := os.Stat(old + ".removing"); !os.IsNotExist(err) {
		t.Errorf("the profile left behind was set aside but not deleted: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "Local State")); err != nil || string(got) != "old" {
		t.Errorf("the profile in place = %q, %v; want it untouched", got, err)
	}
}

// With both application data folders pointed at the same place the old
// location is the new one, and the profile there is the one in use.
func TestAdoptProfileLeavesTheProfileInPlace(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "flockdeck", "window")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Local State"), []byte("in use"), 0o600); err != nil {
		t.Fatal(err)
	}
	adoptProfile(dir, dir)
	if got, err := os.ReadFile(filepath.Join(dir, "Local State")); err != nil || string(got) != "in use" {
		t.Errorf("the profile in place = %q, %v; want it untouched", got, err)
	}
}

// An earlier build's window may still be running on the old profile. Windows
// will not rename a folder with a file in it held open, and so that profile
// is left for a later start rather than deleted under the browser.
func TestAdoptProfileLeavesAProfileInUse(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows refuses to rename a folder that is in use")
	}
	base := t.TempDir()
	old, dir := filepath.Join(base, "roaming", "window"), filepath.Join(base, "local", "flockdeck", "window")
	for _, d := range []string{old, dir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	held, err := os.Create(filepath.Join(old, "lockfile"))
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()

	adoptProfile(old, dir)
	if _, err := os.Stat(filepath.Join(old, "lockfile")); err != nil {
		t.Errorf("a profile in use was moved or deleted: %v", err)
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
