//go:build !windows

package filerewind

import "os"

// isLink says path is a symbolic link or something else that is not a plain
// file or folder.
func isLink(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0
}
