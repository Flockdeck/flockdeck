//go:build linux

package server

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// localVolume refuses a path on a network file system (NFS, SMB, CIFS and the
// like), looked up with statfs on the file, or on the folder it is in when it is
// not there yet.
func localVolume(path string) error {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		if err := syscall.Statfs(filepath.Dir(path), &st); err != nil {
			return nil // the checks that follow say what is wrong with it
		}
	}
	if networkFS(int64(st.Type)) {
		return fmt.Errorf("%s is on a network file system, which a baton is not read from", path)
	}
	return nil
}

// verifyOpened checks the open file the same way, so that a folder swapped for a
// mount between the look at the name and the open does not get past it.
func verifyOpened(f *os.File) error {
	var st syscall.Statfs_t
	if err := syscall.Fstatfs(int(f.Fd()), &st); err != nil {
		return nil
	}
	if networkFS(int64(st.Type)) {
		return fmt.Errorf("%s turned out to be on a network file system, which a baton is not read from", filepath.Base(f.Name()))
	}
	return nil
}
