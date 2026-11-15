// Package filerewind keeps a copy of a checkout's files at the moment a prompt
// is sent, and puts them back later.
//
// A checkpoint is a git commit of everything the checkout holds: tracked files
// as they are on disk, staged or not, and untracked files that .gitignore does
// not hide. It is made with a scratch index, so the checkout's own index, working
// tree and branch are never changed. The commit is kept alive by a ref under
// refs/flockdeck/rewind/, which the caller drops when the checkpoint is not
// needed and which Sweep removes after a crash.
//
// Restoring compares the checkpoint with the files now, lists what differs, and
// writes only the files on a list the caller confirmed. Each listed file carries
// an id made from its path, mode and the content it has now, so a file edited
// after the list was shown is not on the confirmed list any more and the restore
// is refused. Before it writes anything it saves the files as they are now under
// a second ref that is not dropped.
//
// What is never touched, and what refuses the restore instead:
//   - Ignored files are not saved and not written. A restore that would have to
//     overwrite one, or delete a folder that holds one, is refused.
//   - A path that goes through a symbolic link or a junction is neither saved nor
//     written. A restore that needs one is refused.
//   - A file that a git filter (LFS, a smudge driver) or a working-tree encoding
//     applies to is refused: checking it out would run the filter.
//   - Submodules and nested repositories are skipped and reported.
//   - A checkout with more than MaxFiles changed or untracked files, or one of
//     them over MaxFileBytes, is not saved at all, since a partial copy would
//     make a restore delete files it never saw.
//   - A checkout in the middle of a merge, rebase, cherry-pick, revert or bisect
//     is not saved and not restored.
//
// Line endings follow core.autocrlf and .gitattributes both ways, as git does.
package filerewind

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/jmwri/flockdeck/internal/gitx"
	"github.com/jmwri/flockdeck/internal/sysproc"
)

var (
	// ErrNoGit means git is not installed.
	ErrNoGit = errors.New("git is not installed")
	// ErrNotRepo means the folder is not inside a git repository.
	ErrNotRepo = errors.New("the folder is not inside a git repository")
	// ErrMidOperation means a merge, rebase or similar operation is under way.
	ErrMidOperation = errors.New("a merge, rebase or similar operation is under way")
	// ErrTooMany means more files differ than a person can be asked to confirm.
	ErrTooMany = errors.New("too many files differ to list")
	// ErrTooBig means the checkout has too many changed or untracked files, or a
	// file that is too large, to be saved in a reasonable time.
	ErrTooBig = errors.New("too many or too large files to save")
	// ErrSlow means saving or comparing the files took too long.
	ErrSlow = errors.New("saving the files took too long")
	// ErrChanged means the files differ from the list that was confirmed.
	ErrChanged = errors.New("the files changed since the list was shown")
	// ErrBlocked means some file cannot be put back safely; see BlockedError.
	ErrBlocked = errors.New("some files cannot be put back safely")
)

const (
	// MaxChanges is the most files a restore lists. A longer list is refused
	// rather than cut: the person confirms every file that will be touched.
	MaxChanges = 300
	// MaxFiles is the most changed or untracked files a checkpoint is made of.
	MaxFiles = 3000
	// MaxFileBytes is the largest such file, and MaxTotalBytes the most together.
	MaxFileBytes  = 32 << 20
	MaxTotalBytes = 256 << 20

	captureTimeout = 15 * time.Second
	planTimeout    = 30 * time.Second
	restoreTimeout = 60 * time.Second
	// RefPrefix starts every ref this package makes for a checkpoint.
	RefPrefix = "refs/flockdeck/rewind/"
	// UndoPrefix starts the refs that hold the files as they were just before a
	// restore, and checkpoints kept after a restore that did not finish. They are
	// kept for UndoMaxAge and at most UndoMaxCount of them.
	UndoPrefix = "refs/flockdeck/rewind-undo/"
	// UndoMaxAge and UndoMaxCount bound the refs under UndoPrefix.
	UndoMaxAge   = 30 * 24 * time.Hour
	UndoMaxCount = 50
	// StaleRunAge is how old a checkpoint of a run that is not live must be
	// before Sweep removes it.
	StaleRunAge = 7 * 24 * time.Hour
)

