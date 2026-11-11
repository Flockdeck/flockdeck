package store

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// PeekDeviceAccess reads the device records from the preferences file and
// nothing else. Unlike ReadPrefs it never moves a damaged file aside and keeps
// no note of what it read, so something asked on every decision can call it. An
// absent file has no records and no error; a file that cannot be read or parsed
// is an error, which the caller treats as nothing being allowed.
func PeekDeviceAccess() (map[string]DeviceAccess, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	data, err := readState(filepath.Join(dir, prefsFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var v struct {
		Devices map[string]DeviceAccess `json:"devices"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return v.Devices, nil
}

// PrefsStamp is the modification time and size of the preferences file, to tell
// whether it has changed since it was last read. ok is false when it cannot be
// told; an absent file is ok with a zero time.
func PrefsStamp() (mod time.Time, size int64, ok bool) {
	dir, err := Dir()
	if err != nil {
		return time.Time{}, 0, false
	}
	fi, err := os.Stat(filepath.Join(dir, prefsFile))
	if errors.Is(err, fs.ErrNotExist) {
		return time.Time{}, 0, true
	}
	if err != nil {
		return time.Time{}, 0, false
	}
	return fi.ModTime(), fi.Size(), true
}

// PeekConflictRadar is whether the conflict radar is switched on, read from the
// preferences file without moving a damaged file aside or noting the read. The
// radar asks on every git refresh, from a goroutine nobody is waiting on, and
// that is no place to quarantine the user's file: a damaged or unreadable file
// leaves the radar off, and the next real read of the preferences deals with
// the file as it always did.
func PeekConflictRadar() bool {
	dir, err := Dir()
	if err != nil {
		return false
	}
	data, err := readState(filepath.Join(dir, prefsFile))
	if err != nil {
		return false
	}
	var v struct {
		ConflictRadar bool `json:"conflictRadar"`
	}
	return json.Unmarshal(data, &v) == nil && v.ConflictRadar
}
