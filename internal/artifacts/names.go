package artifacts

import (
	"regexp"
	"strings"

	"github.com/jmwri/flockdeck/internal/secretname"
)

// Reason says why a path was refused. It is for this machine's own log and
// tests only: it must never reach a remote client, which is told
// ErrUnavailable and nothing else, so refusing cannot be used to ask what is on
// the disk.
type Reason string

const (
	ReasonLexical    Reason = "lexical"     // the text of the path is unsafe
	ReasonOutside    Reason = "outside"     // not inside the root
	ReasonLink       Reason = "link"        // a symbolic link, junction or reparse point on the way
	ReasonDenied     Reason = "denied"      // a secret file or a denied folder
	ReasonNotRegular Reason = "not-regular" // a directory, device, pipe, socket...
	ReasonHardlink   Reason = "hardlink"    // more than one name for the file
	ReasonChanged    Reason = "changed"     // not what it was when it was checked
	ReasonMissing    Reason = "missing"     // not there, or not readable
)

// denied reports whether rel, a slash-separated path inside a root, has a
// secret by name for any component, file or folder, or lies under a folder that
// exists to keep secrets (.git, .ssh, secrets/, private/ ...). It is applied to
// what the candidate says and again to what the file really is.
//
// It is a name check and nothing more: a secret copied under an innocent name
// is not caught, and nothing here can catch it. See internal/secretname.
func denied(rel string) bool { return secretname.Path(rel) }

// reservedDevice is a name Windows treats as a device however it is spelled:
// with an extension (CON.txt), in any case, with trailing dots or spaces.
var reservedDevice = regexp.MustCompile(`(?i)^(con|prn|aux|nul|conin\$|conout\$|com[0-9\x{b9}\x{b2}\x{b3}]|lpt[0-9\x{b9}\x{b2}\x{b3}])(\..*)?$`)

// shortName is an NTFS 8.3 alias (SECRET~1.TXT): another spelling of a file
// whose real name a name check would have recognised.
var shortName = regexp.MustCompile(`~[0-9]`)

// cleanChars refuses control characters, line separators and, on Windows,
// the characters it cannot have in a name (which are also wildcards). It is
// applied to the whole candidate, including a root prefix that checkLexical
// does not see.
func cleanChars(p string, windows bool) bool {
	for _, r := range p {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == 0x2028 || r == 0x2029 {
			return false
		}
		if windows && strings.ContainsRune(`<>"|?*`, r) {
			return false
		}
	}
	return true
}

// checkLexical refuses a path whose text alone is unsafe. It runs before the
// disk is touched, on the part of a candidate below the root. windows says to
// apply the rules that only mean something on Windows (alternate data
// streams, drive and UNC syntax, reserved device names, which are ordinary
// file names elsewhere); the rest -- control characters, trailing dots and
// spaces, 8.3 aliases -- are applied on every platform, because the same
// repository is opened on all of them and a file that is unsafe to name on one
// is not worth showing on another. The function is pure so that the Windows
// rules are tested on every platform.
//
// p is relative to the root, though one that looks absolute is judged as
// such. Separators may be / or \ whatever the platform.
func checkLexical(p string, windows bool) bool {
	if p == "" || !cleanChars(p, windows) {
		return false
	}
	norm := strings.ReplaceAll(p, `\`, "/")
	if windows {
		// Device (\\.\), extended-length (\\?\) and UNC (\\host\share) forms
		// are another namespace entirely, and the drive's own "C:" is the only
		// colon a path may have.
		if strings.HasPrefix(norm, "//") {
			return false
		}
		rest := norm
		if len(rest) >= 2 && rest[1] == ':' && isLetter(rest[0]) {
			// "C:foo" is relative to the drive's current directory, which is
			// not where it looks; only "C:/..." names a place.
			if len(rest) < 3 || rest[2] != '/' {
				return false
			}
			rest = rest[2:]
		}
		if strings.ContainsRune(rest, ':') {
			return false
		}
	}
	for _, c := range strings.Split(norm, "/") {
		if c == "" || c == "." || c == ".." {
			continue // confinement refuses ".." where it leaves the root
		}
		if strings.HasSuffix(c, ".") || strings.HasSuffix(c, " ") {
			return false
		}
		if windows && reservedDevice.MatchString(strings.TrimRight(c, ". ")) {
			return false
		}
		if shortName.MatchString(c) {
			return false
		}
	}
	return true
}

func isLetter(b byte) bool { return b|0x20 >= 'a' && b|0x20 <= 'z' }
