package radar

import (
	"container/list"
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jmwri/flockdeck/internal/gitx"
)

// Input is one checkout as the engine is told of it for a refresh.
type Input struct {
	// ID names the checkout, for the caller to tell the pair it is in. It has
	// to be the same from one refresh to the next.
	ID string
	// Ready says the checkout has work to compare. A checkout that is clean, or
	// has nothing against the base branch, or is detached, mid-rebase or
	// unborn, is not ready, and any pair it was in is dropped.
	Ready bool
	// Unknown says the checkout could not be read this time -- its snapshot
	// failed or ran out of time -- which is not the same as having nothing to
	// compare. The pairs it is in, and how many refreshes each has conflicted
	// in, are left exactly as they were.
	Unknown bool
	// Commit, Tree, Head and Paths are what gitx.Snapshot made, for a Ready
	// input.
	Commit string
	Tree   string
	Head   string
	Paths  []string
	// Dirs are directories a file of Paths was moved out of: a path another
	// checkout has under one meets this one, though no path is in both.
	Dirs []string
}

// Conflict is a pair of checkouts whose work git cannot merge.
type Conflict struct {
	A, B  string
	Paths []string
}

// confirmAfter is how many refreshes in a row a pair has to conflict in before
// it is reported. An agent half way through an edit can conflict for a few
// seconds and then not, and a chip that comes and goes teaches people to
// ignore it.
const confirmAfter = 2

// Engine holds what the radar knows about one repository between refreshes.
// Make one per repository (gitx.CommonDir), and give each refresh to Update.
type Engine struct {
	// run is held for the length of an Update, so a refresh that finds one
	// still going leaves it to finish rather than starting a second behind it.
	run sync.Mutex

	mu    sync.Mutex
	cache map[treePair]*list.Element // of *entry, newest merge asked for at the front
	order *list.List
	pairs map[pair]*streak
	// last is the latest ready Input of each checkout, so that a pair whose
	// other half is not in a refresh can still be dropped once the paths of
	// the half that is no longer meet it.
	last map[string]Input

	// predict and now are variables for the tests: how many merges are asked
	// for, and how long apart refreshes seem to be, are what they check.
	predict Predictor
	now     func() time.Time
	// minGap is how long after the last refresh that counted a conflict the
	// next one may count again. Refreshes are asked for out of turn, by a
	// commit or a pane opening, and two of them a moment apart are the same
	// moment as far as an edit in progress is concerned.
	minGap time.Duration
	// maxCached is how many merges are kept: maxCached unless a test says less.
	maxCached int
}

type pair struct{ a, b string }

type treePair struct{ a, b string }

// entry is a merge in the cache.
type entry struct {
	key treePair
	v   verdict
}

type verdict struct {
	paths    []string
	conflict bool
}

// streak is a pair that conflicts: the paths, how many refreshes in a row have
// found it so, and when the last of them counted.
type streak struct {
	paths []string
	runs  int
	at    time.Time
}

// maxCached bounds the cache of merges done. Tree ids are content addressed,
// so an entry is never wrong, only unlikely to be asked for again. When it is
// full the one asked for least recently goes, not the whole cache: forty panes
// that all touch a file are 780 pairs, and wiping the cache at the limit would
// have them merged again from nothing.
const maxCached = 4096

// maxMergesPerRefresh bounds how many merges one refresh runs. Sixty panes on one
// file are 1,770 pairs, and a refresh that ran them all would hold the repository
// for as long as git takes. What is not reached is left as it was (Report.Behind),
// and the merges done are cached, so the next refresh goes on from there.
const maxMergesPerRefresh = 50

// Predictor is how an Engine merges two snapshots; Predict is the one that asks
// git.
type Predictor func(ctx context.Context, dir string, s *gitx.Scratch, a, b string) (paths []string, clean bool, err error)

// Options are what a test changes about an Engine: how merges are answered, what
// time it is, and how far apart two refreshes have to be to count as two. Zero
// values are the defaults.
type Options struct {
	Predict Predictor
	Now     func() time.Time
	MinGap  time.Duration
	// MaxCached is how many merges the engine keeps; zero is the default.
	MaxCached int
}

// NewEngine makes an Engine for one repository.
func NewEngine() *Engine { return NewEngineWith(Options{}) }