// limits are the size limits, a variable so that a test can lower them.
var limits = struct {
	files       int
	file, total int64
}{MaxFiles, MaxFileBytes, MaxTotalBytes}

// Checkpoint is a saved state of a checkout.
type Checkpoint struct {
	// Tree is the saved files as a git tree. Plan and Restore take it.
	Tree string
	// Ref is the ref that keeps the commit.
	Ref string
}

// Action says what a restore does to a file.
type Action string

const (
	// Overwrite puts the earlier content back over a file that was edited.
	Overwrite Action = "overwrite"
	// Recreate writes back a file that was deleted since.
	Recreate Action = "recreate"
	// Remove deletes a file that was created since.
	Remove Action = "remove"
)

// Change is one file a restore would touch. Path is relative to the repository
// root and uses forward slashes. ID names the file as it is now: it changes
// when the file is edited, so a confirmation of the list holds only for the
// files as they were shown.
type Change struct {
	Path   string
	Action Action
	ID     string
}

// Confirm is a file the person confirmed.
type Confirm struct{ Path, ID string }

// Reasons a file cannot be put back safely.
const (
	ReasonLink    = "is reached through a link"
	ReasonIgnored = "exists and git ignores it"
	ReasonFolder  = "is a folder that holds other files"
	ReasonInWay   = "has a file in the way"
	ReasonFilter  = "uses a git filter or encoding"
	ReasonExists  = "exists and is not part of the saved files"
)

// BlockedItem is a file that cannot be put back safely and why.
type BlockedItem struct{ Path, Reason string }

// Listing is what a restore would do.
type Listing struct {
	Changes []Change
	// Skipped names submodules and nested repositories that differ; they are not
	// touched.
	Skipped []string
	// Blocked lists the files that make a restore unsafe. A restore with any is
	// refused.
	Blocked []BlockedItem
}

// BlockedError is returned when a restore would be unsafe.
type BlockedError struct{ Items []BlockedItem }

func (e *BlockedError) Error() string { return ErrBlocked.Error() }
func (e *BlockedError) Unwrap() error { return ErrBlocked }

// ChangedError is returned when the files differ from the confirmed list; Now
// is the list as it is.
type ChangedError struct{ Now Listing }

func (e *ChangedError) Error() string { return ErrChanged.Error() }
func (e *ChangedError) Unwrap() error { return ErrChanged }

// Result says what a restore did.
type Result struct {
	// Undo is the ref that holds the files as they were before the restore.
	Undo string
	// Kept is the checkpoint ref kept for a restore that did not finish, for the
	// person to restore from by hand.
	Kept string
	// Written and Removed count the files put back and the files deleted, as
	// found by looking at the files afterwards.
	Written, Removed int
	// Failed names files that could not be restored, each with the reason.
	Failed []string
}

type repo struct {
	root   string // top of the working tree
	base   string // the folder asked about
	prefix string // base relative to root, "" or "sub/dir/", as git spells it
	gitDir string
}

func locate(ctx context.Context, dir string) (repo, error) {
	if !gitx.Available() {
		return repo{}, ErrNoGit
	}
	out, err := git(ctx, dir, nil, nil, "rev-parse", "--show-toplevel", "--show-prefix", "--git-dir")
	if err != nil {
		if ctx.Err() != nil {
			return repo{}, ErrSlow
		}
		return repo{}, ErrNotRepo
	}
	lines := strings.Split(strings.TrimRight(out, "\r\n"), "\n")
	if len(lines) != 3 || strings.TrimSpace(lines[0]) == "" {
		return repo{}, ErrNotRepo
	}
	r := repo{root: filepath.FromSlash(strings.TrimSpace(lines[0])), prefix: strings.TrimSpace(lines[1]), gitDir: filepath.FromSlash(strings.TrimSpace(lines[2]))}
	r.base = filepath.Join(r.root, filepath.FromSlash(r.prefix))
	if !filepath.IsAbs(r.gitDir) {
		r.gitDir = filepath.Join(dir, r.gitDir)
	}
	return r, nil
}

// Available says why dir cannot be checkpointed, or returns nil.
func Available(ctx context.Context, dir string) error {
	r, err := locate(ctx, dir)
	if err != nil {
		return err
	}
	return midOperation(r.gitDir)
}

