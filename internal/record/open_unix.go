//go:build unix

package record

import (
	"os"
	"syscall"
)

// openNoBlock opens a file for reading without waiting. Opening a named pipe
// for reading blocks until a writer turns up, so a name that was swapped for
// one between the check and the open would hang the caller; with O_NONBLOCK the
// open returns at once and the handle's own Stat then shows it is not a regular
// file. The flag has no effect on a regular file.
func openNoBlock(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
