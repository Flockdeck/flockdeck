package gitx

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// This file is what the conflict radar (internal/radar) asks of git: a commit
// holding everything a checkout has -- committed, uncommitted and untracked --
// made without touching the checkout's index, working tree or refs, and the
// merge of two such commits, which git does in memory.
//
// What is not verified, because nothing here was run against them: a clean
// filter or LFS (git add -A runs the filter, and LFS writes its own files under
// the real git directory, which this package does not prevent), a custom merge
// driver (merge-tree runs the drivers a .gitattributes names), a sparse checkout
// with a non-cone pattern list, and a partial clone (GIT_NO_LAZY_FETCH stops
// merge-tree fetching missing blobs from git 2.44; an older git may fetch).
// core.fsmonitor is switched off for every command (ownConfig), so no
// monitor is started or asked.

// MaxSnapshotFiles bounds how many changed paths a checkout may have and still
// be compared. It is changes.go's maxCounted for the same reason: a checkout
// with more than that changed -- a build's output left untracked, a rename of
// a whole tree -- is not the few files two agents are fighting over, and
// hashing and diffing it every refresh is the cost the radar must not add.
const MaxSnapshotFiles = maxCounted

// ErrTooManyFiles is what Snapshot returns for a checkout past MaxSnapshotFiles.
var ErrTooManyFiles = errors.New("too many changed files to compare")

// ErrMidOperation is what Snapshot returns for a checkout in the middle of a
// merge, cherry-pick, revert, am, rebase or bisect, or with unmerged files: its
// files then hold conflict markers and half-applied work, which is no state
// anybody has chosen to merge.
var ErrMidOperation = errors.New("in the middle of an operation")

// ErrShallow is what Snapshot returns when a shallow clone shows no history in
// common with the base: the commits that would be shared may be ones it does not
// have, so "nothing in common" is not something it can say.
var ErrShallow = errors.New("a shallow clone cannot say whether its history is shared with the base")

// ErrNoBase is what ResolveBase returns when it can find no base branch to
// measure work from: no main worktree on a branch with commits, no default
// branch recorded for the repository, and no branch called main or master.
var ErrNoBase = errors.New("no base branch was found")

// Scratch is a throwaway object store and a place for scratch indexes. The
// repository's own object directory is read through as an alternate, and what
// is written -- the blobs of untracked and edited files, the trees, the
// snapshot commits and the trees merge-tree makes -- goes into the scratch
// directory, which Close removes. The radar makes one per repository per
// refresh.
//
// Nothing is written to the repository's object store by git itself, and the
// scratch commands are given configuration that keeps them from writing
// anything else into its git directory: core.splitIndex is off, since a split
// index writes a shared index file there (sharedindex.<id>), and
// core.fsmonitor is off. A clean filter that writes there itself is not
// prevented; see the top of this file.
type Scratch struct {
	root   string
	env    []string
	closed atomic.Bool
}

// errNoScratch is what a call that needs a Scratch says when it is given none,
// or one that has been closed: a caller that did that has a bug, and a panic in
// a goroutine of its own is no way to be told.
var errNoScratch = errors.New("no scratch directory to work in")

func (s *Scratch) usable() bool { return s != nil && !s.closed.Load() }

// scratchPrefix names the directories NewScratch makes, which SweepScratch
// looks for.
const scratchPrefix = "flockdeck-radar-"

// NewScratch makes a Scratch for the repository whose git directory
// CommonDir reports.
func NewScratch(commonDir string) (*Scratch, error) {
	root, err := os.MkdirTemp("", scratchPrefix)
	if err != nil {
		return nil, err
	}
	objects := filepath.Join(root, "obj")
	// The repository's objects are named in the scratch store's alternates file
	// rather than in GIT_ALTERNATE_OBJECT_DIRECTORIES, whose entries are split
	// on ":" (";" on Windows) and so cannot name a directory whose path has one.
	if err := os.MkdirAll(filepath.Join(objects, "info"), 0o700); err != nil {
		_ = os.RemoveAll(root)
		return nil, err
	}
	alternates := filepath.Join(commonDir, "objects") + "\n"
	if err := os.WriteFile(filepath.Join(objects, "info", "alternates"), []byte(alternates), 0o600); err != nil {
		_ = os.RemoveAll(root)
		return nil, err
	}
	config := [][2]string{
		// A signing key set in the user's configuration is not asked for.
		{"commit.gpgsign", "false"},
		// A split index writes sharedindex.<id> into the real git directory,
		// and the scratch index is the one thing here that would be split.
		{"core.splitIndex", "false"},
		// A path past Windows's default limit is otherwise skipped by git without a
		// word, and the snapshot would lack the file.
		{"core.longpaths", "true"},
	}
	// The user's own GIT_CONFIG_COUNT entries are kept, and these come after
	// them: a count of ours alone would drop theirs.
	first, _ := strconv.Atoi(os.Getenv("GIT_CONFIG_COUNT"))
	if first < 0 {
		first = 0
	}
	env := []string{
		"GIT_OBJECT_DIRECTORY=" + objects,
		// commit-tree needs somebody to be the author, and a machine with no
		// identity configured would otherwise refuse. Nothing here is kept.
		"GIT_AUTHOR_NAME=Flockdeck", "GIT_AUTHOR_EMAIL=radar@flockdeck.invalid",
		"GIT_COMMITTER_NAME=Flockdeck", "GIT_COMMITTER_EMAIL=radar@flockdeck.invalid",
		// From git 2.44: a partial clone's merge-tree does not go to the
		// network for a blob it lacks.
		"GIT_NO_LAZY_FETCH=1",
		// What the user's environment says about how pathspecs are read must not
		// change how the ones the scratch commands pass are: GIT_LITERAL_PATHSPECS
		// stops ":(exclude)" being understood at all.
		"GIT_LITERAL_PATHSPECS=0", "GIT_GLOB_PATHSPECS=0", "GIT_NOGLOB_PATHSPECS=0", "GIT_ICASE_PATHSPECS=0",
		// Git's messages are in one language, for the log. Only the messages are pinned:
		// LC_ALL and LC_CTYPE stay as they are, because a clean filter that git add runs
		// (Git LFS, a formatter) may need the user's character set to work at all.
		"LANGUAGE=C", "LC_MESSAGES=C", "GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_COUNT=" + strconv.Itoa(first+len(config)),
	}
	for i, kv := range config {
		env = append(env, fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", first+i, kv[0]), fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", first+i, kv[1]))
	}
	return &Scratch{root: root, env: env}, nil
}

