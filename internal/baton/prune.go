package baton

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// RetainFor is how long a stored baton is kept after it was last saved or used.
// Older ones, and the overflow files written for them, are removed when a baton is
// saved, unless something still wants them (see Prune).
const RetainFor = 30 * 24 * time.Hour

const (
	// overflowsName records where the full text of a baton too big for its prompt
	// was written, as id to a list of paths. The paths are in checkouts, so the
	// store has to remember them to clean them up.
	overflowsName = "overflow.json"
	// retriesName records how many times removing an overflow file has failed, as
	// path to a count, so that one that cannot be removed is given up on.
	retriesName = "overflow-retries.json"
	// usedName records when each baton was last used, as id to a time. It is read
	// beside the file's own modified time and the time the id was made from, because
	// a baton restored from a backup has an old modified time and was just wanted.
	usedName = "used.json"
	// pruneName records the latest time pruning has seen, to notice a clock that
	// went backward.
	pruneName = "prune.json"
	// inuseDir holds one empty file per baton a pane was just started from, so that
	// another Flockdeck running on this machine, whose pane has not been saved to a
	// layout yet, does not see its baton age out.
	inuseDir = "inuse"

	// leaseFor is how long a started-from mark protects a baton. A pane that is
	// still open is protected by its saved layout, which is written every half
	// minute; the mark covers the time before the first save.
	leaseFor = time.Hour
	// maxOverflowFailures is how many times removing an overflow file may fail
	// before it is forgotten.
	maxOverflowFailures = 5
	// removeWithin is how long one overflow file is given to be looked at and
	// removed.
	removeWithin = 3 * time.Second
)

// ErrClock is what Prune says when the clock does not look right: it went
// backward, it jumped while Flockdeck was running, or a baton is dated in the
// future. Nothing is removed.
var ErrClock = errors.New("the clock does not look right, so no baton was removed")

// logf is where housekeeping trouble is said. SetLogf changes it, and it is safe to
// call while other goroutines are using it.
var logf atomic.Pointer[func(string, ...any)]

// SetLogf sets where the store says what went wrong with its housekeeping.
func SetLogf(f func(format string, args ...any)) { logf.Store(&f) }

func logBaton(format string, args ...any) {
	if f := logf.Load(); f != nil {
		(*f)(format, args...)
		return
	}
	fmt.Fprintf(os.Stderr, "flockdeck: "+format+"\n", args...)
}

// clockWatch notices a wall clock that was set while Flockdeck ran. It compares the
// wall clock with the monotonic one between one look and the next, not since the
// process started: the monotonic clock stops while a machine sleeps (on Linux and
// macOS), so a comparison from the start would report a jump for ever after an hour of
// sleep. A jump of more than an hour between two looks is reported once, and the
// baseline is taken again from that look.
type clockWatch struct {
	mu       sync.Mutex
	sample   func() (wall time.Time, mono time.Duration)
	have     bool
	lastWall time.Time
	lastMono time.Duration
}

func (w *clockWatch) jumped() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	wall, mono := w.sample()
	if !w.have {
		w.have, w.lastWall, w.lastMono = true, wall, mono
		return false
	}
	d := wall.Sub(w.lastWall) - (mono - w.lastMono)
	w.lastWall, w.lastMono = wall, mono
	return d > time.Hour || d < -time.Hour
}

// procStart is when this process started, with the monotonic reading time.Now
// carries.
var procStart = time.Now()

// defaultWatch is the clock Prune looks at. A test replaces its sample, and the logic
// that decides is the real one.
var defaultWatch = &clockWatch{sample: func() (time.Time, time.Duration) {
	return time.Now().Round(0), time.Since(procStart)
}}

// absurdAge is how old a baton has to look for the clock to be what is wrong. A
// baton is last used within a few years at the most: older than this, the clock is
// ahead, and nothing is removed.
const absurdAge = 5 * 365 * 24 * time.Hour

// staleTemp is how old a temporary record file is before it is swept.
const staleTemp = time.Hour

// Touch records that a baton was used now, so that one that is sent again, shown
// or started from does not age out while it is still wanted. An id that is not
// stored is ignored.
func (s *Store) Touch(id string) {
	path := s.Path(id)
	if path == "" {
		return
	}
	now := touchNow()
	// The use is always written on the baton's own file time as well, which is what the age
	// rule of Prune reads first: a record of uses that cannot be read does not lose it.
	_ = os.Chtimes(path, now, now)
	if _, err := os.Stat(path); err != nil {
		return
	}
	s.lockRecords(usedName)
	defer sourcesMu.Unlock()
	used, ok := s.readTimes(usedName)
	if !ok {
		if s.recordStateOnce(usedName) == recUnreadable {
			// Said once a day. The use is on the baton's file time (above), not in the record.
			if t, ok := touchLogged.Load(s.dir); !ok || now.Sub(t.(time.Time)) > 24*time.Hour || t.(time.Time).After(now) {
				touchLogged.Store(s.dir, now)
				logBaton("baton: the record %s could not be read, so a use of a baton is kept on the file's time only", usedName)
			}
			return // not damaged, not moved aside
		}
		// A damaged record is repaired here and not left to refuse every use: it is kept
		// aside, started again, and pruning is held for a day, since what it said is lost.
		if !s.restartRecord(usedName, now) {
			return // not kept aside: it is not written over
		}
		s.setPruneKeyHeld("hold_until", now.Add(holdAfterRestart).UTC().Format(time.RFC3339))
		used = map[string]string{}
	}
	used[id] = now.UTC().Format(time.RFC3339)
	_ = s.writeJSON(usedName, used)
}

