// Package filerewind keeps a copy of a checkout's files at the moment a prompt
// is sent, and puts them back later.
//
// A checkpoint is a git commit of everything the checkout holds: tracked files
// as they are on disk, staged or not, and untracked files that .gitignore does
// not hide. It is made with a scratch index, so the checkout's own index, working
// tree and branch are never changed. The commit is kept alive by a ref under
// refs/flockdeck/, which the caller drops when the checkpoint is not needed.
//
// Restoring compares the checkpoint with the files now, lists what differs, and
// writes only the files on a list the caller confirmed. Before it writes
// anything it saves the files as they are now under a second ref that is never
// dropped, so a restore can itself be undone with git.
//
// Ignored files (build output, node_modules) are neither saved nor touched.
// Submodules are skipped. A checkout in the middle of a merge, rebase,
// cherry-pick, revert or bisect is not checkpointed and not restored.
package filerewind

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
	// ErrMidOperation means a merge, rebase, cherry-pick, revert or bisect is
	// under way, and the files hold half-applied work.
	ErrMidOperation = errors.New("a merge, rebase or similar operation is under way")
	// ErrTooMany means more files differ than a person can be asked to confirm.
	ErrTooMany = errors.New("too many files differ to list")
	// ErrChanged means the files differ from the list that was confirmed.
	ErrChanged = errors.New("the files changed since the list was shown")
)

// MaxChanges is the most files a restore lists. A longer list is refused rather
// than cut: the person confirms every file that will be touched.
const MaxChanges = 300

const (
	captureTimeout = 30 * time.Second
	planTimeout    = 30 * time.Second
	restoreTimeout = 60 * time.Second
	// RefPrefix starts every ref this package makes.
	RefPrefix = "refs/flockdeck/rewind/"
	// UndoPrefix starts the refs that hold the files as they were just before a
	// restore. They are not dropped.
	UndoPrefix = "refs/flockdeck/rewind-undo/"
)

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
// root and uses forward slashes.
type Change struct {
	Path   string
	Action Action
}

// Result says what a restore did.
type Result struct {
	// Undo is the ref that holds the files as they were before the restore.
	Undo string
	// Written and Removed count the files put back and the files deleted.
	Written, Removed int
	// Failed names files that could not be restored, each with the reason.
	Failed []string
}

type repo struct {
	root   string // top of the working tree
	prefix string // the folder asked about, relative to root, "" or "sub/dir/"
	gitDir string
}

func locate(ctx context.Context, dir string) (repo, error) {
	if !gitx.Available() {
		return repo{}, ErrNoGit
	}
	out, err := git(ctx, dir, nil, nil, "rev-parse", "--show-toplevel", "--show-prefix", "--git-dir")
	if err != nil {
		return repo{}, ErrNotRepo
	}
	lines := strings.Split(strings.TrimRight(out, "\r\n"), "\n")
	if len(lines) != 3 || strings.TrimSpace(lines[0]) == "" {
		return repo{}, ErrNotRepo
	}
	r := repo{root: filepath.FromSlash(strings.TrimSpace(lines[0])), prefix: strings.TrimSpace(lines[1]), gitDir: filepath.FromSlash(strings.TrimSpace(lines[2]))}
	if !filepath.IsAbs(r.gitDir) {
		r.gitDir = filepath.Join(dir, r.gitDir)
	}
	return r, nil
}

