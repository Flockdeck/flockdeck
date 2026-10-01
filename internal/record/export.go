package record

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jmwri/flockdeck/internal/session/transcript"
)

// ErrNothingToExport is what Export returns for a conversation with nothing
// in it to write. No file is made.
var ErrNothingToExport = errors.New("the conversation has nothing in it to export")

// SyncResult says what one Sync wrote.
type SyncResult struct {
	// Prompts, Messages and ToolCalls count the events written of those kinds.
	Prompts, Messages, ToolCalls int
	// First and Last are the times of the first and last event written.
	First, Last time.Time
	// Full says the file reached MaxFileBytes and the rest of the conversation
	// is left out.
	Full bool
	// Skipped counts entries of the stored conversation that could not be read.
	Skipped int
}

// Sync writes what the follower has read since it was last asked to the pane's
// transcript. It is the one way a transcript is made: a recording calls it as
// its conversation goes on, an export once it is over, and the two write the
// same lines, redaction, clipping and withholding of secret files included,
// because nothing about how it was called reaches what it writes.
//
// It returns transcript.ErrNoTranscript while the conversation is not stored.
func Sync(m *Manager, meta Meta, f transcript.Follower) (SyncResult, error) {
	var res SyncResult
	stats, err := f.Poll(func(ev transcript.ExportEvent) error {
		if res.First.IsZero() {
			res.First = ev.Time
		}
		res.Last = ev.Time
		switch ev.Kind {
		case transcript.ExportPrompt:
			res.Prompts++
		case transcript.ExportMessage:
			res.Messages++
		case transcript.ExportToolCall:
			res.ToolCalls++
		}
		return m.Write(meta, ev)
	})
	res.Skipped = stats.Skipped
	if errors.Is(err, ErrFull) {
		res.Full, err = true, nil
	}
	return res, err
}

// ExportOptions say how a transcript is exported.
type ExportOptions struct {
	// Path is the file to write, which must not exist yet. Empty means a file
	// named for its session in the exports folder of the project's folder under
	// the recordings folder.
	Path string
}

// ExportResult says what an export wrote.
type ExportResult struct {
	SyncResult
	Path string
	// Lines is the number of lines in the file.
	Lines int
}

// Export writes a stored conversation as a transcript, through a Manager of
// its own and Sync, so that it is the transcript a recording of the
// conversation would be.
func Export(dir func() (string, error), meta Meta, f transcript.Follower, opts ExportOptions) (ExportResult, error) {
	var res ExportResult
	if opts.Path != "" {
		if _, err := os.Lstat(opts.Path); err == nil {
			return res, fmt.Errorf("%s already exists; choose a file that does not, so nothing is written over", opts.Path)
		}
	}
	m := NewManager(dir)
	m.export, m.target = true, opts.Path
	defer m.Close()

	sync, err := Sync(m, meta, f)
	if err != nil {
		// Nothing half-made is left behind to be mistaken for an export.
		m.Discard(meta)
		return res, err
	}
	if sync.First.IsZero() {
		return res, ErrNothingToExport
	}
	res.SyncResult, res.Path = sync, m.Path(meta.Pane)
	// A full file ends with the truncation line, which seq does not count; any
	// other ends with the closing line Finish writes.
	res.Lines = int(m.seqOf(meta.Pane)) + 1
	if !sync.Full {
		m.Finish(meta)
	}
	return res, nil
}

// seqOf is how many lines the pane's open file has.
func (m *Manager) seqOf(pane string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.panes[pane]; s != nil {
		return s.seq
	}
	return 0
}

// CheckExportPath refuses a path an export must not be written to: one inside
// the project the conversation belongs to, or inside any git repository, since
// a transcript can hold what a repository should never be given.
func CheckExportPath(path, projectRoot string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if projectRoot != "" && within(abs, projectRoot) {
		return fmt.Errorf("%s is inside the project; a transcript can hold secrets, so it is never written there. Choose a path outside it", path)
	}
	for dir := filepath.Dir(abs); ; dir = filepath.Dir(dir) {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return fmt.Errorf("%s is inside the git repository at %s; a transcript can hold secrets, so it is never written there. Choose a path outside it", path, dir)
		}
		if filepath.Dir(dir) == dir {
			return nil
		}
	}
}

// within reports whether path is dir or below it.
func within(path, dir string) bool {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(abs, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