func midOperation(gitDir string) error {
	for _, n := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "BISECT_LOG", "rebase-merge", "rebase-apply"} {
		if _, err := os.Lstat(filepath.Join(gitDir, n)); err == nil {
			return ErrMidOperation
		}
	}
	return nil
}

// timeoutErr turns a deadline into ErrSlow.
func timeoutErr(ctx context.Context, err error) error {
	if err != nil && ctx.Err() != nil {
		return ErrSlow
	}
	return err
}

// Capture saves the files of the checkout that holds dir under ref, which must
// start with RefPrefix.
func Capture(ctx context.Context, dir, ref string) (Checkpoint, error) {
	if !strings.HasPrefix(ref, RefPrefix) {
		return Checkpoint{}, fmt.Errorf("filerewind: ref %q is not under %s", ref, RefPrefix)
	}
	ctx, cancel := context.WithTimeout(ctx, captureTimeout)
	defer cancel()
	r, err := locate(ctx, dir)
	if err != nil {
		return Checkpoint{}, err
	}
	if err := midOperation(r.gitDir); err != nil {
		return Checkpoint{}, err
	}
	started := time.Now().Add(-time.Second)
	tmp, err := os.MkdirTemp("", "flockdeck-rewind-")
	if err != nil {
		return Checkpoint{}, err
	}
	defer os.RemoveAll(tmp)
	cp, err := r.capture(ctx, tmp, ref)
	if err != nil {
		r.cleanTemporaryObjects(started)
		return Checkpoint{}, timeoutErr(ctx, err)
	}
	sweepOnce(r)
	return cp, nil
}

func (r repo) capture(ctx context.Context, tmp, ref string) (Checkpoint, error) {
	env, err := r.scratchIndex(ctx, tmp, "")
	if err != nil {
		return Checkpoint{}, err
	}
	tree, err := r.worktreeTree(ctx, env)
	if err != nil {
		return Checkpoint{}, err
	}
	commit, err := r.commit(ctx, env, tree, "flockdeck rewind checkpoint")
	if err != nil {
		return Checkpoint{}, err
	}
	if _, err := git(ctx, r.root, env, nil, "update-ref", ref, commit); err != nil {
		return Checkpoint{}, err
	}
	return Checkpoint{Tree: tree, Ref: ref}, nil
}

func (r repo) commit(ctx context.Context, env []string, tree, msg string) (string, error) {
	args := []string{"commit-tree", tree, "-m", msg}
	if head, err := git(ctx, r.root, nil, nil, "rev-parse", "--verify", "-q", "HEAD"); err == nil && strings.TrimSpace(head) != "" {
		args = append(args, "-p", strings.TrimSpace(head))
	}
	out, err := git(ctx, r.root, env, nil, args...)
	return strings.TrimSpace(out), err
}

// cleanTemporaryObjects removes the half-written object files a killed git
// leaves in the object directory.
func (r repo) cleanTemporaryObjects(since time.Time) {
	out, err := git(context.Background(), r.root, nil, nil, "rev-parse", "--git-path", "objects")
	if err != nil {
		return
	}
	dir := filepath.FromSlash(strings.TrimSpace(out))
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(r.root, dir)
	}
	for _, pat := range []string{"tmp_obj_*", filepath.Join("??", "tmp_obj_*")} {
		matches, _ := filepath.Glob(filepath.Join(dir, pat))
		for _, m := range matches {
			if fi, err := os.Lstat(m); err == nil && fi.Mode().IsRegular() && fi.ModTime().After(since) {
				_ = os.Remove(m)
			}
		}
	}
}

// Drop deletes a checkpoint ref. It does nothing for a ref outside RefPrefix.
func Drop(dir, ref string) {
	if !strings.HasPrefix(ref, RefPrefix) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if r, err := locate(ctx, dir); err == nil {
		_, _ = git(ctx, r.root, nil, nil, "update-ref", "-d", ref)
	}
}