// SweepScratch removes scratch directories older than age, left by a run that
// was killed before it could close them. Directories newer than that may be
// another running instance's.
func SweepScratch(age time.Duration) { sweepScratch(os.TempDir(), age) }

func sweepScratch(root string, age time.Duration) {
	matches, _ := filepath.Glob(filepath.Join(root, scratchPrefix+"*"))
	for _, m := range matches {
		if info, err := os.Stat(m); err == nil && info.IsDir() && time.Since(info.ModTime()) > age {
			_ = os.RemoveAll(m)
		}
	}
}

// Root is the scratch directory, for a test to look at.
func (s *Scratch) Root() string { return s.root }

// Close removes everything the scratch held. The error is for the caller to
// log: git's object files are read-only, and a file a virus scanner or an
// indexer holds open cannot be removed on Windows.
func (s *Scratch) Close() error {
	s.closed.Store(true)
	return os.RemoveAll(s.root)
}

// index names the scratch index for the checkout at dir: one file per checkout,
// since snapshots of several checkouts are made at once.
func (s *Scratch) index(dir string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(dir))
	return filepath.Join(s.root, "idx-"+strconv.FormatUint(h.Sum64(), 16))
}

func (s *Scratch) envFor(index string) []string {
	return append(append([]string{}, s.env...), "GIT_INDEX_FILE="+index)
}

// Snap is a snapshot of one checkout.
type Snap struct {
	// Commit is the snapshot as a commit, which is what merge-tree takes: it
	// had a bare tree refused, and the commit gives it a merge base too.
	Commit string
	// Tree identifies the content exactly, so equal trees need no second look.
	Tree string
	// Head is the commit the checkout is on. The merge base of two snapshots
	// comes from their heads, so two snapshots are the same merge only when
	// both their trees and their heads are.
	Head string
	// Dirs are the directories a file of Paths was moved out of, which a path
	// another checkout adds under one of them meets; see changedPaths.
	Dirs []string
	// Paths are the files the checkout changes against the merge base with the
	// base branch -- committed work counts, not only what is uncommitted --
	// both names of a rename, and a submodule bumped to another commit.
	Paths []string
	// Empty says there is nothing to compare: no change against the base.
	// Commit, Tree and Paths are then unset.
	Empty bool
	// Unrelated says Empty is because the checkout has no history in common with
	// the base. For a base the main worktree's own branch names that is an
	// answer; for one guessed by ResolveBaseSource's fallback it is the guess that
	// was wrong, and nothing has been checked.
	Unrelated bool
}

