// Package store persists the window layout between runs.
//
// Only the shape of the workspace is saved — tabs, splits, working
// directories, pane ids, which agent and model each pane runs, and the size
// each pane's terminal was last drawn at.
// Conversation history lives in the agent's own transcript store and is
// reattached by id, which is why pane ids are generated as UUIDs.
package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// Version is the schema version of the persisted state.
//
// Version 2 is where a pane began saying which agent it runs and which model
// it was asked for. Version 1 knew only one agent, so its panes are "claude"
// or "shell"; they are brought forward on the way in rather than being thrown
// away, because a layout is the user's open tabs and losing them on an upgrade
// would be unforgivable.
const Version = 2

// State is the whole persisted workspace.
type State struct {
	Version int    `json:"version"`
	Tabs    []Tab  `json:"tabs"`
	Active  int    `json:"active"`
	Root    string `json:"root,omitempty"` // directory the workspace was opened on
	// HandNames is set by every layout that says, tab by tab, whether a title
	// was chosen by hand. A layout without it was written before that was
	// kept, and a tab's title there may be either; Named being absent means
	// something only where this is set.
	HandNames bool `json:"handNames,omitempty"`
}

// Tab is one tab and its pane tree.
type Tab struct {
	Title string `json:"title,omitempty"`
	Focus string `json:"focus,omitempty"`
	Root  *Node  `json:"root"`
	// Named is set when Title was chosen by hand, and Auto is then the title
	// the tab would have had otherwise, which giving up the name puts back.
	// An empty Auto is not known.
	Named bool   `json:"named,omitempty"`
	Auto  string `json:"auto,omitempty"`
}

// Node mirrors layout.Node in a form that survives a round trip through JSON.
type Node struct {
	Pane     *Pane   `json:"pane,omitempty"`
	Dir      string  `json:"dir,omitempty"` // "h" or "v"
	Weight   float64 `json:"weight,omitempty"`
	Children []*Node `json:"children,omitempty"`
}

// Pane is a single restored pane.
type Pane struct {
	ID   string `json:"id"`
	Kind string `json:"kind"` // "agent" or "shell"
	Cwd  string `json:"cwd"`
	Name string `json:"name,omitempty"`
	// Agent is the id of the agent an agent pane runs, and Model the model it
	// was asked for. Both are absent for a shell pane, and an absent Agent on
	// an agent pane means whatever the catalog's default is — which is what a
	// layout written before panes could choose meant, and what a fresh pane
	// opened with one keystroke still means.
	Agent string `json:"agent,omitempty"`
	Model string `json:"model,omitempty"`
	// Routed names the rule that chose Model, and RoutedFrom the model the
	// pane would have run without it. Both are absent for a model chosen by
	// hand or by default, which is every pane of a layout written before
	// routing, and they come back with the pane so its header goes on saying
	// the model was routed.
	Routed     string `json:"routed,omitempty"`
	RoutedFrom string `json:"routedFrom,omitempty"`
	// Task is what a spawned pane was asked to do. It is kept so a restored
	// agent can still be told why its pane exists.
	Task string `json:"task,omitempty"`
	// Root is the project the pane belongs to, written only when it is not the
	// project of the tab holding it. A tab may show agents from more than one
	// project, and without this a borrowed pane would come back counted
	// against the tab's project: shown under the wrong name, stopped when the
	// wrong project was closed, and told it was working somewhere it was not.
	//
	// Absent — which is every layout written before this — means the tab's own
	// project, which is what those layouts meant.
	Root string `json:"root,omitempty"`
	// Conversation is the agent's own id for the conversation the pane is in,
	// written only when that is no longer the pane's id: Claude Code carries on
	// under a new one after /clear. Absent means the pane's id, which is what
	// every layout written before this meant.
	Conversation string `json:"conversation,omitempty"`
	// Cols and Rows are the size the pane's terminal was last drawn at, so a
	// restored pane starts at that size rather than at eighty by twenty-four.
	// Absent, which is every layout written before this, means the default.
	Cols int `json:"cols,omitempty"`
	Rows int `json:"rows,omitempty"`
}

// Dir returns the per-user directory holding Flockdeck's state.
//
// It is kept private to the user. Below it sit the local server's auth token,
// the browser profile the application window signs in through (except on
// Windows, where BrowserProfileDir keeps it in the local folder instead), and
// the generated settings handed to each agent; the files themselves are written
// 0600, but a world-readable directory still lets any other account on the
// machine list them and read whatever was not written by this package.
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate config dir: %w", err)
	}
	dir := stateDir(base)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create config dir: %w", err)
	}
	makePrivate(dir)
	return dir, nil
}

// stateDir works out which directory this run keeps its state in, and having
// worked it out once, keeps saying so.
//
// Every call into this package goes through Dir, and without this every one of
// them also stats a directory belonging to a build the user stopped running
// long ago: a fifth of a millisecond, on the path of every load and every
// save, for a migration that can only ever happen once.
//
// Remembering the answer also pins it. Where the move could not be made the
// old directory is used where it stands, and a second attempt later in the
// same run could succeed and move the state out from under everything already
// written to it. One run, one directory.
var stateDir = func() func(base string) string {
	var (
		mu     sync.Mutex
		under  string
		chosen string
	)
	return func(base string) string {
		mu.Lock()
		defer mu.Unlock()
		// Keyed on the base, because the tests move it: a remembered answer
		// for somewhere else is no answer at all.
		if chosen != "" && under == base {
			return chosen
		}
		chosen = adoptLegacyDir(base, filepath.Join(base, "flockdeck"))
		under = base
		return chosen
	}
}()

// legacyDirName is the directory this state was kept in before the program was
// renamed. It is looked at only when nothing has been saved under the name in
// use now, and can be dropped a release after the rename.
const legacyDirName = "perch"