// Available says why dir cannot be checkpointed, or returns nil. It is cheap
// enough to ask once when a pane starts.
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
	tmp, err := os.MkdirTemp("", "flockdeck-rewind-")
	if err != nil {
		return Checkpoint{}, err
	}
	defer os.RemoveAll(tmp)
	env, err := r.scratchIndex(ctx, tmp, "")
	if err != nil {
		return Checkpoint{}, err
	}
	tree, err := r.worktreeTree(ctx, env)
	if err != nil {
		return Checkpoint{}, err
	}
	args := []string{"commit-tree", tree, "-m", "flockdeck rewind checkpoint"}
	if head, err := git(ctx, r.root, nil, nil, "rev-parse", "--verify", "-q", "HEAD"); err == nil && strings.TrimSpace(head) != "" {
		args = append(args, "-p", strings.TrimSpace(head))
	}
	commit, err := git(ctx, r.root, env, nil, args...)
	if err != nil {
		return Checkpoint{}, err
	}
	commit = strings.TrimSpace(commit)
	if _, err := git(ctx, r.root, env, nil, "update-ref", ref, commit); err != nil {
		return Checkpoint{}, err
	}
	return Checkpoint{Tree: tree, Ref: ref}, nil
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

// Plan lists the files under dir that differ between the checkpoint tree and
// the files now. Nothing is changed.
func Plan(ctx context.Context, dir, tree string) ([]Change, error) {
	ctx, cancel := context.WithTimeout(ctx, planTimeout)
	defer cancel()
	r, err := locate(ctx, dir)
	if err != nil {
		return nil, err
	}
	if err := midOperation(r.gitDir); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp("", "flockdeck-rewind-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	return r.plan(ctx, tmp, tree)
}

func (r repo) plan(ctx context.Context, tmp, tree string) ([]Change, error) {
	if !validTree(tree) {
		return nil, errors.New("filerewind: not a tree id")
	}
	env, err := r.scratchIndex(ctx, tmp, "")
	if err != nil {
		return nil, err
	}
	now, err := r.worktreeTree(ctx, env)
	if err != nil {
		return nil, err
	}
	args := []string{"diff-tree", "-r", "-z", "--raw", "--no-renames", "--no-abbrev", tree, now}
	if r.prefix != "" {
		args = append(args, "--", ":(literal)"+strings.TrimSuffix(r.prefix, "/"))
	}
	out, err := git(ctx, r.root, nil, nil, args...)
	if err != nil {
		return nil, err
	}
	return parseRaw(out)
}

// parseRaw reads `git diff-tree -z --raw`: ":srcmode dstmode srcsha dstsha S\0path\0".
func parseRaw(out string) ([]Change, error) {
	parts := strings.Split(out, "\x00")
	var changes []Change
	for i := 0; i+1 < len(parts); i += 2 {
		meta, path := parts[i], parts[i+1]
		f := strings.Fields(meta)
		if len(f) != 5 || !strings.HasPrefix(f[0], ":") {
			return nil, fmt.Errorf("filerewind: unexpected diff-tree line %q", meta)
		}
		if f[0] == ":160000" || f[1] == "160000" {
			continue // a submodule is not ours to move
		}
		if !localPath(path) {
			return nil, fmt.Errorf("filerewind: git named a path outside the checkout: %q", path)
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
		changes = append(changes, c)
		if len(changes) > MaxChanges {
			return nil, ErrTooMany
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes, nil
}

// Restore puts the files of the checkpoint tree back, for exactly the files in
// confirmed. If the files now differ from that list in any way, nothing is
// written and ErrChanged is returned with the list as it is now.
func Restore(ctx context.Context, dir, tree string, confirmed []string, undoName string) (Result, []Change, error) {
	ctx, cancel := context.WithTimeout(ctx, restoreTimeout)
	defer cancel()
	r, err := locate(ctx, dir)
	if err != nil {
		return Result{}, nil, err
	}
	if err := midOperation(r.gitDir); err != nil {
		return Result{}, nil, err
	}
	tmp, err := os.MkdirTemp("", "flockdeck-rewind-")
	if err != nil {
		return Result{}, nil, err
	}
	defer os.RemoveAll(tmp)
	now, err := r.plan(ctx, tmp, tree)
	if err != nil {
		return Result{}, nil, err
	}
	if !sameSet(now, confirmed) {
		return Result{}, now, ErrChanged
	}
	res := Result{}
	if len(now) == 0 {
		return res, now, nil
	}
	// The files as they are now are saved before anything is written, and a
	// failure to save stops the restore.
	undo := UndoPrefix + undoName
	env, err := r.scratchIndex(ctx, tmp, "")
	if err != nil {
		return res, now, err
	}
	cur, err := r.worktreeTree(ctx, env)
	if err != nil {
		return res, now, err
	}
	args := []string{"commit-tree", cur, "-m", "flockdeck files before a rewind"}
	if head, err := git(ctx, r.root, nil, nil, "rev-parse", "--verify", "-q", "HEAD"); err == nil && strings.TrimSpace(head) != "" {
		args = append(args, "-p", strings.TrimSpace(head))
	}
	commit, err := git(ctx, r.root, env, nil, args...)
	if err != nil {
		return res, now, err
	}
	if _, err := git(ctx, r.root, env, nil, "update-ref", undo, strings.TrimSpace(commit)); err != nil {
		return res, now, err
	}
	res.Undo = undo

	var write []string
	for _, c := range now {
		if c.Action != Remove {
			write = append(write, c.Path)
			continue
		}
		if err := r.remove(c.Path); err != nil {
			res.Failed = append(res.Failed, c.Path+": "+err.Error())
			continue
		}
		res.Removed++
	}
	if len(write) > 0 {
		wenv, err := r.scratchIndex(ctx, tmp, tree)
		if err != nil {
			return res, now, err
		}
		in := strings.NewReader(strings.Join(write, "\x00") + "\x00")
		if _, err := git(ctx, r.root, wenv, in, "checkout-index", "-f", "-q", "-z", "--stdin"); err != nil {
			res.Failed = append(res.Failed, "writing files: "+err.Error())
		} else {
			res.Written = len(write)
		}
	}
	return res, now, nil
}

// remove deletes the file at rel, which is relative to the root. It refuses a
// path that goes through a link, and anything that is not a plain file, then
// removes folders that are left empty.
func (r repo) remove(rel string) error {
	parts := strings.Split(rel, "/")
	cur := r.root
	for _, p := range parts[:len(parts)-1] {
		cur = filepath.Join(cur, p)
		fi, err := os.Lstat(cur)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
			return errors.New("a folder on the way is a link")
		}
	}
	full := filepath.Join(r.root, filepath.FromSlash(rel))
	fi, err := os.Lstat(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if fi.IsDir() {
		return errors.New("it is a folder now")
	}
	if err := os.Remove(full); err != nil {
		return err
	}
	for d := filepath.Dir(full); d != r.root && strings.HasPrefix(d, r.root); d = filepath.Dir(d) {
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

// worktreeTree adds every file of the checkout to the scratch index and writes
// its tree.
func (r repo) worktreeTree(ctx context.Context, env []string) (string, error) {
	// git add runs the clean filters the repository names, so a repository whose
	// configuration is not safe to read is not read.
	if err := gitx.UnsafeReadConfig(ctx, r.root); err != nil {
		return "", err
	}
	if _, err := git(ctx, r.root, env, nil, "add", "-A", "--", "."); err != nil {
		return "", err
	}
	out, err := git(ctx, r.root, env, nil, "write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func sameSet(now []Change, confirmed []string) bool {
	if len(now) != len(confirmed) {
		return false
	}
	have := make(map[string]bool, len(now))
	for _, c := range now {
		have[c.Path] = true
	}
	seen := make(map[string]bool, len(confirmed))
	for _, p := range confirmed {
		if !have[p] || seen[p] {
			return false
		}
		seen[p] = true
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

// cleanEnv is this process's environment without a GIT_INDEX_FILE it was
// started under, then extra, then the settings every command here needs.
func cleanEnv(extra []string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if len(kv) >= 15 && strings.EqualFold(kv[:15], "GIT_INDEX_FILE=") {
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
