package gitx

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
)

// ErrUnrelated is what ChooseBase returns when none of the candidate bases has
// any history in common with the checkout.
var ErrUnrelated = errors.New("no candidate base shares history with the checkout")

// Base is one branch work may be measured from, and the commit it was at.
type Base struct {
	Ref string // the full ref, "refs/heads/main"
	ID  string
}

// Bases are the branches the work in a repository could be measured from, in the
// order they are preferred when equally near, with what is needed to tell later
// that they have moved.
type Bases struct {
	List []Base
	// watch is every ref that resolved, with the commit it was at, including the
	// ones that name a commit another candidate already names: List has one entry
	// per commit, and a ref hidden behind another still moves the candidates when
	// it moves.
	watch []Base
	// runDir is a checkout of the repository to ask in. mainDir and mainHead are
	// the main worktree, and its HEAD when it was read, whether or not it is on a
	// branch: a detached main worktree that later gets one, or any main worktree
	// that moves, changes the candidates. Empty when it has no commit yet.
	runDir   string
	mainDir  string
	mainHead string
	// bareDir and bareHead are a bare repository's own folder and the commit its
	// HEAD named when it was read, "" if none: its HEAD may be pointed at another
	// branch, which the refs watched do not show.
	bareDir  string
	bareHead string
}

// NewBases makes Bases of commit ids, for a test.
func NewBases(ids ...string) *Bases {
	b := &Bases{}
	for _, id := range ids {
		b.List = append(b.List, Base{Ref: "refs/heads/" + id, ID: id})
	}
	b.watch = append(b.watch, b.List...)
	return b
}

// Key names the commits the candidates were at, for a cache that must change
// when any of them moves.
func (b *Bases) Key() string {
	ids := make([]string, len(b.List))
	for i, c := range b.List {
		ids[i] = c.ID
	}
	return strings.Join(ids, ",")
}

// Fresh reports whether every ref that resolved is still where it was, hidden
// ones included, and the main worktree's HEAD still where it was, with one git
// process. A ref that did not exist when the candidates were resolved, and is
// created later, is not noticed by this: it is by the next resolution. It is false, with
// no error, when git cannot resolve one of them any more, so that the caller
// looks again.
func (b *Bases) Fresh(ctx context.Context) (bool, error) {
	args := []string{"rev-parse"}
	dir := b.runDir
	if b.mainDir != "" {
		args = append(args, "HEAD")
		dir = b.mainDir
	}
	for _, c := range b.watch {
		args = append(args, c.Ref+"^{commit}")
	}
	out, err := runEnvOut(ctx, dir, nil, args...)
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, nil
	}
	if b.bareDir != "" {
		head, _ := revParse(ctx, b.bareDir, nil, "HEAD^{commit}")
		if head != b.bareHead {
			return false, nil
		}
	}
	got := strings.Split(out, "\n")
	want := make([]string, 0, len(args))
	if b.mainDir != "" {
		want = append(want, b.mainHead)
	}
	for _, c := range b.watch {
		want = append(want, c.ID)
	}
	if len(got) != len(want) {
		return false, nil
	}
	for i := range got {
		if strings.TrimSpace(got[i]) != want[i] {
			return false, nil
		}
	}
	return true, nil
}

// ResolveBases finds the branches work in the repository containing dir may be
// measured from: the branch the main worktree has checked out, the branch a bare
// repository's HEAD names, origin's default branch (as last recorded: it is not
// fetched), and the branches called main and master. Each is a full ref, so a tag
// of the same name is not one. A candidate that does not resolve is left out and
// ones at the same commit are one. It is ErrNoBase when there are none, and
// another error when git could not say: neither is "nothing to compare". ctx
// ends every git process it starts.
func ResolveBases(ctx context.Context, dir string) (*Bases, error) {
	list, err := listCtx(ctx, dir)
	if err != nil {
		return nil, err
	}
	b := &Bases{runDir: dir}
	var refs []string
	for _, w := range list {
		if w.Main && !w.Bare && !w.Prunable {
			b.mainDir, b.mainHead = w.Path, w.Head
			if !w.Detached && w.Branch != "" {
				refs = append(refs, "refs/heads/"+w.Branch)
			}
		}
	}
	for _, w := range list {
		if w.Bare {
			if ref, err := runEnvOut(ctx, w.Path, nil, "symbolic-ref", "-q", "HEAD"); err == nil && ref != "" {
				refs = append(refs, ref)
			}
			b.bareDir = w.Path
			b.bareHead, _ = revParse(ctx, w.Path, nil, "HEAD^{commit}")
		}
	}
	if ref, err := runEnvOut(ctx, dir, nil, "symbolic-ref", "-q", "refs/remotes/origin/HEAD"); err == nil && ref != "" {
		refs = append(refs, ref)
	}
	refs = append(refs, "refs/heads/main", "refs/heads/master")
	seen := map[string]bool{}
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id, err := revParse(ctx, dir, nil, ref+"^{commit}")
		if err != nil {
			if exitCode(err) == 1 {
				continue
			}
			return nil, err
		}
		b.watch = append(b.watch, Base{Ref: ref, ID: id})
		if !seen[id] {
			seen[id] = true
			b.List = append(b.List, Base{Ref: ref, ID: id})
		}
	}
	if len(b.List) == 0 {
		return nil, ErrNoBase
	}
	// A main worktree before its first commit has no HEAD to compare: it is
	// not watched, and a first commit there is noticed when another candidate
	// moves or the candidates are next resolved.
	if strings.Trim(b.mainHead, "0") == "" {
		b.mainDir = ""
	}
	return b, nil
}

