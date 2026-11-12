//go:build !windows

package server

import "path/filepath"

// realPath is where path really is, every symbolic link followed. A path that
// does not exist is returned as it is.
func realPath(path string) string {
	if r, err := filepath.EvalSymlinks(path); err == nil {
		return r
	}
	return path
}
