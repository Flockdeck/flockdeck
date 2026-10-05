package workspace

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jmwri/flockdeck/internal/gitx"
	"github.com/jmwri/flockdeck/internal/radar"
	"github.com/jmwri/flockdeck/internal/store"
)

// PaneConflict is another pane whose work git would not merge with this
// pane's, and the paths it would stop on. It is what the header's conflict chip
// is made from. See internal/radar.
type PaneConflict struct {
	// With is the other pane's id.
	With  string
	Paths []string
}

// The radar's reads of git, as variables for the same reason gitStatus is:
// what a refresh asks of git, and how often, is what the tests check.
var (
	// radarEnabled is whether the radar runs: the setting is on, and git is
	// known to be new enough. A git whose version is not yet known is not
	// enough; the probe runs in the background (gitx.MergeTreeSupport) and the
	// next refresh sees its answer.
	radarEnabled = func() bool {
		if !store.LoadPrefs().ConflictRadar {
			return false
		}
		ok, _, known := gitx.MergeTreeSupport()
		return ok && known
	}
	radarCommon = gitx.CommonDir
	radarRoot   = gitx.Root
	radarBases  = gitx.ResolveBases
	radarFresh  = func(ctx context.Context, b *gitx.Bases) (bool, error) { return b.Fresh(ctx) }
	radarChoose = gitx.ChooseBase
	radarLazy   = gitx.LazyFetchPossible
	// radarDirMissing says a checkout's folder is gone, as opposed to git failing in it.
	radarDirMissing = func(dir string) bool { _, err := os.Stat(dir); return os.IsNotExist(err) }
	radarSnapshot   = gitx.Snapshot
	radarEngine     = func() *radar.Engine { return radar.NewEngine() }
	radarLogf       = log.Printf
	// radarSweep removes scratch directories a run that was killed left behind.
	radarSweep = func() { gitx.SweepScratch(time.Hour) }
)

// sweepScratchOnce makes the sweep once for the process, the first time the radar
// runs, and not under any lock: it lists and removes directories.
var sweepScratchOnce = new(sync.Once)

// radarDeadline is how long one repository's snapshots and merges have in a
// refresh before it gives up. Each snapshot is several git processes, so it is
// longer than gitDeadline, and longer than the fifteen seconds between
// refreshes: a refresh that finds the last one still running in a repository
// does nothing (radar.Engine.Begin), and a snapshot that ran out of time counts
// as unknown, so a slow repository is retried and keeps what it showed, where a
// short deadline would fail every cycle. A variable so a test need not wait it out.
var radarDeadline = 30 * time.Second

// radarBasesMaxAge is the longest the candidate bases are kept without being looked
// up again, whatever the freshness check says. The check watches the refs that
// resolved and the main worktree's HEAD (and a bare repository's HEAD), and so
// does not see a branch created later, a new main or master say: this bounds how
// long that can go unnoticed.
var radarBasesMaxAge = 10 * time.Minute

// radarNoBaseTTL is how long "no base branch was found" is believed.
var radarNoBaseTTL = 5 * time.Second

// radarCheckout is a checkout a refresh read the status of. Panes in different
// folders of one checkout are one checkout here, named by its top level.
type radarCheckout struct {
	key string // pathKey of cwd, the top level of the checkout
	cwd string
	st  gitx.Status
	// dirKey is pathKey of the directory the status was read in, which is what
	// a pane's Cwd is looked up by.
	dirKey string
}

// cleanMark records that a checkout with nothing uncommitted, on a commit,
// had nothing against a base: until its HEAD or the base moves, or it is
// edited, there is nothing to snapshot, and nothing is asked of git. commit is
// the full id of the commit, from the status.
type cleanMark struct{ commit, base string }

// choiceMark is the base chosen for a checkout on a commit, when the candidate
// bases were at the commits key names: the choice is the same until one of them
// moves or the checkout does.
type choiceMark struct{ commit, key, base string }

// basesMark is a repository's candidate bases, or the finding that it has none.
type basesMark struct {
	bases *gitx.Bases
	none  bool
	at    time.Time
}