// touchNow is the time a use is recorded at; a test replaces it.
var touchNow = time.Now

// touchLogged is when the unreadable used record was last said for each state folder.
var touchLogged sync.Map

// MarkInUse records that a pane was just started from a baton, for leaseFor.
func (s *Store) MarkInUse(id string) {
	if !ValidID(id) {
		return
	}
	dir := filepath.Join(s.dir, inuseDir)
	path := filepath.Join(dir, id)
	// The folder is never removed (see sweepMarks), but a person or another program
	// may: once more if it went between making it and the file.
	made := false
	var lastErr error
	for try := 0; try < 2 && !made; try++ {
		if lastErr = os.MkdirAll(dir, 0o700); lastErr != nil {
			break
		}
		markBetween()
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0o600)
		if err == nil {
			_ = f.Close()
			made = true
			break
		}
		lastErr = err
	}
	if !made {
		// Said once for each state folder, not once for the process: another store may
		// be the one that is broken.
		if _, said := markFailedDirs.LoadOrStore(s.dir, true); !said {
			logBaton("baton: could not mark a baton as just started from, so pruning may not see it as in use: %v", lastErr)
		}
		return
	}
	now := time.Now()
	_ = os.Chtimes(path, now, now)
}

// markBetween runs between making the marks folder and the mark in it; a test removes the
// folder there. markFailed makes the failure to mark be said once.
var (
	markBetween = func() {}
	// markFailedDirs holds the state folders whose failure to mark was said.
	markFailedDirs sync.Map
)

// recordRetryWait is how long to wait when a record cannot be read or parsed: another
// process may be in the middle of writing it in place. recordSleep is the sleep; a test
// replaces it.
var (
	recordRetryWait = 50 * time.Millisecond
	recordSleep     = time.Sleep
)

// recordNames are the records of a store that are kept as JSON files.
var recordNames = []string{usedName, overflowsName, pruneName, retriesName, sourcesName}

// damagedSeen remembers, for ten minutes, a record that was found unreadable after the wait
// of settle, so that one that stays damaged costs the wait once and not on every call.
var damagedSeen sync.Map

// settle waits once, for recordRetryWait, if one of the records named cannot be read or
// parsed right now (and was not found so within the last ten minutes). It is called before
// sourcesMu is taken and never with it held, so that the wait does not keep another goroutine
// from the records. What reads a record after it takes the lock then sees the whole file in
// the usual case, and calls it damaged only if it still is not. Only the records the caller
// is about to read are looked at.
func (s *Store) settle(names []string) {
	for _, n := range names {
		p := filepath.Join(s.dir, n)
		if s.recordOK(n) {
			damagedSeen.Delete(p)
			continue
		}
		if t, ok := damagedSeen.Load(p); ok && time.Since(t.(time.Time)) < 10*time.Minute {
			continue
		}
		damagedSeen.Store(p, time.Now())
		recordSleep(recordRetryWait)
		return
	}
}

// recState is what a look at a record finds.
type recState int

const (
	recOK         recState = iota // not there, or whole
	recDamaged                    // there, and not a record of the right shape, with no complete copy beside it
	recUnreadable                 // there, and could not be read (a lock, a refusal): not known to be damaged
)

// recordShape reports whether data is a record of the kind name is: an object of the right
// types, and for the records of times (used and prune) times as RFC 3339 text.
func recordShape(name string, data []byte) bool {
	switch name {
	case overflowsName:
		var m map[string][]string
		return json.Unmarshal(data, &m) == nil
	case retriesName:
		var m map[string]int
		return json.Unmarshal(data, &m) == nil
	}
	var m map[string]string
	if json.Unmarshal(data, &m) != nil {
		return false
	}
	if name == usedName || name == pruneName {
		for _, v := range m {
			if _, err := time.Parse(time.RFC3339, v); err != nil {
				return false
			}
		}
	}
	return true
}

// recordStateOnce looks at a record once: whole (or not there, or cut short with a complete
// copy of it beside it: see tempCopy), damaged, or not readable at the moment.
func (s *Store) recordStateOnce(name string) recState {
	data, err := readRecordFile(filepath.Join(s.dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return recOK
	}
	if err != nil {
		return recUnreadable
	}
	if recordShape(name, data) {
		return recOK
	}
	if _, ok := s.tempCopy(name); ok {
		return recOK
	}
	return recDamaged
}

// recordState is recordStateOnce, with a read that fails (as opposed to one that finds
// something wrong) tried again up to three times, a moment apart, before it is called
// unreadable. It must not be called with sourcesMu held.
func (s *Store) recordState(name string) recState {
	st := s.recordStateOnce(name)
	for try := 0; try < 2 && st == recUnreadable; try++ {
		recordSleep(recordRetryWait)
		st = s.recordStateOnce(name)
	}
	return st
}

// recordOK reports whether a record is not there, or can be read (itself or, when it is
// empty or cut short, the newest complete copy of it beside it: see tempCopy).
func (s *Store) recordOK(name string) bool { return s.recordStateOnce(name) == recOK }

// tempCopy is the newest complete copy of a record that a write left beside it (see
// writeJSON), if there is one.
func (s *Store) tempCopy(name string) ([]byte, bool) {
	matches, _ := filepath.Glob(filepath.Join(s.dir, ".record-"+name+"-*.tmp"))
	var best []byte
	var bestTime time.Time
	for _, m := range matches {
		fi, err := os.Stat(m)
		if err != nil || !fi.Mode().IsRegular() || fi.Size() > 8<<20 {
			continue
		}
		data, err := os.ReadFile(m)
		if err != nil || !recordShape(name, data) {
			continue
		}
		if best == nil || fi.ModTime().After(bestTime) {
			best, bestTime = data, fi.ModTime()
		}
	}
	return best, best != nil
}

// lockRecords takes sourcesMu, after giving a record that is being written a moment. The
// names are the records the caller is about to read.
func (s *Store) lockRecords(names ...string) {
	s.settle(names)
	sourcesMu.Lock()
}

// readRecord reads a JSON record into v. missing says there is no such file; damaged says
// it is there and is not readable as v. It does not wait: see settle.
func readRecord(path string, v any) (missing, damaged bool) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return true, false
	}
	if err == nil && json.Unmarshal(data, v) == nil {
		return false, false
	}
	// Empty or cut short: the newest complete copy a write left beside it, if there is one.
	dir, base := filepath.Dir(path), filepath.Base(path)
	if copyData, ok := (&Store{dir: dir}).tempCopy(base); ok && json.Unmarshal(copyData, v) == nil {
		return false, false
	}
	return false, true
}

