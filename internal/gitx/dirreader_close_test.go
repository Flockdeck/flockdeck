package gitx

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// A ReadDir that was given up on is still running when the scan moves on and
// closes the folder. Closing the handle under it freed the directory buffer
// that the read was draining, and on Windows the read died with a nil pointer
// dereference in os.(*File).readdir, taking the whole program with it.
func TestClosingAFolderWhileAGivenUpReadIsStillRunningIsSafe(t *testing.T) {
	wasTimeout := statTimeout
	t.Cleanup(func() { statTimeout = wasTimeout })

	dir := t.TempDir()
	for i := 0; i < 3000; i++ {
		if err := os.WriteFile(filepath.Join(dir, "file-with-a-longish-name-"+strconv.Itoa(i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Time out at once, so every read is given up on while it runs.
	for i := 0; i < 300; i++ {
		statTimeout = 2 * time.Second
		f := tfs()
		d, err := f.OpenDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		statTimeout = time.Nanosecond
		_, _ = d.next(10)
		d.close()
		statTimeout = 2 * time.Second
	}
	// Let the reads that were given up on come back; a panic in one of them
	// ends the test binary.
	time.Sleep(200 * time.Millisecond)
}
