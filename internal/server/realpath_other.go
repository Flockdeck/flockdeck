//go:build !windows

package server

import "path/filepath"

// resolveExisting is where an existing path really is, every symbolic link
// followed. ok is false when the path cannot be resolved.
func resolveExisting(path string) (string, bool) {
	r, err := filepath.EvalSymlinks(path)
	return r, err == nil
}