// leased reports whether a started-from mark for the baton is still fresh.
func (s *Store) leased(id string, now time.Time) bool {
	fi, err := os.Stat(filepath.Join(s.dir, inuseDir, id))
	return err == nil && now.Sub(fi.ModTime()) < leaseFor
}

// sweepMarks removes the started-from marks that no longer protect anything: older
// than leaseFor, or for a baton that is not stored any more.
func (s *Store) sweepMarks(now time.Time) {
	dir := filepath.Join(s.dir, inuseDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		fi, err := e.Info()
		if err != nil || e.IsDir() {
			continue
		}
		_, stored := statRegular(s.Path(e.Name()))
		if !stored || now.Sub(fi.ModTime()) >= leaseFor {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// idTime is the time a baton's id was made from: when it was created.
func idTime(id string) time.Time {
	if len(id) < 15 {
		return time.Time{}
	}
	t, err := time.Parse("20060102-150405", id[:15])
	if err != nil {
		return time.Time{}
	}
	return t
}

// idsRe finds the ids a layout file names.
var idsRe = regexp.MustCompile(`[0-9]{8}-[0-9]{6}-[0-9a-f]{6}`)

// scanIDs adds every baton id in r to refs, reading it in pieces, so a layout of
// any size is searched and none is skipped. The end of one piece is kept in front
// of the next so an id cut by the boundary is still found.
func scanIDs(r io.Reader, refs map[string]bool) {
	const chunk = 1 << 20
	const keep = 32 // longer than an id
	buf := make([]byte, 0, chunk+keep)
	tmp := make([]byte, chunk)
	for {
		n, err := r.Read(tmp)
		buf = append(buf, tmp[:n]...)
		for _, id := range idsRe.FindAllString(string(buf), -1) {
			refs[id] = true
		}
		if len(buf) > keep {
			buf = append(buf[:0], buf[len(buf)-keep:]...)
		}
		if err != nil {
			return
		}
	}
}

// layoutRefs is every baton id named by a saved layout in the state folder, which
// is where a pane's baton is kept between runs: all projects, the ones that are
// closed or that cannot be reached now, and damaged layouts that were kept. A
// layout is searched for the ids as text, so the format it is written in does not
// matter.
//
// The folder not being listed is an error, and so is a layout that cannot be opened:
// the second is reported as the files, so that the caller can decide what to do about
// them, with whatever the others named already in refs.
func layoutRefs(stateDir string) (refs map[string]bool, unreadable []string, err error) {
	refs = map[string]bool{}
	entries, err := readStateDir(stateDir)
	if err != nil {
		return refs, nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "layout-") {
			continue
		}
		path := filepath.Join(stateDir, e.Name())
		f, err := openLayout(path)
		if err != nil {
			unreadable = append(unreadable, path)
			continue
		}
		scanIDs(f, refs)
		_ = f.Close()
	}
	return refs, unreadable, nil
}

// readStateDir lists the state folder; a test replaces it.
var readStateDir = os.ReadDir

// unreadableFor is how long a layout that cannot be read stops pruning, from the first
// time it was seen unreadable. After it pruning goes on, protecting what the readable
// layouts name and what it can find in the unreadable one.
const unreadableFor = 30 * 24 * time.Hour

// pruneKey reads one value of the prune record; setPruneKey writes one, leaving the others.
func (s *Store) pruneKey(key string) string {
	s.lockRecords(pruneName)
	defer sourcesMu.Unlock()
	m, _ := s.readTimes(pruneName)
	return m[key]
}

func (s *Store) setPruneKey(key, val string) {
	s.lockRecords(pruneName)
	defer sourcesMu.Unlock()
	s.setPruneKeyHeld(key, val)
}

// setPruneKeyHeld is setPruneKey with sourcesMu held.
func (s *Store) setPruneKeyHeld(key, val string) {
	// A damaged record is started again: it only holds times.
	m, _ := s.readTimes(pruneName)
	if val == "" {
		delete(m, key)
	} else {
		m[key] = val
	}
	if key == "hold_until" && val != "" {
		holdMemory.Store(s.dir, val)
	}
	_ = s.writeJSON(pruneName, m)
}

// holdMemory is the hold on pruning that was last set for each state folder, so that a prune
// record that is lost does not lose it.
var holdMemory sync.Map

// layoutsOK settles what to do about the layouts: it adds to refs what an unreadable one
// holds as far as it can be read, and says whether pruning may go on. A folder that cannot
// be listed, or a layout that cannot be read for less than unreadableFor, stops this run (said
// once a day); one that has stayed unreadable for longer does not, so a single bad file
// cannot stop pruning for ever.
func (s *Store) layoutsOK(refs map[string]bool, unreadable []string, listErr error, now time.Time) error {
	if listErr != nil {
		s.logDaily("readdir_logged", now, "baton: the state folder could not be listed, so no baton was removed: %v", listErr)
		return fmt.Errorf("the state folder could not be listed, so no baton was removed: %w", listErr)
	}
	if len(unreadable) == 0 {
		s.setPruneKey("unreadable_since", "")
		return nil
	}
	since, _ := time.Parse(time.RFC3339, s.pruneKey("unreadable_since"))
	if since.IsZero() || since.After(now) {
		since = now
		s.setPruneKey("unreadable_since", now.UTC().Format(time.RFC3339))
	}
	if now.Sub(since) < unreadableFor {
		logBaton("baton: a saved layout could not be read, so no baton was removed (pruning goes on without it after %d days)", int(unreadableFor/(24*time.Hour)))
		return errors.New("a saved layout could not be read, so no baton was removed")
	}
	// Long enough: pruning goes on. Each unreadable file is tried by another route (shared
	// access on Windows, which allows what the first open did not), and what it names is
	// protected if that reads it. If it cannot be read that way either, the batons it
	// names are not protected: they are prunable like any other (half of them a run, the
	// oldest first), which is what 30 days of waiting is for.
	for _, p := range unreadable {
		if f, err := openShared(p); err == nil {
			scanIDs(f, refs)
			_ = f.Close()
		}
	}
	s.logDaily("unreadable_logged", now, "baton: %d saved layouts have been unreadable for over %d days; pruning goes on, and a baton only they name can now be removed", len(unreadable), int(unreadableFor/(24*time.Hour)))
	return nil
}

// logDaily says something at most once a day, remembering when in the prune record.
func (s *Store) logDaily(key string, now time.Time, format string, args ...any) {
	if t, _ := time.Parse(time.RFC3339, s.pruneKey(key)); t.IsZero() || now.Sub(t) > 24*time.Hour || t.After(now) {
		logBaton(format, args...)
		s.setPruneKey(key, now.UTC().Format(time.RFC3339))
	}
}

// openLayout opens a saved layout to be searched; a test replaces it.
var openLayout = func(path string) (io.ReadCloser, error) { return os.Open(path) }

// NoteOverflow records an overflow file written for a baton, for Prune to remove.
// A record that is there and cannot be read is kept beside under a name of its own
// and a new one is started, so that one bad file does not stop every later one
// being noted.
func (s *Store) NoteOverflow(id, path string) error {
	if !ValidID(id) || path == "" {
		return errors.New("not a baton id or an empty path")
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	s.lockRecords(overflowsName)
	defer sourcesMu.Unlock()
	m, ok := s.readOverflows()
	if !ok {
		raw, err := os.ReadFile(filepath.Join(s.dir, overflowsName))
		if err != nil {
			return fmt.Errorf("the overflow record cannot be read, so it is left as it is: %w", err)
		}
		if err := keepBad(filepath.Join(s.dir, overflowsName), raw); err != nil {
			return fmt.Errorf("the overflow record is damaged and a copy could not be kept: %w", err)
		}
		m = map[string][]string{}
	}
	for _, p := range m[id] {
		if p == path {
			return nil
		}
	}
	m[id] = append(m[id], path)
	return s.writeJSON(overflowsName, m)
}

// readOverflows reads the overflow record; ok is false for a file that is there
// and cannot be read as one.
func (s *Store) readOverflows() (map[string][]string, bool) {
	m := map[string][]string{}
	if _, damaged := readRecord(filepath.Join(s.dir, overflowsName), &m); damaged {
		return map[string][]string{}, false
	}
	if m == nil {
		m = map[string][]string{}
	}
	return m, true
}

// readTimes reads a record of id to RFC 3339 time.
func (s *Store) readTimes(name string) (map[string]string, bool) {
	m := map[string]string{}
	if _, damaged := readRecord(filepath.Join(s.dir, name), &m); damaged {
		return map[string]string{}, false
	}
	if m == nil {
		m = map[string]string{}
	}
	// A record of times with a value that is not a time is not a record of times.
	if name == usedName || name == pruneName {
		for _, v := range m {
			if _, err := time.Parse(time.RFC3339, v); err != nil {
				return map[string]string{}, false
			}
		}
	}
	return m, true
}

// readRetries reads the failure counts of overflow files.
func (s *Store) readRetries() map[string]int {
	m := map[string]int{}
	if _, damaged := readRecord(filepath.Join(s.dir, retriesName), &m); damaged {
		m = map[string]int{}
	}
	if m == nil {
		m = map[string]int{}
	}
	return m
}

// writeJSON replaces a record in the store folder with v, or removes it when v is
// an empty map. It writes a temporary file and renames it over the record, so a
// reader sees the old one or the new one whole. The caller holds sourcesMu, which
// orders this process's writes; two Flockdeck processes writing the same record in
// the same instant keep the later one, and the records are rebuilt from what they
// are about the next time that is used (a used time is also read from the file's
// own, an overflow path that was missed is a file left behind).
func (s *Store) writeJSON(name string, v any) error {
	path := filepath.Join(s.dir, name)
	empty := false
	switch m := v.(type) {
	case map[string]string:
		empty = len(m) == 0
	case map[string][]string:
		empty = len(m) == 0
	case map[string]int:
		empty = len(m) == 0
	}
	if empty {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".record-"+name+"-*.tmp")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
		return werr
	}
	// On Windows a rename onto a file another process has open is refused for as long as
	// it is open: tried again a few times, and then the record is written in place, which
	// the open file allows, rather than not at all.
	var rerr error
	for try := 0; try < 5; try++ {
		if rerr = renameFile(tmp.Name(), path); rerr == nil {
			return nil
		}
		time.Sleep(time.Duration(20+try*8) * time.Millisecond)
	}
	// Written in place, once, while the complete copy is still beside it: the copy goes
	// only when the write has been made, so a write that is cut short leaves the whole
	// record in a file the sweep of temporary files removes later, not nothing.
	previous, _ := os.ReadFile(path)
	for try := 0; try < 3; try++ {
		if writeInPlace(path, data, 0o600) == nil {
			_ = os.Remove(tmp.Name())
			return nil
		}
		time.Sleep(time.Duration(20*(try+1)) * time.Millisecond)
	}
	// A write that failed may have cut the record short. What was there is put back at
	// once, so that the record is whole and the error is the only harm; if that cannot be
	// done either, the complete new copy stays beside it.
	switch {
	case len(previous) > 0 && writeInPlace(path, previous, 0o600) == nil:
		_ = os.Remove(tmp.Name())
	case len(previous) == 0:
		// There was nothing before: a cut-short file is not left in its place.
		if os.Remove(path) == nil {
			_ = os.Remove(tmp.Name())
		}
	}
	return rerr
}

// writeInPlace is os.WriteFile; a test replaces it to make a write fail part of the way.
var writeInPlace = os.WriteFile

// renameFile is os.Rename; a test replaces it to make renaming fail.
var renameFile = os.Rename

// writeSources replaces the sources file with m, or removes it when m is empty.
func (s *Store) writeSources(m map[string]string) error { return s.writeJSON(sourcesName, m) }

// clockOK says whether now can be trusted for pruning: the wall clock was not set
// while this process ran, it is not earlier than the latest time pruning has seen
// (a time that is a day ahead of now is a record gone wrong, and is ignored), and it
// is not more than a year past the newest file in the state folder, which a running
// Flockdeck writes every few seconds. A clock that is far wrong is not written down:
// nothing here records now until the run has found its ages sane (see noteSeen).
func (s *Store) clockOK(now time.Time) (skip bool, err error) {
	if defaultWatch.jumped() {
		return true, ErrClock
	}
	s.lockRecords(pruneName)
	defer sourcesMu.Unlock()
	rec, ok := s.readTimes(pruneName)
	if !ok {
		// Damaged, or empty: a record that says only when pruning last ran, so it is
		// started again rather than stopping pruning for good. The old one is kept as
		// prune.json.bad, the new one says now, and pruning goes on from the next run: this
		// one has nothing to compare the clock with.
		if newest := s.newestInState(); !newest.IsZero() && now.Sub(newest) > 366*24*time.Hour {
			return true, ErrClock
		}
		s.restartPruneRecord(now)
		return true, nil
	}
	seen, _ := time.Parse(time.RFC3339, rec["seen"])
	if seen.After(now.Add(24 * time.Hour)) {
		seen = time.Time{} // not believed: it would stop pruning for ever
	}
	if !seen.IsZero() && now.Before(seen.Add(-time.Hour)) {
		return true, ErrClock
	}
	if newest := s.newestInState(); !newest.IsZero() && now.Sub(newest) > 366*24*time.Hour {
		return true, ErrClock
	}
	return false, nil
}

// unreadableKey is the key in the prune record for the time a record was first seen unreadable.
func unreadableKey(name string) string { return "unreadable_since_" + name }

// unreadableLongEnough notes the time each of the records first seen unreadable and says
// whether all of them have been so for unreadableFor.
func (s *Store) unreadableLongEnough(names []string, now time.Time) bool {
	long := true
	for _, n := range names {
		since, _ := time.Parse(time.RFC3339, s.pruneKey(unreadableKey(n)))
		if since.IsZero() || since.After(now) {
			since = now
			s.setPruneKey(unreadableKey(n), now.UTC().Format(time.RFC3339))
		}
		if now.Sub(since) < unreadableFor {
			long = false
		}
	}
	if !long {
		s.logDaily("unreadable_record_logged", now, "baton: a record could not be read, so no baton was removed (pruning goes on without it after %d days)", int(unreadableFor/(24*time.Hour)))
	} else {
		s.logDaily("unreadable_record_logged", now, "baton: a record has been unreadable for over %d days; pruning goes on with it taken as empty", int(unreadableFor/(24*time.Hour)))
	}
	return long
}

// clearUnreadable forgets the records that were seen unreadable and are not now: all but
// those named.
func (s *Store) clearUnreadable(still []string) {
	for _, n := range recordNames {
		if n != pruneName && !slices.Contains(still, n) && s.pruneKey(unreadableKey(n)) != "" {
			s.setPruneKey(unreadableKey(n), "")
		}
	}
}

// holdAfterRestart is how long nothing is pruned after a record that said what was used
// was lost and started again.
const holdAfterRestart = 24 * time.Hour

// errKeptCap says that as many copies are kept aside as are kept.
var errKeptCap = errors.New("100 copies of this record are already kept aside")

// capLogged remembers the state folders whose cap was said.
var capLogged sync.Map

// keepAside moves a record that cannot be read to a name of its own: name.bad, and if that
// is taken name.bad.1 and so on up to .bad.99. None is written over; with all of them taken
// it returns errKeptCap and the record is not moved.
func (s *Store) keepAside(name string) error {
	path := filepath.Join(s.dir, name)
	target := path + ".bad"
	for i := 1; ; i++ {
		if _, err := os.Lstat(target); err != nil {
			break
		}
		if i >= 100 {
			return errKeptCap
		}
		target = fmt.Sprintf("%s.bad.%d", path, i)
	}
	return renameFile(path, target)
}

// restartRecord keeps a record that cannot be read aside and starts it again empty, and
// reports whether it did. When 100 copies are kept already no more are, and it is said once;
// the record is removed so that it can start again. When it cannot be moved it is left, and
// not written over. The complete copies a write left beside it go too: they are older than
// the record that starts now. The caller holds sourcesMu.
func (s *Store) restartRecord(name string, now time.Time) bool {
	err := s.keepAside(name)
	switch {
	case errors.Is(err, errKeptCap):
		if _, said := capLogged.LoadOrStore(s.dir, true); !said {
			logBaton("baton: %s copies of unreadable records are kept aside and no more are kept", "100")
		}
		if rerr := os.Remove(filepath.Join(s.dir, name)); rerr != nil {
			logBaton("baton: the record %s could not be read and could not be removed: %v", name, rerr)
			return false
		}
	case err != nil:
		logBaton("baton: the record %s could not be read and could not be kept aside: %v", name, err)
		return false
	default:
		logBaton("baton: the record %s could not be read; it was kept aside as %s.bad and started again", name, name)
	}
	if copies, _ := filepath.Glob(filepath.Join(s.dir, ".record-"+name+"-*.tmp")); len(copies) > 0 {
		for _, c := range copies {
			_ = os.Remove(c)
		}
	}
	return true
}

// holdFrom is the hold on pruning that a prune record says, from its bytes if they can be
// read, else from what was set last in this process: a record that is lost does not lose it.
func (s *Store) holdFrom() string {
	var m map[string]string
	if data, err := os.ReadFile(filepath.Join(s.dir, pruneName)); err == nil && json.Unmarshal(data, &m) == nil && m["hold_until"] != "" {
		return m["hold_until"]
	}
	if v, ok := holdMemory.Load(s.dir); ok {
		return v.(string)
	}
	return ""
}

// restartPruneRecord keeps a prune record that cannot be read aside and writes a new one
// that says now, with the hold on pruning it had. The caller holds sourcesMu.
func (s *Store) restartPruneRecord(now time.Time) {
	hold := s.holdFrom()
	if !s.restartRecord(pruneName, now) {
		return
	}
	rec := map[string]string{"seen": now.UTC().Format(time.RFC3339)}
	if t, _ := time.Parse(time.RFC3339, hold); !t.IsZero() && t.After(now) {
		rec["hold_until"] = hold
		holdMemory.Store(s.dir, hold)
	}
	if err := s.writeJSON(pruneName, rec); err != nil {
		logBaton("baton: the prune record could not be started again: %v", err)
		return
	}
	logBaton("baton: pruning goes on from the next run")
}

// damagedRecords are the records (not the prune record, which clockOK deals with) that are
// there and damaged, and whether any could not be read at all (a lock, a refusal): that is
// not damage, and nothing is moved for it. It must not be called with sourcesMu held.
func (s *Store) damagedRecords() (damaged, unreadable []string) {
	for _, n := range recordNames {
		if n == pruneName {
			continue
		}
		switch s.recordState(n) {
		case recDamaged:
			damaged = append(damaged, n)
		case recUnreadable:
			unreadable = append(unreadable, n)
		}
	}
	return damaged, unreadable
}

// noteSeen records that pruning ran at now.
func (s *Store) noteSeen(now time.Time) { s.setPruneKey("seen", now.UTC().Format(time.RFC3339)) }

// newestInState is the latest modified time of a file in the state folder or the
// baton folder (layouts, sessions, preferences and the batons), or zero.
func (s *Store) newestInState() time.Time {
	var newest time.Time
	for _, dir := range []string{filepath.Dir(s.dir), s.dir} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if fi, err := e.Info(); err == nil && fi.ModTime().After(newest) {
				newest = fi.ModTime()
			}
		}
	}
	return newest
}