// Keep moves a checkpoint ref under UndoPrefix, so that it outlives the pane and
// the sweep of stale checkpoints, and returns the new name.
func Keep(dir, ref string) (string, error) {
	if !strings.HasPrefix(ref, RefPrefix) {
		return "", errors.New("filerewind: not a checkpoint ref")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r, err := locate(ctx, dir)
	if err != nil {
		return "", err
	}
	kept := UndoPrefix + "kept/" + strings.TrimPrefix(ref, RefPrefix)
	if _, err := git(ctx, r.root, nil, nil, "update-ref", kept, ref); err != nil {
		return "", err
	}
	_, _ = git(ctx, r.root, nil, nil, "update-ref", "-d", ref)
	return kept, nil
}

// Plan lists the files under dir that differ between the checkpoint tree and
// the files now. Nothing is changed.
func Plan(ctx context.Context, dir, tree string) (Listing, error) {
	ctx, cancel := context.WithTimeout(ctx, planTimeout)
	defer cancel()
	r, err := locate(ctx, dir)
	if err != nil {
		return Listing{}, err
	}
	if err := midOperation(r.gitDir); err != nil {
		return Listing{}, err
	}
	tmp, err := os.MkdirTemp("", "flockdeck-rewind-")
	if err != nil {
		return Listing{}, err
	}
	defer os.RemoveAll(tmp)
	l, _, err := r.evaluate(ctx, tmp, tree)
	return l, timeoutErr(ctx, err)
}

// Check does what Restore would check, writing nothing: the files must match
// the confirmed list and none may be blocked.
func Check(ctx context.Context, dir, tree string, confirmed []Confirm) error {
	ctx, cancel := context.WithTimeout(ctx, planTimeout)
	defer cancel()
	r, err := locate(ctx, dir)
	if err != nil {
		return err
	}
	if err := midOperation(r.gitDir); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "flockdeck-rewind-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	l, _, err := r.evaluate(ctx, tmp, tree)
	if err != nil {
		return timeoutErr(ctx, err)
	}
	return judge(l, confirmed)
}

func judge(l Listing, confirmed []Confirm) error {
	if !sameList(l.Changes, confirmed) {
		return &ChangedError{Now: l}
	}
	if len(l.Blocked) > 0 {
		return &BlockedError{Items: l.Blocked}
	}
	return nil
}

// evaluate compares the checkpoint tree with the files now. It returns the
// listing and the tree of the files now.
func (r repo) evaluate(ctx context.Context, tmp, tree string) (Listing, string, error) {
	if !validTree(tree) {
		return Listing{}, "", errors.New("filerewind: not a tree id")
	}
	env, err := r.scratchIndex(ctx, tmp, "")
	if err != nil {
		return Listing{}, "", err
	}
	now, err := r.worktreeTree(ctx, env)
	if err != nil {
		return Listing{}, "", err
	}
	l, err := r.diff(ctx, tree, now)
	if err != nil {
		return Listing{}, "", err
	}
	r.block(ctx, &l)
	return l, now, nil
}

// diff lists what differs between two trees under the folder.
func (r repo) diff(ctx context.Context, tree, now string) (Listing, error) {
	out, err := git(ctx, r.root, nil, nil, "diff-tree", "-r", "-z", "--raw", "--no-renames", "--no-abbrev", tree, now)
	if err != nil {
		return Listing{}, err
	}
	return parseRaw(out, r.prefix)
}

// parseRaw reads `git diff-tree -z --raw`: ":srcmode dstmode srcsha dstsha S\0path\0".
// Only paths under prefix are kept.
func parseRaw(out, prefix string) (Listing, error) {
	parts := strings.Split(out, "\x00")
	var l Listing
	for i := 0; i+1 < len(parts); i += 2 {
		meta, path := parts[i], parts[i+1]
		f := strings.Fields(meta)
		if len(f) != 5 || !strings.HasPrefix(f[0], ":") {
			return Listing{}, fmt.Errorf("filerewind: unexpected diff-tree line %q", meta)
		}
		if !localPath(path) {
			return Listing{}, fmt.Errorf("filerewind: git named a path outside the checkout: %q", path)
		}
		if !underPrefix(path, prefix) {
			continue
		}
		if f[0] == ":160000" || f[1] == "160000" {
			l.Skipped = append(l.Skipped, path)
			continue
		}
		c := Change{Path: path}
		switch f[4] {
		case "A":
			c.Action = Remove
		case "D":
			c.Action = Recreate
		default:
			c.Action = Overwrite
		}
		sum := sha1.Sum([]byte(strings.Join([]string{path, f[4], f[0], f[1], f[2], f[3]}, "\x00")))
		c.ID = hex.EncodeToString(sum[:8])
		l.Changes = append(l.Changes, c)
		if len(l.Changes) > MaxChanges {
			return Listing{}, ErrTooMany
		}
	}
	sort.Slice(l.Changes, func(i, j int) bool { return l.Changes[i].Path < l.Changes[j].Path })
	sort.Strings(l.Skipped)
	return l, nil
}

// underPrefix says path is inside the folder prefix names. Windows and macOS
// spell a folder in whatever case it was opened with, so the comparison ignores
// case there.
func underPrefix(path, prefix string) bool {
	if prefix == "" {
		return true
	}
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.HasPrefix(strings.ToLower(path), strings.ToLower(prefix))
	}
	return strings.HasPrefix(path, prefix)
}

