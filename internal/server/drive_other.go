//go:build !windows && !linux

package server

import "os"

// localVolume refuses a path on a drive that is not a local one. Only Windows has
// drive letters that can be mapped to a share or made with subst; here a path is
// a path, and the checks on its name and its links are all there is.
func localVolume(string) error { return nil }

// verifyOpened checks a file once it is open against where it really is. On
// Windows it asks for the handle's final path; here the check on the name before
// the open and the comparison of the file with the one looked at are enough.
func verifyOpened(*os.File) error { return nil }
