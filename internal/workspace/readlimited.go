package workspace

import (
	"io"
	"os"
	"unicode/utf16"
)

// readSettingsFile reads a configuration file that decides where an agent sends what it is
// given, with every limit that a file that somebody else chose has to be read under: it is
// looked at without following a link, a link has to lead to a regular file (a device, a pipe
// or a socket is refused before it is opened, since opening a pipe blocks and a device may
// never end), and no more than settingsFileMax bytes are read. absent says there is no such
// file. why is "" when data was read, and else what is wrong with the file, to follow its path
// in a message.
func readSettingsFile(p string) (data []byte, absent bool, why string) {
	li, err := os.Lstat(p)
	if err != nil {
		if absentErr(err) {
			return nil, true, ""
		}
		return nil, false, "could not be opened"
	}
	fi := li
	if li.Mode()&os.ModeSymlink != 0 {
		if fi, err = os.Stat(p); err != nil {
			if absentErr(err) {
				return nil, true, ""
			}
			return nil, false, "is a link that could not be followed"
		}
	}
	if fi.IsDir() {
		return nil, false, "is a folder"
	}
	if !fi.Mode().IsRegular() {
		return nil, false, "is not a regular file"
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, false, "could not be read"
	}
	defer f.Close()
	if after, err := f.Stat(); err != nil || !after.Mode().IsRegular() {
		return nil, false, "changed while it was being opened"
	}
	data, err = io.ReadAll(io.LimitReader(f, settingsFileMax+1))
	if err != nil {
		return nil, false, "could not be read"
	}
	if len(data) > settingsFileMax {
		return nil, false, "is too large"
	}
	return data, false, ""
}

// decodeUTF16 turns a file that starts with a UTF-16 byte order mark (what some Windows
// editors write) into UTF-8; any other text is returned as it is.
func decodeUTF16(data []byte) []byte {
	if len(data) < 2 {
		return data
	}
	var big bool
	switch {
	case data[0] == 0xFF && data[1] == 0xFE:
	case data[0] == 0xFE && data[1] == 0xFF:
		big = true
	default:
		return data
	}
	data = data[2:]
	units := make([]uint16, 0, len(data)/2)
	for i := 0; i+1 < len(data); i += 2 {
		if big {
			units = append(units, uint16(data[i])<<8|uint16(data[i+1]))
		} else {
			units = append(units, uint16(data[i+1])<<8|uint16(data[i]))
		}
	}
	return []byte(string(utf16.Decode(units)))
}