// NewEngineWith is NewEngine with o.
func NewEngineWith(o Options) *Engine {
	e := &Engine{
		cache:     map[treePair]*list.Element{},
		order:     list.New(),
		pairs:     map[pair]*streak{},
		last:      map[string]Input{},
		predict:   o.Predict,
		now:       o.Now,
		minGap:    o.MinGap,
		maxCached: o.MaxCached,
	}
	if e.maxCached == 0 {
		e.maxCached = maxCached
	}
	if e.predict == nil {
		e.predict = Predict
	}
	if e.now == nil {
		e.now = time.Now
	}
	if e.minGap == 0 {
		e.minGap = 5 * time.Second
	}
	return e
}

// Report is what a refresh found.
type Report struct {
	// Conflicts are the pairs to show: those that have conflicted in
	// confirmAfter refreshes in a row, whether or not this one asked about
	// them again.
	Conflicts []Conflict
	// Behind says ctx ran out, or the refresh reached its limit of merges
	// (maxMergesPerRefresh), before every pair was looked at. What is reported
	// is then the last that was found for those left.
	Behind bool
	// Merges is how many merge-trees this refresh ran.
	Merges int
}

// Begin claims the engine for one refresh, and says false when another is
// still running in it. The caller does it before the work that costs --
// snapshotting the checkouts -- so that a refresh that overlaps another does
// nothing instead of doing it all and throwing it away. end releases it, and
// Update is called between the two.
func (e *Engine) Begin() (end func(), ok bool) {
	if !e.run.TryLock() {
		return nil, false
	}
	return e.run.Unlock, true
}

// Update takes the checkouts a refresh read, in dir (any checkout of the
// repository) with the snapshots made under s, and returns the pairs to show.
//
// A pair is compared only when both its checkouts are in in and ready, and only
// when their changed paths meet: git merges two sets of files that share none
// without trouble, and a fan-out of a dozen agents on disjoint files runs no
// merge at all. A pair whose two trees were merged before is not merged again.
// A checkout absent from in -- a refresh of the panes on screen leaves the rest
// out -- keeps its pairs as they were.
//
// A checkout that is Unknown is left as it was too: nothing is learned about its
// pairs from a read that failed.
//
// ctx is the refresh's deadline; when it runs out the pairs not yet reached
// are left as they were and Report.Behind is set. Only one Update runs at a
// time; see Begin.
func (e *Engine) Update(ctx context.Context, dir string, s *gitx.Scratch, in []Input) (r Report) {
	var ready []Input
	for _, c := range in {
		switch {
		case c.Unknown:
		case c.Ready:
			ready = append(ready, c)
			e.setLast(c)
		default:
			e.dropFor(c.ID)
		}
	}
	sort.Slice(ready, func(i, j int) bool { return ready[i].ID < ready[j].ID })

	for i := range ready {
		for j := i + 1; j < len(ready); j++ {
			a, b := ready[i], ready[j]
			p := pair{a.ID, b.ID}
			if !meetInputs(a, b) {
				e.drop(p)
				continue
			}
			// Two snapshots are the same merge only when their trees and the
			// commits they stand on are: the merge base comes from the latter.
			key := treePair{a.Tree + " " + a.Head, b.Tree + " " + b.Head}
			if key.a > key.b {
				key.a, key.b = key.b, key.a
			}
			v, cached := e.cached(key)
			if !cached {
				if ctx.Err() != nil || r.Merges >= maxMergesPerRefresh {
					r.Behind = true
					continue
				}
				// Not under e.mu: a merge takes as long as git does, and
				// Conflicts is read from the window's own goroutine.
				paths, clean, err := e.predict(ctx, dir, s, a.Commit, b.Commit)
				if err != nil {
					// Not an answer. The pair stays as it was, and is
					// asked about again next time.
					if ctx.Err() != nil {
						r.Behind = true
					}
					continue
				}
				r.Merges++
				v = verdict{paths: paths, conflict: !clean}
				e.remember(key, v)
			}
			if v.conflict {
				e.sighted(p, v.paths)
			} else {
				e.drop(p)
			}
		}
	}
	e.dropSeparated(ready)
	return Report{Conflicts: e.Conflicts(), Behind: r.Behind, Merges: r.Merges}
}

// Retain forgets every checkout whose ID is not in live, with the pairs it was
// in, so that the engine does not go on holding what belonged to panes that have
// closed.
func (e *Engine) Retain(live map[string]bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for id := range e.last {
		if !live[id] {
			delete(e.last, id)
		}
	}
	for p := range e.pairs {
		if !live[p.a] || !live[p.b] {
			delete(e.pairs, p)
		}
	}
}