// adoptLegacyDir moves state saved under the name an earlier build used to the
// name in use now, and returns the directory to work in.
//
// Everything below the directory travels in one rename — every saved layout,
// the auth token, the generated per-session settings and the browser profile —
// so upgrading opens the tabs the user left rather than an empty workspace
// beside a layout nothing would ever look at again.
//
// It moves the old directory only when nothing exists under the new name. A
// directory already there is this build's own state and is never written over,
// which also makes the migration harmless to run on every call.
//
// Where the move cannot be made the old directory is used where it stands. On
// Windows a file still held open by a detached instance is enough to fail a
// rename, and continuing to write to the old name costs nothing next to
// starting empty.
func adoptLegacyDir(base, dir string) string {
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		return dir
	}
	old := filepath.Join(base, legacyDirName)
	if fi, err := os.Stat(old); err != nil || !fi.IsDir() {
		return dir
	}
	if err := renameDir(old, dir); err != nil {
		// A second instance starting at the same moment can be part-way through
		// this very move: it looked for the name in use now, found nothing, and
		// got its rename in first. Ours then fails and the old directory is
		// already gone. Handing back the old name there would have this run
		// create it again, empty, and write every layout, the recent list and
		// its own instance record into a directory nothing will ever adopt,
		// because the name in use now exists and the old one is never looked at
		// again. The two instances would not even find each other, each reading
		// an instance record the other never wrote.
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return dir
		}
		return old
	}
	return dir
}

// renameDir moves a directory. It is a variable so a test can stand in for the
// instant between one instance finding nothing under the name in use now and
// another moving the old directory onto it.
var renameDir = os.Rename

// makePrivate narrows a directory that was created before it was kept private,
// or by a umask that let group and other in. Windows does not express
// permissions this way and os.Chmod there means something else entirely, so it
// is left alone.
func makePrivate(dir string) {
	if runtime.GOOS == "windows" {
		return
	}
	fi, err := os.Stat(dir)
	if err != nil || fi.Mode().Perm()&0o077 == 0 {
		return
	}
	_ = os.Chmod(dir, 0o700)
}

// SessionsDir returns the directory holding generated per-session settings.
func SessionsDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	sub := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		return "", fmt.Errorf("create sessions dir: %w", err)
	}
	makePrivate(sub)
	return sub, nil
}

// path returns the state file path for a workspace root. Each root gets its
// own layout, so opening two repositories does not clobber one another.
func path(root string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "layout-"+hashRoot(root)+".json"), nil
}

// legacyPath returns the layout file a root was saved under before roots were
// normalized on their way into the hash. Those builds hashed the root exactly
// as it was spelled, so on the platforms that fold case the upgrade moved the
// file name out from under every saved layout: the app looked up a name
// nothing had ever been written to, found nothing, and opened an empty
// workspace while the layout it should have restored sat beside it under the
// old name.
//
// It returns "" where the two names cannot differ, which is every platform
// that does not fold case and every root already spelled in normal form.
func legacyPath(root string) (string, error) {
	clean := filepath.Clean(root)
	if clean == normalizeRoot(root) {
		return "", nil
	}
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "layout-"+hashString(clean)+".json"), nil
}

// readLegacy returns the contents of a layout saved under the
// pre-normalization name, along with the path it was read from so the caller
// can adopt it once it has been checked. A missing file is the ordinary case:
// it simply means nothing was saved under the old name either.
func readLegacy(root string) (string, []byte, error) {
	old, err := legacyPath(root)
	if err != nil {
		return "", nil, err
	}
	if old == "" {
		return "", nil, fs.ErrNotExist
	}
	data, err := readState(old)
	if err != nil {
		return "", nil, err
	}
	return old, data, nil
}

// normalizeRoot puts a workspace path into the one form used for comparing and
// hashing roots. Roots reach us from a directory picker, the command line and
// saved state, so the same project can arrive as "C:\Repo\App\" one run and
// "C:\repo\app" the next; without this each spelling would get its own layout
// file while sameRoot still treated them as one project.
func normalizeRoot(root string) string {
	clean := filepath.Clean(root)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.ToLower(clean)
	}
	return clean
}

// Load reads the saved state for a workspace root. A missing file is not an
// error: it simply means there is nothing to restore.
//
// It is for a caller about to restore what it reads, because it records how
// the read went: a layout that could not be read is kept from the next save,
// and one that could is not.
func Load(root string) (*State, error) { return load(root, true) }

// Peek is Load for a caller that only wants to look at what was saved and is
// not going to restore it, and so says nothing about whether the file could be
// read.
//
// The save of a project nobody has been in since it was opened reads the tab
// it was last saved on. Through Load, that read taking a moment's trouble in
// its stride cleared the record of the one at start that had not: a project
// whose layout could not be read comes up on one fresh tab, and the timed save
// half a minute later then found nothing to keep and wrote that tab straight
// over every tab the user had saved.
func Peek(root string) (*State, error) { return load(root, false) }

