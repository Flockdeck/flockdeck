package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
)

// recentsOut writes the list of recent projects, one write at a time, off the
// goroutine that changed it.
//
// The list is changed in memory, under recentsMu, and the file is written by one
// goroutine of this package afterwards. The workspace's own goroutine records a
// project switch, and the file write is a create, a flush to the device and a
// rename: several milliseconds on Windows, and longer behind a rename that
// another goroutine's write had under the lock. It no longer waits for either.
//
// What is written is the newest state, and writes are made in the order the
// states were made: each change builds on the state before it, which is the
// pending one until it has been written, and the writer takes whatever is
// pending when it gets to it. A burst of changes is one write, and the last of
// them is what the file holds when it ends.
type recentsWriter struct {
	mu   sync.Mutex
	cond *sync.Cond
	// pending is the newest state of each list not yet confirmed on disk,
	// keyed by the file's path. gen counts the states made for a path and
	// written is the newest the writer has settled, one way or the other.
	// results says how each stretch of states was settled, oldest first: a
	// write that succeeded settles every state up to the one it wrote, and
	// one that failed settles every state made so far (see run).
	pending map[string][]Project
	gen     map[string]uint64
	written map[string]uint64
	results map[string][]recentsResult
	// trimmed is the newest state whose result was dropped, per path.
	trimmed map[string]uint64
	running bool
}

// recentsResult is how the states up to and including upTo, back to the last
// result's, were settled.
type recentsResult struct {
	upTo uint64
	err  error
}

// recentsResultsKept bounds the results kept for a path. A caller reads its own
// as soon as it is settled, so only a very late one could find it gone, and
// that one is told so (errRecentsResultLost) and not that its change succeeded.
// A variable so that a test can make it small.
var recentsResultsKept = 4096

// errRecentsResultLost is what a caller is told when how its change was settled
// is no longer kept. Whether it reached the disk is not known from here.
var errRecentsResultLost = errors.New("how the change to the recent projects was written is no longer known")

var recentsOut = newRecentsWriter()

func newRecentsWriter() *recentsWriter {
	w := &recentsWriter{
		pending: map[string][]Project{},
		gen:     map[string]uint64{},
		written: map[string]uint64{},
		results: map[string][]recentsResult{},
		trimmed: map[string]uint64{},
	}
	w.cond = sync.NewCond(&w.mu)
	return w
}

// recentsFileWriter is what the writer writes with, a variable so that a test
// can make the disk slow or fail.
var recentsFileWriter = writeRecentsFile

// pendingFor returns a copy of the state of path not yet on disk, if there is
// one.
func (w *recentsWriter) pendingFor(path string) ([]Project, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	list, ok := w.pending[path]
	if !ok {
		return nil, false
	}
	return append([]Project(nil), list...), true
}

// enqueue makes list the newest state of path and returns its number.
func (w *recentsWriter) enqueue(path string, list []Project) uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending[path] = append([]Project(nil), list...)
	w.gen[path]++
	if !w.running {
		w.running = true
		go w.run()
	}
	return w.gen[path]
}

// wait returns once the state numbered g of path has been settled, with how: the
// result of the first write that includes it, which is the write that put it on
// disk or the one that failed and took it back. It is never another write's.
func (w *recentsWriter) wait(path string, g uint64) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	for w.written[path] < g {
		w.cond.Wait()
	}
	if g <= w.trimmed[path] {
		return errRecentsResultLost
	}
	for _, r := range w.results[path] {
		if r.upTo >= g {
			return r.err
		}
	}
	return errRecentsResultLost
}

// flush returns once nothing is waiting to be written or being written.
func (w *recentsWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for len(w.pending) > 0 {
		w.cond.Wait()
	}
}

// run writes what is pending until nothing is.
//
// A write that succeeds settles the states up to the one it wrote with success,
// and what was made while it wrote is written next, having been built on it.
//
// A write that fails settles every state made so far with its error, those made
// while it wrote included, and drops them: the list is what the file holds
// again. The states made while it wrote were built on the one that failed and
// would put its change on disk if they were written, after its caller had been
// told it had not happened. So what is told to have failed is not on disk, and
// what is told to have succeeded is. A change made after that starts from the
// file. recentsMu is held while this is done, so no change that read the
// failed state is on its way to being made the newest after it.
func (w *recentsWriter) run() {
	for {
		w.mu.Lock()
		var path string
		var list []Project
		var g uint64
		for p, l := range w.pending {
			if w.gen[p] > w.written[p] {
				path, list, g = p, l, w.gen[p]
				break
			}
		}
		if path == "" {
			w.running = false
			w.mu.Unlock()
			return
		}
		w.mu.Unlock()

		err := recentsFileWriter(path, list)

		if err != nil {
			recentsMu.Lock()
		}
		w.mu.Lock()
		upTo := g
		if err != nil {
			upTo = w.gen[path]
			delete(w.pending, path)
		} else if w.gen[path] == g {
			delete(w.pending, path)
		}
		w.written[path] = upTo
		rs := append(w.results[path], recentsResult{upTo: upTo, err: err})
		if len(rs) > recentsResultsKept {
			drop := len(rs) - recentsResultsKept
			w.trimmed[path] = rs[drop-1].upTo
			rs = rs[drop:]
		}
		w.results[path] = rs
		w.cond.Broadcast()
		w.mu.Unlock()
		if err != nil {
			recentsMu.Unlock()
		}
	}
}

// FlushRecents returns once every change made to the list of recent projects has
// been written. A project switch is recorded without waiting for the disk, so a
// run that is ending calls this to leave nothing behind.
func FlushRecents() { recentsOut.flush() }

// writeRecentsFile writes list as the recent projects file at path.
func writeRecentsFile(path string, list []Project) error {
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return fmt.Errorf("encode projects: %w", err)
	}
	// A damaged list that would not move aside is recorded as unread, and this
	// write is what it has to be kept from: every directory the user has
	// opened, replaced by the list read from it, which is nothing.
	if err := keepUnread(path, recentsWhat); err != nil {
		return fmt.Errorf("write projects: %w", err)
	}
	if err := writeAtomic(path, data); err != nil {
		return fmt.Errorf("write projects: %w", err)
	}
	return nil
}

// modifyRecents changes the list of recent projects with change, which is given
// the list as it stands and returns the list to keep and whether it differs.
// The list is read, changed and made the newest state under recentsMu, so two
// changes cannot both start from the same list; the file is written afterwards,
// outside it. With wait the call returns when the change is on disk, and says
// if it could not be; without, once it is made, and a failure to write it is not
// reported to anyone.
func modifyRecents(wait bool, change func(list []Project) ([]Project, bool)) error {
	recentsMu.Lock()
	dir, err := Dir()
	if err != nil {
		recentsMu.Unlock()
		return err
	}
	list, err := Recents()
	if err != nil {
		recentsMu.Unlock()
		return err
	}
	out, changed := change(list)
	if !changed {
		recentsMu.Unlock()
		return nil
	}
	path := filepath.Join(dir, recentsFile)
	g := recentsOut.enqueue(path, out)
	recentsMu.Unlock()
	if !wait {
		return nil
	}
	return recentsOut.wait(path, g)
}