// ChooseBase picks, of the candidates, the one to measure a checkout whose HEAD
// is head from: of those that share history with it, the nearest, which is the
// one with the fewest commits between where they meet and head. Ties go to the
// one preferred by ResolveBases. A candidate with no history in common is never
// chosen, however it ranks, so a stale or wrong default branch is passed over for
// a better one; with none left it is ErrUnrelated, or ErrShallow in a shallow
// clone, where history may be missing. head may be empty, and is then read.
func ChooseBase(ctx context.Context, dir, head string, b *Bases) (string, error) {
	if len(b.List) == 1 {
		return b.List[0].ID, nil
	}
	var err error
	if head == "" {
		if head, err = revParse(ctx, dir, nil, "HEAD"); err != nil {
			return "", err
		}
	}
	best, bestN := "", -1
	for _, c := range b.List {
		out, _, err := runCapture(ctx, commandTimeout, dir, "merge-base", head, c.ID)
		if err != nil {
			if exitCode(err) == 1 {
				continue
			}
			return "", err
		}
		n := 0
		if mb := strings.TrimSpace(out); mb != head {
			count, err := runEnvOut(ctx, dir, nil, "rev-list", "--count", mb+".."+head)
			if err != nil {
				return "", err
			}
			if n, err = strconv.Atoi(count); err != nil {
				return "", err
			}
		}
		if bestN < 0 || n < bestN {
			best, bestN = c.ID, n
		}
	}
	if best != "" {
		return best, nil
	}
	shallow, err := runEnvOut(ctx, dir, nil, "rev-parse", "--is-shallow-repository")
	if err != nil {
		return "", err
	}
	if shallow == "true" {
		return "", ErrShallow
	}
	return "", ErrUnrelated
}

// ResolveBase is the first of ResolveBases, for a caller that wants one commit.
func ResolveBase(ctx context.Context, dir string) (string, error) {
	b, err := ResolveBases(ctx, dir)
	if err != nil {
		return "", err
	}
	return b.List[0].ID, nil
}

// BaseOf is ResolveBase for a caller that does not need to tell "no base" from
// not being able to say: both are "".
func BaseOf(ctx context.Context, dir string) string {
	id, _ := ResolveBase(ctx, dir)
	return id
}

// NoLazyFetchMajor and NoLazyFetchMinor are the first git that honours GIT_NO_LAZY_FETCH, which the
// scratch commands are given so that a partial clone's missing objects are not
// fetched from its remote while the radar runs.
const NoLazyFetchMajor, NoLazyFetchMinor = 2, 44

// LazyFetchPossible reports whether the repository containing dir is a partial
// clone and this git is too old (before 2.44) to be told not to fetch the objects
// it lacks: the radar would then be able to contact the remote, and is not run
// there. A git whose version has not been read is taken as too old.
//
// A partial clone is one with extensions.partialclone set to a remote, or a
// remote whose promisor setting is true, or a pack with a .promisor file beside
// it: the values are read, not only the keys, so a remote with promisor false is
// not one. It fails closed: when the configuration cannot be read, the answer is
// that it may be a partial clone, with the error returned beside it so that the
// caller can say why.
func LazyFetchPossible(ctx context.Context, dir string) (bool, error) {
	partial, err := isPartialClone(ctx, dir)
	if err != nil {
		partial = true
	}
	if !partial {
		return false, nil
	}
	versionState.mu.Lock()
	major, minor := versionState.major, versionState.minor
	versionState.mu.Unlock()
	return major < NoLazyFetchMajor || (major == NoLazyFetchMajor && minor < NoLazyFetchMinor), err
}

// isPartialClone reads whether the repository is a partial clone.
func isPartialClone(ctx context.Context, dir string) (bool, error) {
	out, err := runEnvOut(ctx, dir, nil, "config", "--get", "extensions.partialclone")
	if err != nil && exitCode(err) != 1 {
		return false, err
	}
	if err == nil && strings.TrimSpace(out) != "" {
		return true, nil
	}
	out, err = runEnvOut(ctx, dir, nil, "config", "--type=bool", "--get-regexp", `^remote\..*\.promisor$`)
	if err != nil && exitCode(err) != 1 {
		return false, err
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasSuffix(strings.TrimSpace(line), " true") {
			return true, nil
		}
	}
	common, err := CommonDir(dir)
	if err != nil {
		return false, err
	}
	packs, _ := filepath.Glob(filepath.Join(common, "objects", "pack", "*.promisor"))
	return len(packs) > 0, nil
}
