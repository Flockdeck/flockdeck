package structured

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jmwri/flockdeck/internal/filerewind"
)

// Putting files back when a pane goes back to an earlier prompt.
//
// When Options.FileRewind is set and the agent can go back, the files of the
// pane's folder are saved as a git checkpoint (internal/filerewind) just before
// each prompt the person typed is sent, and the checkpoint is kept on the prompt's
// mark. A window asks what going back would change (FilePlan), shows the person
// the list, and sends the list back with the rewind (RewindFiles). The files are
// only written when the list still matches the files on disk.
//
// A pane outside a git repository, or one where git is missing or a merge is
// under way, has no checkpoints; FilePoints says why.

var (
	// ErrNoFiles: no checkpoint was kept for that prompt.
	ErrNoFiles = errors.New("structured: no saved files for that prompt")
	// ErrFilesChanged: the files differ from the list the person confirmed.
	ErrFilesChanged = errors.New("structured: the files changed since the list was shown")
)

const (
	// maxCheckpoints is how many prompts keep their files. Older ones lose
	// them, and can still be gone back to without the files.
	maxCheckpoints = 40

	// Why a prompt has no saved files. Fixed text; git's own words are not shown.
	FilesWhyNotRepo = "This folder is not inside a git repository, so Flockdeck has no saved files to put back. Only the conversation can go back."
	FilesWhyNoGit   = "Git is not installed, so Flockdeck has no saved files to put back. Only the conversation can go back."
	FilesWhyMid     = "A merge, rebase or similar operation was under way when that prompt was sent, so its files were not saved. Only the conversation can go back."
	FilesWhyFailed  = "Flockdeck could not save the files when that prompt was sent. Only the conversation can go back."
	FilesWhyTooBig  = "The folder has too many changed or untracked files (over 3000), or one over 32 MB, so its files were not saved when that prompt was sent. Only the conversation can go back."
	FilesWhySlow    = "Saving the files took too long, so they were not saved when that prompt was sent. Only the conversation can go back."
)

// FilesOutcome says what became of the files in a RewindFiles call whose
// conversation went back.
type FilesOutcome struct {
	// Result is what was written, when the restore ran.
	Result filerewind.Result
	// Err is why the files were not (all) put back; nil when they were.
	Err error
}

// FilesError carries the list as it is now when it differs from the one
// confirmed, so that the window can show it again.
type FilesError struct{ Now filerewind.Listing }

func (e *FilesError) Error() string { return ErrFilesChanged.Error() }
func (e *FilesError) Unwrap() error { return ErrFilesChanged }

func whyText(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, filerewind.ErrNotRepo):
		return FilesWhyNotRepo
	case errors.Is(err, filerewind.ErrNoGit):
		return FilesWhyNoGit
	case errors.Is(err, filerewind.ErrMidOperation):
		return FilesWhyMid
	case errors.Is(err, filerewind.ErrTooBig):
		return FilesWhyTooBig
	case errors.Is(err, filerewind.ErrSlow):
		return FilesWhySlow
	}
	return FilesWhyFailed
}

// checkpoint saves the files, or notes why it could not. It is called from the
// loop, before a typed prompt is sent. It returns the zero Checkpoint when the
// files are not kept.
func (r *Runtime) checkpoint() filerewind.Checkpoint {
	if !r.opts.FileRewind {
		return filerewind.Checkpoint{}
	}
	r.mu.Lock()
	c := r.client
	if r.ckptRun == "" {
		r.ckptRun = newToken()
		filerewind.Register(r.ckptRun)
	}
	run, n := r.ckptRun, len(r.marks)+1
	r.mu.Unlock()
	if c == nil || !c.Capabilities().Rewind {
		return filerewind.Checkpoint{}
	}
	cp, err := filerewind.Capture(r.ctx, r.opts.Cwd, fmt.Sprintf("%s%s/%d-%d", filerewind.RefPrefix, run, n, time.Now().UnixNano()))
	r.mu.Lock()
	r.filesWhy = whyText(err)
	r.mu.Unlock()
	if err != nil {
		return filerewind.Checkpoint{}
	}
	return cp
}

// setCheckpoint keeps cp on the mark of token, and drops the checkpoints of
// prompts that are too old to keep. A checkpoint whose mark is gone is dropped.
func (r *Runtime) setCheckpoint(token string, cp filerewind.Checkpoint) {
	if cp.Ref == "" {
		return
	}
	var drop []string
	r.mu.Lock()
	found := false
	for i := len(r.marks) - 1; i >= 0; i-- {
		if r.marks[i].token == token {
			r.marks[i].ckpt = cp
			found = true
			break
		}
	}
	if !found {
		drop = append(drop, cp.Ref)
	}
	kept := 0
	for i := len(r.marks) - 1; i >= 0; i-- {
		if r.marks[i].ckpt.Ref == "" {
			continue
		}
		if kept++; kept > maxCheckpoints {
			drop = append(drop, r.marks[i].ckpt.Ref)
			r.marks[i].ckpt = filerewind.Checkpoint{}
		}
	}
	r.mu.Unlock()
	r.dropRefs(drop)
}

