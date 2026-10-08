//go:build !unix

package record

import "os"

// openNoBlock opens a file for reading. Only unix has named pipes in a folder; a
// name that is not a regular file is refused before this is called.
func openNoBlock(path string) (*os.File, error) {
	return os.Open(path)
}
