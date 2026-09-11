//go:build !windows

package tool

import "path/filepath"

// realPath is where a path really leads, through every symbolic link on the way.
func realPath(p string) (string, error) { return filepath.EvalSymlinks(p) }
