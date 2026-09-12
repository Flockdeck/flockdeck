package tool

import (
	"fmt"
	"os"
)

// regularFile refuses a path that exists and is not a regular file. The file
// tools read and write files, and anything else under a file's name does not
// do what they would say it did: NUL on Windows -- and CON, AUX or COM1 on
// Windows 10 -- swallows a write reported as written or reads the console, and
// a named pipe on Linux or macOS makes a read wait forever for a writer.
// A directory is not asked about here; each tool says what to use instead.
func regularFile(root *Root, abs string, info os.FileInfo) error {
	if info.IsDir() || info.Mode().IsRegular() {
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