// radarState is what the workspace keeps of the radar between refreshes, under
// gitMu: an engine for each repository, by gitx.CommonDir; which repository
// each checkout is in, since asking costs a git process; the base of each
// repository; and the checkouts known to be clean.
//
// gen counts the times the radar has been switched off. Each refresh notes the
// number it began under and its results are applied only while it is still the
// current one, so a refresh in flight when the radar is turned off cannot put
// its chips back after they have been taken down.
type radarState struct {
	on        bool
	gen       int
	engines   map[string]*radar.Engine
	repoOf    map[string]string // repository, by the key of a pane's own directory
	rootOf    map[string]string // the checkout's top level, by the same key
	roots     map[string]string // the top level as a path, by its own key
	bases     map[string]basesMark
	choices   map[string]choiceMark   // the base chosen for a checkout, by the key of its top level
	lazy      map[string]bool         // whether a repository is a partial clone to leave alone
	lazyErr   map[string]bool         // repositories whose partial-clone check failed, once logged
	missing   map[string]missMark     // misses of a checkout whose folder is gone, by its top level
	timeouts  map[string]*timeoutMark // checkouts whose git commands keep timing out
	errLogged map[string]time.Time    // when a checkout's snapshot failure was last logged
	clean     map[string]cleanMark
}

// radarEnable notes that a refresh is running with the radar on, and returns
// the generation its results belong to.
func (w *Workspace) radarEnable() int {
	w.gitMu.Lock()
	defer w.gitMu.Unlock()
	w.radar.on = true
	return w.radar.gen
}

// current reports whether gen is the generation the radar is on in.
func (w *Workspace) radarCurrent(gen int) bool {
	w.gitMu.Lock()
	defer w.gitMu.Unlock()
	return w.radar.on && w.radar.gen == gen
}

// radarWorthy says whether a checkout is in a state with something coherent to
// snapshot: on a branch, not detached, mid-rebase or mid-bisect, or before its
// first commit, and not with so many files changed that it is not the few two
// agents are fighting over. A merge, cherry-pick, revert or am in progress is
// not in the status, and is found by the snapshot (gitx.ErrMidOperation).
func radarWorthy(st gitx.Status) bool {
	return st.Branch != "" && !st.Detached && st.Operation == "" && !st.Unborn &&
		st.Dirty+st.Untracked <= gitx.MaxSnapshotFiles
}

// refreshRadar is the part of a refresh after every checkout has been read: it
// groups the checkouts by repository, snapshots those in a state worth it, and
// gives each repository's engine the result. A pane's Conflicts are then set
// from what the engine reports.
//
// Checkouts are compared only with others read in the same refresh, since a
// snapshot is made under a scratch directory that is gone when the refresh
// ends; a refresh of the panes on screen leaves the rest as they were (see
// radar.Engine.Update). The snapshots take the same slots as the status reads,
// so the radar does not add to how many git processes run at once.
func (w *Workspace) refreshRadar(apply func(func()), seen []radarCheckout, gen int) {
	sweepScratchOnce.Do(radarSweep)
	groups := map[string][]radarCheckout{}
	done := map[string]bool{}
	for _, c := range seen {
		common, top, ok := w.radarRepo(c)
		if !ok || done[top.key] {
			continue
		}
		// Panes in two folders of one checkout are read twice by the status
		// and are one checkout to the radar: it is snapshotted once, and its
		// answer applies to every pane in it.
		done[top.key] = true
		groups[common] = append(groups[common], top)
	}
	var wg sync.WaitGroup
	for common, members := range groups {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// This goroutine is the radar's own and nothing above it would
			// catch what it did not.
			defer func() {
				if r := recover(); r != nil {
					radarLogf("conflict radar: a refresh of %s panicked: %v", common, r)
				}
			}()
			// The conflicts are read when the result is applied, not when it
			// was made, so two refreshes that apply out of order still leave
			// what the engine last knew.
			if engine := w.radarOf(gen, common, members); engine != nil {
				apply(func() { w.applyConflicts(gen, common, engine) })
			}
		}()
	}
	wg.Wait()
	w.pruneRadar(gen)
}

// radarRepo says which repository c is in and where its top level is, from what
// was learned about it before when there is something.
func (w *Workspace) radarRepo(c radarCheckout) (common string, top radarCheckout, ok bool) {
	w.gitMu.Lock()
	common, known := w.radar.repoOf[c.dirKey]
	rootKey := w.radar.rootOf[c.dirKey]
	w.gitMu.Unlock()
	if known && rootKey != "" {
		return common, radarCheckout{key: rootKey, cwd: w.rootDirOf(rootKey, c.cwd), st: c.st, dirKey: c.dirKey}, true
	}
	common, err := radarCommon(c.cwd)
	if err != nil {
		return "", radarCheckout{}, false
	}
	root, err := radarRoot(c.cwd)
	if err != nil {
		return "", radarCheckout{}, false
	}
	rootKey = pathKey(root)
	w.gitMu.Lock()
	if w.radar.repoOf == nil {
		w.radar.repoOf = map[string]string{}
		w.radar.rootOf = map[string]string{}
	}
	w.radar.repoOf[c.dirKey] = common
	w.radar.rootOf[c.dirKey] = rootKey
	if w.radar.roots == nil {
		w.radar.roots = map[string]string{}
	}
	w.radar.roots[rootKey] = root
	w.gitMu.Unlock()
	return common, radarCheckout{key: rootKey, cwd: root, st: c.st, dirKey: c.dirKey}, true
}

