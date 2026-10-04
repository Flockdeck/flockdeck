package helpers

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/jmwri/flockdeck/internal/selfupdate"
)

// versionPattern is the only shape of version that becomes part of a path or
// a URL: major.minor.patch with an optional pre-release. It has no separators,
// no leading "v" and no "..", so a version read from a file name cannot name
// anything but a folder directly under versions/.
const versionPattern = `\d{1,6}\.\d{1,6}\.\d{1,6}(?:-[0-9A-Za-z]+(?:\.[0-9A-Za-z]+)*)?`

var versionRe = regexp.MustCompile("^" + versionPattern + "$")

// validVersion reports whether s is safe to use as a version folder name and
// can be ordered against another version.
func validVersion(s string) bool {
	return len(s) <= 64 && versionRe.MatchString(s) && selfupdate.Parseable(s)
}

// Version rule errors. Each is a different thing for a person to do about it,
// so each is its own value.
var (
	// ErrBelowMinimum: the release is older than the oldest this build of
	// Flockdeck will install, whatever its signature says.
	ErrBelowMinimum = errors.New("version is older than the oldest this build of Flockdeck installs")
	// ErrDowngrade: the release is older than what is installed and the user
	// did not ask for that version by name.
	ErrDowngrade = errors.New("version is older than the installed one")
	// ErrAlreadyInstalled: that version is the installed one.
	ErrAlreadyInstalled = errors.New("that version is already installed")
)

// checkVersion applies the version rules to a release's version. installed is
// "" when nothing is installed. explicit is true when the user named the
// version, which lets a downgrade through and nothing else.
func checkVersion(e Entry, version, installed string, explicit bool) error {
	if !validVersion(version) {
		return fmt.Errorf("%q is not a version", version)
	}
	// A minimum that cannot be read would let everything through, so it is a
	// bug in the catalogue and stops the install.
	if !validVersion(e.MinVersion) {
		return fmt.Errorf("the catalogue's minimum version %q for %s is not a version", e.MinVersion, e.ID)
	}
	if version != e.MinVersion && !selfupdate.Newer(version, e.MinVersion) {
		return fmt.Errorf("%w: %s is older than %s", ErrBelowMinimum, version, e.MinVersion)
	}
	if installed == "" || !validVersion(installed) {
		return nil
	}
	switch {
	case version == installed:
		return fmt.Errorf("%w: %s", ErrAlreadyInstalled, version)
	case selfupdate.Newer(version, installed):
		return nil
	case explicit:
		return nil
	}
	return fmt.Errorf("%w: %s is older than %s, so it is not offered; name it with a version to install it anyway", ErrDowngrade, version, installed)
}

// UpdateAvailable reports whether latest is a version to offer an update to
// from installed. Updates are offered upward only.
func UpdateAvailable(e Entry, latest, installed string) bool {
	return validVersion(latest) && validVersion(installed) && selfupdate.Newer(latest, installed) &&
		checkVersion(e, latest, installed, false) == nil
}
