//go:build windows

package store

import (
	"errors"
	"syscall"
)

// sharingViolation is what Windows returns for an open refused because
// somebody else holds the file. Go's syscall package does not name it.
const sharingViolation = syscall.Errno(32)

// heldByAnother reports whether a failed open means another process has the
// file open at this moment, rather than that the open was never going to work.
//
// It is the difference between waiting and hanging. A sharing violation is the
// other instance part-way through replacing the file, and it is gone in
// milliseconds. Everything else — a directory where a file should be, a name
// this user may not read — will still be true a second from now, and spending
// the whole budget on it would make the project picker, which reads the recent
// list every time it opens, feel broken rather than fail.
func heldByAnother(err error) bool {
	return errors.Is(err, sharingViolation)
}

// renameHeld is heldByAnother for a rename. Windows refuses to replace a file
// anything else has open, whatever sharing it asked for, with access denied —
// which a directory standing in the way also gets, and is waited out with it,
// briefly and for nothing — or with a sharing violation.
func renameHeld(err error) bool {
	return errors.Is(err, syscall.ERROR_ACCESS_DENIED) || errors.Is(err, sharingViolation)
}