// Snapshot makes a commit of everything checkout dir has, under scratch.
//
// base is a commit the checkout's work is measured from (see BaseOf); empty
// measures from the checkout's own HEAD, so only uncommitted work counts.
// dirty says the checkout has uncommitted or untracked files. Without any, the
// snapshot is HEAD itself and nothing is hashed.
//
// The checkout's own index is never opened for writing and its lock never
// taken. The scratch index is seeded with a copy of the real one, so git add
// has a stat cache to skip unchanged files by, which matters on a large
// checkout; the copy is checked (see indexIsWhole) and, when it cannot be read
// -- caught mid-write, or a split index whose shared part is elsewhere -- the
// scratch index starts from HEAD instead. git's own messages are dropped: the
// CRLF warnings of a Windows checkout are not the radar's to show.
//
// An untracked file is in no index, so the stat cache does not help with it:
// every one is read and hashed again every time. That is why the radar skips a
// checkout with more than MaxSnapshotFiles of them before it gets here.
//
// A checkout in the middle of an operation, or with unmerged files, is not
// snapshotted: ErrMidOperation. One with too many changed files is ErrTooManyFiles.
func Snapshot(ctx context.Context, dir string, s *Scratch, base string, dirty bool) (Snap, error) {
	if !s.usable() {
		return Snap{}, errNoScratch
	}
	out, err := runEnvOut(ctx, dir, nil, "rev-parse", "--git-dir", "HEAD")
	if err != nil {
		return Snap{}, err
	}
	lines := strings.Split(out, "\n")
	if len(lines) != 2 {
		return Snap{}, fmt.Errorf("git rev-parse said %q", out)
	}
	gitDir, head := strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1])
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(dir, gitDir)
	}
	if op := midOperation(gitDir); op != "" {
		return Snap{}, fmt.Errorf("%w: %s", ErrMidOperation, op)
	}
	from := head
	if base != "" {
		out, _, err := runCapture(ctx, commandTimeout, dir, "merge-base", head, base)
		if err != nil {
			// Exit status 1 is git saying there is no common history. Anything
			// else -- a base it cannot find, a timeout -- is no answer, and an
			// answer of "nothing to compare" given for it would be remembered.
			if exitCode(err) == 1 {
				// In a shallow clone the history that would be shared may be
				// the part that was not fetched: no answer, not an empty one.
				shallow, serr := runEnvOut(ctx, dir, nil, "rev-parse", "--is-shallow-repository")
				if serr != nil {
					return Snap{}, serr
				}
				if shallow == "true" {
					return Snap{}, ErrShallow
				}
				return Snap{Empty: true, Unrelated: true}, nil
			}
			return Snap{}, err
		}
		from = strings.TrimSpace(out)
	}
	if !dirty && from == head {
		return Snap{Empty: true}, nil
	}

	snap := Snap{Commit: head, Head: head}
	env := s.env
	if dirty {
		index := s.index(dir)
		env = s.envFor(index)
		seeded, unmerged := seedFromIndex(ctx, dir, env, index)
		if unmerged {
			return Snap{}, fmt.Errorf("%w: unmerged files", ErrMidOperation)
		}
		var staged map[string]bool // repositories only the checkout's own index holds
		if !seeded {
			_ = os.Remove(index)
			// An index built from HEAD has every file in it checked out. In a
			// sparse checkout most are not, and git add -A would stage all of
			// them as deleted. Better no answer this time than that one.
			if out, _, err := runCapture(ctx, commandTimeout, dir, "config", "--bool", "--get", "core.sparseCheckout"); err == nil && strings.TrimSpace(out) == "true" {
				return Snap{}, errors.New("the index of a sparse checkout could not be copied")
			}
			if _, err := runToEnv(ctx, commandTimeout, dir, env, nil, &bytes.Buffer{}, "read-tree", "HEAD"); err != nil {
				return Snap{}, err
			}
			// An index built from HEAD does not hold a repository somebody staged
			// and nobody committed, which would then look untracked and be left out.
			// The checkout's own index is read, and not written, for them.
			staged = realGitlinks(ctx, dir, env)
		}
		if err := addAll(ctx, dir, env, staged); err != nil {
			return Snap{}, err
		}
		tree, err := runEnvOut(ctx, dir, env, "write-tree")
		if err != nil {
			return Snap{}, err
		}
		snap.Commit, err = runEnvOut(ctx, dir, env, "commit-tree", tree, "-p", head, "-m", "snapshot")
		if err != nil {
			return Snap{}, err
		}
	}
	snap.Tree, err = revParse(ctx, dir, env, snap.Commit+"^{tree}")
	if err != nil {
		return Snap{}, err
	}
	snap.Paths, snap.Dirs, err = changedPaths(ctx, dir, env, from, snap.Commit)
	if err != nil {
		return Snap{}, err
	}
	if len(snap.Paths) == 0 {
		return Snap{Empty: true}, nil
	}
	return snap, nil
}

// exitCode is the exit status of the git command err came from, or -1 when it
// did not come from one that ran and exited.
func exitCode(err error) int {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	return -1
}

// midOperation names what the checkout whose git directory is gitDir is in the
// middle of, or "" when it is in none, from the files git leaves there while it
// is.
func midOperation(gitDir string) string {
	for _, f := range []struct{ file, name string }{
		{"MERGE_HEAD", "a merge"},
		{"CHERRY_PICK_HEAD", "a cherry-pick"},
		{"REVERT_HEAD", "a revert"},
		{"rebase-merge", "a rebase"},
		{"rebase-apply", "a rebase or am"},
		{"BISECT_LOG", "a bisect"},
	} {
		if _, err := os.Stat(filepath.Join(gitDir, f.file)); err == nil {
			return f.name
		}
	}
	return ""
}

