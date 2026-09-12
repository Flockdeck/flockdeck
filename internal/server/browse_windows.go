//go:build windows

package server

import (
	"io/fs"
	"syscall"
)

// hiddenOnDisk reports whether Windows itself marks a directory hidden.
//
// A leading dot is how every other system hides one, and was the only test the
// picker made. Windows marks them with an attribute instead, and a home folder
// there holds a row of hidden system junctions kept for programs from before
// Vista -- "Application Data", "Cookies", "NetHood", "My Documents" -- which
// refuse to be listed. The picker offered them among the folders anybody would
// actually open, and answered "Access is denied" for each one tried.
func hiddenOnDisk(e fs.DirEntry) bool {
	info, err := e.Info()
	if err != nil {
		return false
	}
	d, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return ok && d.FileAttributes&syscall.FILE_ATTRIBUTE_HIDDEN != 0
}