// load is Load, recording how the read went only when restoring says what it
// reads is going to be restored.
func load(root string, restoring bool) (*State, error) {
	p, err := path(root)
	if err != nil {
		return nil, err
	}
	data, err := readState(p)
	if restoring {
		noteRead(p, err)
	}
	// Nothing under the name in use now may only mean this layout was last
	// saved by a build that named it differently.
	legacy := ""
	if errors.Is(err, fs.ErrNotExist) {
		legacy, data, err = readLegacy(root)
	}
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read layout: %w", err)
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		// A corrupt layout must never stop the app from starting, but it must
		// not be left where it is either: the run that starts empty saves over
		// this very name on the way out, so the damaged file is the last copy
		// of the user's tabs and this would be the moment it went for good.
		// Moved aside it costs nothing and can still be repaired by hand.
		quarantine(from(p, legacy))
		return nil, nil
	}
	if !migrate(&s) {
		// Not damage but a layout from a build that numbered the format
		// differently, most often a newer one the user has just stepped back
		// from. It meets the same end as a damaged file, ignored now and
		// written over on the way out, so it is kept for the same reason:
		// going forward again is then a matter of moving one file back.
		quarantine(from(p, legacy))
		return nil, nil
	}
	// The filename is a hash of the root, so a file can only be the wrong one
	// through a collision or through a change to how the hash is taken. Either
	// way, restoring another project's tabs into this one would be far worse
	// than restoring nothing. Layouts written before the root was recorded
	// have nothing to check and are taken as they are.
	if s.Root != "" && !sameRoot(s.Root, root) {
		return nil, nil
	}
	// Only once the layout has been shown to be this project's is the old file
	// moved to the name in use now, so a rejected one is left where it is
	// rather than being renamed over this project's name. Moving it is best
	// effort: a layout that cannot be renamed has still been read, and failing
	// to tidy the old file away matters far less than losing the tabs it holds.
	if legacy != "" {
		_ = os.Rename(legacy, p)
	}
	return &s, nil
}

// migrate brings a layout read from disk up to the schema this build writes,
// reporting whether it could. A version it does not recognise — a newer one,
// or a zero from a file that is not a layout at all — is left for the caller
// to put aside.
//
// It works on the value in memory and leaves the file alone. The next save
// writes the current version over it, so a run that only looks at a layout
// does not rewrite it, and an upgrade costs the user nothing and asks them
// nothing.
func migrate(s *State) bool {
	switch s.Version {
	case Version:
		return true
	case 1:
		// Version 1 had one agent, so a pane was "claude" or "shell" and there
		// was nothing else to say about it. Every one of those Claude panes is
		// an agent pane running the agent named "claude", with whatever model
		// the CLI was already configured with — which is an empty model, not a
		// guess at which one it was.
		for i := range s.Tabs {
			migratePaneKinds(s.Tabs[i].Root)
		}
		s.Version = Version
		return true
	}
	return false
}

// migratePaneKinds rewrites version 1's pane kinds throughout one tab's tree.
func migratePaneKinds(n *Node) {
	if n == nil {
		return
	}
	if n.Pane != nil && n.Pane.Kind != "shell" {
		n.Pane.Kind = "agent"
		if n.Pane.Agent == "" {
			n.Pane.Agent = "claude"
		}
	}
	for _, c := range n.Children {
		migratePaneKinds(c)
	}
}

// from returns the file a layout was actually read from: the name in use now,
// unless it came from the pre-normalization name.
func from(current, legacy string) string {
	if legacy != "" {
		return legacy
	}
	return current
}

// damagedSuffix marks a state file this build could not use. It is not
// ".json", so nothing looks for it again, and it holds no ".tmp", so the sweep
// leaves it alone: the point is that it is still there when someone goes
// looking for what vanished.
const damagedSuffix = ".damaged"

// quarantine moves a state file out of the way, best effort.
//
// A copy already kept is not replaced: the next one goes to "<name>.damaged.1"
// and on, as a file that could not be read goes beside ".unread". Moved onto
// the one name, a second copy replaced the first, and the first is the one that
// mattered as often as not: a layout from a newer build is put aside by the
// older one the user stepped back to, and stepping forward and back once more
// put aside the layout the older build had saved on top of the newer one's
// tabs.
//
// A file that will not move — held for a moment by the virus scanner that
// reads every file in the state directory, or blocked by whatever stands at
// the name it is going to — was left where the next save wrote straight over
// it, which is the loss moving it was for. It is recorded as unread instead,
// so that save moves it aside first, or is refused if it still cannot.
func quarantine(path string) {
	aside, err := asideName(path, damagedSuffix)
	if err == nil {
		err = os.Rename(path, aside)
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		noteRead(path, err)
	}
}

// Save writes the state atomically so an interrupted write cannot leave a
// truncated layout behind.
func Save(root string, s *State) error {
	p, err := path(root)
	if err != nil {
		return err
	}
	s.Version = Version
	s.Root = filepath.Clean(root)
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encode layout: %w", err)
	}
	if err := keepUnread(p, "the saved layout for "+s.Root); err != nil {
		return fmt.Errorf("write layout: %w", err)
	}
	if err := writeAtomic(p, data); err != nil {
		return fmt.Errorf("write layout: %w", err)
	}
	return nil
}

// unreadSuffix marks a state file this run could not read, moved out of the
// way of the save that replaced it. Like damagedSuffix it is neither ".json"
// nor ".tmp", so nothing looks for it again and nothing sweeps it away.
const unreadSuffix = ".unread"

// unreadFiles holds the state files this run looked for and could not read,
// for a reason other than their not being there: layouts, the list of open
// projects, and the preferences.
//
// A file that cannot be read is taken as empty — a project that opens with no
// tabs or with the one fresh tab the caller gives it, a start that reopens no
// other project, the default preferences — and the next save used to write
// that over the file nobody had been able to read. What the user had saved was
// lost to a moment's trouble reading it: a file held past the retry budget, a
// drive that was slow to wake, a permission put right by the next run. The
// file is still theirs, so it is moved aside before it is written over, and
// kept.
var unreadFiles = struct {
	sync.Mutex
	paths map[string]bool
	// kept is every one of them moved aside since TakeKept last looked.
	kept []Kept
}{paths: map[string]bool{}}

// Kept is a state file this run could not read, moved aside before the save
// that replaced it.
type Kept struct {
	// What names what the file held, as a sentence would: "the saved layout
	// for /repo/a".
	What string
	// Path is where it is kept now.
	Path string
}

