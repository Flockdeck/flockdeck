package helpers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jmwri/flockdeck/internal/store"
)

// Layout on disk, under <state>/apps/<id>/:
//
//	versions/<v>/        the unpacked release: binary, README.md, install.json
//	current.json         {"version":"0.4.0"}, written last by an install
//	data/                the helper's DATA_DIR; an uninstall leaves it alone
//	logs/helper.log      and helper.log.1 to .3
//	run.json             only while it runs: pid, start time, port
//	staging.new-*/       an install in progress, swept when old
//
// Every path is built from an id that passed validID and a version that passed
// validVersion, never from anything read out of an archive or a helper.

// Store is the apps directory.
type Store struct {
	// Root is <state>/apps.
	Root string
}

// DefaultStore is the apps directory under the state directory.
func DefaultStore() (*Store, error) {
	dir, err := store.Dir()
	if err != nil {
		return nil, err
	}
	return &Store{Root: filepath.Join(dir, "apps")}, nil
}

func (s *Store) appDir(id string) string        { return filepath.Join(s.Root, id) }
func (s *Store) versionsDir(id string) string   { return filepath.Join(s.appDir(id), "versions") }
func (s *Store) versionDir(id, v string) string { return filepath.Join(s.versionsDir(id), v) }
func (s *Store) currentFile(id string) string   { return filepath.Join(s.appDir(id), "current.json") }
func (s *Store) runFile(id string) string       { return filepath.Join(s.appDir(id), "run.json") }
func (s *Store) logsDir(id string) string       { return filepath.Join(s.appDir(id), "logs") }
func (s *Store) logFile(id string) string       { return filepath.Join(s.logsDir(id), "helper.log") }

// DataDir is where the helper keeps its own data.
func (s *Store) DataDir(id string) string { return filepath.Join(s.appDir(id), "data") }

// LogFile is the helper's current log file.
func (s *Store) LogFile(id string) string { return s.logFile(id) }

// InstallInfo is what install.json records about a version.
type InstallInfo struct {
	Version     string    `json:"version"`
	Source      string    `json:"source"`
	SHA256      string    `json:"sha256"`
	Signed      bool      `json:"signed"`
	InstalledAt time.Time `json:"installedAt"`
}

type currentFile struct {
	Version string `json:"version"`
}

// Current is the installed version of a helper, and false when there is none.
// A current.json that is missing, unreadable, or names a version that is not a
// version or has no folder counts as not installed: it is what an install
// interrupted before its last step leaves behind.
func (s *Store) Current(id string) (string, bool) {
	if !validID(id) {
		return "", false
	}
	data, err := os.ReadFile(s.currentFile(id))
	if err != nil {
		return "", false
	}
	var c currentFile
	if json.Unmarshal(data, &c) != nil || !validVersion(c.Version) {
		return "", false
	}
	if fi, err := os.Stat(s.versionDir(id, c.Version)); err != nil || !fi.IsDir() {
		return "", false
	}
	return c.Version, true
}

// Info reads install.json for the installed version.
func (s *Store) Info(id string) (InstallInfo, bool) {
	v, ok := s.Current(id)
	if !ok {
		return InstallInfo{}, false
	}
	data, err := os.ReadFile(filepath.Join(s.versionDir(id, v), "install.json"))
	if err != nil {
		return InstallInfo{Version: v}, true
	}
	var info InstallInfo
	if json.Unmarshal(data, &info) != nil {
		return InstallInfo{Version: v}, true
	}
	info.Version = v
	return info, true
}

// BinaryPath is the installed executable.
func (s *Store) BinaryPath(e Entry, goos string) (string, error) {
	v, ok := s.Current(e.ID)
	if !ok {
		return "", fmt.Errorf("%s is not installed", e.Name)
	}
	return filepath.Join(s.versionDir(e.ID, v), e.BinaryName(goos)), nil
}

// writeFileAtomic writes a file by writing a temporary one beside it and
// renaming it over the target, so a reader sees the old content or the new and
// a crash leaves one of them.
func writeFileAtomic(path string, data []byte, perm fs.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, perm)
	}
	if err == nil {
		err = store.RenameWithRetry(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

// staleStaging is how old a staging folder is before the sweep removes it. An
// install that is really running keeps writing to it, and an hour is longer
// than the download's own limits.
const staleStaging = time.Hour

// SweepStaging removes staging folders and temporary files an interrupted
// install left, when they are older than an hour. It returns how many it
// removed.
func (s *Store) SweepStaging(id string, now time.Time) int {
	if !validID(id) {
		return 0
	}
	entries, err := os.ReadDir(s.appDir(id))
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "staging.new-") && !strings.HasPrefix(name, ".tmp-") {
			continue
		}
		fi, err := e.Info()
		if err != nil || now.Sub(fi.ModTime()) < staleStaging {
			continue
		}
		if os.RemoveAll(filepath.Join(s.appDir(id), name)) == nil {
			n++
		}
	}
	return n
}

// Uninstall removes the versions, current.json, the logs and run.json, and
// leaves data/. With purge it removes data/ too. It refuses while the helper
// is running.
func (s *Store) Uninstall(id string, purge bool) error {
	if !validID(id) {
		return fmt.Errorf("%q is not a helper", id)
	}
	if pid, ok := s.RunningPID(id); ok {
		return fmt.Errorf("%s is running (process %d); stop it first", id, pid)
	}
	dir := s.appDir(id)
	for _, name := range []string{"versions", "logs"} {
		if err := os.RemoveAll(filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	for _, name := range []string{"current.json", "run.json", "run.lock"} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "staging.new-") || strings.HasPrefix(e.Name(), ".tmp-") {
				_ = os.RemoveAll(filepath.Join(dir, e.Name()))
			}
		}
	}
	if purge {
		return s.PurgeData(id)
	}
	// An empty folder with nothing in it is not worth leaving.
	_ = os.Remove(dir)
	return nil
}

// PurgeData deletes the helper's data folder, which is always <app>/data and
// is never a path the helper reported. A data folder that is a link or a
// junction is refused: removing what it points at is not this function's job.
func (s *Store) PurgeData(id string) error {
	if !validID(id) {
		return fmt.Errorf("%q is not a helper", id)
	}
	if pid, ok := s.RunningPID(id); ok {
		return fmt.Errorf("%s is running (process %d); stop it first", id, pid)
	}
	data := s.DataDir(id)
	fi, err := os.Lstat(data)
	if errors.Is(err, fs.ErrNotExist) {
		_ = os.Remove(s.appDir(id))
		return nil
	}
	if err != nil {
		return err
	}
	if !fi.IsDir() || fi.Mode()&fs.ModeSymlink != 0 || fi.Mode()&fs.ModeIrregular != 0 {
		return fmt.Errorf("%s is not a plain folder, so it was left alone", data)
	}
	if err := os.RemoveAll(data); err != nil {
		return err
	}
	_ = os.Remove(s.appDir(id))
	return nil
}

// HasData reports whether the helper has a data folder, for the message after an uninstall.
func (s *Store) HasData(id string) bool {
	fi, err := os.Stat(s.DataDir(id))
	return err == nil && fi.IsDir()
}