// sweepTemps removes temporary record files a write left behind (a process that was
// stopped between writing and renaming): older than an hour.
func (s *Store) sweepTemps(now time.Time) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".tmp") || !(strings.HasPrefix(n, ".record-") || strings.HasPrefix(n, ".sources-") || strings.HasPrefix(n, ".overflow-")) {
			continue
		}
		if fi, err := e.Info(); err == nil && now.Sub(fi.ModTime()) > staleTemp {
			if s.isNeededCopy(n) {
				continue
			}
			_ = os.Remove(filepath.Join(s.dir, n))
		}
	}
}

// Prune removes the batons last saved or used before now-maxAge, with their
// sources record and the overflow files written for them, and returns the ids it
// removed. A baton is last used at the latest of its file's modified time, the time
// its id was made from and the time recorded when it was used, so one restored from
// a backup with an old modified time is not taken for old.
//
// It never removes a baton that inUse reports (the ones open panes were started
// from), that a saved layout names (any project, open or not), or that a pane was
// started from within the hour, which covers another Flockdeck running on this
// machine. It removes nothing when the clock is earlier than when it last ran, was
// set while Flockdeck ran, or a baton is dated in the future, and at most half the
// batons in one run, the oldest first, so a machine that was idle for months prunes
// over several saves. The overflow files are removed afterwards in the background,
// one cleanup at a time (see cleanOverflow), so a share that does not answer cannot
// hold up a save. An overflow file is removed only when it is a regular file named
// baton-<id>.md in a folder named flockdeck, for the id it was recorded under.
func (s *Store) Prune(maxAge time.Duration, now time.Time, inUse func(id string) bool) ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if skip, err := s.clockOK(now); skip {
		return nil, err
	}
	// Nothing is pruned for a day after a record of what was used was lost, nor in the run
	// that finds one: its batons may have been used a moment ago and look unused.
	if t, _ := time.Parse(time.RFC3339, s.pruneKey("hold_until")); !t.IsZero() && now.Before(t) && t.Before(now.Add(2*holdAfterRestart)) {
		return nil, nil
	}
	bad, cannotRead := s.damagedRecords()
	if len(cannotRead) > 0 {
		// Not known to be damaged: nothing is moved. Nothing is pruned either, for as long as
		// unreadableFor from the first time it was seen so, said once a day. After that pruning
		// goes on with the record taken as empty (nothing younger than the retention window,
		// named by a layout or in use is removed in any case).
		if !s.unreadableLongEnough(cannotRead, now) {
			return nil, nil
		}
	}
	// Each record's own time is forgotten as soon as that record reads fine, whatever the others do.
	s.clearUnreadable(cannotRead)
	if len(bad) > 0 {
		s.lockRecords()
		restarted := 0
		for _, n := range bad {
			// Looked at again with the records locked: another process may have written a
			// whole one since.
			if s.recordStateOnce(n) == recDamaged && s.restartRecord(n, now) {
				restarted++
			}
		}
		if restarted > 0 {
			s.setPruneKeyHeld("hold_until", now.Add(holdAfterRestart).UTC().Format(time.RFC3339))
		}
		sourcesMu.Unlock()
		return nil, nil
	}
	s.sweepMarks(now)
	s.sweepTemps(now)
	cutoff := now.Add(-maxAge)
	s.lockRecords(usedName)
	used, _ := s.readTimes(usedName)
	sourcesMu.Unlock()
	refs, unreadable, lerr := layoutRefs(filepath.Dir(s.dir))
	if err := s.layoutsOK(refs, unreadable, lerr, now); err != nil {
		return nil, err
	}
	unreferenced, absurdCount := 0, 0

	type candidate struct {
		id   string
		last time.Time
	}
	var cands []candidate
	all := 0
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".md")
		if !ok || !ValidID(id) || e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		all++
		if info.ModTime().After(now.Add(24 * time.Hour)) {
			return nil, ErrClock
		}
		last := info.ModTime()
		if t := idTime(id); t.After(last) && !t.After(now) {
			last = t
		}
		if t, err := time.Parse(time.RFC3339, used[id]); err == nil && t.After(last) {
			last = t
		}
		// One a saved layout names is wanted, however old it looks, and does not say
		// anything about the clock.
		if refs[id] {
			continue
		}
		unreferenced++
		// A baton last used longer ago than anything can have been: the clock may be
		// ahead. It is skipped, one by one.
		if now.Sub(last) > absurdAge {
			absurdCount++
			continue
		}
		if last.After(cutoff) || s.leased(id, now) || inUse != nil && inUse(id) {
			continue
		}
		cands = append(cands, candidate{id, last})
	}
	// A baton that looks that old is never removed (that is the safe side), and is said so
	// once a day. When most of them look it the clock is the likelier fault, and is not
	// written down; nothing is returned as an error for it, since nothing that old is
	// removed in any case.
	if absurdCount > 0 {
		s.logDaily("absurd_logged", now, "baton: %d batons look more than five years old and were left alone", absurdCount)
	}
	if unreferenced == 0 || absurdCount*2 < unreferenced {
		s.noteSeen(now)
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].last.Before(cands[j].last) })
	// At most half of them in one run, the oldest first; with fewer than four, one.
	limit := 1
	if all >= 4 {
		limit = all / 2
	}
	if len(cands) > limit {
		cands = cands[:limit]
	}

	var removed []string
	var firstErr error
	old := map[string]bool{}
	for _, c := range cands {
		if err := os.Remove(s.Path(c.id)); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		removed = append(removed, c.id)
		old[c.id] = true
	}

	s.lockRecords(sourcesName, usedName)
	if len(removed) > 0 {
		if m := s.readSources(); len(m) > 0 {
			changed := false
			for _, id := range removed {
				if _, ok := m[id]; ok {
					delete(m, id)
					changed = true
				}
			}
			if changed {
				if err := s.writeSources(m); err != nil && firstErr == nil {
					firstErr = err
				}
			}
		}
		if u, ok := s.readTimes(usedName); ok && len(u) > 0 {
			for _, id := range removed {
				delete(u, id)
			}
			_ = s.writeJSON(usedName, u)
		}
	}
	sourcesMu.Unlock()

	s.cleanOverflowSoon(old, refs, cutoff, now, inUse)
	return removed, firstErr
}

