package helpers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync/atomic"
	"testing"
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
	// OwnerPID and OwnerStarted name the Flockdeck that started the helper, with
	// its start time, so a later Flockdeck can tell a helper that belongs to a
	// Flockdeck still running (a -solo instance, say) from one left by a crash.
	OwnerPID     int       `json:"ownerPid,omitempty"`
	OwnerStarted time.Time `json:"ownerStarted,omitempty"`
	// Starting marks a record written before there is a process to name: from
	// the moment a start is asked for, through every restart delay, so an
	// uninstall or an install cannot slip in while the program is being checked
	// and launched. PID is 0 in it.
	Starting bool `json:"starting,omitempty"`
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
	if json.Unmarshal(data, &r) != nil || (r.PID <= 0 && !(r.Starting && r.OwnerPID > 0)) {
		return RunInfo{}, false
	}
	return r, true
}

// ownerAlive reports whether the Flockdeck that wrote a record is still the
// process it was. Where its start time was not recorded, a live pid counts.
func ownerAlive(r RunInfo) bool {
	return r.OwnerPID > 0 && sameProcess(RunInfo{PID: r.OwnerPID, Started: r.OwnerStarted}, true)
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

// removeFile is os.Remove. A variable so a test can make it fail, as an
// antivirus scanner holding a file open does on Windows.
var removeHook atomic.Pointer[func(string) error]

func removeFile(path string) error {
	if h := removeHook.Load(); h != nil {
		return (*h)(path)
	}
	return os.Remove(path)
}

// clearTimings is how long a record that cannot be removed is retried for in
// the background, and how often. Atomic because a retry outlives the call that
// started it, and a test changes it.
var clearTimings atomic.Pointer[[2]time.Duration]

func clearFor() (total, every time.Duration) {
	if t := clearTimings.Load(); t != nil {
		return t[0], t[1]
	}
	return time.Minute, time.Second
}

// clearRun removes run.json. A failed remove is retried a few times at once, and
// then in the background, because a record that is left behind says a helper is
// starting or running when none is, and blocks a start, an install and an
// uninstall until something removes it. (While it is left, this process does not
// believe it: see bindOwner.)
func (s *Store) clearRun(id string) {
	for try := 0; try < 5; try++ {
		err := removeFile(s.runFile(id))
		if err == nil || errors.Is(err, fs.ErrNotExist) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	total, every := clearFor()
	go func() {
		deadline := time.Now().Add(total)
		for time.Now().Before(deadline) {
			time.Sleep(every)
			if s.ownerActive(id) {
				return // a new run has written its own record
			}
			if err := removeFile(s.runFile(id)); err == nil || errors.Is(err, fs.ErrNotExist) {
				return
			}
		}
	}()
}

// bindOwner tells the store which process is the supervisor and how to ask it
// whether a helper has a live instance. A starting record that names this
// process while its supervisor has no instance for the helper is a leftover
// (a remove that failed), not a start under way, and RunningPID does not count
// it.
func (s *Store) bindOwner(pid int, active func(id string) bool) {
	s.ownMu.Lock()
	s.ownPID, s.ownActive = pid, active
	s.ownMu.Unlock()
}

func (s *Store) ownerActive(id string) bool {
	s.ownMu.Lock()
	active := s.ownActive
	s.ownMu.Unlock()
	return active != nil && active(id)
}

// ownLeftover reports whether a record is this process's own and no instance
// of its supervisor stands behind it.
func (s *Store) ownLeftover(id string, r RunInfo) bool {
	s.ownMu.Lock()
	pid, active := s.ownPID, s.ownActive
	s.ownMu.Unlock()
	return active != nil && r.OwnerPID == pid && !active(id)
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
	if !ok {
		return 0, false
	}
	if r.PID == 0 {
		// A start under way: it counts for as long as the Flockdeck doing it does,
		// unless it is this process's own and nothing here is starting.
		if s.ownLeftover(id, r) {
			return 0, false
		}
		if ownerAlive(r) {
			return r.OwnerPID, true
		}
		return 0, false
	}
	if !sameProcess(r, true) {
		return 0, false
	}
	return r.PID, true
}

// WriteStartingForTest writes the record of a start under way, owned by this
// process, for tests of other packages that need a helper that is starting.
func (s *Store) WriteStartingForTest(id string) error {
	if !testing.Testing() {
		panic("helpers: WriteStartingForTest is for tests only")
	}
	started, _ := store.ProcessStartedAt(os.Getpid())
	return s.writeRun(id, RunInfo{Starting: true, OwnerPID: os.Getpid(), OwnerStarted: started})
}

// sendCtrlBreak is CtrlBreak. A variable so a test does not send one.
var sendCtrlBreak = CtrlBreak

// CtrlBreakHelper sends CTRL_BREAK to a helper's process group, and refuses any
// pid that is not one this Flockdeck's run.json names, with the start time it
// recorded. The hidden subcommand that calls it can be run by any program on
// the machine, and without this check it would send the event to any process
// group whose pid it was handed.
func CtrlBreakHelper(st *Store, pid int) error {
	if pid > 0 {
		for _, e := range Catalogue() {
			r, ok := st.ReadRun(e.ID)
			if ok && r.PID == pid && sameProcess(r, false) {
				return sendCtrlBreak(pid)
			}
		}
	}
	return fmt.Errorf("process %d is not a helper that Flockdeck started, so no signal was sent to it", pid)
}