// TakeKept returns the files moved aside since it was last called, and
// forgets them.
//
// A file that could not be read at start is moved aside by the first save
// after, and the project it held came up on one fresh tab: without this, the
// only sign of either was a file with a new name in a folder nobody looks in.
// Whoever can tell the user asks, and says where their file went.
func TakeKept() []Kept {
	unreadFiles.Lock()
	defer unreadFiles.Unlock()
	kept := unreadFiles.kept
	unreadFiles.kept = nil
	return kept
}

// noteRead records how reading a state file went. A file read, or found not
// to be there, has nothing left to keep.
func noteRead(p string, err error) {
	unreadFiles.Lock()
	defer unreadFiles.Unlock()
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		unreadFiles.paths[p] = true
		return
	}
	delete(unreadFiles.paths, p)
}

// keepUnread moves a file this run could not read aside, ahead of the save
// about to replace it, and records it for TakeKept under what, which names
// what it held. Where it cannot be moved either, the save is refused: what is
// lost with it is what the user has just seen, and the file that could not be
// read is what they have not.
func keepUnread(p, what string) error {
	unreadFiles.Lock()
	defer unreadFiles.Unlock()
	if !unreadFiles.paths[p] {
		return nil
	}
	aside, err := asideName(p, unreadSuffix)
	if err == nil {
		err = renameWithRetry(p, aside)
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("what was saved there before could not be read, and could not be moved aside either, so it has been left as it was rather than written over: %w", err)
	}
	delete(unreadFiles.paths, p)
	if err == nil {
		unreadFiles.kept = append(unreadFiles.kept, Kept{What: what, Path: aside})
	}
	return nil
}

// maxKeptAside bounds how many copies of one file are kept aside under one
// suffix. It is far more than any run will make; it is there so a folder where
// every name answers something other than "not there" is not searched forever.
const maxKeptAside = 100

// asideName is the name a state file is moved aside to under suffix — the
// file this run could not read to "<name>.unread", the one this build could
// not use to "<name>.damaged" — or where that is taken, the first of
// "<name><suffix>.1", "<name><suffix>.2" and on that is not.
//
// A rename replaces whatever stands at the name it is given, on every
// platform, and a copy already there is one kept for the user from an earlier
// time the file could not be read: the tabs or settings they had before a run
// came up without them, which nothing says they have been back for. The file
// being moved now is most often what that run saved in their place, so the two
// are not the same, and moving one onto the other lost the copy that mattered.
//
// Looking first is not atomic: a second instance moving the same file aside
// in the same moment could take the name between the look and the rename.
// That needs two instances failing to read one file at once, and costs one of
// the two copies, which is what every second move used to cost.
func asideName(p, suffix string) (string, error) {
	for i := 0; i < maxKeptAside; i++ {
		name := p + suffix
		if i > 0 {
			name = fmt.Sprintf("%s.%d", name, i)
		}
		if _, err := os.Lstat(name); errors.Is(err, fs.ErrNotExist) {
			return name, nil
		}
	}
	return "", fmt.Errorf("%d copies of it are already kept beside it", maxKeptAside)
}

// WriteAtomic is writeAtomic for the packages that keep a file of their own in
// the state directory. It carries the same promise — the old file or the new
// one, never a mixture — and the same 0600, which is what any file sitting
// beside the local server's token wants too.
func WriteAtomic(path string, data []byte) error { return writeAtomic(path, data) }

// writeLocks holds one lock for each file writeAtomic writes. Saves made by
// this process queue for the file they replace instead of racing each other to
// the rename: on Windows a rename onto a file another save is reading, or is
// renaming onto, is refused, and eight at once on a slow machine could wait
// out renameWithRetry's whole budget. The retry is still there for a second
// instance, which a lock in this process cannot reach.
var writeLocks sync.Map // cleaned path -> *sync.Mutex

func writeLock(path string) *sync.Mutex {
	m, _ := writeLocks.LoadOrStore(filepath.Clean(path), new(sync.Mutex))
	return m.(*sync.Mutex)
}

// beforeRename, when set, is called by writeAtomic just before it renames the
// finished file into place, so that a test can see how many saves are there
// at once.
var beforeRename func(path string)

// writeAtomic replaces a file with new contents, leaving either the old file
// or the new one behind and never a half-written mixture of the two.
//
// The temporary file gets a unique name because two instances can be running at
// once: with a shared "<name>.tmp" one process could rename the other's
// half-written file into place. It is flushed to disk before the rename, since
// a rename that reaches the disk ahead of the contents it names would leave an
// empty file after a crash.
func writeAtomic(path string, data []byte) error {
	mu := writeLock(path)
	mu.Lock()
	defer mu.Unlock()

	// A file already holding these bytes does not need writing. The save on the
	// way out writes every open project's layout and the list of which ones
	// were open, and all but the one project the user was actually working in
	// are unchanged: for each of those this is a file creation, a flush to the
	// device and a rename that buy nothing. It also takes away a chance for a
	// second instance's save to be the one that loses, which is the reason
	// ForgetRecent already declines to rewrite a list it did not change.
	//
	// The length is looked at before the contents. Reading a file that has
	// changed since the last look is not free on Windows — it is the moment
	// the virus scanner reads it too, and here that costs several times what
	// the whole write does — while reading one that has not is served from the
	// cache. A layout that has changed has nearly always changed length, so
	// the stat sends those straight to the write and the read is paid almost
	// only where it is about to save one.
	if fi, err := os.Stat(path); err == nil && fi.Size() == int64(len(data)) {
		if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
			return nil
		}
	}
	dir, base := filepath.Split(path)
	f, err := os.CreateTemp(dir, base+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op once the rename below has succeeded

	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// os.CreateTemp already makes the file 0600, which is what these state
	// files want: they carry the local server's auth token.
	if beforeRename != nil {
		beforeRename(path)
	}
	if err := renameWithRetry(tmp, path); err != nil {
		return err
	}
	syncDir(dir)
	return nil
}