// rootDirOf is the top level of the checkout named by key as it was spelt when
// it was found, or fallback.
func (w *Workspace) rootDirOf(key, fallback string) string {
	w.gitMu.Lock()
	defer w.gitMu.Unlock()
	if d, ok := w.radar.roots[key]; ok {
		return d
	}
	return fallback
}

// radarEngineOf returns the engine of a repository, made if it has none, or nil
// when gen is no longer the generation the radar is on in.
func (w *Workspace) radarEngineOf(gen int, common string) (*radar.Engine, chan struct{}) {
	w.gitMu.Lock()
	defer w.gitMu.Unlock()
	if !w.radar.on || w.radar.gen != gen {
		return nil, nil
	}
	if w.radar.engines == nil {
		w.radar.engines = map[string]*radar.Engine{}
	}
	engine := w.radar.engines[common]
	if engine == nil {
		engine = radarEngine()
		w.radar.engines[common] = engine
	}
	return engine, w.gitSlots
}

// radarBasesOf is the candidate bases of the repository, from dir. They are kept
// until one of them moves or the main worktree changes branch, which one git
// process per refresh finds out (gitx.Bases.Fresh), so a commit to the base
// branch is noticed at the next refresh. That none could be found
// (gitx.ErrNoBase) is kept for radarNoBaseTTL, so that a repository without one
// is not searched for it every refresh, and only on a refresh, about every
// fifteen seconds. A base git could not be asked for is an error, asked for
// again next time, and no snapshot is made without one, since work measured from
// the wrong base looks like no work at all. Nothing is kept once the radar has
// been switched off since gen: a result from before cannot outlive that.
func (w *Workspace) radarBasesOf(ctx context.Context, gen int, common, dir string) (*gitx.Bases, error) {
	w.gitMu.Lock()
	m, ok := w.radar.bases[common]
	w.gitMu.Unlock()
	if ok && m.none && time.Since(m.at) < radarNoBaseTTL {
		return nil, gitx.ErrNoBase
	}
	if ok && !m.none && time.Since(m.at) < radarBasesMaxAge {
		fresh, err := radarFresh(ctx, m.bases)
		if err == nil && fresh {
			return m.bases, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	b, err := radarBases(ctx, dir)
	if err != nil && !errors.Is(err, gitx.ErrNoBase) {
		return nil, err
	}
	w.gitMu.Lock()
	defer w.gitMu.Unlock()
	if w.radar.on && w.radar.gen == gen {
		if w.radar.bases == nil {
			w.radar.bases = map[string]basesMark{}
		}
		w.radar.bases[common] = basesMark{bases: b, none: err != nil, at: time.Now()}
	}
	return b, err
}

// radarBaseFor is the base to measure c from: the nearest candidate that shares
// history with it (gitx.ChooseBase), remembered for the commit it is on while the
// candidates stay where they are. gitx.ErrUnrelated says none does.
func (w *Workspace) radarBaseFor(ctx context.Context, gen int, c radarCheckout, bs *gitx.Bases) (string, error) {
	w.gitMu.Lock()
	m, ok := w.radar.choices[c.key]
	w.gitMu.Unlock()
	if ok && c.st.Commit != "" && m.commit == c.st.Commit && m.key == bs.Key() {
		return m.base, nil
	}
	id, err := radarChoose(ctx, c.cwd, c.st.Commit, bs)
	if err != nil {
		return "", err
	}
	if c.st.Commit != "" {
		w.gitMu.Lock()
		if w.radar.on && w.radar.gen == gen {
			if w.radar.choices == nil {
				w.radar.choices = map[string]choiceMark{}
			}
			w.radar.choices[c.key] = choiceMark{commit: c.st.Commit, key: bs.Key(), base: id}
		}
		w.gitMu.Unlock()
	}
	return id, nil
}

// radarLazyFetch says whether the repository is a partial clone on a git too old
// to be told not to fetch what it lacks, found once and kept. The radar is not
// run there: it could contact the remote. It says so in the log, once.
//
// It fails closed. When the configuration cannot be read the answer is the one
// for a partial clone, it is not kept so that the next refresh reads it again,
// and the reason is logged once per repository.
func (w *Workspace) radarLazyFetch(ctx context.Context, common, dir string) bool {
	w.gitMu.Lock()
	lazy, known := w.radar.lazy[common]
	w.gitMu.Unlock()
	if known {
		return lazy
	}
	lazy, err := radarLazy(ctx, dir)
	w.gitMu.Lock()
	if err != nil {
		logged := w.radar.lazyErr[common]
		if w.radar.lazyErr == nil {
			w.radar.lazyErr = map[string]bool{}
		}
		w.radar.lazyErr[common] = true
		w.gitMu.Unlock()
		if !logged {
			radarLogf("conflict radar: could not tell whether %s is a partial clone (%v), so on a git before 2.44 it is treated as one", common, err)
		}
		return lazy
	}
	delete(w.radar.lazyErr, common)
	if w.radar.lazy == nil {
		w.radar.lazy = map[string]bool{}
	}
	w.radar.lazy[common] = lazy
	w.gitMu.Unlock()
	if lazy {
		radarLogf("conflict radar: %s is a partial clone, and this git cannot be told not to fetch what it lacks from the remote, so the radar is not run there", common)
	}
	return lazy
}

// radarOf runs one repository's part of a refresh and returns its engine, or
// nil when nothing was done: another refresh is still working in it, or the
// radar was switched off.
func (w *Workspace) radarOf(gen int, common string, members []radarCheckout) (result *radar.Engine) {
	engine, slots := w.radarEngineOf(gen, common)
	if engine == nil {
		return nil
	}
	// Before anything that costs: a refresh that finds another still running in
	// this repository does none of it, rather than doing it and being refused.
	end, ok := engine.Begin()
	if !ok {
		return nil
	}
	defer end()
	// An engine let go of between being found and being claimed is nobody's.
	w.gitMu.Lock()
	registered := w.radar.engines[common] == engine
	w.gitMu.Unlock()
	if !registered {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), radarDeadline)
	defer cancel()
	// Whatever the merge does, it does under this recover: it runs git and a
	// parser on data from outside.
	defer func() {
		if r := recover(); r != nil {
			radarLogf("conflict radar: working in %s panicked: %v", common, r)
			result = nil
		}
	}()
	if w.radarLazyFetch(ctx, common, members[0].cwd) {
		return nil
	}

	var worthy []radarCheckout
	in := make([]radar.Input, 0, len(members))
	for _, c := range members {
		if radarWorthy(c.st) {
			worthy = append(worthy, c)
		} else {
			in = append(in, radar.Input{ID: c.key})
		}
	}
	dir := members[0].cwd
	// The scratch directory is made when something is to be snapshotted, and
	// kept until the merges that read it are done.
	var scratch *gitx.Scratch
	defer func() {
		if scratch == nil {
			return
		}
		if err := scratch.Close(); err != nil {
			radarLogf("conflict radar: could not remove its scratch directory %s: %v", scratch.Root(), err)
		}
	}()
	// A checkout alone has nothing to be paired with in this refresh, and is
	// snapshotted only when the engine holds pairs it may be in, so that they
	// can be dropped once its files no longer meet theirs.
	if len(worthy) >= 2 || (len(worthy) == 1 && engine.Tracking()) {
		dir = worthy[0].cwd
		bs, err := w.radarBasesOf(ctx, gen, common, dir)
		if err != nil {
			// Not knowing the base says nothing about the pairs.
			in = append(in, unknownInputs(worthy)...)
		} else {
			var todo []radarCheckout
			for _, c := range worthy {
				if w.radarKnownClean(c, bs) {
					in = append(in, radar.Input{ID: c.key})
				} else {
					todo = append(todo, c)
				}
			}
			if len(todo) >= 2 || (len(todo) == 1 && engine.Tracking()) {
				var err error
				if scratch, err = gitx.NewScratch(common); err != nil {
					radarLogf("conflict radar: no scratch directory: %v", err)
					scratch = nil
					in = append(in, unknownInputs(todo)...)
				} else {
					in = append(in, w.radarSnapshots(ctx, gen, scratch, slots, bs, todo)...)
				}
			}
		}
	}
	engine.Update(ctx, dir, scratch, in)
	return engine
}

// radarKnownClean says whether c is a checkout with nothing uncommitted that
// was found to have nothing against the base chosen for it, on the commit it is
// on still. That costs no git process, where finding it out is several.
func (w *Workspace) radarKnownClean(c radarCheckout, bs *gitx.Bases) bool {
	if c.st.HasChanges() || c.st.Commit == "" {
		return false
	}
	w.gitMu.Lock()
	defer w.gitMu.Unlock()
	ch, ok := w.radar.choices[c.key]
	if !ok || ch.commit != c.st.Commit || ch.key != bs.Key() {
		return false
	}
	m, ok := w.radar.clean[c.key]
	return ok && m.commit == c.st.Commit && m.base == ch.base
}

// radarSnapshots snapshots the checkouts, a few at a time under the workspace's
// git slots, and says what each came to:
//
//   - a snapshot with something in it is ready for pairing;
//   - one with nothing against the base, a checkout mid-operation or past the
//     file cap is known to have nothing to pair, and any pair it was in goes;
//   - any other failure, running out of time, or a base with no history in common
//     with the checkout, is unknown: nothing was checked, so the pairs are kept
//     as they were and the checkout is never marked clean.
func (w *Workspace) radarSnapshots(ctx context.Context, gen int, scratch *gitx.Scratch, slots chan struct{}, bs *gitx.Bases, todo []radarCheckout) []radar.Input {
	results := make([]radar.Input, len(todo))
	var wg sync.WaitGroup
	for i, c := range todo {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = radar.Input{ID: c.key, Unknown: true}
			// One checkout's snapshot going wrong must not take the window down.
			defer func() {
				if r := recover(); r != nil {
					radarLogf("conflict radar: snapshot of %s panicked: %v", c.cwd, r)
					results[i] = radar.Input{ID: c.key, Unknown: true}
				}
			}()
			if w.radarBackedOff(c) {
				return
			}
			slots <- struct{}{}
			defer func() { <-slots }()
			// How long this checkout itself has been running is what the radar's time
			// running out is held against it for: one that only just got a slot did not
			// use it.
			started := time.Now()
			base, err := w.radarBaseFor(ctx, gen, c, bs)
			if err != nil {
				w.radarLogUnknown(ctx, c, err, time.Since(started), "choosing its base")
				return
			}
			snap, err := radarSnapshot(ctx, c.cwd, scratch, base, c.st.HasChanges())
			switch {
			case err == nil && !snap.Empty:
				w.radarSnapshotWorked(c)
				results[i] = radar.Input{ID: c.key, Ready: true, Commit: snap.Commit, Tree: snap.Tree, Head: snap.Head, Paths: snap.Paths, Dirs: snap.Dirs}
			case err == nil && snap.Unrelated:
				// Nothing in common with the base chosen: nothing was checked.
			case err == nil, errors.Is(err, gitx.ErrTooManyFiles), errors.Is(err, gitx.ErrMidOperation):
				w.radarSnapshotWorked(c)
				results[i] = radar.Input{ID: c.key}
				// A clean checkout with nothing against the base, or with too many files
				// against it, stays so until its HEAD or the base moves: it is not
				// snapshotted again every refresh to find that out.
				if (err == nil || errors.Is(err, gitx.ErrTooManyFiles)) && !c.st.HasChanges() {
					w.markClean(gen, c, base)
				}
			default:
				// Unknown, and not silently: say why, once in a while, so that a
				// checkout the radar cannot read is not a mystery.
				w.radarLogUnknown(ctx, c, err, time.Since(started), "copying it")
			}
		}()
	}
	wg.Wait()
	return results
}

