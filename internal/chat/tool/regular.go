package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"
)

// notAFile reports whether a mode is a named pipe, a socket or a device: what
// the file tools must neither open nor write, because it does not behave as a
// file does. A mode that is merely irregular is not one of them -- on Windows
// that is a file behind a reparse point of an uncommon kind, a OneDrive
// placeholder or a deduplicated file, which reads like any other.
func notAFile(mode os.FileMode) bool {
	return mode&(os.ModeNamedPipe|os.ModeSocket|os.ModeDevice|os.ModeCharDevice) != 0
}

// regularFile refuses a path that exists and is not a regular file. The file
// tools read and write files, and anything else under a file's name does not
// do what they would say it did: NUL on Windows -- and CON, AUX or COM1 on
// Windows 10 -- swallows a write reported as written or reads the console, and
// a named pipe on Linux or macOS makes a read wait forever for a writer.
// A directory is not asked about here; each tool says what to use instead.
func regularFile(root *Root, abs string, info os.FileInfo) error {
	if info.IsDir() || !notAFile(info.Mode()) {
		return nil
	}
	what := "a device"
	switch mode := info.Mode(); {
	case mode&os.ModeNamedPipe != 0:
		what = "a named pipe"
	case mode&os.ModeSocket != 0:
		what = "a socket"
	}
	return fmt.Errorf("%s is not a regular file but %s, which the file tools do not read or write", root.Rel(abs), what)
}

// deviceName refuses, on Windows, a file name the system keeps for a device:
// CON, PRN, AUX, NUL, COM0 to COM9 and LPT0 to LPT9 (and the superscript
// COM¹ to LPT³), CONIN$ and CONOUT$. Windows drops a trailing dot or space,
// and Windows 10 also reads such a name as the device whatever extension
// follows, so nul., "nul " and nul.txt are counted too.
//
// regularFile can only look at what is there, and in a directory write_file
// has yet to make there is nothing to look at: a write to newdir/NUL was
// taken for a new file, and once the directory was made it went to the device
// and was reported as written.
func deviceName(root *Root, abs string) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	name := strings.TrimRight(filepath.Base(abs), ". ")
	name, _, _ = strings.Cut(name, ".")
	name, _, _ = strings.Cut(name, ":")
	name = strings.ToUpper(strings.TrimRight(name, " "))
	device := false
	switch name {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		device = true
	default:
		if n, ok := strings.CutPrefix(name, "COM"); ok {
			device = strings.Contains("0123456789¹²³", n) && utf8.RuneCountInString(n) == 1
		} else if n, ok := strings.CutPrefix(name, "LPT"); ok {
			device = strings.Contains("0123456789¹²³", n) && utf8.RuneCountInString(n) == 1
		}
	}
	if !device {
		return nil
	}
	return fmt.Errorf("%s is not a regular file but a name Windows keeps for a device, which the file tools do not read or write", root.Rel(abs))
}