// block fills l.Blocked: the files whose restore would touch something that
// the checkpoint does not hold, or run a filter.
func (r repo) block(ctx context.Context, l *Listing) {
	remove := map[string]bool{}
	for _, c := range l.Changes {
		if c.Action == Remove {
			remove[c.Path] = true
		}
	}
	var writes []string
	var ignoreCheck []string
	memo := map[string]bool{}
	add := func(p, why string) { l.Blocked = append(l.Blocked, BlockedItem{Path: p, Reason: why}) }
	for _, c := range l.Changes {
		if r.viaLink(c.Path, memo) {
			add(c.Path, ReasonLink)
			continue
		}
		if c.Action == Remove {
			continue
		}
		writes = append(writes, c.Path)
		full := filepath.Join(r.root, filepath.FromSlash(c.Path))
		fi, err := os.Lstat(full)
		switch {
		case err == nil && fi.IsDir():
			if hasOther(full, r.root, remove) {
				add(c.Path, ReasonFolder)
				continue
			}
		case err == nil && c.Action == Recreate:
			// A file that is on disk but was not in the files now: git ignores it,
			// or the checkout left it out.
			ignoreCheck = append(ignoreCheck, c.Path)
			continue
		}
		// A file where a folder of the saved files should be.
		parts := strings.Split(c.Path, "/")
		for i := 1; i < len(parts); i++ {
			anc := strings.Join(parts[:i], "/")
			if fi, err := os.Lstat(filepath.Join(r.root, filepath.FromSlash(anc))); err == nil && !fi.IsDir() && !remove[anc] {
				add(c.Path, ReasonInWay)
				break
			}
		}
	}
	if len(ignoreCheck) > 0 {
		ignored := r.checkIgnore(ctx, ignoreCheck)
		for _, p := range ignoreCheck {
			if ignored[p] {
				add(p, ReasonIgnored)
			} else {
				add(p, ReasonExists)
			}
		}
	}
	for _, p := range r.filtered(ctx, writes) {
		add(p, ReasonFilter)
	}
	sort.SliceStable(l.Blocked, func(i, j int) bool { return l.Blocked[i].Path < l.Blocked[j].Path })
	// One reason per file is enough.
	out := l.Blocked[:0]
	for i, b := range l.Blocked {
		if i == 0 || b.Path != l.Blocked[i-1].Path {
			out = append(out, b)
		}
	}
	l.Blocked = out
}

// hasOther says the folder holds a file that is not about to be removed.
func hasOther(dir, root string, remove map[string]bool) bool {
	other := false
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			other = true
			return filepath.SkipAll
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil || !remove[filepath.ToSlash(rel)] {
			other = true
			return filepath.SkipAll
		}
		return nil
	})
	return other
}

// checkIgnore returns the paths git ignores, whether or not git tracks them.
func (r repo) checkIgnore(ctx context.Context, paths []string) map[string]bool {
	out, err := git(ctx, r.root, nil, strings.NewReader(strings.Join(paths, "\x00")+"\x00"), "check-ignore", "--no-index", "-z", "--stdin")
	res := map[string]bool{}
	if err != nil {
		// check-ignore exits 1 when nothing is ignored; that is an answer.
		if !strings.Contains(err.Error(), "exit status 1") {
			for _, p := range paths {
				res[p] = true // not knowing is treated as ignored: refuse
			}
		}
		return res
	}
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			res[p] = true
		}
	}
	return res
}

