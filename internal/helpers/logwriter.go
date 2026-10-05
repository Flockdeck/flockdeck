package helpers

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jmwri/flockdeck/internal/store"
)

// Log files: Flockdeck has no rotation helper elsewhere and this is small
// enough to write here.
const (
	logMax  = 5 << 20
	logKeep = 3
)

// RotatingWriter is an io.Writer onto a log file that is rotated when it
// reaches Max bytes: helper.log becomes helper.log.1, that becomes .2, and so
// on to Keep old files, and the oldest is deleted. It is safe for concurrent
// use.
type RotatingWriter struct {
	Path string
	Max  int64
	Keep int

	mu   sync.Mutex
	f    *os.File
	size int64
}

// NewRotatingWriter is a writer with the default limits: 5 MiB and three old
// files.
func NewRotatingWriter(path string) *RotatingWriter {
	return &RotatingWriter{Path: path, Max: logMax, Keep: logKeep}
}

func (w *RotatingWriter) open() error {
	if err := os.MkdirAll(filepath.Dir(w.Path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(w.Path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	w.f, w.size = f, fi.Size()
	return nil
}

// rotate moves the files up one place. The current file is closed first, since
// Windows will not rename an open one.
func (w *RotatingWriter) rotate() error {
	if w.f != nil {
		w.f.Close()
		w.f = nil
	}
	keep := w.Keep
	if keep < 1 {
		keep = 1
	}
	_ = os.Remove(fmt.Sprintf("%s.%d", w.Path, keep))
	for i := keep - 1; i >= 1; i-- {
		from, to := fmt.Sprintf("%s.%d", w.Path, i), fmt.Sprintf("%s.%d", w.Path, i+1)
		if _, err := os.Stat(from); err == nil {
			if err := store.RenameWithRetry(from, to); err != nil {
				return err
			}
		}
	}
	if err := store.RenameWithRetry(w.Path, w.Path+".1"); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return w.open()
}

// Write appends p, rotating first if it would take the file past Max. A single
// write larger than Max goes into a file of its own rather than being split,
// so a line is never cut in two.
func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		if err := w.open(); err != nil {
			return 0, err
		}
	}
	if w.size > 0 && w.size+int64(len(p)) > w.Max {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

// Close closes the current file. A later Write opens it again.
func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}

// TailLines is the last n non-empty lines of a file, read from its last 64 KiB.
func TailLines(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	const window = 64 << 10
	if fi, err := f.Stat(); err == nil && fi.Size() > window {
		if _, err := f.Seek(fi.Size()-window, io.SeekStart); err != nil {
			return nil
		}
	}
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		if line := strings.TrimRight(sc.Text(), "\r"); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}