// seedFromIndex is seedIndex, a variable so that a benchmark can leave the
// seeding out and measure what it saves.
var seedFromIndex = seedIndex

// seedIndex copies the checkout's real index to index and reports whether git
// can read the copy, and whether it holds unmerged entries. It only reads the
// real file: the lock beside it is never taken, so an agent committing at the
// same moment is not held up. A copy made while git replaces the file is torn
// or missing, which the checks catch, and the caller then builds the index from
// HEAD instead.
func seedIndex(ctx context.Context, dir string, env []string, index string) (seeded, unmerged bool) {
	out, _, err := runCapture(ctx, commandTimeout, dir, "rev-parse", "--git-path", "index")
	if err != nil {
		return false, false
	}
	real := strings.TrimSpace(out)
	if !filepath.IsAbs(real) {
		real = filepath.Join(dir, real)
	}
	if copyIndexRetrying(real, index) != nil {
		return false, false
	}
	// ls-files reads the whole index, which says it can be read, and lists only
	// its unmerged entries.
	var listed bytes.Buffer
	if _, err := runToEnv(ctx, commandTimeout, dir, env, nil, &listed, "ls-files", "--unmerged", "-z"); err != nil {
		return false, false
	}
	return true, listed.Len() > 0
}

// copyIndex copies the index file from to, keeping its modification time, and
// fails when what it read is not a whole index.
//
// The time matters: git decides which files it must read again, because they
// may have changed within the same instant the index was written, by comparing
// their times with the index file's, and a copy stamped with the time it was
// made would pass an edit made a moment before as unchanged.
func copyIndex(from, to string) error {
	f, err := os.Open(from)
	if err != nil {
		return err
	}
	defer f.Close()
	// Of the handle that was read, so the time is the file's that was copied
	// and not of one that replaced it since.
	info, err := f.Stat()
	if err != nil {
		return err
	}
	data := make([]byte, 0, info.Size())
	buf := bytes.NewBuffer(data)
	if _, err := buf.ReadFrom(f); err != nil {
		return err
	}
	if !indexIsWhole(buf.Bytes()) {
		return errors.New("the index was not read whole")
	}
	if err := os.WriteFile(to, buf.Bytes(), 0o600); err != nil {
		return err
	}
	return os.Chtimes(to, info.ModTime(), info.ModTime())
}

// indexIsWhole judges a copy of an index by the checksum git ends the file
// with, over everything before it, which git ls-files does not check: it read
// an index cut short and listed what was in it without complaint. The checksum
// is SHA-1 or, in a repository using SHA-256, that.
//
// A checksum of all zeros is what index.skipHash (set by feature.manyFiles)
// writes. It says nothing, so the copy is judged by its signature alone, and
// a torn copy of such an index is not caught here; ls-files is then all there is
// to stand between it and the snapshot, and a copy it cannot read falls back to
// HEAD. That is unverified against a file cut at an entry boundary.
func indexIsWhole(data []byte) bool {
	if len(data) < 12+sha1.Size || string(data[:4]) != "DIRC" {
		return false
	}
	if sum := sha1.Sum(data[:len(data)-sha1.Size]); bytes.Equal(sum[:], data[len(data)-sha1.Size:]) {
		return true
	}
	if len(data) >= 12+sha256.Size {
		sum := sha256.Sum256(data[:len(data)-sha256.Size])
		if bytes.Equal(sum[:], data[len(data)-sha256.Size:]) {
			return true
		}
	}
	// skipHash: the trailer is zeros, in either hash's width.
	zeros := func(n int) bool {
		return len(data) >= 12+n && bytes.Equal(data[len(data)-n:], make([]byte, n))
	}
	return zeros(sha1.Size) || zeros(sha256.Size)
}

// runEnvOut runs git with env and returns its trimmed output.
func runEnvOut(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	var out bytes.Buffer
	if _, err := runToEnv(ctx, commandTimeout, dir, env, nil, &out, args...); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}

func revParse(ctx context.Context, dir string, env []string, rev string) (string, error) {
	return runEnvOut(ctx, dir, env, "rev-parse", "--verify", "--quiet", rev)
}

