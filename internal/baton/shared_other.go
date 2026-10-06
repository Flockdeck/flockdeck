//go:build !windows

package baton

import (
	"io"
	"os"
)

// openShared opens a file to be read. Elsewhere a file is read as it is, with no sharing
// to ask for.
func openShared(path string) (io.ReadCloser, error) { return os.Open(path) }
