//go:build !windows

package appwindow

import "os"

// lockDown sets path's mode explicitly to mode: what MkdirAll or WriteFile
// asked for is not always what is left once a parent folder's own
// permissions, or the process's umask, have had a say, and a redirect file
// carrying a one-time link is worth being exact about.
func lockDown(path string, mode os.FileMode) error { return os.Chmod(path, mode) }