func unknownInputs(cs []radarCheckout) []radar.Input {
	out := make([]radar.Input, len(cs))
	for i, c := range cs {
		out[i] = radar.Input{ID: c.key, Unknown: true}
	}
	return out
}

func (w *Workspace) markClean(gen int, c radarCheckout, base string) {
	if c.st.Commit == "" {
		return
	}
	w.gitMu.Lock()
	defer w.gitMu.Unlock()
	if !w.radar.on || w.radar.gen != gen {
		return
	}
	if w.radar.clean == nil {
		w.radar.clean = map[string]cleanMark{}
	}
	w.radar.clean[c.key] = cleanMark{commit: c.st.Commit, base: base}
}

// pruneRadar lets go of what belongs to checkouts no pane is in any more: their
// pairs, their engines once nothing is left in them, and what was learned of
// where they are.
func (w *Workspace) pruneRadar(gen int) {
	w.mu.RLock()
	cwdKeys := map[string]bool{}
	for _, p := range w.panes {
		if p.Cwd != "" {
			cwdKeys[pathKey(p.Cwd)] = true
		}
	}
	w.mu.RUnlock()

	w.gitMu.Lock()
	if !w.radar.on || w.radar.gen != gen {
		w.gitMu.Unlock()
		return
	}
	// The checkouts the live panes are in, by top level.
	live := map[string]bool{}
	for dirKey := range cwdKeys {
		if root, ok := w.radar.rootOf[dirKey]; ok {
			live[root] = true
		}
	}
	for dirKey := range w.radar.repoOf {
		if !cwdKeys[dirKey] {
			delete(w.radar.repoOf, dirKey)
			delete(w.radar.rootOf, dirKey)
		}
	}
	// What was learned of a repository goes when no live pane is in it.
	commons := map[string]bool{}
	for _, common := range w.radar.repoOf {
		commons[common] = true
	}
	for common := range w.radar.bases {
		if !commons[common] {
			delete(w.radar.bases, common)
		}
	}
	for common := range w.radar.lazy {
		if !commons[common] {
			delete(w.radar.lazy, common)
		}
	}
	for common := range w.radar.lazyErr {
		if !commons[common] {
			delete(w.radar.lazyErr, common)
		}
	}
	for key := range w.radar.roots {
		if !live[key] {
			delete(w.radar.roots, key)
		}
	}
	for key := range w.radar.clean {
		if !live[key] {
			delete(w.radar.clean, key)
		}
	}
	for key := range w.radar.choices {
		if !live[key] {
			delete(w.radar.choices, key)
		}
	}
	for key := range w.radar.errLogged {
		if !live[key] {
			delete(w.radar.errLogged, key)
		}
	}
	for key := range w.radar.timeouts {
		if !live[key] {
			delete(w.radar.timeouts, key)
		}
	}
	for key := range w.radar.missing {
		if !live[key] {
			delete(w.radar.missing, key)
		}
	}
	engines := make(map[string]*radar.Engine, len(w.radar.engines))
	for common, e := range w.radar.engines {
		engines[common] = e
	}
	w.gitMu.Unlock()

	for common, e := range engines {
		// An engine another refresh is working in is left to it: what it
		// holds is about to change, and it must not be dropped from under it.
		end, ok := e.Begin()
		if !ok {
			continue
		}
		e.Retain(live)
		if e.Empty() {
			w.gitMu.Lock()
			if w.radar.engines[common] == e {
				delete(w.radar.engines, common)
			}
			w.gitMu.Unlock()
		}
		end()
	}
}