// cleaning is set while an overflow cleanup runs, so that there is never more
// than one, and cleanupDone lets a test wait for it.
var (
	cleaning    atomic.Bool
	cleanupDone sync.WaitGroup
	// inflight are the overflow files a removal is still running for, so a path on
	// a share that does not answer is not asked about again while it is stuck.
	inflightMu sync.Mutex
	inflight   = map[string]bool{}
)

// WaitOverflowCleanup waits for an overflow cleanup that was started, for a test.
func WaitOverflowCleanup() { cleanupDone.Wait() }

// cleanOverflowSoon starts the removal of the overflow files whose batons were
// just removed, and of any that are old or orphaned, in a goroutine of its own.
// At most one runs: a save that finds one running starts none, and the next
// finds the work still there.
func (s *Store) cleanOverflowSoon(old, refs map[string]bool, cutoff, now time.Time, inUse func(string) bool) {
	if !cleaning.CompareAndSwap(false, true) {
		return
	}
	cleanupDone.Add(1)
	go func() {
		defer cleanupDone.Done()
		defer cleaning.Store(false)
		s.cleanOverflow(old, refs, cutoff, now, inUse)
	}()
}

// cleanOverflow removes overflow files: those of the removed batons, and old ones
// whose baton is gone. A file that cannot be removed is kept in the record with a
// count of the failures, and forgotten, with a line in the log, after
// maxOverflowFailures. The records lock is held only to read and to write the
// records, never while a file is looked at.
func (s *Store) cleanOverflow(old, refs map[string]bool, cutoff, now time.Time, inUse func(string) bool) {
	s.lockRecords(overflowsName, retriesName)
	overflows, ok := s.readOverflows()
	retries := s.readRetries()
	sourcesMu.Unlock()
	if !ok {
		logBaton("baton: the overflow record cannot be read, so its files were left")
		return
	}
	gone := map[string]map[string]bool{} // the paths that were removed or given up, by id
	forget := func(id, p string) {
		if gone[id] == nil {
			gone[id] = map[string]bool{}
		}
		gone[id][p] = true
	}
	for id, paths := range overflows {
		_, stored := statRegular(s.Path(id))
		if !old[id] && (stored || refs[id] || s.leased(id, now) || inUse != nil && inUse(id)) {
			continue
		}
		for _, p := range paths {
			if !old[id] && !overflowOld(p, cutoff) {
				continue
			}
			if overflowRemove(p, id, removeWithin) {
				delete(retries, p)
				forget(id, p)
				continue
			}
			retries[p]++
			if retries[p] >= maxOverflowFailures {
				logBaton("baton: giving up on the overflow file %s after %d tries; it is left where it is", p, retries[p])
				delete(retries, p)
				forget(id, p)
			}
		}
	}
	s.lockRecords(overflowsName)
	defer sourcesMu.Unlock()
	// The record is read again, and only what was dealt with is taken out of it: a path
	// noted while the files were being looked at stays.
	if fresh, ok := s.readOverflows(); ok && len(gone) > 0 {
		for id, set := range gone {
			var keep []string
			for _, p := range fresh[id] {
				if !set[p] {
					keep = append(keep, p)
				}
			}
			if len(keep) == 0 {
				delete(fresh, id)
			} else {
				fresh[id] = keep
			}
		}
		if err := s.writeJSON(overflowsName, fresh); err != nil {
			logBaton("baton: the overflow record could not be written: %v", err)
		}
	}
	_ = s.writeJSON(retriesName, retries)
}

