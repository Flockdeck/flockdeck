//go:build !windows

package store

// heldByAnother reports whether a failed open means another process has the
// file open at this moment. Nowhere but Windows refuses an open for that
// reason, so there is nothing here to wait for.
func heldByAnother(error) bool { return false }

// renameHeld is heldByAnother for a rename: nowhere but Windows refuses one
// because somebody else has the file open.
func renameHeld(error) bool { return false }