// radarOff takes the radar's chips down when it has been switched off since the
// last refresh, and forgets what it knew. A refresh that began before, and is
// still running, finds the generation moved on and applies nothing.
func (w *Workspace) radarOff(apply func(func())) {
	w.gitMu.Lock()
	was := w.radar.on
	w.radar = radarState{gen: w.radar.gen + 1}
	w.gitMu.Unlock()
	if was {
		apply(func() { w.clearConflicts() })
	}
}

// clearConflicts takes the conflicts off every pane.
func (w *Workspace) clearConflicts() {
	changed := false
	w.mu.Lock()
	for _, p := range w.panes {
		if p.Conflicts != nil {
			p.Conflicts = nil
			changed = true
		}
	}
	w.mu.Unlock()
	if changed {
		w.wake()
	}
}

// applyConflicts gives every pane in the repository common the pairs the engine
// holds for it, replacing what it had, unless the radar has been switched off
// since the refresh that made them began. It runs on the workspace's goroutine.
func (w *Workspace) applyConflicts(gen int, common string, engine *radar.Engine) {
	if !w.radarCurrent(gen) {
		return
	}
	conflicts := engine.Conflicts()
	w.gitMu.Lock()
	repoOf := make(map[string]string, len(w.radar.repoOf))
	rootOf := make(map[string]string, len(w.radar.rootOf))
	for k, v := range w.radar.repoOf {
		repoOf[k] = v
		rootOf[k] = w.radar.rootOf[k]
	}
	w.gitMu.Unlock()

	changed := false
	w.mu.Lock()
	// The panes of each checkout, by the key of its top level. A pane in a
	// folder the radar has not been told of yet is in none.
	byKey := map[string][]*Pane{}
	for _, p := range w.panes {
		if p.Cwd == "" {
			continue
		}
		dirKey := pathKey(p.Cwd)
		if repoOf[dirKey] != common {
			continue
		}
		byKey[rootOf[dirKey]] = append(byKey[rootOf[dirKey]], p)
	}
	for key, panes := range byKey {
		for _, p := range panes {
			var next []PaneConflict
			for _, c := range conflicts {
				other := c.A
				switch key {
				case c.A:
					other = c.B
				case c.B:
				default:
					continue
				}
				for _, q := range byKey[other] {
					if q.ID != p.ID {
						next = append(next, PaneConflict{With: q.ID, Paths: c.Paths})
					}
				}
			}
			sort.Slice(next, func(i, j int) bool { return next[i].With < next[j].With })
			if !sameConflicts(p.Conflicts, next) {
				p.Conflicts = next
				changed = true
			}
		}
	}
	w.mu.Unlock()
	if changed {
		w.wake()
	}
}