// filtered returns the paths a git filter or a working-tree encoding applies to.
func (r repo) filtered(ctx context.Context, paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	out, err := git(ctx, r.root, nil, strings.NewReader(strings.Join(paths, "\x00")+"\x00"), "check-attr", "-z", "--stdin", "filter", "working-tree-encoding")
	if err != nil {
		return paths // not knowing is refused
	}
	f := strings.Split(out, "\x00")
	seen := map[string]bool{}
	var bad []string
	for i := 0; i+2 < len(f); i += 3 {
		if v := f[i+2]; v != "unspecified" && v != "unset" && !seen[f[i]] {
			seen[f[i]] = true
			bad = append(bad, f[i])
		}
	}
	return bad
}

// viaLink says a component of the path, the file itself included, is a link or
// junction.
func (r repo) viaLink(rel string, memo map[string]bool) bool {
	parts := strings.Split(rel, "/")
	for i := 1; i <= len(parts); i++ {
		sub := strings.Join(parts[:i], "/")
		v, ok := memo[sub]
		if !ok {
			full := filepath.Join(r.root, filepath.FromSlash(sub))
			_, err := os.Lstat(full)
			v = err == nil && isLink(full)
			memo[sub] = v
		}
		if v {
			return true
		}
	}
	return false
}

// Restore puts the files of the checkpoint tree back, for exactly the files in
// confirmed. If the files now differ from that list in any way, nothing is
// written and the error is a *ChangedError with the list as it is. If any file
// cannot be put back safely the error is a *BlockedError.
func Restore(ctx context.Context, dir, tree string, confirmed []Confirm, undoName string) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, restoreTimeout)
	defer cancel()
	r, err := locate(ctx, dir)
	if err != nil {
		return Result{}, err
	}
	if err := midOperation(r.gitDir); err != nil {
		return Result{}, err
	}
	tmp, err := os.MkdirTemp("", "flockdeck-rewind-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(tmp)
	// The files now are read once. The same tree is what the list is compared
	// against and what the undo ref holds, so what is saved is what was judged.
	l, now, err := r.evaluate(ctx, tmp, tree)
	if err != nil {
		return Result{}, timeoutErr(ctx, err)
	}
	if err := judge(l, confirmed); err != nil {
		return Result{}, err
	}
	res := Result{}
	if len(l.Changes) == 0 {
		return res, nil
	}
	undo := UndoPrefix + undoName
	env, err := r.scratchIndex(ctx, tmp, "")
	if err != nil {
		return res, err
	}
	commit, err := r.commit(ctx, env, now, "flockdeck files before a rewind")
	if err != nil {
		return res, timeoutErr(ctx, err)
	}
	if _, err := git(ctx, r.root, env, nil, "update-ref", undo, commit); err != nil {
		return res, timeoutErr(ctx, err)
	}
	res.Undo = undo

	memo := map[string]bool{}
	var write []string
	for _, c := range l.Changes {
		// Checked again straight before the write.
		if r.viaLink(c.Path, memo) {
			res.Failed = append(res.Failed, c.Path+": "+ReasonLink)
			continue
		}
		if c.Action != Remove {
			write = append(write, c.Path)
			continue
		}
		if err := r.remove(c.Path, memo); err != nil {
			res.Failed = append(res.Failed, c.Path+": "+err.Error())
			continue
		}
		res.Removed++
	}
	var writeErr error
	if len(write) > 0 {
		wenv, err := r.scratchIndex(ctx, tmp, tree)
		if err == nil {
			in := strings.NewReader(strings.Join(write, "\x00") + "\x00")
			_, err = git(ctx, r.root, wenv, in, "checkout-index", "-f", "-q", "-z", "--stdin")
		}
		writeErr = err
	}
	// What was written is read off the files, not assumed from the exit status.
	vctx, vcancel := context.WithTimeout(context.WithoutCancel(ctx), planTimeout)
	defer vcancel()
	left, verr := r.remaining(vctx, tmp, tree)
	switch {
	case verr != nil:
		if len(write) > 0 {
			res.Failed = append(res.Failed, "could not check which files were written")
		}
	default:
		for _, p := range write {
			if left[p] {
				res.Failed = append(res.Failed, p+": not written")
			} else {
				res.Written++
			}
		}
	}
	if writeErr != nil && len(res.Failed) == 0 {
		res.Failed = append(res.Failed, "writing files: "+writeErr.Error())
	}
	return res, nil
}