func (r *Runtime) dropRefs(refs []string) {
	for _, ref := range refs {
		filerewind.Drop(r.opts.Cwd, ref)
	}
}

// dropMarks drops the checkpoints of marks that were cut off.
func (r *Runtime) dropMarks(marks []mark) {
	var refs []string
	for _, m := range marks {
		if m.ckpt.Ref != "" {
			refs = append(refs, m.ckpt.Ref)
		}
	}
	r.dropRefs(refs)
}

// dropCheckpoints drops every checkpoint this runtime made. The refs saved
// before a restore are kept.
func (r *Runtime) dropCheckpoints() {
	r.mu.Lock()
	if r.ckptRun != "" {
		filerewind.Unregister(r.ckptRun)
	}
	marks := slices.Clone(r.marks)
	for i := range r.marks {
		r.marks[i].ckpt = filerewind.Checkpoint{}
	}
	r.mu.Unlock()
	r.dropMarks(marks)
}

// FilePoints are the rewind points that have saved files, oldest first, and, when
// the latest prompt could not be saved, why.
func (r *Runtime) FilePoints() ([]string, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.opts.FileRewind || r.client == nil || !r.client.Capabilities().Rewind || r.cleared {
		return nil, ""
	}
	var out []string
	for i, m := range r.marks {
		if m.entry != "" && m.ckpt.Tree != "" && i > 0 && r.marks[i-1].complete {
			out = append(out, m.entry)
		}
	}
	return out, r.filesWhy
}

// treeFor returns the saved files of the prompt entry id, after the checks a
// rewind makes, without taking the pane back.
func (r *Runtime) treeFor(id, token string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.client == nil || r.phase != PhaseReady {
		return "", ErrNotRunning
	}
	if token != fmt.Sprintf("%d.%d", r.seq, r.epoch) {
		return "", ErrRewindStale
	}
	if !r.client.Capabilities().Rewind || !r.opts.FileRewind {
		return "", ErrRewindNotOffered
	}
	if !r.idleLocked() {
		return "", ErrRewindBusy
	}
	i, ok := r.pointLocked(id)
	if !ok || !r.br.HasPrompt(id) {
		return "", ErrNoPoint
	}
	if r.marks[i].ckpt.Tree == "" {
		return "", ErrNoFiles
	}
	return r.marks[i].ckpt.Tree, nil
}

// FilePlan lists the files going back to before the prompt entry id would
// change: edited ones get their earlier content, deleted ones come back, and
// ones created since are removed. Nothing is changed.
func (r *Runtime) FilePlan(id, token string) (filerewind.Listing, error) {
	tree, err := r.treeFor(id, token)
	if err != nil {
		return filerewind.Listing{}, err
	}
	return filerewind.Plan(r.ctx, r.opts.Cwd, tree)
}

// RewindFiles is Rewind that also puts the files back, for exactly the paths in
// files, which the person confirmed from a FilePlan. If the files differ from
// that list nothing happens at all and the error is a *FilesError. Once the
// conversation has gone back, a failure to write the files is in out.Err and
// the error returned is nil.
func (r *Runtime) RewindFiles(id, token string, files []filerewind.Confirm) (string, FilesOutcome, error) {
	var out FilesOutcome
	if files == nil {
		files = []filerewind.Confirm{}
	}
	text, err := r.rewind(id, token, files, &out)
	return text, out, err
}

// checkFiles is the check made before the conversation goes back. A list that
// does not match the files, or a file that cannot be put back safely, stops the
// rewind before anything changes.
func (r *Runtime) checkFiles(tree string, files []filerewind.Confirm) error {
	err := filerewind.Check(r.ctx, r.opts.Cwd, tree, files)
	return asFilesError(err)
}

// asFilesError wraps a changed-list error in a FilesError.
func asFilesError(err error) error {
	var ce *filerewind.ChangedError
	if errors.As(err, &ce) {
		return &FilesError{Now: ce.Now}
	}
	return err
}

// restoreFiles writes the files back after the conversation went back.
func (r *Runtime) restoreFiles(tree string, files []filerewind.Confirm) (filerewind.Result, error) {
	r.mu.Lock()
	r.undoSeq++
	name := fmt.Sprintf("%s/%s-%d", r.ckptRun, time.Now().UTC().Format("20060102T150405"), r.undoSeq)
	r.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.ctx), 2*time.Minute)
	defer cancel()
	res, err := filerewind.Restore(ctx, r.opts.Cwd, tree, files, name)
	return res, asFilesError(err)
}