func sameConflicts(a, b []PaneConflict) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].With != b[i].With || len(a[i].Paths) != len(b[i].Paths) {
			return false
		}
		for j := range a[i].Paths {
			if a[i].Paths[j] != b[i].Paths[j] {
				return false
			}
		}
	}
	return true
}

// radarMissingAfter is how many refreshes, at least radarMissingGap apart, a
// checkout's top-level folder must be found gone, with git unable to read the
// folder a pane is in, before the radar lets go of what it knew about it: one is a
// drive coming back or a folder renamed for a moment, three are not.
const radarMissingAfter = 3

// radarMissingGap is how far apart two misses must be to count as two, so that a
// burst of refreshes asked for out of turn is one miss.
var radarMissingGap = 5 * time.Second

// missMark is the misses counted of a checkout, and when the last was.
type missMark struct {
	n  int
	at time.Time
}

// radarSeen notes that the checkout the folder is in was read.
func (w *Workspace) radarSeen(dirKey string) {
	w.gitMu.Lock()
	delete(w.radar.missing, w.radar.rootOf[dirKey])
	w.gitMu.Unlock()
}

// radarMissing notes a refresh at which the folder a pane is in could not be read.
// It counts only when the checkout's top-level folder is gone: a subfolder deleted
// from a checkout that is there says nothing about the checkout. Misses are
// counted by checkout, and each must be radarMissingGap after the last. At
// radarMissingAfter of them, and at every refresh after, the checkout's pairs and
// sightings are dropped, and with them the chips on the panes it conflicted with,
// which would otherwise stay while the files they recorded still met. A refresh
// that cannot drop them because another is working in the repository, or does not
// yet know the checkout, tries again at the next.
func (w *Workspace) radarMissing(apply func(func()), gen int, dirKey string) {
	w.gitMu.Lock()
	if !w.radar.on || w.radar.gen != gen {
		w.gitMu.Unlock()
		return
	}
	rootKey := w.radar.rootOf[dirKey]
	common := w.radar.repoOf[dirKey]
	rootDir := w.radar.roots[rootKey]
	engine := w.radar.engines[common]
	w.gitMu.Unlock()
	if rootKey == "" || rootDir == "" || !radarDirMissing(rootDir) {
		return
	}
	w.gitMu.Lock()
	if w.radar.missing == nil {
		w.radar.missing = map[string]missMark{}
	}
	m := w.radar.missing[rootKey]
	if m.n == 0 || time.Since(m.at) >= radarMissingGap {
		m.n++
		m.at = time.Now()
		w.radar.missing[rootKey] = m
	}
	n := m.n
	w.gitMu.Unlock()
	if n < radarMissingAfter || engine == nil {
		return
	}
	if !engine.Forget(rootKey) {
		return
	}
	apply(func() { w.applyConflicts(gen, common, engine) })
}