// changedPaths lists what the commit to changes against the commit from, and the
// directories a file was moved out of. A rename is named by both its paths, so
// that one agent renaming a file and another editing it still meet in the
// radar's first filter, and a submodule -- a gitlink, mode 160000 -- is a path
// like another, so that two panes bumping it differently meet and merge-tree
// decides.
//
// Only paths count toward MaxSnapshotFiles. The directories are a second set,
// bounded by the same number: a file moved to another directory may be one of a
// directory moved whole, and git carries a file another pane adds to the old
// directory across to the new, or stops on it. That pane's path is not in the
// first set, so the old directories are in the second, which a path under them
// meets.
func changedPaths(ctx context.Context, dir string, env []string, from, to string) (paths, dirs []string, err error) {
	out, err := runEnvOut(ctx, dir, env, "diff", "--raw", "-z", "--no-abbrev", "--find-renames", "--no-ext-diff", "--no-textconv", from, to)
	if err != nil {
		return nil, nil, err
	}
	fields := strings.Split(out, "\x00")
	seen, seenDir := map[string]bool{}, map[string]bool{}
	for i := 0; i < len(fields); i++ {
		meta := fields[i]
		if !strings.HasPrefix(meta, ":") {
			continue
		}
		// ":100644 100644 <old id> <new id> M", or R/C with a score.
		parts := strings.Fields(meta)
		if len(parts) < 5 {
			continue
		}
		names := 1
		if c := parts[4][0]; c == 'R' || c == 'C' {
			names = 2
		}
		var named []string
		for j := 0; j < names && i+1 < len(fields); j++ {
			i++
			named = append(named, fields[i])
		}
		for _, p := range named {
			if !seen[p] {
				seen[p] = true
				paths = append(paths, p)
			}
		}
		if len(named) == 2 && path.Dir(named[0]) != path.Dir(named[1]) {
			for d := path.Dir(named[0]); d != "." && d != "/"; d = path.Dir(d) {
				if !seenDir[d] {
					seenDir[d] = true
					dirs = append(dirs, d)
				}
			}
		}
		if len(paths) > MaxSnapshotFiles || len(dirs) > MaxSnapshotFiles {
			return nil, nil, ErrTooManyFiles
		}
	}
	return paths, dirs, nil
}

// MergeTree merges two snapshot commits in memory and returns what git said:
// the merged tree's id and then, when conflict is set, the conflicted paths,
// NUL separated. A conflict is the answer git gives with exit status 1 and a
// tree id first on stdout. Exit 1 with anything else -- git exits 1 for a
// commit it cannot find too -- and every other non-zero status are errors.
//
// It runs under scratch, so the merged tree goes into the scratch object
// store, and in dir, any checkout of the repository. No ref, index or working
// tree is read or written. The merge is in memory; the snapshots it merges
// were written to the scratch directory.
func MergeTree(ctx context.Context, dir string, s *Scratch, a, b string) (out string, conflict bool, err error) {
	if !s.usable() {
		return "", false, errNoScratch
	}
	var buf bytes.Buffer
	_, err = runToEnv(ctx, commandTimeout, dir, s.env, nil, &buf,
		"merge-tree", "--write-tree", "--name-only", "--no-messages", "-z", a, b)
	if err == nil {
		return buf.String(), false, nil
	}
	if exitCode(err) == 1 {
		if !startsWithTreeID(buf.String()) {
			return "", false, fmt.Errorf("git merge-tree exited 1 without a merged tree: %w", err)
		}
		return buf.String(), true, nil
	}
	return "", false, err
}

