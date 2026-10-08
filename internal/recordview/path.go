package recordview

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	maxNameComponents = 8
	maxNameBytes      = 512
)

// resolve returns the path of name inside root after checking both, and that no
// component of name is a link. Following a link is what it must not do, so each
// component is looked at with Lstat before the next is joined on, and the file
// is opened by the caller afterwards by the path that was walked.
//
// What it cannot do is hold the folders still: a component can be swapped for a
// link between the walk and the open, as with any path-based check. The folder a
// recording lives in is the host's own, and a program that can write there can
// already write the recording.
func resolve(root, name string) (string, error) {
	if !plainRoot(root) || !plainName(name) {
		return "", ErrUnsupported
	}
	fi, err := os.Lstat(root)
	if err != nil {
		return "", ErrUnavailable
	}
	if !fi.IsDir() || isLink(fi) {
		return "", ErrUnsupported
	}
	path := root
	parts := strings.Split(name, "/")
	for i, part := range parts {
		path = filepath.Join(path, part)
		if i == len(parts)-1 {
			break
		}
		fi, err := os.Lstat(path)
		if err != nil {
			return "", ErrUnavailable
		}
		if !fi.IsDir() || isLink(fi) {
			return "", ErrUnsupported
		}
	}
	return path, nil
}

// isLink reports whether a file is a link of any kind. On Windows a junction or
// another reparse point is not a symbolic link to Go, and shows as irregular. The
// check is also made by the folder test that goes with it, which a link fails, so
// it is a second guard and not the only one.
func isLink(fi os.FileInfo) bool {
	return fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0
}

// plainRoot reports whether root is an absolute path to a local folder: not
// relative, not a UNC, device or extended-length path, and with no colon after
// the drive.
func plainRoot(root string) bool {
	if root == "" || !filepath.IsAbs(root) || strings.ContainsRune(root, 0) {
		return false
	}
	if strings.HasPrefix(root, `\\`) || strings.HasPrefix(root, "//") {
		return false
	}
	if runtime.GOOS == "windows" {
		rest := root[len(filepath.VolumeName(root)):]
		if strings.ContainsAny(rest, ":*?\"<>|") {
			return false
		}
	}
	return true
}

// plainName reports whether name is a relative path of folder and file names
// separated by "/", every one of which means itself on every system: no "." or
// "..", no backslash or colon, no character a file name may not have on Windows,
// no control character, no trailing dot or space, no reserved device name.
func plainName(name string) bool {
	if name == "" || len(name) > maxNameBytes {
		return false
	}
	parts := strings.Split(name, "/")
	if len(parts) > maxNameComponents {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || isReserved(part) {
			return false
		}
		if last := part[len(part)-1]; last == '.' || last == ' ' {
			return false
		}
		for _, r := range part {
			if r < 0x20 || r == 0x7f || strings.ContainsRune(`\:*?"<>|`, r) {
				return false
			}
		}
	}
	return true
}

// isReserved reports whether a name is a device on Windows: CON, PRN, AUX, NUL,
// COM1 to COM9, LPT1 to LPT9, CONIN$ and CONOUT$, with or without an extension.
func isReserved(part string) bool {
	base, _, _ := strings.Cut(part, ".")
	base = strings.ToUpper(strings.TrimRight(base, " "))
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	if len(base) >= 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) {
		rest := base[3:]
		return rest == "1" || rest == "2" || rest == "3" || rest == "4" || rest == "5" ||
			rest == "6" || rest == "7" || rest == "8" || rest == "9" ||
			rest == "¹" || rest == "²" || rest == "³"
	}
	return false
}
