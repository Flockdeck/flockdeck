package workspace

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/jmwri/flockdeck/internal/ghcli"
	"github.com/jmwri/flockdeck/internal/store"
)

// The knowledge bundle is .flockdeck/knowledge/ in a checkout: notes written
// from the code. A pane's briefing can point at it, but its text goes to
// whatever model the agent runs on, and for a repository that is not private
// that is a decision the user makes. So the pointer is on by default only
// where the repository is known to be private; everywhere else the user is
// told once and answers with `flockdeck knowledge on` or `off`.

// bundleSubdir is where the bundle is kept, below a checkout.
var bundleSubdir = filepath.Join(".flockdeck", "knowledge")

// bundleIndex is the file whose presence makes a directory a bundle.
const bundleIndex = "index.md"

// lookupVisibility asks GitHub how visible a repository is. It is a variable
// so a test can answer without gh.
var lookupVisibility = ghcli.Visibility

// knowledgeAskNotice is the one-time sentence shown for a project that is not
// known to be private and has not been answered.
const knowledgeAskNotice = "This project has notes in .flockdeck/knowledge, but its repository is not known to be private, " +
	"so agents are not pointed at them. Run `flockdeck knowledge on` in the project to point them there, " +
	"or `flockdeck knowledge off` to stop this message."

// knowledgeVisibility remembers, for each project root, whether its repository
// is known to be private. Each project is asked about once per run.
type knowledgeVisibility struct {
	mu   sync.Mutex
	seen map[string]*visibilityResult
}

// visibilityResult is one project's lookup. done is closed when private and
// known are set.
type visibilityResult struct {
	done    chan struct{}
	private bool
}

// knowledgeDir returns the directory the briefing of a pane working in cwd, in
// the project rooted at root, should point at, or "" when it should not point
// at anything. It does not wait for GitHub: the first call for a project starts
// the lookup and answers as if the repository were not private, so the pane
// that triggers it is briefed without the pointer and the next briefing, after
// a compaction or a resume, has it.
func (w *Workspace) knowledgeDir(paneID, cwd, root string) string {
	dir := filepath.Join(cwd, bundleSubdir)
	// A bundle is a regular file named index.md in a real directory under the
	// pane's own checkout. A link there could name anything on the machine.
	if fi, err := os.Lstat(filepath.Join(dir, bundleIndex)); err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	switch store.LoadPrefs().KnowledgeSetting(root) {
	case store.KnowledgeOn:
		return dir
	case store.KnowledgeOff:
		return ""
	}
	if w.knowledge.private(w, paneID, root) {
		return dir
	}
	return ""
}

// private reports whether root's repository is known to be private, starting
// the lookup the first time and reporting false until it has finished.
func (k *knowledgeVisibility) private(w *Workspace, paneID, root string) bool {
	key := store.MetaKey(root)
	k.mu.Lock()
	if k.seen == nil {
		k.seen = map[string]*visibilityResult{}
	}
	r, ok := k.seen[key]
	if !ok {
		r = &visibilityResult{done: make(chan struct{})}
		k.seen[key] = r
		go k.look(w, paneID, root, r)
	}
	k.mu.Unlock()
	select {
	case <-r.done:
		return r.private
	default:
		return false
	}
}

func (k *knowledgeVisibility) look(w *Workspace, paneID, root string, r *visibilityResult) {
	v, err := lookupVisibility(root)
	r.private = err == nil && v == "private"
	close(r.done)
	if r.private {
		return
	}
	w.askAboutKnowledge(paneID, root)
}

// askAboutKnowledge shows the one-time notice for a project, if it has not
// been shown and the user has not answered. It is recorded as shown only when
// the preferences could be read and written, so a file that cannot be read
// does not turn it into a notice on every run.
func (w *Workspace) askAboutKnowledge(paneID, root string) {
	tip := "knowledge-ask:" + store.MetaKey(root)
	p, err := store.ReadPrefs()
	if err != nil || p.KnowledgeSetting(root) != "" || p.Dismissed(tip) {
		return
	}
	p.Dismiss(tip)
	if store.SavePrefs(p) != nil {
		return
	}
	w.note(paneID, knowledgeAskNotice)
}