// Empty reports whether the engine holds nothing about any checkout.
func (e *Engine) Empty() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.last) == 0 && len(e.pairs) == 0
}

func (e *Engine) setLast(c Input) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.last[c.ID] = c
}

// dropSeparated drops the pairs that are in the engine, with one half in ready
// and the other not, once their changed paths no longer meet. Whether such a
// pair still conflicts cannot be asked of git -- the snapshot of the half left
// out is gone with its scratch directory -- but that the files no longer
// overlap can be told from the paths.
func (e *Engine) dropSeparated(ready []Input) {
	fresh := make(map[string]bool, len(ready))
	for _, c := range ready {
		fresh[c.ID] = true
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for p := range e.pairs {
		if fresh[p.a] && fresh[p.b] {
			continue
		}
		a, b := e.last[p.a], e.last[p.b]
		if !meetInputs(a, b) {
			delete(e.pairs, p)
		}
	}
}

// dropFor forgets every pair checkout id is in.
func (e *Engine) dropFor(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.last, id)
	for p := range e.pairs {
		if p.a == id || p.b == id {
			delete(e.pairs, p)
		}
	}
}

func (e *Engine) drop(p pair) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.pairs, p)
}

func (e *Engine) cached(key treePair) (verdict, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	el, ok := e.cache[key]
	if !ok {
		return verdict{}, false
	}
	e.order.MoveToFront(el)
	return el.Value.(*entry).v, true
}

func (e *Engine) remember(key treePair, v verdict) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if el, ok := e.cache[key]; ok {
		el.Value.(*entry).v = v
		e.order.MoveToFront(el)
		return
	}
	e.cache[key] = e.order.PushFront(&entry{key, v})
	for len(e.cache) > e.maxCached {
		oldest := e.order.Back()
		e.order.Remove(oldest)
		delete(e.cache, oldest.Value.(*entry).key)
	}
}

// sighted notes that this refresh found p conflicting over paths.
func (e *Engine) sighted(p pair, paths []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	st := e.pairs[p]
	switch {
	case st == nil:
		e.pairs[p] = &streak{paths: paths, runs: 1, at: now}
	case now.Sub(st.at) >= e.minGap:
		st.runs++
		st.at = now
		st.paths = paths
	default:
		st.paths = paths
	}
}

// Tracking reports whether the engine holds any pair that conflicts, shown yet
// or not.
func (e *Engine) Tracking() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.pairs) > 0
}

// Conflicts returns the pairs to show, as of the last Update.
func (e *Engine) Conflicts() []Conflict {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Conflict
	for p, st := range e.pairs {
		if st.runs >= confirmAfter {
			out = append(out, Conflict{A: p.a, B: p.b, Paths: append([]string(nil), st.paths...)})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].A != out[j].A {
			return out[i].A < out[j].A
		}
		return out[i].B < out[j].B
	})
	return out
}

// meet reports whether the two sets of paths share one, where a path also
// meets any path under it: one pane adding a file foo and another adding
// foo/bar conflict, though no path is in both. The test is at a "/" boundary, so
// foo does not meet foobar.
func meet(a, b []string) bool {
	return anyUnder(a, b) || anyUnder(b, a)
}

// anyUnder reports whether some path in inner is a path in outer, or is under
// one: outer holds it, or one of its parent directories.
func anyUnder(outer, inner []string) bool {
	in := make(map[string]struct{}, len(outer))
	for _, p := range outer {
		in[p] = struct{}{}
	}
	for _, p := range inner {
		for q := p; ; {
			if _, ok := in[q]; ok {
				return true
			}
			slash := strings.LastIndexByte(q, '/')
			if slash < 0 {
				break
			}
			q = q[:slash]
		}
	}
	return false
}

// meetInputs reports whether two checkouts' changes meet: they share a path, a
// file and a directory of one name, or one added a path under a directory the
// other moved a file out of.
func meetInputs(a, b Input) bool {
	return meet(a.Paths, b.Paths) || anyUnder(a.Dirs, b.Paths) || anyUnder(b.Dirs, a.Paths)
}

// Forget drops a checkout and every pair it is in, for one that cannot be read
// any more. It takes the engine's run lock, so that an Update in progress cannot
// put back what it drops, and says false, doing nothing, when one is: the caller
// asks again.
func (e *Engine) Forget(id string) bool {
	if !e.run.TryLock() {
		return false
	}
	defer e.run.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.last, id)
	for p := range e.pairs {
		if p.a == id || p.b == id {
			delete(e.pairs, p)
		}
	}
	return true
}
