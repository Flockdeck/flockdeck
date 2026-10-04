package record

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
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
	metaSet bool
	// Events counts the events written, of every kind.
	Events int
}

// Sync writes what the follower has read since it was last asked to the pane's
// transcript. It is the one way a transcript is made: a recording calls it as
// its conversation goes on, an export once it is over, and the two write the
// same lines, redaction, clipping and withholding of secret files included,
// because nothing about how it was called reaches what it writes.
//
// It returns transcript.ErrNoTranscript while the conversation is not stored.
//
// meta says whose lines they are, and is asked for when an event is about to be
// written to a file that is not open yet, so that what the conversation has said
// of itself by then (the directory it is in) is what the file has.
func Sync(m *Manager, meta func() Meta, f transcript.Follower) (SyncResult, error) {
	var res SyncResult
	var mt Meta
	stats, err := f.Poll(func(ev transcript.ExportEvent) error {
		if res.First.IsZero() {
			res.First = ev.Time
		}
		if !res.metaSet || !m.Active(mt.Conversation) {
			mt, res.metaSet = meta(), true
		}
		res.Last = ev.Time
		res.Events++
		switch ev.Kind {
		case transcript.ExportPrompt:
			res.Prompts++
		case transcript.ExportMessage:
			res.Messages++
		case transcript.ExportToolCall:
			res.ToolCalls++
		}
		return m.Write(mt, ev)
	})
	res.Skipped = stats.Skipped
	if errors.Is(err, ErrFull) {
		res.Full, err = true, nil
	}
	return res, err
}

// MetaFor says whose lines a conversation's transcript has. It is the one place
// a transcript's identity comes from -- recording, the window's export and the
// command line's all call it -- and it is worked out from the stored
// conversation alone: the conversation's id (a transcript is of a conversation and not of a pane,
// which may have had several), the agent's id, and the directory the conversation recorded,
// from which the project's name and folder come. Nothing about a pane is in it (a
// pane's name and model change, and a saved layout can have them stale), so the
// same conversation has the same lines whichever way it was reached.
func MetaFor(spec agent.Spec, ex transcript.Exporter, conversation string) Meta {
	cwd := ex.Cwd(spec, conversation)
	project := ""
	if cwd != "" {
		project = filepath.Base(cwd)
	}
	return Meta{Project: project, ProjectRoot: cwd, Agent: spec.ID, Conversation: conversation}
}

// ExportOptions say how a transcript is exported.
type ExportOptions struct {
	// Path is the file to write, which must not exist yet. Empty means a file
	// named for its session in the exports folder of the project's folder under
	// the recordings folder.
	Path string
	// MaxBytes lowers the size the file may reach, for a caller that cannot make
	// a conversation of 16 MiB. Zero is the usual cap.
	MaxBytes int64
}

// ExportResult says what an export wrote.
type ExportResult struct {
	SyncResult
	Path string
	// Lines is the number of lines in the file.
	Lines int
	// Replaced says an earlier export of the conversation was there and has been
	// replaced by this one, which has every event it had.
	Replaced bool
	// Kept says an earlier, finished export of the conversation has an event this
	// one lacks (the stored conversation was cut or changed since), so it was left
	// as it was and Path is that file, not a new one.
	Kept bool
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
	if opts.MaxBytes > 0 {
		m.max = opts.MaxBytes
	}
	defer m.Close()

	sync, err := Sync(m, func() Meta { return meta }, f)
	if err != nil {
		// Nothing half-made is left behind to be mistaken for an export.
		m.Discard(meta)
		return res, err
	}
	if sync.First.IsZero() {
		return res, ErrNothingToExport
	}
	res.SyncResult, res.Path = sync, m.Path(meta.Conversation)
	// A full file ends with the truncation line, which seq does not count; any
	// other ends with the closing line Finish writes.
	res.Lines = int(m.seqOf(meta.Conversation)) + 1
	replaced := m.replacing(meta.Conversation)
	if err := m.Finish(meta); err != nil {
		if errors.Is(err, ErrEarlierKept) {
			// Not a failure: the file there is a fuller transcript than this one.
			res.Kept = true
			return res, nil
		}
		return res, err
	}
	res.Replaced = replaced
	return res, nil
}

// replacing says the pane's open file is being written in place of an earlier,
// finished one.
func (m *Manager) replacing(pane string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.open[pane]
	return s != nil && s.replaces
}

// seqOf is how many lines the pane's open file has.
func (m *Manager) seqOf(pane string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.open[pane]; s != nil {
		return s.seq
	}
	return 0
}

// CheckExportPath refuses a path an export must not be written to: one inside
// the project the conversation belongs to, or inside any git repository, since
// a transcript can hold what a repository should never be given. Symbolic links
// are followed, so a path that gets into the project through one is refused too.
func CheckExportPath(path, projectRoot string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	abs = resolveExisting(abs)
	if projectRoot != "" && within(abs, resolveExisting(projectRoot)) {
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

// resolveExisting is path with the symbolic links in its longest existing
// leading part followed; what does not exist yet is left as it is.
func resolveExisting(path string) string {
	rest := ""
	for p := path; ; {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return path
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
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

// FindExport is the newest export of the conversation meta names, in its
// project's exports folder, or "" if there is none. It looks only at what this
// package wrote there: regular files named for the conversation.
func FindExport(dir func() (string, error), meta Meta) (string, error) {
	root, err := Root(dir)
	if err != nil {
		return "", err
	}
	folder := filepath.Join(root, Folder(meta.Project, meta.ProjectRoot), exportsDir)
	ents, err := os.ReadDir(folder)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	suffix := "-" + shortConversation(meta) + sessionFileExt
	best, bestMod := "", time.Time{}
	for _, e := range ents {
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), suffix) {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		if best == "" || fi.ModTime().After(bestMod) {
			best, bestMod = filepath.Join(folder, e.Name()), fi.ModTime()
		}
	}
	return best, nil
}

// InRecordings checks that path is a regular file inside the recordings folder,
// with symbolic links followed on both, and returns the path with them
// resolved: the path to hand to anything that acts on it, so that a link
// swapped in afterwards is not followed. It is how a file Flockdeck is about to
// show or open is known to be one of its own transcripts.
func InRecordings(dir func() (string, error), path string) (string, error) {
	root, err := Root(dir)
	if err != nil {
		return "", err
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	fi, err := os.Lstat(real)
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", path)
	}
	if !within(real, realRoot) {
		return "", fmt.Errorf("%s is not inside the recordings folder", path)
	}
	return real, nil
}