// startsWithTreeID says whether out begins with a 40 or 64 digit hex object
// id followed by a NUL, which is how merge-tree -z starts an answer.
func startsWithTreeID(out string) bool {
	id, _, found := strings.Cut(out, "\x00")
	if !found || (len(id) != 40 && len(id) != 64) {
		return false
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

// MinMergeTreeGit is the first git whose merge-tree can merge two commits and
// write the result (--write-tree).
const MinMergeTreeGit = "2.38"

// gitVersion asks git for its version string. A variable so a test can say
// what git is.
var gitVersion = func() (string, error) {
	out, _, err := runCapture(context.Background(), commandTimeout, os.TempDir(), "--version")
	return out, err
}

// versionRetry is how long a probe that failed -- git missing, or its version
// unreadable -- is believed before it is made again: those can mend while
// Flockdeck runs, where "git 2.34" cannot.
const versionRetry = time.Minute

var versionState struct {
	mu      sync.Mutex
	known   bool
	ok      bool
	why     string
	until   time.Time // when a failed answer stops being believed; zero never
	probing bool
	gen     uint64 // counts the answers that differed from the one before
	major   int    // the version of git that was read, 0 if none was
	minor   int
}

// versionNow is the clock the probe's expiry reads, for the test.
var versionNow = time.Now

// MergeTreeSupport reports whether this machine's git can predict conflicts,
// and when it cannot, why, in words for the Settings page. It does not wait
// for git: the first call starts a probe in the background and says known is
// false until it has an answer, which is then kept -- for good when git is
// there but too old, and for a minute when the probe itself failed, after which
// the next call asks again, still answering with what it knew meanwhile.
func MergeTreeSupport() (ok bool, why string, known bool) {
	ok, why, known, _, _ = versionView()
	return ok, why, known
}

// versionView is MergeTreeSupport with the number of the answer and whether it
// is final, all read under one lock: a probe finishing between two reads would
// otherwise give the old answer with the new number.
func versionView() (ok bool, why string, known bool, gen uint64, final bool) {
	versionState.mu.Lock()
	defer versionState.mu.Unlock()
	fresh := versionState.known && (versionState.until.IsZero() || versionNow().Before(versionState.until))
	if !fresh && !versionState.probing {
		versionState.probing = true
		go probeVersion()
	}
	return versionState.ok, versionState.why, versionState.known, versionState.gen,
		versionState.known && versionState.until.IsZero()
}

// WatchMergeTreeSupport waits until the answer MergeTreeSupport gives has
// changed since the one numbered since (0 before any), and returns it with its
// number. final says the answer will not change again: a version that was read,
// where a failed probe is asked again after a minute and may come out
// differently. It polls, which is what makes a failed answer expire and be
// asked again, so a caller that keeps watching is told when it mends. The
// answer, its number and final come from one read. ok is false and known false
// when done closed first.
func WatchMergeTreeSupport(since uint64, done <-chan struct{}) (ok bool, why string, gen uint64, final bool, known bool) {
	for {
		ok, why, known, gen, final = versionView()
		if known && gen != since {
			return ok, why, gen, final, true
		}
		// Quick while there is no answer, slow while a failed one is waiting
		// out its minute.
		wait := 2 * time.Second
		if !known {
			wait = 20 * time.Millisecond
		}
		select {
		case <-done:
			return false, "", since, false, false
		case <-time.After(wait):
		}
	}
}

// CheckMergeTree is the probe MergeTreeSupport makes, made now and not kept, for
// a test or benchmark that needs the answer before going on.
func CheckMergeTree() (ok bool, why string) {
	if !Available() {
		return false, "git is not installed"
	}
	out, err := gitVersion()
	return checkMergeTree(out, err)
}

// probeVersion asks git for its version and records what that means.
func probeVersion() {
	var (
		ok           bool
		why          string
		retry        bool
		major, minor int
	)
	if !Available() {
		why, retry = "git is not installed", true
	} else {
		out, err := gitVersion()
		ok, why = checkMergeTree(out, err)
		// Only a version that was read is final.
		var parsed bool
		major, minor, parsed = parseGitVersion(out)
		retry = err != nil || !parsed
	}
	versionState.mu.Lock()
	defer versionState.mu.Unlock()
	if !versionState.known || versionState.ok != ok || versionState.why != why {
		versionState.gen++
	}
	versionState.known, versionState.ok, versionState.why = true, ok, why
	versionState.major, versionState.minor = major, minor
	versionState.until = time.Time{}
	if retry {
		versionState.until = versionNow().Add(versionRetry)
	}
	versionState.probing = false
}

// checkMergeTree judges the output of git --version.
func checkMergeTree(out string, err error) (bool, string) {
	if err != nil {
		return false, "could not read the version of git: " + err.Error()
	}
	major, minor, ok := parseGitVersion(out)
	if !ok {
		return false, fmt.Sprintf("could not read the version of git from %q", strings.TrimSpace(out))
	}
	if major < 2 || (major == 2 && minor < 38) {
		return false, fmt.Sprintf("it needs git %s or newer, and this is git %d.%d", MinMergeTreeGit, major, minor)
	}
	return true, ""
}

// parseGitVersion reads "git version 2.43.0.windows.1" or "git version
// 2.39.5 (Apple Git-154)".
func parseGitVersion(s string) (major, minor int, ok bool) {
	rest, found := strings.CutPrefix(strings.TrimSpace(s), "git version ")
	if !found {
		return 0, 0, false
	}
	fields := strings.Split(strings.Fields(rest + " ")[0], ".")
	if len(fields) < 2 {
		return 0, 0, false
	}
	var err error
	if major, err = strconv.Atoi(fields[0]); err != nil {
		return 0, 0, false
	}
	// "2.38rc1" has its release candidate after the number.
	digits := fields[1]
	for i, r := range digits {
		if r < '0' || r > '9' {
			digits = digits[:i]
			break
		}
	}
	if minor, err = strconv.Atoi(digits); err != nil {
		return 0, 0, false
	}
	return major, minor, true
}

// copyIndexFn is copyIndex, a variable so a test can make it fail once.
var copyIndexFn = copyIndex

// copyIndexRetrying is copyIndex, asked again once after 50 ms when the index could
// not be opened for a permission or sharing reason: an agent's git replacing it
// at that moment, which on Windows refuses a reader for an instant. Anything
// else, a half-written copy included, is not asked about again, and the caller
// builds the index from HEAD instead.
func copyIndexRetrying(from, to string) error {
	err := copyIndexFn(from, to)
	if err != nil && transientOpenError(err) {
		time.Sleep(50 * time.Millisecond)
		err = copyIndexFn(from, to)
	}
	return err
}

// transientOpenError says whether err is a file being refused to a reader for a
// moment: a permission error, or on Windows a sharing or lock violation.
func transientOpenError(err error) bool {
	if errors.Is(err, fs.ErrPermission) {
		return true
	}
	var errno syscall.Errno
	return runtime.GOOS == "windows" && errors.As(err, &errno) && (errno == 32 || errno == 33)
}

// beforeAdd is called between listing the untracked repositories and staging, so
// that a test can change the tree in that gap.
var beforeAdd = func() {}

// realGitlinks lists the gitlinks (mode 160000) the checkout's own index holds,
// reading it and not writing it; any failure is an empty answer, which only means
// they are not kept.
func realGitlinks(ctx context.Context, dir string, env []string) map[string]bool {
	real, err := runEnvOut(ctx, dir, nil, "rev-parse", "--git-path", "index")
	if err != nil {
		return nil
	}
	if !filepath.IsAbs(real) {
		real = filepath.Join(dir, real)
	}
	var out bytes.Buffer
	realEnv := append(append([]string{}, env...), "GIT_INDEX_FILE="+real)
	if _, err := runToEnv(ctx, commandTimeout, dir, realEnv, nil, &out, "ls-files", "-s", "-z"); err != nil {
		return nil
	}
	links := map[string]bool{}
	for _, rec := range strings.Split(out.String(), "\x00") {
		if mode, rest, ok := strings.Cut(rec, " "); ok && mode == "160000" {
			if _, p, ok := strings.Cut(rest, "\t"); ok {
				links[p] = true
			}
		}
	}
	return links
}

// listWork lists what differs from the scratch index in the working tree: the
// files that are modified (or gone) and those that are untracked, and an
// untracked nested repository as one entry ending in a slash.
func listWork(ctx context.Context, dir string, env []string) ([]string, error) {
	var listed bytes.Buffer
	if _, err := runToEnv(ctx, commandTimeout, dir, env, nil, &listed, "ls-files", "--modified", "--others", "--exclude-standard", "-z"); err != nil {
		return nil, err
	}
	return strings.Split(listed.String(), "\x00"), nil
}

// maxSnapshotBytes and maxSnapshotFileBytes bound what a snapshot copies into
// the scratch object store: the untracked and modified files of a checkout may
// total 100 MB, and none may be larger than 25 MB. A build's output, a database
// dump or a video left in a checkout is not the few files two agents are
// fighting over, and git add would hash and write all of it into the scratch
// store every poll. Past either the checkout is not read (ErrTooBig).
const (
	maxSnapshotBytes     int64 = 100 << 20
	maxSnapshotFileBytes int64 = 25 << 20
)

// ErrTooBig is what Snapshot returns for a checkout whose untracked and modified
// files are too large to copy every refresh.
var ErrTooBig = errors.New("the changed files are too large to compare")

// checkSize adds up the sizes of the listed files, with a stat each and nothing
// read, and fails with ErrTooBig past the limits. A path that cannot be examined
// (gone since the listing), a folder and a link do not count: git add stores a
// link as its target's name.
func checkSize(dir string, listed []string, perFile, all int64) error {
	var total int64
	for _, p := range listed {
		if p == "" || strings.HasSuffix(p, "/") {
			continue
		}
		info, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(p)))
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if info.Size() > perFile {
			return fmt.Errorf("%w: %s is %d MB", ErrTooBig, p, info.Size()>>20)
		}
		if total += info.Size(); total > all {
			return fmt.Errorf("%w: more than %d MB in all", ErrTooBig, all>>20)
		}
	}
	return nil
}