// syncDir flushes a directory's own entries to disk.
//
// Syncing the file only promises its contents are there; the rename that gives
// them their name is a change to the directory, and on a crash before that
// reaches disk the file reverts to the version before this write. Failures are
// ignored: the rename has already happened, so the write did succeed.
//
// Windows is left out rather than left to fail. FlushFileBuffers wants a
// handle opened for writing and a directory cannot be opened that way here, so
// the call is refused every time — and it was still paid for on every write:
// an open, a refusal and a close, a fifth of a millisecond each. The file
// system journals the rename regardless, which is why there was nothing to
// ask for in the first place.
func syncDir(dir string) {
	if runtime.GOOS == "windows" {
		return
	}
	if dir == "" {
		dir = "."
	}
	f, err := os.Open(dir)
	if err != nil {
		return
	}
	defer f.Close()
	_ = f.Sync()
}

// A file another process is holding open is the one failure in this package
// worth waiting out rather than reporting, so both the reads and the rename
// keep trying for this long before they give up.
//
// The budget is a length of time and not a count of tries, because what
// decides it is how long somebody else keeps the file, not how many times we
// ask. Backing off to a cap rather than doubling without one spends the same
// second on many more attempts, which is what a sparse tail of long holds
// needs. A second is a long time to spend on the way out; losing the layout is
// worse, and a rename that is never going to work is a broken installation
// that fails on every save anyway.
const (
	contentionBudget   = time.Second
	contentionMaxDelay = 32 * time.Millisecond
)

// backOff waits before the next attempt and returns the next wait, doubling up
// to the cap.
func backOff(delay time.Duration) time.Duration {
	time.Sleep(delay)
	if delay *= 2; delay > contentionMaxDelay {
		return contentionMaxDelay
	}
	return delay
}

// renameWithRetry replaces dst with src, waiting out anything holding dst.
//
// On Windows replacing a file fails outright while anything else has it open —
// any handle at all, whatever sharing it asked for — and these files are
// opened constantly: by the other instance saving or restoring at the same
// moment, and by the virus scanner and search indexer that follow every write
// in the user's AppData directory. On other platforms the first attempt always
// decides it.
//
// Most holds are gone by the first retry. Measured with four writers on one
// state directory, the tail reached eight tries, which was every try there
// used to be: the saves just past it were not slow, they were lost, and a lost
// save is a workspace the user does not get back.
func renameWithRetry(src, dst string) error {
	deadline := time.Now().Add(contentionBudget)
	delay := time.Millisecond
	for {
		err := os.Rename(src, dst)
		if err == nil {
			return nil
		}
		// Only a file somebody else has open is worth waiting out. Anything
		// else — a missing source, a directory where the file goes, a disk
		// that is read-only — will be just as true a second from now, and
		// waiting it out cost a second a save: at shutdown, where every open
		// project is saved in turn, enough of them to run out the deadline
		// before the last layout was written.
		if !renameHeld(err) || time.Now().After(deadline) {
			return err
		}
		delay = backOff(delay)
	}
}

// readState reads a state file, retrying briefly on a failure that means
// somebody else has the file open rather than that there is nothing to read.
//
// Windows refuses an open with a sharing violation while another process is
// part-way through replacing the file. That is the same contention
// renameWithRetry deals with from the writing side, and no reader here was
// ready for it. Under two instances saving at once, around one read in thirty
// fails this way, and what it costs is not a slow read: Load returning an
// error is a project that restores no tabs, and the save on the way out then
// writes that emptiness over the layout the user still had.
//
// Only a sharing violation is waited out. Every other failure — the file is
// not there, it is a directory, this user may not read it — will be just as
// true a second from now, and the recent list is read every time the project
// picker opens: spending the budget on those would make the picker feel broken
// rather than fail.
func readState(path string) ([]byte, error) {
	deadline := time.Now().Add(contentionBudget)
	delay := time.Millisecond
	for {
		data, err := readFile(path)
		if err == nil {
			return data, nil
		}
		if !heldByAnother(err) || time.Now().After(deadline) {
			return nil, err
		}
		delay = backOff(delay)
	}
}

// readFile reads a whole file. It is a variable so a test can stand in for a
// read that fails for a reason other than the file not being there, which no
// test can arrange portably: permissions stop nobody running as root, and a
// sharing violation exists only on Windows.
var readFile = os.ReadFile

// RenameWithRetry is renameWithRetry for files other packages keep in the
// state directory. The key store is replaced the same way these files are and
// opened just as often -- by every pane that starts, by the picker asking
// whether an agent has a key, and by the virus scanner after each save -- so
// on Windows a save of it failed "Access is denied" whenever one of those had
// it open.
func RenameWithRetry(src, dst string) error { return renameWithRetry(src, dst) }

// ReadState is readState for files other packages keep in the state
// directory, so a read of the key store made while a save is replacing it
// waits the save out rather than reading as no key at all.
func ReadState(path string) ([]byte, error) { return readState(path) }

// hashRoot turns a path into a short stable filename component.
func hashRoot(root string) string {
	return hashString(normalizeRoot(root))
}

// hashString is FNV-1a, inlined to keep the dependency surface small.
func hashString(s string) string {
	const (
		offset = 14695981039346656037
		prime  = 1099511628211
	)
	h := uint64(offset)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime
	}
	return fmt.Sprintf("%016x", h)
}