// remaining returns the paths that still differ from the tree.
func (r repo) remaining(ctx context.Context, tmp, tree string) (map[string]bool, error) {
	env, err := r.scratchIndex(ctx, tmp, "")
	if err != nil {
		return nil, err
	}
	now, err := r.worktreeTree(ctx, env)
	if err != nil {
		return nil, err
	}
	l, err := r.diff(ctx, tree, now)
	if err != nil {
		return nil, err
	}
	left := map[string]bool{}
	for _, c := range l.Changes {
		left[c.Path] = true
	}
	return left, nil
}

// remove deletes the file at rel. It refuses a path that goes through a link and
// anything that is not a plain file, then removes folders that are left empty,
// up to but not including the folder asked about.
func (r repo) remove(rel string, memo map[string]bool) error {
	if r.viaLink(rel, memo) {
		return errors.New(ReasonLink)
	}
	full := filepath.Join(r.root, filepath.FromSlash(rel))
	fi, err := os.Lstat(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !fi.Mode().IsRegular() {
		return errors.New("it is not a plain file now")
	}
	if err := os.Remove(full); err != nil {
		return err
	}
	base := strings.ToLower(r.base)
	for d := filepath.Dir(full); strings.ToLower(d) != base && strings.HasPrefix(strings.ToLower(d), base); d = filepath.Dir(d) {
		if os.Remove(d) != nil { // not empty, or not ours to remove
			break
		}
	}
	return nil
}

// scratchIndex returns the environment for git commands that use a scratch
// index in tmp. With a tree, the index holds that tree; without, it is a copy of
// the checkout's own index (so unchanged files are not hashed again), or HEAD's
// tree when the copy cannot be made.
func (r repo) scratchIndex(ctx context.Context, tmp, tree string) ([]string, error) {
	index := filepath.Join(tmp, "index-"+fmt.Sprint(len(tree)))
	_ = os.Remove(index)
	env := []string{"GIT_INDEX_FILE=" + index}
	if tree != "" {
		if _, err := git(ctx, r.root, env, nil, "read-tree", tree); err != nil {
			return nil, err
		}
		return env, nil
	}
	if p, err := git(ctx, r.root, nil, nil, "rev-parse", "--git-path", "index"); err == nil {
		src := filepath.FromSlash(strings.TrimSpace(p))
		if !filepath.IsAbs(src) {
			src = filepath.Join(r.root, src)
		}
		if data, err := os.ReadFile(src); err == nil && len(data) > 0 {
			if os.WriteFile(index, data, 0o600) == nil {
				// git decides whether a file may have changed without its stat data
				// showing it by comparing the file's time with the index file's. The
				// copy keeps the original's time, or a file edited in the same second
				// the index was written would read as unchanged.
				if fi, err := os.Stat(src); err == nil {
					_ = os.Chtimes(index, fi.ModTime(), fi.ModTime())
				}
				// A copy that git cannot read is thrown away for HEAD's tree.
				if _, err := git(ctx, r.root, env, nil, "write-tree"); err == nil {
					return env, nil
				}
				_ = os.Remove(index)
			}
		}
	}
	if _, err := git(ctx, r.root, nil, nil, "rev-parse", "--verify", "-q", "HEAD"); err == nil {
		if _, err := git(ctx, r.root, env, nil, "read-tree", "HEAD"); err != nil {
			return nil, err
		}
	}
	return env, nil
}

// worktreeTree adds every file of the checkout to the scratch index, takes out
// the paths that go through a link, and writes the tree.
func (r repo) worktreeTree(ctx context.Context, env []string) (string, error) {
	// git add runs the clean filters the repository names, so a repository whose
	// configuration is not safe to read is not read.
	if err := gitx.UnsafeReadConfig(ctx, r.root); err != nil {
		return "", err
	}
	if err := r.sizeGuard(ctx); err != nil {
		return "", err
	}
	if _, err := git(ctx, r.root, env, nil, "add", "-A", "--", "."); err != nil {
		return "", err
	}
	out, err := git(ctx, r.root, env, nil, "ls-files", "-z")
	if err != nil {
		return "", err
	}
	memo := map[string]bool{}
	var bad []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" && r.viaLink(p, memo) {
			bad = append(bad, p)
		}
	}
	if len(bad) > 0 {
		if _, err := git(ctx, r.root, env, strings.NewReader(strings.Join(bad, "\x00")+"\x00"), "update-index", "--force-remove", "-z", "--stdin"); err != nil {
			return "", err
		}
	}
	tree, err := git(ctx, r.root, env, nil, "write-tree")
	return strings.TrimSpace(tree), err
}

