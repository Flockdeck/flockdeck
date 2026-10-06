package gitx

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// A tracked folder whose contents cannot be read is a walk error, and Extras gives it back
// as an error. It must not come back as a list with nothing in it, which a caller reads as
// "no extra files" and deletes the checkout on.
func TestExtrasOfATrackedFolderThatCannotBeReadIsAnError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a folder cannot be made unreadable with chmod on Windows")
	}
	repo := newRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, "pkg"), 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, repo, filepath.Join("pkg", "a.txt"), "a\n")
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-m", "add pkg")

	dir := filepath.Join(repo, "pkg")
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	if _, err := os.ReadDir(dir); err == nil {
		t.Skip("reads are not denied here (running as root?)")
	}

	names, total, err := Extras(repo, 5)
	if err == nil {
		t.Fatalf("Extras = %v, %d, nil error; want an error for the folder it could not read", names, total)
	}
}