// overflowRemove is removeOverflow; a test replaces it to look at what is held
// while it runs, or to make it not answer.
var overflowRemove = removeOverflow

// overflowOld reports whether an overflow file is older than the cutoff, or is
// not there.
func overflowOld(path string, cutoff time.Time) bool {
	fi, err := os.Lstat(path)
	return err != nil || fi.ModTime().Before(cutoff)
}

// removeOverflow removes an overflow file that was recorded for a baton, under a
// deadline: it reports true when the file is gone or was never there, and false
// when it was left (a removal that failed, or no answer in time). A path whose
// removal is already running, stuck, is not asked about again.
func removeOverflow(path, id string, within time.Duration) bool {
	if !overflowName(path, id) {
		return true // not ours to remove: forgotten, not touched
	}
	inflightMu.Lock()
	if inflight[path] {
		inflightMu.Unlock()
		return false
	}
	inflight[path] = true
	inflightMu.Unlock()
	done := make(chan bool, 1)
	go func() {
		defer func() {
			inflightMu.Lock()
			delete(inflight, path)
			inflightMu.Unlock()
		}()
		fi, err := os.Lstat(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			done <- true
		case err != nil || !fi.Mode().IsRegular():
			done <- true
		default:
			done <- os.Remove(path) == nil
		}
	}()
	select {
	case ok := <-done:
		return ok
	case <-time.After(within):
		return false
	}
}

func statRegular(path string) (os.FileInfo, bool) {
	if path == "" {
		return nil, false
	}
	fi, err := os.Lstat(path)
	return fi, err == nil && fi.Mode().IsRegular()
}

// overflowName reports whether path is the name OverflowPath gives a baton's
// overflow file.
func overflowName(path, id string) bool {
	return filepath.Base(path) == "baton-"+id+".md" && filepath.Base(filepath.Dir(path)) == "flockdeck"
}

// isNeededCopy reports whether a temporary file is the newest complete copy of a record
// whose own file cannot be read: it is all there is of that record, and is not swept.
func (s *Store) isNeededCopy(tmpName string) bool {
	for _, n := range recordNames {
		if !strings.HasPrefix(tmpName, ".record-"+n+"-") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, n))
		if err == nil && recordShape(n, data) || errors.Is(err, fs.ErrNotExist) {
			return false
		}
		best, ok := s.tempCopy(n)
		mine, merr := os.ReadFile(filepath.Join(s.dir, tmpName))
		return ok && merr == nil && string(best) == string(mine)
	}
	return false
}

// readRecordFile is os.ReadFile; a test replaces it to make a read fail or change.
var readRecordFile = os.ReadFile
