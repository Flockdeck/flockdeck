package server

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
)

// TestAFolderThatCannotBeListedIsSaidInWords covers a folder the picker is not
// allowed into, or cannot read for another reason. The listing failed in the
// operating system's words, which begin with the call that failed and repeat
// the path ("open C:\Users\sam\private: Access is denied.").
func TestAFolderThatCannotBeListedIsSaidInWords(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	dir := t.TempDir()
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"not allowed", fs.ErrPermission, "flockdeck is not allowed to look inside " + dir + " — go up, or choose another folder"},
		{"anything else", errors.New("the device is not ready"), "could not list " + dir + ": the device is not ready"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := readDir
			readDir = func(path string) ([]os.DirEntry, error) {
				return nil, &fs.PathError{Op: "open", Path: path, Err: tc.err}
			}
			t.Cleanup(func() { readDir = old })

			var br browseMsg
			sendCmd(t, conn, command{Cmd: "browse", Path: dir})
			readUntil(t, conn, "browse", &br)
			if br.Error != tc.want {
				t.Errorf("a listing refused with %v was answered %q, want %q", tc.err, br.Error, tc.want)
			}
			if strings.HasPrefix(br.Error, "open ") {
				t.Errorf("the answer %q begins with the call that failed", br.Error)
			}
		})
	}
}