// SweepSessions removes files left behind by runs that were killed rather than
// closed, and returns how many it deleted: generated per-session settings, and
// the temporaries of writes that never reached their rename.
//
// Files are only removed once they are older than maxAge. Normal shutdown
// deletes both as it goes, so what is left after that is orphaned.
//
// Age settles it for a temporary: a write is a few milliseconds, so nothing
// part-way through is a day old. It does not settle it for a pane's settings.
// Those are written once when the pane starts and never touched again, so an
// agent that has been running since yesterday has a settings file that looks a
// day abandoned — and deleting it takes the hooks out from under a pane that
// is still working, which is how flockdeck knows whether that agent is waiting on
// its user. Whether they are in use is not a question about the file, so it is
// asked of the instance instead: while another one is running, its panes keep
// their settings and only the temporaries go.
//
// That case is `flockdeck -solo`, which is the one way to get a second instance
// past a first that is still answering — including one left detached with its
// agents still going, which is exactly the run with the most to lose.
func SweepSessions(maxAge time.Duration) (int, error) {
	// Without an age there is nothing separating an orphan from a file a
	// running instance wrote a moment ago, and the sweep would delete the live
	// instance's settings and half-written state. Refuse rather than guess.
	if maxAge <= 0 {
		return 0, fmt.Errorf("sweep sessions: maxAge must be positive, got %s", maxAge)
	}
	state, err := Dir()
	if err != nil {
		return 0, err
	}

	cutoff := time.Now().Add(-maxAge)
	isSettings := func(name string) bool { return strings.HasSuffix(name, ".settings.json") }
	// writeAtomic names its temporaries "<file>.tmp<random>"; nothing the
	// instance keeps has ".tmp" anywhere in its name.
	isTemp := func(name string) bool { return strings.Contains(name, ".tmp") }

	if rivalRunning() {
		isSettings = func(string) bool { return false }
	}

	// Both directories are swept whatever happens to either. They fill up
	// independently, so giving up on the second because the first could not be
	// reached would leave its orphans there for good.
	removed := 0
	sessions, firstErr := SessionsDir()
	if firstErr == nil {
		removed, firstErr = sweepDir(sessions, cutoff, func(name string) bool {
			return isSettings(name) || isTemp(name)
		})
	}
	n, err := sweepDir(state, cutoff, isTemp)
	if firstErr == nil {
		firstErr = err
	}
	return removed + n, firstErr
}

// rivalRunning reports whether another instance is recorded and still running.
//
// A record left by a run that crashed names a process that is gone, and that
// answers no, which is right: nothing is holding those files. A record we
// cannot read at all also answers no, which is the same answer the sweep gave
// before there was a question, and the worst it costs is a settings file a
// running agent would have liked to keep.
func rivalRunning() bool {
	inst, err := LoadInstance()
	if err != nil || inst == nil || inst.PID == os.Getpid() {
		return false
	}
	return processAlive(inst.PID)
}

// sweepDir deletes the matching files in a directory that were last written
// before cutoff, and reports how many went. A file that will not delete is
// skipped rather than failing the sweep: it is somebody else's to worry about.
func sweepDir(dir string, cutoff time.Time, match func(name string) bool) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", dir, err)
	}
	removed := 0
	for _, e := range entries {
		if e.IsDir() || !match(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if os.Remove(filepath.Join(dir, e.Name())) == nil {
			removed++
		}
	}
	return removed, nil
}

// BrowserProfileDir returns the directory holding the browser profile used for
// the application window. Keeping it separate from the user's own profile
// means the window opens clean and does not disturb their browsing session.
//
// On Windows it is kept in the local application data folder rather than
// beside the rest of the state, which is in the roaming one. The profile is a
// couple of hundred megabytes of browser components and caches, and where
// profiles roam Windows copies the roaming folder to and from the network at
// every logon, or keeps it on a network share where it is redirected; Chrome
// and Edge keep their own profiles out of it for the same reason. A profile
// an earlier build left in the roaming folder is moved across, or deleted
// once there is one here.
func BrowserProfileDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	sub := filepath.Join(dir, "window")
	if runtime.GOOS == "windows" {
		if local, err := os.UserCacheDir(); err == nil {
			moved := filepath.Join(local, "flockdeck", "window")
			adoptProfile(sub, moved)
			sub = moved
		}
	}
	if err := os.MkdirAll(sub, 0o700); err != nil {
		return "", fmt.Errorf("create window profile dir: %w", err)
	}
	makePrivate(sub)
	return sub, nil
}

// adoptProfile moves a window profile from where an earlier build kept it to
// dir, when there is one there and nothing at dir yet.
//
// Otherwise the old one is deleted. A move can fail, across volumes where the
// roaming folder is on a network share or while an earlier build's window
// still has the profile open, and once the window has started a fresh profile
// at dir the old one was left for good: a couple of hundred megabytes that
// hold nothing the window could use, since each run is a new origin to the
// browser. It is renamed aside before it is deleted, because Windows refuses
// that rename while a browser has files in it open, so a profile in use is
// left for a later start rather than half deleted under the browser.
func adoptProfile(old, dir string) {
	aside := old + ".removing"
	_ = os.RemoveAll(aside) // what an interrupted deletion left
	oi, err := os.Stat(old)
	if err != nil || !oi.IsDir() {
		return
	}
	switch di, err := os.Stat(dir); {
	case errors.Is(err, fs.ErrNotExist):
		if os.MkdirAll(filepath.Dir(dir), 0o700) == nil && os.Rename(old, dir) == nil {
			return
		}
	case err != nil, os.SameFile(oi, di):
		// Unreadable, or the two are one folder, as when both application
		// data folders are pointed at the same place: that is the profile in
		// use, not a copy of it.
		return
	}
	if os.Rename(old, aside) == nil {
		_ = os.RemoveAll(aside)
	}
}

// Project is a directory the user has opened.
type Project struct {
	Root     string    `json:"root"`
	LastUsed time.Time `json:"lastUsed"`
}

// recentsFile is the global list of projects, kept separately from the
// per-project layouts so the picker can offer them before any is opened.
const recentsFile = "projects.json"

// maxRecents caps the remembered list.
const maxRecents = 40