// radarErrLogEvery is how often a checkout's snapshot failure is logged. A
// variable so a test need not wait it out.
var radarErrLogEvery = 10 * time.Minute

// radarTimeoutsBeforeBackoff is how many git commands in a row timing out on one
// checkout make the radar leave it alone, for radarBackoffStart, then twice as long
// each time up to radarBackoffMax. A checkout that does that costs the commands'
// whole deadline (20 seconds) every refresh, and is most often one with a very
// large untracked file in it, a video or a checkpoint. A snapshot that gets
// through starts the count again.
const radarTimeoutsBeforeBackoff = 3

var (
	radarBackoffStart = 10 * time.Minute
	radarBackoffMax   = 2 * time.Hour
)

// radarMinRun is how long a checkout's own snapshot must have been running when the
// radar's time runs out for that to be held against it: the ones queued behind it, and
// the ones that only just began, are not slow themselves. radarStrikesExpire is how
// long without a timeout it takes for a checkout's strikes to be forgotten.
var (
	radarMinRun        = 15 * time.Second
	radarStrikesExpire = 24 * time.Hour
)

// timeoutMark is the timeouts counted of a checkout and how long it is left alone.
type timeoutMark struct {
	n     int
	step  time.Duration
	until time.Time
	// last is when the last timeout was counted: a checkout that has had none for a
	// day starts again.
	last time.Time
	// sig is what the checkout's status said when the count was last added to: its
	// commit and its counts of changed and new files. A checkout that no longer says
	// the same has changed, and is tried again.
	sig string
}