// reposIn picks the folders holding a repository of their own that nobody
// registered out of what listWork says: git lists an untracked nested repository
// as one entry ending in a slash. Those in keep are the checkout's own, and are
// not in the list.
func reposIn(listed []string, keep map[string]bool) []string {
	var nested []string
	for _, p := range listed {
		if strings.HasSuffix(p, "/") && !keep[strings.TrimSuffix(p, "/")] {
			nested = append(nested, strings.TrimSuffix(p, "/"))
		}
	}
	return nested
}

// addAll stages everything in the scratch index, and fails on any error git makes.
//
// A folder holding a repository of its own that nobody registered cannot be
// staged: with commits git adds it as a gitlink, which is no change of this
// checkout's to share, and with none git refuses the whole add and stages nothing,
// exit 128. Such folders are found first (see reposIn) and left out of what
// is staged by pathspec, except the ones in keep, which the checkout's own index
// holds. What is staged is then staged with no allowance for errors: a file git
// cannot read, or a path it cannot add, makes the snapshot fail, and the radar
// treats the checkout as not read this time.
//
// Nothing here reads what git says, which is in the user's language and changes
// between versions, and long before the part that matters when the user's
// configuration has git warn about every file (core.autocrlf does). A repository
// that appears between the listing and the add is met by what the tree says:
//
//   - If git refuses the add, the folders are listed again. If there are more than
//     the first time the add is made once more with them left out; if not, the
//     failure stands, with what git said, warnings left out.
//   - If git adds it as a gitlink, the gitlinks the add made are looked for (see
//     dropLateRepos) and the ones the checkout does not itself hold are taken out.
//
// dir is the checkout's top level.
func addAll(ctx context.Context, dir string, env []string, keep map[string]bool) error {
	listed, err := listWork(ctx, dir, env)
	if err != nil {
		return err
	}
	if err := checkSize(dir, listed, maxSnapshotFileBytes, maxSnapshotBytes); err != nil {
		return err
	}
	nested := reposIn(listed, keep)
	for attempt := 0; ; attempt++ {
		beforeAdd()
		var err error
		if len(nested) == 0 {
			_, err = runToEnv(ctx, commandTimeout, dir, env, nil, &bytes.Buffer{}, "add", "-A")
		} else {
			// The pathspecs go on stdin, so that any number of folders fit.
			var specs bytes.Buffer
			specs.WriteString(".\x00")
			for _, p := range nested {
				specs.WriteString(":(exclude,literal)" + p + "\x00")
			}
			_, err = runToEnv(ctx, commandTimeout, dir, env, strings.NewReader(specs.String()), &bytes.Buffer{},
				"add", "-A", "--pathspec-from-file=-", "--pathspec-file-nul")
		}
		if err == nil {
			break
		}
		if attempt == 0 && ctx.Err() == nil {
			if again, lerr := listWork(ctx, dir, env); lerr == nil && grew(reposIn(again, keep), nested) {
				nested = reposIn(again, keep)
				continue
			}
		}
		return withoutWarnings(err)
	}
	return dropLateRepos(ctx, dir, env, keep)
}