// Recents returns known projects, most recently used first.
func Recents() ([]Project, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	data, err := readState(filepath.Join(dir, recentsFile))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read projects: %w", err)
	}
	var list []Project
	if json.Unmarshal(data, &list) != nil {
		// A damaged list is not worth failing over; it is only a convenience.
		// It is still moved aside, because the next project the user opens
		// rewrites this file from what was read, and what was read is nothing:
		// every directory they have ever opened, replaced by the one in front
		// of them. The file is a plain list of paths, so the kept copy is
		// something they can read and put back by hand.
		quarantine(filepath.Join(dir, recentsFile))
		return nil, nil
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].LastUsed.After(list[j].LastUsed) })

	// Collapse entries that name the same directory, keeping the most recent.
	// The file outlives any one version of this code, so it can hold paths
	// that only later became equal — two spellings of one root, or a blank
	// left by a caller that did not check. The picker must not show either.
	out := make([]Project, 0, len(list))
	for _, p := range list {
		if strings.TrimSpace(p.Root) == "" {
			continue
		}
		seen := false
		for _, kept := range out {
			if sameRoot(kept.Root, p.Root) {
				seen = true
				break
			}
		}
		if !seen {
			out = append(out, p)
		}
	}
	if len(out) > maxRecents {
		out = out[:maxRecents]
	}
	return out, nil
}

// TouchRecent records that a project was opened, moving it to the front.
func TouchRecent(root string) error { return TouchRecents(root) }

// TouchRecents records that several projects were opened, moving them to the
// front in the order given, the first given first.
//
// It is one read of the list and at most one write. Recording them one at a
// time was a write each, and a start records every project it reopens around
// the one it was started on: on Windows each write came to eleven
// milliseconds of the start.
func TouchRecents(roots ...string) error {
	var clean []string
	for _, root := range roots {
		// An empty root is not a project. It would be stored as "." and then
		// offered in the picker as a directory that opens somewhere
		// unpredictable.
		if strings.TrimSpace(root) == "" {
			return errors.New("recent project: empty path")
		}
		// Store the tidied path, not whatever spelling this run happened to
		// use: the picker shows these to the user verbatim.
		c := filepath.Clean(root)
		if !containsRoot(clean, c) {
			clean = append(clean, c)
		}
	}
	if len(clean) == 0 {
		return nil
	}
	// The rewrite below replaces the whole file, so a list we could not read
	// has to stop us: carrying on would quietly discard every other project
	// the user has opened. A damaged or absent list reads as empty, which is
	// the case where starting again is the right answer.
	list, err := Recents()
	if err != nil {
		return err
	}
	// Projects already at the front in this order, spelled as they would be
	// written, are where the rewrite would put them: the times only order the
	// list, and nothing shows them. Rewriting it anyway is a file created,
	// flushed to the device and renamed on every switch between projects,
	// eleven milliseconds on Windows spent on the goroutine that owns the
	// workspace -- and at every start that reopens the same projects as the
	// one before.
	if len(list) >= len(clean) {
		first := true
		for i, c := range clean {
			if list[i].Root != c {
				first = false
				break
			}
		}
		if first {
			return nil
		}
	}
	now := time.Now()
	out := make([]Project, 0, len(list)+len(clean))
	for i, c := range clean {
		// A nanosecond apart, so the list comes back in the order given
		// however it is sorted.
		out = append(out, Project{Root: c, LastUsed: now.Add(-time.Duration(i))})
	}
	for _, p := range list {
		if !containsRoot(clean, p.Root) {
			out = append(out, p)
		}
	}
	if len(out) > maxRecents {
		out = out[:maxRecents]
	}
	return writeRecents(out)
}

// containsRoot reports whether roots names the same project as root.
func containsRoot(roots []string, root string) bool {
	for _, r := range roots {
		if sameRoot(r, root) {
			return true
		}
	}
	return false
}

// ForgetRecent drops a project from the remembered list.
func ForgetRecent(root string) error {
	list, err := Recents()
	if err != nil {
		return err
	}
	out := make([]Project, 0, len(list))
	for _, p := range list {
		if !sameRoot(p.Root, root) {
			out = append(out, p)
		}
	}
	if len(out) == len(list) {
		// Nothing to forget. Rewriting the file anyway would be a needless
		// chance for another instance's save to be the one that loses.
		return nil
	}
	return writeRecents(out)
}

func writeRecents(list []Project) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return fmt.Errorf("encode projects: %w", err)
	}
	// A damaged list that would not move aside is recorded as unread, and this
	// write is what it has to be kept from: every directory the user has
	// opened, replaced by the list read from it, which is nothing.
	if err := keepUnread(filepath.Join(dir, recentsFile), "the list of recent projects"); err != nil {
		return fmt.Errorf("write projects: %w", err)
	}
	if err := writeAtomic(filepath.Join(dir, recentsFile), data); err != nil {
		return fmt.Errorf("write projects: %w", err)
	}
	return nil
}

// sameRoot compares project paths, case-insensitively on platforms whose file
// systems are.
func sameRoot(a, b string) bool {
	return normalizeRoot(a) == normalizeRoot(b)
}

// Session is the set of projects that were open when the application last
// exited, so a restart brings the whole workspace back rather than only the
// directory named on the command line.
type Session struct {
	Open   []string `json:"open"`
	Active string   `json:"active"`
}

const sessionFile = "session.json"

// LoadSession returns the last workspace, or nil when there is none.
func LoadSession() (*Session, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	data, err := readState(filepath.Join(dir, sessionFile))
	noteRead(filepath.Join(dir, sessionFile), err)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read session: %w", err)
	}
	var s Session
	if json.Unmarshal(data, &s) != nil {
		// A damaged session must not stop startup, and must not be lost to the
		// save on the way out either: it names every project that was open,
		// which is the one record of a workspace spread over several
		// directories.
		quarantine(filepath.Join(dir, sessionFile))
		return nil, nil
	}
	return tidySession(&s), nil
}