// radarSig is a checkout's commit and counts of changed and new files, the part of
// its state a back-off is kept for.
func radarSig(st gitx.Status) string {
	return fmt.Sprintf("%s/%d/%d", st.Commit, st.Dirty, st.Untracked)
}

// radarBackedOff says whether the checkout is being left alone after commands that
// kept timing out.
func (w *Workspace) radarBackedOff(c radarCheckout) bool {
	w.gitMu.Lock()
	defer w.gitMu.Unlock()
	m := w.radar.timeouts[c.key]
	if m != nil && m.sig != radarSig(c.st) {
		// Its HEAD or its files changed: the skip is over and it is tried again at this
		// refresh. Its strikes and its step are kept. So a quiet checkout that is always
		// slow is skipped for 10, 20, 40, 80 and then 120 minutes, and one whose counts
		// keep changing is tried at every refresh and pays up to the radar's 30 s each
		// time; its strikes only raise the step for when it stays quiet.
		m.sig = radarSig(c.st)
		m.until = time.Time{}
		return false
	}
	return m != nil && time.Now().Before(m.until)
}

// radarSnapshotWorked starts the count of timeouts again.
func (w *Workspace) radarSnapshotWorked(c radarCheckout) {
	w.gitMu.Lock()
	delete(w.radar.timeouts, c.key)
	w.gitMu.Unlock()
}

// radarLogUnknown says why a checkout could not be snapshotted, with the first line
// of what git said, at most once every radarErrLogEvery for each checkout. phase is
// what the radar was doing for it.
//
// What the radar did not mean to end, or that was not this checkout's doing, is not
// counted or said: the radar being cancelled, and a checkout whose turn came after
// the radar's time had already run out, and a checkout that had been running for
// under radarMinRun (15 s) when it did. What is: a git command that gave up at its
// own deadline, and the radar's own deadline running out while this checkout's
// snapshot, which had been running for radarMinRun or more, was the one running. Both cost the checkout's whole time on every refresh
// and show no chip, so three in a row leave it alone for a while (see
// radarTimeoutsBeforeBackoff). gitx's timeout error also reads as a deadline, which
// is why ctx is asked and not the error alone.
func (w *Workspace) radarLogUnknown(ctx context.Context, c radarCheckout, err error, ranFor time.Duration, phase string) {
	own := ctx.Err() == context.DeadlineExceeded // the radar's own time ran out
	if ctx.Err() != nil && !(own && ranFor >= radarMinRun) {
		return
	}
	var backoff time.Duration
	if errors.Is(err, context.DeadlineExceeded) || own {
		w.gitMu.Lock()
		if w.radar.timeouts == nil {
			w.radar.timeouts = map[string]*timeoutMark{}
		}
		m := w.radar.timeouts[c.key]
		if m == nil {
			m = &timeoutMark{}
			w.radar.timeouts[c.key] = m
		}
		m.sig = radarSig(c.st)
		if !m.last.IsZero() && time.Since(m.last) > radarStrikesExpire {
			m.n, m.step = 0, 0
		}
		m.last = time.Now()
		m.n++
		if m.n >= radarTimeoutsBeforeBackoff {
			m.n = 0
			if m.step == 0 {
				m.step = radarBackoffStart
			} else {
				m.step = min(m.step*2, radarBackoffMax)
			}
			m.until = time.Now().Add(m.step)
			backoff = m.step
		}
		w.gitMu.Unlock()
	}
	first, _, _ := strings.Cut(err.Error(), "\n")
	if backoff > 0 {
		why := "git took longer than 20 s"
		if own {
			why = fmt.Sprintf("it was still being copied (%s) when the radar's %d s ran out", phase, int(radarDeadline.Seconds()))
		}
		radarLogf("conflict radar: %s is skipped for %d minutes: %s (large untracked files?): %s", c.cwd, int(backoff.Minutes()), why, first)
		return
	}
	w.gitMu.Lock()
	if w.radar.errLogged == nil {
		w.radar.errLogged = map[string]time.Time{}
	}
	if at, ok := w.radar.errLogged[c.key]; ok && time.Since(at) < radarErrLogEvery {
		w.gitMu.Unlock()
		return
	}
	w.radar.errLogged[c.key] = time.Now()
	w.gitMu.Unlock()
	if own {
		radarLogf("conflict radar: %s was still being copied (%s) when the radar's %d s ran out, so it is not compared this time: %s", c.cwd, phase, int(radarDeadline.Seconds()), first)
		return
	}
	radarLogf("conflict radar: could not snapshot %s, so it is not compared this time: %s", c.cwd, first)
}
