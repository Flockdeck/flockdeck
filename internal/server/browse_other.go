//go:build !windows

package server

import "io/fs"

// hiddenOnDisk is Windows' hidden attribute; everywhere else a leading dot is
// the whole of it. See browse_windows.go.
func hiddenOnDisk(fs.DirEntry) bool { return false }