// sizeGuard refuses a checkout with too many changed or untracked files, or one
// that is too large, before anything is hashed.
func (r repo) sizeGuard(ctx context.Context) error {
	var paths []string
	for _, args := range [][]string{
		{"ls-files", "-o", "--exclude-standard", "-z"},
		{"diff-files", "--name-only", "-z"},
	} {
		out, err := git(ctx, r.root, nil, nil, args...)
		if err != nil {
			return err
		}
		for _, p := range strings.Split(out, "\x00") {
			if p != "" {
				paths = append(paths, p)
			}
		}
		if len(paths) > limits.files {
			return ErrTooBig
		}
	}
	var total int64
	for _, p := range paths {
		fi, err := os.Lstat(filepath.Join(r.root, filepath.FromSlash(p)))
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		total += fi.Size()
		if fi.Size() > limits.file || total > limits.total {
			return ErrTooBig
		}
	}
	return nil
}

func sameList(now []Change, confirmed []Confirm) bool {
	if len(now) != len(confirmed) {
		return false
	}
	have := make(map[string]string, len(now))
	for _, c := range now {
		have[c.Path] = c.ID
	}
	seen := make(map[string]bool, len(confirmed))
	for _, c := range confirmed {
		if id, ok := have[c.Path]; !ok || id != c.ID || seen[c.Path] {
			return false
		}
		seen[c.Path] = true
	}
	return true
}

func validTree(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// localPath says p is a plain relative path with no way out of the checkout.
func localPath(p string) bool {
	return p != "" && !strings.Contains(p, "\\") && filepath.IsLocal(filepath.FromSlash(p))
}

// git runs git in dir with the scratch settings and returns stdout.
func git(ctx context.Context, dir string, env []string, in io.Reader, args ...string) (string, error) {
	base := []string{
		"-c", "core.fsmonitor=", "-c", "core.hooksPath=" + os.DevNull, "-c", "core.splitIndex=false",
		"-c", "core.longpaths=true", "-c", "commit.gpgsign=false", "-c", "core.quotepath=false",
	}
	cmd := exec.CommandContext(ctx, "git", append(base, args...)...)
	cmd.Dir = dir
	sysproc.NoWindow(cmd)
	var out, errb bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = in, &out, &errb
	cmd.Env = cleanEnv(env)
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		msg := strings.TrimSpace(errb.String())
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, msg)
	}
	return out.String(), nil
}

// keepEnv are the GIT_ variables that are passed on. Everything else that
// starts with GIT_ is dropped: GIT_DIR, GIT_WORK_TREE, GIT_OBJECT_DIRECTORY and
// the rest would point git at a repository other than the pane's folder.
var keepEnv = map[string]bool{"GIT_CEILING_DIRECTORIES": true}

// cleanEnv is this process's environment without the GIT_ variables that choose
// a repository, then extra, then the settings every command here needs.
func cleanEnv(extra []string) []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(strings.ToUpper(name), "GIT_") && !keepEnv[strings.ToUpper(name)] {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, extra...)
	return append(env,
		"GIT_TERMINAL_PROMPT=0", "LANGUAGE=C", "LC_MESSAGES=C",
		"GIT_AUTHOR_NAME=Flockdeck", "GIT_AUTHOR_EMAIL=rewind@flockdeck.invalid",
		"GIT_COMMITTER_NAME=Flockdeck", "GIT_COMMITTER_EMAIL=rewind@flockdeck.invalid",
		"GIT_LITERAL_PATHSPECS=0", "GIT_GLOB_PATHSPECS=0", "GIT_NOGLOB_PATHSPECS=0", "GIT_ICASE_PATHSPECS=0",
	)
}