// withoutWarnings is a failed git command's error with what git said in full, minus
// its warnings. A command that warned of every file before it failed (core.autocrlf
// does) had its own reason cut off by the five lines an error keeps.
func withoutWarnings(err error) error {
	var ce *commandError
	if !errors.As(err, &ce) || ce.full == "" {
		return err
	}
	var keep []string
	for _, l := range strings.Split(ce.full, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "warning:") {
			keep = append(keep, l)
		}
	}
	if len(keep) == 0 {
		return err
	}
	return &commandError{msg: "git add: " + firstLines(withoutHints(strings.Join(keep, "\n")), 5), err: ce.err, full: ce.full}
}

// grew says whether now has a folder that before does not.
func grew(now, before []string) bool {
	had := make(map[string]bool, len(before))
	for _, p := range before {
		had[p] = true
	}
	for _, p := range now {
		if !had[p] {
			return true
		}
	}
	return false
}

// dropLateRepos takes out of the scratch index the gitlinks that git add made of
// repositories that appeared after they were listed. They are the gitlinks new
// against HEAD that the checkout's own index does not hold (which are a repository
// staged on purpose, and kept) and that are not in keep. With none new, which is
// nearly always, it is one git process that prints nothing. Its cost grows with the
// number of new files. Listing the whole index instead was measured, on one machine
// with 20,000 new files, at about two thirds of this (this is about 1.5 times the cost
// of ls-files -s), but that needs HEAD's tree to tell the new ones, so it is not used.
func dropLateRepos(ctx context.Context, dir string, env []string, keep map[string]bool) error {
	var added bytes.Buffer
	if _, err := runToEnv(ctx, commandTimeout, dir, env, nil, &added, "diff-index", "--cached", "--raw", "-z", "--no-renames", "--diff-filter=A", "HEAD"); err != nil {
		return err
	}
	var links []string
	toks := strings.Split(added.String(), "\x00")
	for i := 0; i+1 < len(toks); i++ {
		if !strings.HasPrefix(toks[i], ":") {
			continue
		}
		f := strings.Fields(toks[i]) // :oldmode newmode oldsha newsha status
		if len(f) >= 2 && f[1] == "160000" && !keep[toks[i+1]] {
			links = append(links, toks[i+1])
		}
		i++
	}
	if len(links) == 0 {
		return nil
	}
	// Those the checkout holds itself are its own. If its index cannot be read the
	// answer is not known, and the snapshot is not made.
	own := map[string]bool{}
	real, err := runEnvOut(ctx, dir, nil, "rev-parse", "--git-path", "index")
	if err != nil {
		return err
	}
	if !filepath.IsAbs(real) {
		real = filepath.Join(dir, real)
	}
	args := []string{"ls-files", "-s", "-z", "--"}
	for _, p := range links {
		args = append(args, ":(literal)"+p)
	}
	var out bytes.Buffer
	realEnv := append(append([]string{}, env...), "GIT_INDEX_FILE="+real)
	if _, err := runToEnv(ctx, commandTimeout, dir, realEnv, nil, &out, args...); err != nil {
		return err
	}
	for _, rec := range strings.Split(out.String(), "\x00") {
		if mode, rest, ok := strings.Cut(rec, " "); ok && mode == "160000" {
			if _, p, ok := strings.Cut(rest, "\t"); ok {
				own[p] = true
			}
		}
	}
	var gone bytes.Buffer
	for _, p := range links {
		if !own[p] {
			gone.WriteString(p + "\x00")
		}
	}
	if gone.Len() == 0 {
		return nil
	}
	_, err = runToEnv(ctx, commandTimeout, dir, env, strings.NewReader(gone.String()), &bytes.Buffer{},
		"update-index", "--force-remove", "-z", "--stdin")
	return err
}