// tidySession returns the session with each project named once, in a form the
// caller can compare against paths of its own.
//
// The restorer reopens every entry in Open, skipping the ones it already has.
// Two spellings of one directory read as two projects there, so it would open
// the project twice and restore the same saved layout into both, doubling the
// user's tabs. Nothing else notices, because the duplicates are only equal
// once the paths are cleaned.
func tidySession(s *Session) *Session {
	out := &Session{Open: make([]string, 0, len(s.Open)), Active: filepath.Clean(s.Active)}
	if strings.TrimSpace(s.Active) == "" {
		out.Active = ""
	}
	for _, root := range s.Open {
		if strings.TrimSpace(root) == "" {
			continue
		}
		seen := false
		for _, kept := range out.Open {
			if sameRoot(kept, root) {
				seen = true
				break
			}
		}
		if !seen {
			out.Open = append(out.Open, filepath.Clean(root))
		}
	}
	// An active project that is not open cannot be restored to; leaving it set
	// would only mislead whoever reads it next.
	if out.Active != "" {
		found := false
		for _, root := range out.Open {
			if sameRoot(root, out.Active) {
				found = true
				break
			}
		}
		if !found {
			out.Active = ""
		}
	}
	return out
}

// Landing is the project a launch that named none goes back to: the one the
// user was last in, or failing that the first of the others that is still
// there, so one deleted project does not cost them the rest. It is "" when
// none of them is a directory any more, and for a nil session, so a caller
// with nothing saved can ask all the same.
func (s *Session) Landing() string {
	if s == nil {
		return ""
	}
	for _, root := range append([]string{s.Active}, s.Open...) {
		if root == "" {
			continue
		}
		if fi, err := os.Stat(root); err == nil && fi.IsDir() {
			return root
		}
	}
	return ""
}

// SaveSession records which projects are open.
func SaveSession(s *Session) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(tidySession(s), "", "  ")
	if err != nil {
		return fmt.Errorf("encode session: %w", err)
	}
	if err := keepUnread(filepath.Join(dir, sessionFile), "the list of open projects"); err != nil {
		return fmt.Errorf("write session: %w", err)
	}
	if err := writeAtomic(filepath.Join(dir, sessionFile), data); err != nil {
		return fmt.Errorf("write session: %w", err)
	}
	return nil
}

// Instance records a running flockdeck so a second launch can attach to it
// instead of starting a rival server, and so agents can outlive the window.
type Instance struct {
	PID     int       `json:"pid"`
	URL     string    `json:"url"`
	Token   string    `json:"token"`
	Started time.Time `json:"started"`
}

const instanceFile = "instance.json"

func instancePath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, instanceFile), nil
}

// LoadInstance returns the recorded running instance, or nil when there is
// none. A stale record left by a crash is not detected here: the caller has to
// probe the address, which is the only reliable test.
func LoadInstance() (*Instance, error) {
	path, err := instancePath()
	if err != nil {
		return nil, err
	}
	data, err := readState(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read instance: %w", err)
	}
	var inst Instance
	if json.Unmarshal(data, &inst) != nil || inst.URL == "" {
		return nil, nil
	}
	return &inst, nil
}

// SaveInstance records this process as the running instance.
func SaveInstance(inst *Instance) error {
	path, err := instancePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(inst, "", "  ")
	if err != nil {
		return fmt.Errorf("encode instance: %w", err)
	}
	if err := writeAtomic(path, data); err != nil {
		return fmt.Errorf("write instance: %w", err)
	}
	return nil
}

// ClearInstance removes the record, and does nothing if it is already gone.
//
// A record naming another process that is still running is left alone. There
// are two callers — an instance shutting down, and a launch that found the
// recorded address unreachable — and neither can tell whether the record it is
// about to delete is still its own. It often is not: an instance whose server
// briefly failed to answer a probe has its record cleared and then overwritten
// by the rival launch, and when the first one finally exits it would delete
// the second one's record, leaving a running instance that no later launch can
// find and attach to.
func ClearInstance() error {
	path, err := instancePath()
	if err != nil {
		return err
	}
	if inst, err := LoadInstance(); err == nil && inst != nil {
		if inst.PID != os.Getpid() && processAlive(inst.PID) {
			return nil
		}
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// processAlive reports whether a process id still names a running process. It
// is a variable so tests can answer for a process table they control.
var processAlive = func(pid int) bool {
	if pid <= 0 {
		return false
	}
	return pidAlive(pid)
}

// ProcessAlive reports whether a process id still names a running process. It
// is how `flockdeck -quit` tells that the instance it asked to stop has gone,
// rather than only stopped listening.
func ProcessAlive(pid int) bool { return processAlive(pid) }

// StillRunning reports whether the process that made this record is still
// running.
//
// Its id alone cannot say. Once that process has exited, the system is free to
// give the id to any process started since, and asking after the id then
// answers for that one. A process that started after the record was made
// cannot be the one that made it, so where the system says when a process
// started -- Windows and Linux -- that is checked as well. Elsewhere, and for a
// record with no start time, a live process id is taken at its word.
func (inst *Instance) StillRunning() bool {
	if inst == nil || !processAlive(inst.PID) {
		return false
	}
	if inst.Started.IsZero() {
		return true
	}
	started, ok := processStarted(inst.PID)
	if !ok {
		return true
	}
	return !started.After(inst.Started.Add(startSlack))
}

// startSlack allows for the precision a process's start is known to: Linux
// gives the boot time only to the second. A process given the id of an
// instance that had exited inside that second of recording itself is not a
// case worth more.
const startSlack = 2 * time.Second
