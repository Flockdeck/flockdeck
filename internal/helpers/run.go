package helpers

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"time"

	"github.com/jmwri/flockdeck/internal/store"
)

// RunInfo is run.json: the record of a running helper, kept so a later
// Flockdeck can tell that one was left behind.
type RunInfo struct {
	PID int `json:"pid"`
	// Started is when the operating system says the process started, so a pid
	// that has since been given to another process is told apart. Zero where
	// the system cannot say.
	Started   time.Time `json:"started,omitempty"`
	Port      int       `json:"port"`
	StartedAt time.Time `json:"startedAt"`
}

// ReadRun reads run.json.
func (s *Store) ReadRun(id string) (RunInfo, bool) {
	if !validID(id) {
		return RunInfo{}, false
	}
	data, err := os.ReadFile(s.runFile(id))
	if err != nil {
		return RunInfo{}, false
	}
	var r RunInfo
	if json.Unmarshal(data, &r) != nil || r.PID <= 0 {
		return RunInfo{}, false
	}
	return r, true
}

func (s *Store) writeRun(id string, r RunInfo) error {
	if err := os.MkdirAll(s.appDir(id), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return writeFileAtomic(s.runFile(id), data, 0o600)
}

func (s *Store) clearRun(id string) {
	if err := os.Remove(s.runFile(id)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return
	}
}

// startSlack is how far apart two readings of one process's start time may be.
const startSlack = 2 * time.Second

// sameProcess reports whether the process running under r.PID is the one the
// record was made for. A live pid whose start time differs from the record is a
// recycled one. Where the record has no start time the answer is "yes" only
// when the caller said that is acceptable (the lenient flag), which an
// uninstall uses to be careful and a reaper does not.
func sameProcess(r RunInfo, lenient bool) bool {
	if r.PID <= 0 || !store.ProcessAlive(r.PID) {
		return false
	}
	if r.Started.IsZero() {
		return lenient
	}
	got, ok := store.ProcessStartedAt(r.PID)
	if !ok {
		return lenient
	}
	d := got.Sub(r.Started)
	return d < startSlack && d > -startSlack
}

// RunningPID reports whether run.json names a process that is still the one it
// was written for. It is conservative: when that cannot be told, it says yes.
func (s *Store) RunningPID(id string) (int, bool) {
	r, ok := s.ReadRun(id)
	if !ok || !sameProcess(r, true) {
		return 0, false
	}
	return r.PID, true
}
