package store

import (
	"encoding/json"
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
	// keyed by the file's path. gen counts the states made for a path, written
	// the newest the writer has finished with, and result how that write went.
	pending map[string][]Project
	gen     map[string]uint64
	written map[string]uint64
	result  map[string]error
	running bool
}

var recentsOut = newRecentsWriter()

func newRecentsWriter() *recentsWriter {
	w := &recentsWriter{
		pending: map[string][]Project{},
		gen:     map[string]uint64{},
		written: map[string]uint64{},
		result:  map[string]error{},
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

// wait returns once the state numbered g of path, or a later one, has been
// written, with how that write went.
func (w *recentsWriter) wait(path string, g uint64) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	for w.written[path] < g {
		w.cond.Wait()
	}
	return w.result[path]
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

		w.mu.Lock()
		w.written[path] = g
		w.result[path] = err
		// A state made while this was written is written next. One that was
		// not is on disk, or, when the write failed, did not happen: the list
		// goes back to what the file holds, as it did when the failure was
		// returned to the caller and nothing was kept.
		if w.gen[path] == g {
			delete(w.pending, path)
		}
		w.cond.Broadcast()
		w.mu.Unlock()
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
