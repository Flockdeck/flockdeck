//go:build windows

package appwindow

import "os"

// lockDown is a no-op on Windows: a folder under %LocalAppData% is already
// private to this Windows account by its own inherited ACLs, and os.Chmod
// here only ever toggles the read-only attribute, which mode always leaves
// clear -- nothing this is asked for needs setting.
func lockDown(path string, mode os.FileMode) error { return nil }
